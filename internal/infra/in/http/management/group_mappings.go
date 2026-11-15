package management

import (
	"context"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
)

// GetAuthProviderGroupMappings answers an auth provider's group mappings.
func (a *api) GetAuthProviderGroupMappings(ctx context.Context, req GetAuthProviderGroupMappingsRequestObject) (GetAuthProviderGroupMappingsResponseObject, error) {
	list, err := a.h.GetGroupMappings.Handle(ctx, req.Provider)
	if err != nil {
		return nil, err
	}
	out := GroupMappings{Items: make([]GroupMapping, len(list.Items))}
	for i, m := range list.Items {
		out.Items[i] = GroupMapping{Group: m.Group}
		if m.InstanceAdmin {
			out.Items[i].InstanceAdmin = new(true)
			continue
		}
		out.Items[i].Project, out.Items[i].Role = &m.ProjectKey, &m.Role
	}
	return GetAuthProviderGroupMappings200JSONResponse{Body: out, Headers: GetAuthProviderGroupMappings200ResponseHeaders{ETag: etag(list.Version)}}, nil
}

// ListAuthProviderScimGroups answers the groups an auth provider's directory
// pushed through SCIM, with their member counts.
func (a *api) ListAuthProviderScimGroups(ctx context.Context, req ListAuthProviderScimGroupsRequestObject) (ListAuthProviderScimGroupsResponseObject, error) {
	groups, err := a.h.ListProviderSCIMGroups.Handle(ctx, req.Provider)
	if err != nil {
		return nil, err
	}
	return ListAuthProviderScimGroups200JSONResponse{Items: each(groups, func(g appidentity.SCIMGroupSummary) ScimGroup {
		out := ScimGroup{Id: g.ID.Bytes(), DisplayName: g.DisplayName, MemberCount: g.MemberCount}
		if g.ExternalID != "" {
			out.ExternalId = &g.ExternalID
		}
		return out
	})}, nil
}

// ReplaceAuthProviderGroupMappings replaces an auth provider's group mappings
// and runs them for every member the provider knows.
func (a *api) ReplaceAuthProviderGroupMappings(ctx context.Context, req ReplaceAuthProviderGroupMappingsRequestObject) (ReplaceAuthProviderGroupMappingsResponseObject, error) {
	expected, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	items := make([]identity.GroupMappingInput, len(req.Body.Items))
	for i, m := range req.Body.Items {
		items[i] = identity.GroupMappingInput{
			Group: m.Group, Project: valueOf(m.Project), Role: valueOf(m.Role), InstanceAdmin: m.InstanceAdmin != nil && *m.InstanceAdmin,
		}
	}
	run, err := a.h.ReplaceGroupMappings.Handle(ctx, appidentity.ReplaceGroupMappingsCommand{
		Slug: req.Provider, Items: items, Expected: expected, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return ReplaceAuthProviderGroupMappings200JSONResponse{
		Body: GroupMappingsRun{Granted: run.Granted, Revoked: run.Revoked, KeptInstanceAdmin: run.KeptInstanceAdmin},
		Headers: ReplaceAuthProviderGroupMappings200ResponseHeaders{ETag: etag(run.Version)},
	}, nil
}
