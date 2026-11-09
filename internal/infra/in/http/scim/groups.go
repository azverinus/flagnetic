package scim

import (
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func groupID(g GroupID) identity.SCIMGroupID { return id.FromBytes[identity.SCIMGroup]([16]byte(g)) }

// groupRefs is the groups a User is in, as /Users answers them.
func groupRefs(groups []appidentity.GroupRef) *[]ResourceRef {
	out := make([]ResourceRef, len(groups))
	for i, g := range groups {
		gid := g.ID.String()
		out[i] = ResourceRef{Value: gid, Display: new(g.DisplayName), Ref: new(BaseURL + "/Groups/" + gid)}
	}
	return &out
}

// groupOut is g as /Groups answers it, its members left out when
// withMembers is false.
func groupOut(g appidentity.SCIMGroupRecord, withMembers bool) Group {
	gid := uuid.UUID(g.ID.Bytes())
	out := Group{
		Schemas: &[]string{identity.GroupSchema}, Id: &gid, DisplayName: &g.DisplayName,
		Meta: &Meta{
			ResourceType: new("Group"), Created: &g.CreatedAt, LastModified: &g.UpdatedAt,
			Location: new(BaseURL + "/Groups/" + gid.String()),
		},
	}
	if g.ExternalID != "" {
		out.ExternalId = &g.ExternalID
	}
	if withMembers {
		members := make([]ResourceRef, len(g.Members))
		for i, m := range g.Members {
			members[i] = ResourceRef{Value: m.String(), Ref: new(BaseURL + "/Users/" + m.String())}
		}
		out.Members = &members
	}
	return out
}

// wantsMembers reports whether excludedAttributes leaves the members in.
// Directories list groups without them, which is all they need to find one.
func wantsMembers(excluded *string) bool {
	if excluded == nil {
		return true
	}
	for name := range strings.SplitSeq(*excluded, ",") {
		if strings.EqualFold(strings.TrimSpace(name), "members") {
			return false
		}
	}
	return true
}

// groupState reads the group a POST or a PUT states.
func groupState(g Group) (identity.GroupState, error) {
	var members []string
	if g.Members != nil {
		for _, m := range *g.Members {
			members = append(members, m.Value)
		}
	}
	name, external := "", ""
	if g.DisplayName != nil {
		name = *g.DisplayName
	}
	if g.ExternalId != nil {
		external = *g.ExternalId
	}
	return identity.NewGroupState(name, external, members)
}

// ListGroups answers a page of the token's provider's groups.
func (a *api) ListGroups(w http.ResponseWriter, r *http.Request, params ListGroupsParams) {
	startIndex, count := 1, defaultCount
	if params.StartIndex != nil {
		startIndex = max(*params.StartIndex, 1)
	}
	if params.Count != nil {
		count = min(max(*params.Count, 0), defaultCount)
	}
	raw := ""
	if params.Filter != nil {
		raw = *params.Filter
	}
	page, err := a.h.ListGroups.Handle(r.Context(), callerOf(r), raw, startIndex, count)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	withMembers := wantsMembers(params.ExcludedAttributes)
	resources := make([]Group, len(page.Groups))
	for i, g := range page.Groups {
		resources[i] = groupOut(g, withMembers)
	}
	write(w, http.StatusOK, GroupListResponse{
		Schemas: []string{listSchema}, TotalResults: page.Total, StartIndex: &startIndex, ItemsPerPage: new(len(resources)),
		Resources: slices.Clip(resources),
	})
}

// GetGroup answers one group.
func (a *api) GetGroup(w http.ResponseWriter, r *http.Request, group GroupID, params GetGroupParams) {
	g, err := a.h.GetGroup.Handle(r.Context(), callerOf(r), groupID(group))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, http.StatusOK, groupOut(g, wantsMembers(params.ExcludedAttributes)))
}

// CreateGroup creates a group.
func (a *api) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var body Group
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err)
		return
	}
	state, err := groupState(body)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	g, err := a.h.CreateGroup.Handle(r.Context(), requestOf(r), state)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Location", BaseURL+"/Groups/"+g.ID.String())
	write(w, http.StatusCreated, groupOut(g, true))
}

// ReplaceGroup replaces a group.
func (a *api) ReplaceGroup(w http.ResponseWriter, r *http.Request, group GroupID) {
	var body Group
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err)
		return
	}
	state, err := groupState(body)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	g, err := a.h.ReplaceGroup.Handle(r.Context(), requestOf(r), groupID(group), state, body.ExternalId == nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, http.StatusOK, groupOut(g, true))
}

// PatchGroup applies a PatchOp to a group.
func (a *api) PatchGroup(w http.ResponseWriter, r *http.Request, group GroupID) {
	var body PatchOp
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err)
		return
	}
	g, err := a.h.PatchGroup.Handle(r.Context(), requestOf(r), groupID(group), patchOperations(body))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, http.StatusOK, groupOut(g, true))
}

// DeleteGroup deletes a group.
func (a *api) DeleteGroup(w http.ResponseWriter, r *http.Request, group GroupID) {
	if err := a.h.DeleteGroup.Handle(r.Context(), requestOf(r), groupID(group)); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
