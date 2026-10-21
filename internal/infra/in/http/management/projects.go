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
	items, err := a.projectsOut(ctx, projects...)
	if err != nil {
		return nil, err
	}
	return ListProjects200JSONResponse{Items: items}, nil
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
	// A project is created with no grant, so nobody but the instance
	// admins works in it yet.
	return CreateProject201JSONResponse(projectOut(p, 0)), nil
}

// GetProject answers the project the seam found.
func (a *api) GetProject(ctx context.Context, _ GetProjectRequestObject) (GetProjectResponseObject, error) {
	out, err := a.projectsOut(ctx, projectOf(ctx).project)
	if err != nil {
		return nil, err
	}
	return GetProject200JSONResponse(out[0]), nil
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
	out, err := a.projectsOut(ctx, p)
	if err != nil {
		return nil, err
	}
	return UpdateProject200JSONResponse(out[0]), nil
}

// GetProjectMe answers what the caller holds in the project: their grants
// and their effective permissions there.
func (a *api) GetProjectMe(ctx context.Context, _ GetProjectMeRequestObject) (GetProjectMeResponseObject, error) {
	in := projectOf(ctx)
	perms := each(in.permissions, func(p access.Permission) Permission { return string(p) })
	return GetProjectMe200JSONResponse{Grants: each(in.grants, grantOf), Permissions: perms}, nil
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

// projectsOut answers projects with how many members work in each, counted
// in one query whatever their number.
func (a *api) projectsOut(ctx context.Context, projects ...workspace.Project) ([]Project, error) {
	counts, err := a.h.CountProjectMembers.Handle(ctx, each(projects, func(p workspace.Project) workspace.ProjectID { return p.ID }))
	if err != nil {
		return nil, err
	}
	return each(projects, func(p workspace.Project) Project { return projectOut(p, counts[p.ID]) }), nil
}

func projectOut(p workspace.Project, members int) Project {
	return Project{Id: openapi_types.UUID(p.ID.Bytes()), Key: p.Key, Name: p.Name, Description: p.Description, MemberCount: members}
}
