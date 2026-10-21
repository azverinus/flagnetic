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

// ids mints the identifiers of a new project's defaults from one source.
type ids struct{ src *id.Source }

func (i ids) Environment() workspace.EnvironmentID { return id.New[workspace.Environment](i.src) }
func (i ids) UnitType() workspace.UnitTypeID       { return id.New[workspace.UnitType](i.src) }
func (i ids) Attribute() workspace.AttributeID     { return id.New[workspace.Attribute](i.src) }

// Recorder makes the audit entries of a context's changes: it holds what
// every command handler of the context records with.
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
