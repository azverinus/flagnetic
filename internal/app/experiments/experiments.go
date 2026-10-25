// Package experiments holds the use cases of the experiments context. Every
// write locks its project's row first, the lock the flags context's writes
// take, so an experiment is judged against the flags and the salts of its
// project as they are when it commits.
package experiments

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ErrNotFound is what a store answers for an entity it does not hold.
var ErrNotFound = errors.New("experiments: not found")

// Transactor runs a unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// ProjectLocker locks a project's row for the unit of work. Every write of
// the context takes this lock first and no other, in the lock order of the
// flags context's writes.
type ProjectLocker interface {
	// LockProject locks the project's row, or answers ErrNotFound when
	// there is none.
	LockProject(ctx context.Context, project workspace.ProjectID) error
}

// lockProject takes project's row lock through l. The request found the
// project before the handler ran, so a project missing now was deleted in
// between, and is answered as the unknown project a moment-later request
// would have been told of, not as a failure of the server.
func lockProject(ctx context.Context, l ProjectLocker, project workspace.ProjectID) error {
	err := l.LockProject(ctx, project)
	if errors.Is(err, ErrNotFound) {
		return access.ErrProjectNotFound
	}
	return err
}

// Owners judges who may own an experiment.
type Owners interface {
	// MayOwn reports whether member is an active member holding a grant
	// in project, or an instance admin.
	MayOwn(ctx context.Context, project workspace.ProjectID, member access.MemberID) (bool, error)
}

// Flags reads the flags of a project, as much of them as an experiment
// needs.
type Flags interface {
	// ExperimentFlag returns project's flag with key, or ErrNotFound.
	ExperimentFlag(ctx context.Context, project workspace.ProjectID, key string) (experiments.FlagFacts, error)
}

// UnitTypes reads the unit types a project declares.
type UnitTypes interface {
	// UnitTypes returns the project's unit types.
	UnitTypes(ctx context.Context, project workspace.ProjectID) ([]workspace.UnitType, error)
}

// RolloutSalts reads the salts rollouts use.
type RolloutSalts interface {
	// RolloutSalts returns the salt of every rollout in every flag
	// configuration of project, archived flags included.
	RolloutSalts(ctx context.Context, project workspace.ProjectID) ([]string, error)
}

// Reader reads experiments.
type Reader interface {
	// ExperimentByKey returns project's experiment with key, or ErrNotFound.
	ExperimentByKey(ctx context.Context, project workspace.ProjectID, key string) (experiments.Experiment, error)
}

// Recorder is what every command handler of the context runs on besides its
// store: the clock a change is timed by, the source its identifiers are
// drawn from, and the audit log it is recorded in, so a change and its entry
// carry the same instant.
type Recorder struct {
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewRecorder returns the recorder writing to audit, timing changes by c and
// identifying them from ids.
func NewRecorder(audit AuditLog, c clock.Clock, ids *id.Source) Recorder {
	return Recorder{audit: audit, clock: c, ids: ids}
}

// now returns the instant a change is made at, to the microsecond in UTC as
// the database keeps it, so what a write answers is what a later read does.
func (rec Recorder) now() time.Time { return rec.clock.Now().UTC().Truncate(time.Microsecond) }

// record writes the audit entry of r, made at now in req, in the unit of
// work ctx carries.
func (rec Recorder) record(ctx context.Context, now time.Time, req governance.Request, r governance.Record) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](rec.ids), now, req, r)
	if err != nil {
		return err
	}
	return rec.audit.Record(ctx, entry)
}

// randomSalt returns 16 random lower-case hex characters, which no one can
// guess a unit's bucket from before the experiment exists.
func randomSalt() (string, error) {
	var b [experiments.SaltLength / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	const digits = "0123456789abcdef"
	out := make([]byte, 0, experiments.SaltLength)
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0xf])
	}
	return string(out), nil
}

// errExperimentNotFound answers an experiment key the project does not have.
var errExperimentNotFound = apperrors.NotFound("not_found", "the project has no experiment with this key")

// experimentNotFound answers ErrNotFound as not_found, any other error as it
// is.
func experimentNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return errExperimentNotFound
	}
	return err
}

func experimentEntity(e experiments.Experiment) governance.Entity {
	return governance.Entity{Kind: "experiment", ID: e.ID.String(), Key: e.Key}
}

// experimentValues are what an audit entry records of an experiment: the
// whole definition with its version.
func experimentValues(e experiments.Experiment) map[string]any {
	weights := make([]map[string]any, len(e.Variations))
	for i, w := range e.Variations {
		weights[i] = map[string]any{"variation": w.Variation, "weight": w.Weight}
	}
	var created any
	if e.CreatedBy != nil {
		created = e.CreatedBy.String()
	}
	return map[string]any{
		"key": e.Key, "name": e.Name, "hypothesis": e.Hypothesis, "owner": e.Owner.String(), "tags": e.Tags,
		"flag": e.FlagKey, "unit_type": e.UnitType, "variations": weights, "traffic_allocation": e.TrafficAllocation,
		"inclusion_salt": e.InclusionSalt, "variation_salt": e.VariationSalt, "status": e.Status,
		"expected_end_at": e.ExpectedEndAt, "started_at": e.StartedAt, "stopped_at": e.StoppedAt,
		"archived_at": e.ArchivedAt, "decision": decisionValues(e.Decision), "created_by": created, "version": e.Version,
	}
}

// decisionValues are what an audit entry records of a decision; nil for
// none.
func decisionValues(d *experiments.Decision) any {
	if d == nil {
		return nil
	}
	var winner any
	if d.WinnerVariation != "" {
		winner = d.WinnerVariation
	}
	return map[string]any{"outcome": string(d.Outcome), "winner_variation": winner, "note": d.Note}
}
