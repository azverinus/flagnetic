package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ListProjects answers the projects the caller works in: every project for
// an instance admin, else those where they hold a grant. A project token
// never gets here: it reaches its own project only.
func (a *api) ListProjects(ctx context.Context, _ ListProjectsRequestObject) (ListProjectsResponseObject, error) {
	c := callerOf(ctx)
	// Grants are not stored yet, so a member who is not an instance admin
	// works in no project.
	q := appworkspace.ListProjectsQuery{All: c.Member != nil && c.Member.InstanceAdmin}
	projects, err := a.h.ListProjects.Handle(ctx, q)
	if err != nil {
		return nil, err
	}
	out := ListProjects200JSONResponse{Items: make([]Project, len(projects))}
	for i, p := range projects {
		out.Items[i] = projectOut(p)
	}
	return out, nil
}

// CreateProject creates a project with its defaults.
func (a *api) CreateProject(ctx context.Context, req CreateProjectRequestObject) (CreateProjectResponseObject, error) {
	cmd := appworkspace.CreateProjectCommand{Key: req.Body.Key, Name: req.Body.Name, Request: requestOf(ctx)}
	if req.Body.Description != nil {
		cmd.Description = *req.Body.Description
	}
	p, err := a.h.CreateProject.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return CreateProject201JSONResponse(projectOut(p)), nil
}

// GetProject answers the project the seam found.
func (a *api) GetProject(ctx context.Context, _ GetProjectRequestObject) (GetProjectResponseObject, error) {
	return GetProject200JSONResponse(projectOut(projectOf(ctx).project)), nil
}

// UpdateProject renames the project or changes its description.
func (a *api) UpdateProject(ctx context.Context, req UpdateProjectRequestObject) (UpdateProjectResponseObject, error) {
	p, err := a.h.UpdateProject.Handle(ctx, appworkspace.UpdateProjectCommand{
		Project: projectOf(ctx).project.ID,
		Change:  workspace.ProjectChange{Key: req.Body.Key, Name: req.Body.Name, Description: req.Body.Description},
		Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UpdateProject200JSONResponse(projectOut(p)), nil
}

// GetProjectMe answers what the caller holds in the project: their grants
// and their effective permissions there.
func (a *api) GetProjectMe(ctx context.Context, _ GetProjectMeRequestObject) (GetProjectMeResponseObject, error) {
	in := projectOf(ctx)
	out := GetProjectMe200JSONResponse{Grants: []Grant{}, Permissions: make([]Permission, len(in.permissions))}
	for i, p := range in.permissions {
		out.Permissions[i] = string(p)
	}
	return out, nil
}

// ListRoles answers every role with its permissions.
func (a *api) ListRoles(context.Context, ListRolesRequestObject) (ListRolesResponseObject, error) {
	roles := access.Roles()
	out := ListRoles200JSONResponse{Items: make([]Role, len(roles))}
	for i, r := range roles {
		perms := make([]Permission, len(r.Permissions))
		for j, p := range r.Permissions {
			perms[j] = string(p)
		}
		out.Items[i] = Role{Name: RoleName(r.Name), Permissions: perms}
	}
	return out, nil
}

func projectOut(p workspace.Project) Project {
	return Project{Id: openapi_types.UUID(p.ID.Bytes()), Key: p.Key, Name: p.Name, Description: p.Description}
}
