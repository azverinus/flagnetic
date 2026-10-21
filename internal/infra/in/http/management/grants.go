package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// projectRefOf returns the project the seam found for the request ctx
// belongs to, with what the caller holds there.
func projectRefOf(ctx context.Context) appaccess.ProjectRef {
	in := projectOf(ctx)
	return appaccess.ProjectRef{ID: in.project.ID, Held: in.permissions}
}

// ListProjectMembers answers the members holding grants in the project.
func (a *api) ListProjectMembers(ctx context.Context, _ ListProjectMembersRequestObject) (ListProjectMembersResponseObject, error) {
	members, err := a.h.ListProjectMembers.Handle(ctx, projectOf(ctx).project.ID)
	if err != nil {
		return nil, err
	}
	items := make([]ProjectMember, len(members))
	for i, m := range members {
		items[i] = ProjectMember{
			Member: openapi_types.UUID(m.Member.ID.Bytes()), Email: m.Member.Email, DisplayName: m.Member.DisplayName,
			Status: MemberStatus(m.Member.Status), Grants: each(m.Grants, grantOf),
		}
	}
	return ListProjectMembers200JSONResponse{Items: items}, nil
}

// ListGrants answers the grants of the project.
func (a *api) ListGrants(ctx context.Context, _ ListGrantsRequestObject) (ListGrantsResponseObject, error) {
	grants, err := a.h.ListGrants.Handle(ctx, projectOf(ctx).project.ID)
	if err != nil {
		return nil, err
	}
	return ListGrants200JSONResponse{Items: each(grants, grantOf)}, nil
}

// CreateGrant grants a member a role in the project.
func (a *api) CreateGrant(ctx context.Context, req CreateGrantRequestObject) (CreateGrantResponseObject, error) {
	g, err := a.h.CreateGrant.Handle(ctx, appaccess.CreateGrantCommand{
		Caller: callerOf(ctx), Project: projectRefOf(ctx),
		Member: id.FromBytes[access.Member](req.Body.Member), Role: access.RoleName(req.Body.Role), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CreateGrant201JSONResponse(grantOf(g)), nil
}

// DeleteGrant removes one grant of the project.
func (a *api) DeleteGrant(ctx context.Context, req DeleteGrantRequestObject) (DeleteGrantResponseObject, error) {
	grant, err := id.Parse[access.Grant](req.Grant)
	if err != nil {
		// Not an identifier, so no grant has it.
		return nil, appaccess.ErrGrantNotFound
	}
	if err := a.h.DeleteGrant.Handle(ctx, appaccess.DeleteGrantCommand{
		Project: projectRefOf(ctx), Grant: grant, Request: requestOf(ctx),
	}); err != nil {
		return nil, err
	}
	return DeleteGrant204Response{}, nil
}

func grantOf(g access.Grant) Grant {
	out := Grant{
		Id: openapi_types.UUID(g.ID.Bytes()), Member: openapi_types.UUID(g.Member.Bytes()), Role: RoleName(g.Role),
		Source: GrantSource(g.Source), CreatedAt: g.CreatedAt,
	}
	if !g.CreatedBy.IsZero() {
		by := openapi_types.UUID(g.CreatedBy.Bytes())
		out.CreatedBy = &by
	}
	return out
}
