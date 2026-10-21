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
	all, only, err := a.h.WorkingProjects.Handle(ctx, callerOf(ctx))
	if err != nil {
		return nil, err
	}
	projects, err := a.h.ListProjects.Handle(ctx, appworkspace.ListProjectsQuery{All: all, Only: only})
	if err != nil {
		return nil, err
	}
	return ListProjects200JSONResponse{Items: each(projects, projectOut)}, nil
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
	perms := each(projectOf(ctx).permissions, func(p access.Permission) Permission { return string(p) })
	return GetProjectMe200JSONResponse{Grants: []Grant{}, Permissions: perms}, nil
}

// ListRoles answers every role with its permissions.
func (a *api) ListRoles(context.Context, ListRolesRequestObject) (ListRolesResponseObject, error) {
	roles := access.Roles()
	out := ListRoles200JSONResponse{Items: make([]Role, len(roles))}
	for i, r := range roles {
		out.Items[i] = Role{Name: RoleName(r.Name), Permissions: each(r.Permissions, func(p access.Permission) Permission { return string(p) })}
	}
	return out, nil
}

func projectOut(p workspace.Project) Project {
	return Project{Id: openapi_types.UUID(p.ID.Bytes()), Key: p.Key, Name: p.Name, Description: p.Description}
}
