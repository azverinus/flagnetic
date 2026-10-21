package access

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// GrantReader reads the grants members hold.
type GrantReader interface {
	// MemberGrants returns the grants member holds in project, oldest
	// first.
	MemberGrants(ctx context.Context, member access.MemberID, project workspace.ProjectID) ([]access.Grant, error)
	// GrantedProjects returns the projects member holds a grant in.
	GrantedProjects(ctx context.Context, member access.MemberID) ([]workspace.ProjectID, error)
}

// ProjectPermissions answers what a caller holds in a project: the
// permission seam judges every operation in a project by it, and the
// console shows or disables actions by it.
type ProjectPermissions struct{ grants GrantReader }

// NewProjectPermissions returns the handler.
func NewProjectPermissions(grants GrantReader) *ProjectPermissions {
	return &ProjectPermissions{grants: grants}
}

// ProjectHoldings are what a caller holds in a project.
type ProjectHoldings struct {
	// Grants are those the caller's member holds there; none for a project
	// token, which acts for no member.
	Grants []access.Grant
	// Permissions are the caller's effective permissions there, in catalog
	// order.
	Permissions []access.Permission
}

// Handle returns the grants of the caller's member in project and the
// caller's effective permissions there: an instance admin holds every
// permission, a member the union of their grants' roles, narrowed by the
// scopes of a token; a project token its role's in its own project.
func (h *ProjectPermissions) Handle(ctx context.Context, caller access.Caller, project workspace.ProjectID) (ProjectHoldings, error) {
	var grants []access.Grant
	if caller.Member != nil {
		var err error
		if grants, err = h.grants.MemberGrants(ctx, caller.Member.ID, project); err != nil {
			return ProjectHoldings{}, err
		}
	}
	roles := make([]access.RoleName, len(grants))
	for i, g := range grants {
		roles[i] = g.Role
	}
	return ProjectHoldings{Grants: grants, Permissions: access.EffectivePermissions(caller, project, roles)}, nil
}

// WorkingProjects answers which projects a caller works in, for listing
// them.
type WorkingProjects struct{ grants GrantReader }

// NewWorkingProjects returns the handler.
func NewWorkingProjects(grants GrantReader) *WorkingProjects { return &WorkingProjects{grants: grants} }

// Handle reports whether the caller works in every project, as an instance
// admin does, and otherwise the projects their member holds a grant in.
func (h *WorkingProjects) Handle(ctx context.Context, caller access.Caller) (all bool, only []workspace.ProjectID, err error) {
	if caller.Member == nil {
		return false, nil, nil
	}
	if caller.Member.InstanceAdmin {
		return true, nil, nil
	}
	only, err = h.grants.GrantedProjects(ctx, caller.Member.ID)
	return false, only, err
}
