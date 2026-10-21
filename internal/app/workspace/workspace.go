// Package workspace holds the use cases of the workspace context: projects,
// their environments, unit types and attributes.
package workspace

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
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
