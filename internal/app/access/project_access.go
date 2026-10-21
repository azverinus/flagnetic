package access

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ProjectPermissions answers what a caller holds in a project: the
// permission seam judges every operation in a project by it, and the
// console shows or disables actions by it.
type ProjectPermissions struct{}

// NewProjectPermissions returns the handler.
func NewProjectPermissions() *ProjectPermissions { return &ProjectPermissions{} }

// Handle returns the caller's effective permissions in project, in catalog
// order. Grants are not stored yet, so a member holds none: an instance
// admin holds every permission, a project token its role's in its project.
func (h *ProjectPermissions) Handle(_ context.Context, caller access.Caller, project workspace.ProjectID) ([]access.Permission, error) {
	return access.EffectivePermissions(caller, project, nil), nil
}

// WorkingProjects answers which projects a caller works in, for listing
// them.
type WorkingProjects struct{}

// NewWorkingProjects returns the handler.
func NewWorkingProjects() *WorkingProjects { return &WorkingProjects{} }

// Handle reports whether the caller works in every project, as an instance
// admin does, and otherwise the projects they hold a grant in: none yet,
// since grants are not stored yet.
func (h *WorkingProjects) Handle(_ context.Context, caller access.Caller) (all bool, only []workspace.ProjectID, err error) {
	return caller.Member != nil && caller.Member.InstanceAdmin, nil, nil
}
