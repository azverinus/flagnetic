// Package flags holds the use cases of the flags context: flags, their
// configuration in each environment, and segments. Every write locks its
// project's row first and nothing else, so the writes of one project run one
// at a time and every rule spanning several flags and segments, checked over
// what the handler loaded, holds when the write commits.
package flags

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ErrNotFound is what a store answers for an entity it does not hold.
var ErrNotFound = errors.New("flags: not found")

// Transactor runs a unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// Snapshotter runs a query of several statements on one consistent view, so
// what it answers never mixes the states before and after a write committed
// between its statements.
type Snapshotter interface {
	Snapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// ProjectLocker locks a project's row for the unit of work. Every write of
// the context takes this lock first and no other, so the writes of a
// project are made one at a time in one lock order.
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

// Environments lists a project's environments.
type Environments interface {
	// Environments returns the project's environments by sort order.
	Environments(ctx context.Context, project workspace.ProjectID) ([]workspace.Environment, error)
}

// Rulesets marks the rulesets a write changed, in the write's unit of work.
type Rulesets interface {
	// Project marks the ruleset of every environment of project changed.
	Project(ctx context.Context, project workspace.ProjectID) error
	// Environment marks the ruleset of environment changed.
	Environment(ctx context.Context, environment workspace.EnvironmentID) error
}

// Owners judges who may own a flag.
type Owners interface {
	// MayOwn reports whether member is an active member holding a grant
	// in project, or an instance admin.
	MayOwn(ctx context.Context, project workspace.ProjectID, member access.MemberID) (bool, error)
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

// mint returns what a write draws for what it creates: rule identifiers
// from the recorder's source, and salts of 16 random lower-case letters and
// digits, which no one can guess a unit's bucket from before the rollout
// exists.
func (rec Recorder) mint() flags.Mint { return mintFrom(rec.ids) }

// mintFrom returns a mint drawing rule identifiers from ids.
func mintFrom(ids *id.Source) flags.Mint {
	return flags.Mint{RuleID: func() flags.RuleID { return id.New[flags.Rule](ids) }, Salt: newSalt}
}

// saltAlphabet is what a salt is drawn from: characters that read the same
// in a URL, a log line and a spreadsheet.
const saltAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// newSalt returns a salt of 16 characters drawn uniformly from saltAlphabet.
func newSalt() string {
	b := make([]byte, 16)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(saltAlphabet))))
		if err != nil {
			// crypto/rand never fails on the platforms Go supports; a salt
			// drawn from anything weaker would be guessable.
			panic(err)
		}
		b[i] = saltAlphabet[n.Int64()]
	}
	return string(b)
}

// record writes the audit entry of r, made at now in req, in the unit of
// work ctx carries.
func (rec Recorder) record(ctx context.Context, now time.Time, req governance.Request, r governance.Record) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](rec.ids), now, req, r)
	if err != nil {
		return err
	}
	return rec.audit.Record(ctx, entry)
}

// errEnvironmentNotFound answers an environment key the project does not
// have.
var errEnvironmentNotFound = apperrors.NotFound("not_found", "the project has no environment with this key")

// errFlagNotFound answers a flag key the project does not have.
var errFlagNotFound = apperrors.NotFound("not_found", "the project has no flag with this key")

// flagNotFound answers ErrNotFound as not_found, any other error as it is.
func flagNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return errFlagNotFound
	}
	return err
}

func flagEntity(f flags.Flag) governance.Entity {
	return governance.Entity{Kind: "flag", ID: f.ID.String(), Key: f.Key}
}

// definitionValues are what an audit entry records of a flag's definition.
func definitionValues(f flags.Flag) map[string]any {
	variations := make([]map[string]any, len(f.Variations))
	for i, v := range f.Variations {
		variations[i] = map[string]any{"key": v.Key, "value": v.Value, "name": v.Name, "description": v.Description}
	}
	return map[string]any{
		"key": f.Key, "type": f.Type, "name": f.Name, "description": f.Description, "owner": f.Owner.String(),
		"tags": f.Tags, "lifetime": f.Lifetime, "expected_removal_at": f.ExpectedRemovalAt,
		"automatic_exposure": f.AutomaticExposure, "variations": variations, "archived_at": f.ArchivedAt,
		"version": f.Version,
	}
}

// configurationValues are what an audit entry records of a configuration:
// the whole document with its version, which is what a revert restores,
// rule identifiers, salts, notes and authors included.
func configurationValues(c flags.Configuration) map[string]any {
	prerequisites := make([]map[string]any, len(c.Prerequisites))
	for i, p := range c.Prerequisites {
		prerequisites[i] = map[string]any{"flag": p.Flag, "variation": p.Variation}
	}
	overrides := make([]map[string]any, len(c.Overrides))
	for i, o := range c.Overrides {
		overrides[i] = map[string]any{
			"unit_type": o.UnitType, "unit_id": o.UnitID, "variation": o.Variation, "note": o.Note, "added_by": o.AddedBy,
		}
	}
	rules := make([]map[string]any, len(c.Rules))
	for i, r := range c.Rules {
		conditions := make([]map[string]any, len(r.Conditions))
		for j, cond := range r.Conditions {
			conditions[j] = conditionValues(cond)
		}
		serve := map[string]any{"kind": r.Serve.Kind, "variation": r.Serve.Variation}
		if r.Serve.Kind == flags.ServeRollout {
			serve["unit_type"], serve["percentage"], serve["salt"] = r.Serve.UnitType, r.Serve.Percentage, r.Serve.Salt
		}
		rules[i] = map[string]any{"id": r.ID, "description": r.Description, "conditions": conditions, "serve": serve}
	}
	return map[string]any{
		"on": c.On, "off_variation": c.OffVariation, "fallthrough": c.Fallthrough, "prerequisites": prerequisites,
		"overrides": overrides, "rules": rules, "updated_by": c.UpdatedBy, "version": c.Version,
	}
}

// flagValues are a flag's definition with its configuration in every
// environment, for an entry recording the flag created or deleted.
func flagValues(v FlagView) map[string]any {
	out := definitionValues(v.Flag)
	envs := make([]map[string]any, len(v.Configurations))
	for i, ec := range v.Configurations {
		c := configurationValues(ec.Configuration)
		c["environment"] = ec.Environment
		envs[i] = c
	}
	out["environments"] = envs
	return out
}

// FlagView is a flag with its configuration in every environment, by the
// environments' order.
type FlagView struct {
	Flag           flags.Flag
	Configurations []flags.EnvironmentConfiguration
}
