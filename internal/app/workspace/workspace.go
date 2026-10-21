// Package workspace holds the use cases of the workspace context: projects,
// their environments, unit types and attributes.
package workspace

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ErrNotFound is what a store answers for an entity it does not hold.
var ErrNotFound = errors.New("workspace: not found")

// Transactor runs a unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// ProjectFinder finds a project by its key.
type ProjectFinder interface {
	// ProjectByKey returns the project with key, or ErrNotFound.
	ProjectByKey(ctx context.Context, key string) (workspace.Project, error)
}

// ProjectLocker locks a project's row for the unit of work, so the changes
// to a project and to what it holds are made one at a time.
type ProjectLocker interface {
	// LockProject returns the project, or ErrNotFound.
	LockProject(ctx context.Context, project workspace.ProjectID) (workspace.Project, error)
}

// Rulesets marks the rulesets a write changed, in the write's unit of work.
type Rulesets interface {
	// Project marks the ruleset of every environment of project changed.
	Project(ctx context.Context, project workspace.ProjectID) error
}

// DeclarationGuard refuses to delete a unit type or attribute that rules,
// rollouts, overrides or segments still name, which the flags context
// knows.
type DeclarationGuard interface {
	// RefuseReferenced answers in_use, listing what names the declaration
	// keyed key in project, or nil when nothing does, under the project's
	// lock the caller holds.
	RefuseReferenced(ctx context.Context, project workspace.ProjectID, key string) error
}

// ids mints the identifiers of a new project's defaults from one source.
type ids struct{ src *id.Source }

func (i ids) Environment() workspace.EnvironmentID { return id.New[workspace.Environment](i.src) }
func (i ids) UnitType() workspace.UnitTypeID       { return id.New[workspace.UnitType](i.src) }
func (i ids) Attribute() workspace.AttributeID     { return id.New[workspace.Attribute](i.src) }

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

// record writes the audit entry of r, made at now in req, in the unit of
// work ctx carries.
func (rec Recorder) record(ctx context.Context, now time.Time, req governance.Request, r governance.Record) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](rec.ids), now, req, r)
	if err != nil {
		return err
	}
	return rec.audit.Record(ctx, entry)
}

// notFound answers ErrNotFound as the not_found refusal naming what, such as
// "environment"; any other error as it is.
func notFound(err error, what string) error {
	if errors.Is(err, ErrNotFound) {
		return apperrors.NotFound("not_found", "the project has no "+what+" with this key")
	}
	return err
}

// Snapshotter runs a query of several statements on one consistent view, so
// what it answers never mixes the states before and after a write committed
// between its statements.
type Snapshotter interface {
	Snapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

// DeclarationUsages counts what names a project's unit types and
// attributes, which the flags context knows.
type DeclarationUsages interface {
	// DeclarationUsageCounts returns, by declaration key, how many flags
	// and segments of project name it; a key nothing names is absent.
	DeclarationUsageCounts(ctx context.Context, project workspace.ProjectID) (map[string]int, error)
}
