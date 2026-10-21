// Package flags holds the use cases of the flags context: flags, their
// configuration in each environment, and segments. Every write locks its
// project's row first and nothing else, so the writes of one project run one
// at a time and every rule spanning several flags and segments, checked over
// what the handler loaded, holds when the write commits.
package flags

import (
	"context"
	"errors"
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

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// ProjectLocker locks a project's row for the unit of work. Every write of
// the context takes this lock first and no other, so the writes of a
// project are made one at a time in one lock order.
type ProjectLocker interface {
	// LockProject returns the project, or an error when there is none.
	LockProject(ctx context.Context, project workspace.ProjectID) (workspace.Project, error)
}

// Environments lists a project's environments.
type Environments interface {
	// Environments returns the project's environments by sort order.
	Environments(ctx context.Context, project workspace.ProjectID) ([]workspace.Environment, error)
}

// Rulesets marks the rulesets a write changed, in the write's unit of work.
type Rulesets interface {
	// Environment marks one environment's ruleset changed.
	Environment(ctx context.Context, environment workspace.EnvironmentID) error
	// Project marks the ruleset of every environment of project changed.
	Project(ctx context.Context, project workspace.ProjectID) error
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

// record writes the audit entry of r, made at now in req, in the unit of
// work ctx carries.
func (rec Recorder) record(ctx context.Context, now time.Time, req governance.Request, r governance.Record) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](rec.ids), now, req, r)
	if err != nil {
		return err
	}
	return rec.audit.Record(ctx, entry)
}

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
// the whole document with its version, which is what a revert restores.
func configurationValues(c flags.Configuration) map[string]any {
	return map[string]any{
		"on": c.On, "off_variation": c.OffVariation, "fallthrough": c.Fallthrough, "version": c.Version,
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
