package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/protection"
)

// RulesetBump is the port every write changing what SDKs receive calls to
// move the rulesets it changed on.
type RulesetBump interface {
	Project(ctx context.Context, project workspace.ProjectID) error
	Environment(ctx context.Context, environment workspace.EnvironmentID) error
}

// WriteRulesets materializes a ruleset as the unit of work in ctx would
// leave it.
type WriteRulesets interface {
	InUnitOfWork(ctx context.Context, environment workspace.EnvironmentID) (appdelivery.Ruleset, error)
}

// CommittedRulesets answers the ruleset of a committed version.
type CommittedRulesets interface {
	CommittedRuleset(ctx context.Context, environment workspace.EnvironmentID, version int64) (appdelivery.Ruleset, error)
}

// ProtectionGuardDeps are what a ProtectionGuard runs on.
type ProtectionGuardDeps struct {
	Pool *pgxpool.Pool
	// Next is the bump the guard lets through.
	Next RulesetBump
	// After materializes what the write would serve; Before what the
	// environment serves now.
	After  WriteRulesets
	Before CommittedRulesets
	// Registerer receives the guard's duration.
	Registerer prometheus.Registerer
}

// ProtectionGuard wraps the ruleset bump, the one call every write that
// changes what an environment's SDKs receive makes, whatever entity it
// touches, so that no write path reaches a protected environment without
// a change request, a path added later included. Before letting a bump
// through it materializes the ruleset of each protected environment the
// bump reaches, as the write's transaction would leave it, and compares it
// with the ruleset of the environment's committed version
// (governance.RulesetChanged); a difference refuses the whole write with
// change_request_required, before any environment is bumped. A write that
// changes no ruleset never bumps and pays nothing.
//
// The committed version is the right before only because every write
// changing a ruleset holds its project's row: no other write can commit
// to the project between what the transaction read and its bump. A bump in
// a transaction not holding it is therefore refused as a programming
// error, whether or not an environment is protected. The bypasses travel in
// the context (package protection).
type ProtectionGuard struct {
	d        ProtectionGuardDeps
	duration prometheus.Histogram
}

// NewProtectionGuard returns the guard and registers its duration.
func NewProtectionGuard(d ProtectionGuardDeps) *ProtectionGuard {
	g := &ProtectionGuard{d: d, duration: prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: metrics.Namespace, Subsystem: "protection", Name: "guard_seconds",
		Help:    "Time the protection guard spent judging a write that reached protected environments, refused or not.",
		Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
	})}
	d.Registerer.MustRegister(g.duration)
	return g
}

// ErrProjectNotHeld refuses a bump made by a write that did not lock its
// project's row first: a defect of that write, never of its caller.
var ErrProjectNotHeld = errors.New("a ruleset was bumped in a transaction that does not hold its project's row")

// Project judges every protected environment of project, then bumps them
// all.
func (g *ProtectionGuard) Project(ctx context.Context, project workspace.ProjectID) error {
	if err := g.judge(ctx, `e.project_id = $1`, project.Bytes()); err != nil {
		return err
	}
	return g.d.Next.Project(ctx, project)
}

// Environment judges environment when it is protected, then bumps it.
func (g *ProtectionGuard) Environment(ctx context.Context, environment workspace.EnvironmentID) error {
	if err := g.judge(ctx, `e.id = $1`, environment.Bytes()); err != nil {
		return err
	}
	return g.d.Next.Environment(ctx, environment)
}

// bumped is an environment a bump reaches, as the write's transaction sees
// it.
type bumped struct {
	id        workspace.EnvironmentID
	key       string
	protected bool
	version   int64
	// held is whether the transaction holds the row of its project.
	held bool
}

// committedVersionsKey keeps, for the life of a transaction, the ruleset
// version each environment it bumped had before its first bump there, which
// a second bump of the same environment in the transaction no longer reads.
type committedVersionsKey struct{}

// judge refuses the bump of the environments where selects when the
// transaction does not hold their project's row, or when the write changes
// the ruleset of one of them that is protected and not bypassed.
func (g *ProtectionGuard) judge(ctx context.Context, where string, arg [16]byte) error {
	// The row lock lives in the project row's xmax, or the row is the
	// transaction's own when it updated it: either way the transaction's
	// id is there, and no other transaction's can be while it holds the
	// lock, which conflicts with every other.
	rows, err := pgxtx.Querier(ctx, g.d.Pool).Query(ctx, `
		SELECT e.id, e.key, e.protected, e.ruleset_version,
			xid(pg_current_xact_id()) IN (p.xmin, p.xmax)
		FROM environments e JOIN projects p ON p.id = e.project_id
		WHERE `+where+`
		ORDER BY e.sort_order, e.key`, arg)
	if err != nil {
		return fmt.Errorf("reading the environments a bump reaches: %w", err)
	}
	environments, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (bumped, error) {
		var (
			b           bumped
			environment [16]byte
		)
		err := row.Scan(&environment, &b.key, &b.protected, &b.version, &b.held)
		b.id = id.FromBytes[workspace.Environment](environment)
		return b, err
	})
	if err != nil {
		return fmt.Errorf("reading the environments a bump reaches: %w", err)
	}
	committed, _ := pgxtx.Local(ctx, committedVersionsKey{}, func() map[workspace.EnvironmentID]int64 {
		return map[workspace.EnvironmentID]int64{}
	})
	var judged []bumped
	for _, b := range environments {
		if !b.held {
			return ErrProjectNotHeld
		}
		if _, ok := committed[b.id]; !ok {
			committed[b.id] = b.version
		}
		if b.protected && !protection.Bypassed(ctx, b.id) {
			judged = append(judged, b)
		}
	}
	if len(judged) == 0 {
		return nil
	}
	start := time.Now()
	defer func() { g.duration.Observe(time.Since(start).Seconds()) }()
	var changed []string
	for _, b := range judged {
		after, err := g.d.After.InUnitOfWork(ctx, b.id)
		if err != nil {
			return fmt.Errorf("materializing the ruleset of %s as the write leaves it: %w", b.key, err)
		}
		before, err := g.d.Before.CommittedRuleset(ctx, b.id, committed[b.id])
		if err != nil {
			return fmt.Errorf("reading the committed ruleset of %s: %w", b.key, err)
		}
		if governance.RulesetChanged(before.Evaluable, after.Evaluable) {
			changed = append(changed, b.key)
		}
	}
	if changed != nil {
		return governance.ErrChangeRequestRequired(changed)
	}
	return nil
}
