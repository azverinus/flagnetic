package scim_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

const groupSchema = "urn:ietf:params:scim:schemas:core:2.0:Group"

// user provisions a member through token and returns their identifier.
func (e env) user(t *testing.T, token string) string {
	t.Helper()
	return e.mustCreate(t, token, userBody(email("example.com"), nil))
}

// memberRefs is members as a Group's members list.
func memberRefs(members ...string) []map[string]any {
	out := make([]map[string]any, len(members))
	for i, m := range members {
		out[i] = map[string]any{"value": m}
	}
	return out
}

func groupBody(name string, more map[string]any) map[string]any {
	body := map[string]any{"schemas": []string{groupSchema}, "displayName": name}
	for k, v := range more {
		body[k] = v
	}
	return body
}

// mustCreateGroup creates a group through token and returns its identifier.
func (e env) mustCreateGroup(t *testing.T, token string, body map[string]any) string {
	t.Helper()
	r := e.call(t, token, http.MethodPost, "/Groups", marshal(t, body))
	if r.status != http.StatusCreated {
		t.Fatalf("POST /Groups = %d: %s", r.status, r.body)
	}
	return r.json(t)["id"].(string)
}

// patch sends a PatchOp of ops to path and fails the test unless it is
// answered 200; it returns the resource answered.
func (e env) patch(t *testing.T, token, path string, ops ...map[string]any) map[string]any {
	t.Helper()
	r := e.call(t, token, http.MethodPatch, path, marshal(t, map[string]any{"schemas": []string{patchOpSchema}, "Operations": ops}))
	if r.status != http.StatusOK {
		t.Fatalf("PATCH %s = %d: %s", path, r.status, r.body)
	}
	return r.json(t)
}

// membersOf returns the members' identifiers of a Group as answered, sorted.
func membersOf(group map[string]any) []string {
	var out []string
	list, _ := group["members"].([]any)
	for _, m := range list {
		out = append(out, m.(map[string]any)["value"].(string))
	}
	slices.Sort(out)
	return out
}

func sorted(s ...string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

// project creates a project and returns its key.
func (e env) project(t *testing.T) string {
	t.Helper()
	key := "p" + random()
	res, err := e.admin.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: key, Name: "Project " + key})
	if err != nil || res.JSON201 == nil {
		t.Fatalf("creating a project: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	return key
}

func projectMapping(group, project, role string) management.GroupMapping {
	return management.GroupMapping{Group: group, Project: &project, Role: &role}
}

func adminMapping(group string) management.GroupMapping {
	return management.GroupMapping{Group: group, InstanceAdmin: ptr(true)}
}

// mapGroups replaces the group mappings of provider.
func (e env) mapGroups(t *testing.T, provider string, items ...management.GroupMapping) management.GroupMappingsRun {
	t.Helper()
	res, err := e.admin.ReplaceAuthProviderGroupMappingsWithResponse(t.Context(), provider, management.GroupMappings{Items: items})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("replacing the group mappings of %s: %v, %d: %s", provider, err, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// roles returns the roles member holds in project from source, sorted.
func (e env) roles(t *testing.T, project, member string, source management.GrantSource) []string {
	t.Helper()
	res, err := e.admin.ListGrantsWithResponse(t.Context(), project)
	if err != nil || res.JSON200 == nil {
		t.Fatalf("listing the grants of %s: %v, %d: %s", project, err, res.StatusCode(), res.Body)
	}
	var out []string
	for _, g := range res.JSON200.Items {
		if g.Member.String() == member && g.Source == source {
			out = append(out, string(g.Role))
		}
	}
	slices.Sort(out)
	return out
}

func (e env) wantRoles(t *testing.T, what, project, member string, source management.GrantSource, roles ...string) {
	t.Helper()
	if got := e.roles(t, project, member, source); !slices.Equal(got, sorted(roles...)) {
		t.Errorf("%s: %s roles in %s = %v, want %v", what, source, project, got, roles)
	}
}

func (e env) grantByHand(t *testing.T, project, member, role string) {
	t.Helper()
	res, err := e.admin.CreateGrantWithResponse(t.Context(), project, management.GrantCreate{Member: uuid.MustParse(member), Role: role})
	if err != nil || res.JSON201 == nil {
		t.Fatalf("granting by hand: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
}

// entries returns the instance's audit entries of action made by SCIM
// tokens, newest first.
func (e env) scimEntries(t *testing.T, action string) []management.AuditEntry {
	t.Helper()
	kind := management.ActorKindScim
	res, err := e.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action, ActorKind: &kind, Limit: ptr(200)})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit = %v, %d", err, res.StatusCode())
	}
	return res.JSON200.Items
}

// entriesAbout returns the entries of entries whose entity is entity.
func entriesAbout(entries []management.AuditEntry, entity string) []management.AuditEntry {
	var out []management.AuditEntry
	for _, en := range entries {
		if en.Entity.Id == entity {
			out = append(out, en)
		}
	}
	return out
}

func TestGroupsAreCreatedFilteredReadReplacedAndDeleted(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	token := e.token(t, slug)
	jo, sam := e.user(t, token), e.user(t, token)
	name := "eng-" + random()

	r := e.call(t, token, http.MethodPost, "/Groups", marshal(t, groupBody(name, map[string]any{
		"externalId": "okta-" + name, "members": memberRefs(jo, sam),
	})))
	group := r.json(t)
	gid, _ := group["id"].(string)
	if r.status != http.StatusCreated || r.header.Get("Location") != "/scim/v2/Groups/"+gid || group["displayName"] != name ||
		group["externalId"] != "okta-"+name || !slices.Equal(membersOf(group), sorted(jo, sam)) {
		t.Fatalf("POST /Groups = %d, Location %q: %s", r.status, r.header.Get("Location"), r.body)
	}
	if meta, _ := group["meta"].(map[string]any); meta["resourceType"] != "Group" || meta["location"] != "/scim/v2/Groups/"+gid {
		t.Errorf("meta = %v", group["meta"])
	}

	t.Run("a name or externalId the provider's groups hold is not unique", func(t *testing.T) {
		assertScimError(t, "same name", e.call(t, token, http.MethodPost, "/Groups", marshal(t, groupBody(name, nil))), http.StatusConflict, "uniqueness")
		assertScimError(t, "same externalId", e.call(t, token, http.MethodPost, "/Groups",
			marshal(t, groupBody("other-"+name, map[string]any{"externalId": "okta-" + name}))), http.StatusConflict, "uniqueness")
	})

	t.Run("only the provider's provisioned members join", func(t *testing.T) {
		nobody := e.call(t, token, http.MethodPost, "/Groups", marshal(t, groupBody("x-"+name, map[string]any{"members": memberRefs(newUUID())})))
		assertScimError(t, "an unknown member", nobody, http.StatusBadRequest, "invalidValue")
		notAnID := e.call(t, token, http.MethodPost, "/Groups", marshal(t, groupBody("x-"+name, map[string]any{"members": memberRefs("jo")})))
		assertScimError(t, "a value that is no id", notAnID, http.StatusBadRequest, "invalidValue")
		assertScimError(t, "no name", e.call(t, token, http.MethodPost, "/Groups", marshal(t, groupBody(" ", nil))), http.StatusBadRequest, "invalidValue")
		assertScimError(t, "a body that is no group", e.call(t, token, http.MethodPost, "/Groups", `{"displayName": 5}`), http.StatusBadRequest, "invalidSyntax")
		assertScimError(t, "a patch adding an unknown member", e.call(t, token, http.MethodPatch, "/Groups/"+gid, marshal(t, map[string]any{
			"schemas": []string{patchOpSchema}, "Operations": []map[string]any{{"op": "add", "path": "members", "value": memberRefs(newUUID())}},
		})), http.StatusBadRequest, "invalidValue")
	})

	t.Run("filters on displayName, externalId and id", func(t *testing.T) {
		for _, f := range []string{`displayName eq "` + name + `"`, `externalId eq "okta-` + name + `"`, `id eq "` + gid + `"`} {
			list := e.call(t, token, http.MethodGet, "/Groups?filter="+escape(f), "").json(t)
			res, _ := list["Resources"].([]any)
			if list["totalResults"] != float64(1) || len(res) != 1 || res[0].(map[string]any)["id"] != gid {
				t.Errorf("filter %s = %v, want the group", f, list)
			}
		}
		for _, f := range []string{`displayName eq "` + name + `x"`, `displayName eq ""`, `id eq "nope"`} {
			if list := e.call(t, token, http.MethodGet, "/Groups?filter="+escape(f), "").json(t); list["totalResults"] != float64(0) {
				t.Errorf("filter %s = %v, want none", f, list)
			}
		}
		for _, f := range []string{`members.value eq "` + jo + `"`, `userName eq "x@example.com"`, `displayName co "eng"`} {
			assertScimError(t, f, e.call(t, token, http.MethodGet, "/Groups?filter="+escape(f), ""), http.StatusBadRequest, "invalidFilter")
		}
		lean := e.call(t, token, http.MethodGet, "/Groups?excludedAttributes=members&filter="+escape(`displayName eq "`+name+`"`), "").json(t)
		if res := lean["Resources"].([]any); len(res) != 1 || res[0].(map[string]any)["members"] != nil {
			t.Errorf("excludedAttributes=members answered %v, want the group without members", lean)
		}
		one := e.call(t, token, http.MethodGet, "/Groups/"+gid+"?excludedAttributes=members", "").json(t)
		if one["members"] != nil || one["displayName"] != name {
			t.Errorf("GET with excludedAttributes=members = %v", one)
		}
		if page := e.call(t, token, http.MethodGet, "/Groups?count=0", "").json(t); page["totalResults"].(float64) < 1 || len(page["Resources"].([]any)) != 0 {
			t.Errorf("count=0 = %v, want the total alone", page)
		}
	})

	t.Run("a member's groups are derived and read only", func(t *testing.T) {
		u := e.call(t, token, http.MethodGet, "/Users/"+jo, "").json(t)
		groups, _ := u["groups"].([]any)
		if len(groups) != 1 || groups[0].(map[string]any)["value"] != gid || groups[0].(map[string]any)["display"] != name ||
			groups[0].(map[string]any)["$ref"] != "/scim/v2/Groups/"+gid {
			t.Errorf("the user's groups = %v, want the group", u["groups"])
		}
		listed := e.call(t, token, http.MethodGet, "/Users?filter="+escape(`id eq "`+sam+`"`), "").json(t)
		if g := listed["Resources"].([]any)[0].(map[string]any)["groups"].([]any); len(g) != 1 {
			t.Errorf("a listed user's groups = %v, want the group", g)
		}
		assertScimError(t, "PATCH a user's groups", e.call(t, token, http.MethodPatch, "/Users/"+jo, marshal(t, map[string]any{
			"schemas": []string{patchOpSchema}, "Operations": []map[string]any{{"op": "add", "path": "groups", "value": memberRefs(gid)}},
		})), http.StatusBadRequest, "mutability")
	})

	t.Run("a PUT replaces the name and members and keeps an externalId it does not send", func(t *testing.T) {
		r := e.call(t, token, http.MethodPut, "/Groups/"+gid, marshal(t, groupBody(name, map[string]any{"members": memberRefs(jo)})))
		if got := r.json(t); r.status != http.StatusOK || got["externalId"] != "okta-"+name || !slices.Equal(membersOf(got), []string{jo}) {
			t.Errorf("PUT = %d: %s", r.status, r.body)
		}
		if r := e.call(t, token, http.MethodPut, "/Groups/"+newUUID(), marshal(t, groupBody(name, nil))); r.status != http.StatusNotFound {
			t.Errorf("PUT of an unknown group = %d", r.status)
		}
		assertScimError(t, "a PUT that is no group", e.call(t, token, http.MethodPut, "/Groups/"+gid, `[`), http.StatusBadRequest, "invalidSyntax")
		assertScimError(t, "a PUT without a name", e.call(t, token, http.MethodPut, "/Groups/"+gid, marshal(t, groupBody("", nil))), http.StatusBadRequest, "invalidValue")
		e.mustCreateGroup(t, token, groupBody(name+"-taken", nil))
		assertScimError(t, "a PUT taking another group's name", e.call(t, token, http.MethodPut, "/Groups/"+gid,
			marshal(t, groupBody(name+"-taken", nil))), http.StatusConflict, "uniqueness")
		assertScimError(t, "a PATCH that is no PatchOp", e.call(t, token, http.MethodPatch, "/Groups/"+gid, `{"Operations": 5}`), http.StatusBadRequest, "invalidSyntax")
	})

	t.Run("discovery serves Group", func(t *testing.T) {
		types := e.call(t, token, http.MethodGet, "/ResourceTypes", "").json(t)
		schemas := e.call(t, token, http.MethodGet, "/Schemas", "").json(t)
		var endpoints, names []any
		for _, r := range types["Resources"].([]any) {
			endpoints = append(endpoints, r.(map[string]any)["endpoint"])
		}
		for _, r := range schemas["Resources"].([]any) {
			names = append(names, r.(map[string]any)["name"])
		}
		if !slices.Contains(endpoints, any("/Groups")) || !slices.Contains(names, any("Group")) {
			t.Errorf("discovery = %v, %v; want Group", endpoints, names)
		}
	})

	if r := e.call(t, token, http.MethodDelete, "/Groups/"+gid, ""); r.status != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", r.status, r.body)
	}
	assertScimError(t, "GET after DELETE", e.call(t, token, http.MethodGet, "/Groups/"+gid, ""), http.StatusNotFound, "")
	assertScimError(t, "DELETE after DELETE", e.call(t, token, http.MethodDelete, "/Groups/"+gid, ""), http.StatusNotFound, "")
	assertScimError(t, "PATCH after DELETE", e.call(t, token, http.MethodPatch, "/Groups/"+gid, marshal(t, map[string]any{
		"Operations": []map[string]any{{"op": "remove", "path": "members"}},
	})), http.StatusNotFound, "")
	assertScimError(t, "a group id that is none", e.call(t, token, http.MethodGet, "/Groups/nope", ""), http.StatusNotFound, "")
	if g := e.call(t, token, http.MethodGet, "/Users/"+jo, "").json(t)["groups"].([]any); len(g) != 0 {
		t.Errorf("a deleted group's member is still in %v", g)
	}

	for action, want := range map[string]int{"scim_group.created": 1, "scim_group.updated": 1, "scim_group.deleted": 1} {
		got := entriesAbout(e.scimEntries(t, action), gid)
		if len(got) != want {
			t.Errorf("%d %s entries, want %d", len(got), action, want)
			continue
		}
		if got[0].Actor.Kind != management.ActorKindScim || got[0].Entity.Key != name {
			t.Errorf("%s = %+v, want the group named by the SCIM token", action, got[0])
		}
	}
	updated := entriesAbout(e.scimEntries(t, "scim_group.updated"), gid)[0]
	if after, _ := updated.After.(map[string]any); !slices.Equal(anyStrings(after["members_removed"]), []string{sam}) {
		t.Errorf("scim_group.updated after = %v, want %s removed", updated.After, sam)
	}
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, len(list))
	for i, s := range list {
		out[i], _ = s.(string)
	}
	return out
}

// Microsoft Entra ID provisions a group as these requests, in this order:
// it looks for the group by name without its members, creates it empty,
// adds members with `Add`, renames it with `Replace` on a path, and removes
// a member with `Remove` and a value.
func TestEntraIDGroupRequests(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	token := e.token(t, slug)
	alpha, beta := e.project(t), e.project(t)
	name := "Engineering " + random()
	e.mapGroups(t, slug, projectMapping(name, alpha, "developer"), projectMapping("Ops "+name, beta, "viewer"))
	ada, bob := e.user(t, token), e.user(t, token)

	found := e.call(t, token, http.MethodGet, "/Groups?excludedAttributes=members&filter="+escape(`displayName eq "`+name+`"`), "")
	if found.status != http.StatusOK || found.json(t)["totalResults"] != float64(0) {
		t.Fatalf("the lookup = %d: %s", found.status, found.body)
	}
	gid := e.mustCreateGroup(t, token, map[string]any{
		"schemas": []string{groupSchema}, "externalId": newUUID(), "displayName": name, "members": []any{},
	})
	path := "/Groups/" + gid

	e.patch(t, token, path, map[string]any{"op": "Add", "path": "members", "value": memberRefs(ada, bob)})
	e.wantRoles(t, "after Add", alpha, ada, management.GrantSourceSso, "developer")
	e.wantRoles(t, "after Add", alpha, bob, management.GrantSourceSso, "developer")

	renamed := e.patch(t, token, path, map[string]any{"op": "Replace", "path": "displayName", "value": "Ops " + name})
	if renamed["displayName"] != "Ops "+name {
		t.Errorf("the rename answered %v", renamed)
	}
	e.wantRoles(t, "after the rename", alpha, ada, management.GrantSourceSso)
	e.wantRoles(t, "after the rename", beta, ada, management.GrantSourceSso, "viewer")

	left := e.patch(t, token, path, map[string]any{"op": "Remove", "path": "members", "value": memberRefs(bob)})
	if !slices.Equal(membersOf(left), []string{ada}) {
		t.Errorf("after Remove the members are %v, want %s", membersOf(left), ada)
	}
	e.wantRoles(t, "after Remove", beta, bob, management.GrantSourceSso)
	e.wantRoles(t, "after Remove", beta, ada, management.GrantSourceSso, "viewer")

	if r := e.call(t, token, http.MethodDelete, path, ""); r.status != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", r.status, r.body)
	}
	e.wantRoles(t, "after DELETE", beta, ada, management.GrantSourceSso)
}

// Okta pushes a group as these requests: it creates it with members (each
// with a display), finds it by name, renames it with a path-less replace that
// repeats the group's id, adds members, removes one by a filter on its value,
// replaces it whole and deletes it.
func TestOktaGroupRequests(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	token := e.token(t, slug)
	alpha, beta := e.project(t), e.project(t)
	name := "eng-" + random()
	e.mapGroups(t, slug, projectMapping(name, alpha, "developer"), projectMapping("renamed-"+name, beta, "maintainer"))
	ada, bob := e.user(t, token), e.user(t, token)

	gid := e.mustCreateGroup(t, token, map[string]any{
		"schemas": []string{groupSchema}, "displayName": name,
		"members": []map[string]any{{"value": ada, "display": "ada@example.com"}},
	})
	path := "/Groups/" + gid
	e.wantRoles(t, "after the create", alpha, ada, management.GrantSourceSso, "developer")

	list := e.call(t, token, http.MethodGet, "/Groups?filter="+escape(`displayName eq "`+name+`"`)+"&startIndex=1&count=100", "").json(t)
	if list["totalResults"] != float64(1) {
		t.Errorf("the lookup = %v", list)
	}

	e.patch(t, token, path, map[string]any{"op": "replace", "value": map[string]any{"id": gid, "displayName": "renamed-" + name}})
	e.wantRoles(t, "after the rename", alpha, ada, management.GrantSourceSso)
	e.wantRoles(t, "after the rename", beta, ada, management.GrantSourceSso, "maintainer")

	e.patch(t, token, path, map[string]any{"op": "add", "path": "members", "value": []map[string]any{{"value": bob, "display": "bob@example.com"}}})
	e.wantRoles(t, "after the add", beta, bob, management.GrantSourceSso, "maintainer")

	got := e.patch(t, token, path, map[string]any{"op": "remove", "path": `members[value eq "` + ada + `"]`})
	if !slices.Equal(membersOf(got), []string{bob}) {
		t.Errorf("after the removal by filter the members are %v, want %s", membersOf(got), bob)
	}
	e.wantRoles(t, "after the removal", beta, ada, management.GrantSourceSso)

	if r := e.call(t, token, http.MethodPut, path, marshal(t, groupBody("renamed-"+name, map[string]any{"members": []any{}}))); r.status != http.StatusOK {
		t.Fatalf("PUT = %d: %s", r.status, r.body)
	}
	e.wantRoles(t, "after the PUT", beta, bob, management.GrantSourceSso)

	if r := e.call(t, token, http.MethodDelete, path, ""); r.status != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", r.status, r.body)
	}
}

func TestGroupMembershipDrivesTheProvidersGroupMappings(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	token := e.token(t, slug)
	alpha, beta, gamma := e.project(t), e.project(t), e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"), projectMapping("ops", beta, "viewer"), adminMapping("it-admins"))
	dana := e.user(t, token)
	e.grantByHand(t, gamma, dana, "owner")

	gid := e.mustCreateGroup(t, token, groupBody("eng", nil))
	path := "/Groups/" + gid
	e.wantRoles(t, "an empty group", alpha, dana, management.GrantSourceSso)

	e.patch(t, token, path, map[string]any{"op": "add", "path": "members", "value": memberRefs(dana)})
	e.wantRoles(t, "joining", alpha, dana, management.GrantSourceSso, "developer")
	e.wantRoles(t, "joining", gamma, dana, management.GrantSourceManual, "owner")
	created := e.scimEntries(t, "grant.created")
	if len(created) == 0 || created[0].Actor.Kind != management.ActorKindScim {
		t.Errorf("grant.created by SCIM = %v, want the grant recorded for the SCIM token", created)
	} else if after, _ := created[0].After.(map[string]any); after["source"] != "sso" || after["provider"] != slug {
		t.Errorf("grant.created after = %v, want source sso and the provider", created[0].After)
	}

	e.patch(t, token, path, map[string]any{"op": "remove", "path": `members[value eq "` + dana + `"]`})
	e.wantRoles(t, "leaving", alpha, dana, management.GrantSourceSso)
	e.wantRoles(t, "leaving", gamma, dana, management.GrantSourceManual, "owner")
	if deleted := e.scimEntries(t, "grant.deleted"); len(deleted) == 0 || deleted[0].Actor.Kind != management.ActorKindScim {
		t.Errorf("grant.deleted by SCIM = %v, want the removal recorded for the SCIM token", deleted)
	}

	e.patch(t, token, path, map[string]any{"op": "add", "path": "members", "value": memberRefs(dana)})
	e.patch(t, token, path, map[string]any{"op": "replace", "value": map[string]any{"displayName": "ops"}})
	e.wantRoles(t, "renamed", alpha, dana, management.GrantSourceSso)
	e.wantRoles(t, "renamed", beta, dana, management.GrantSourceSso, "viewer")

	if r := e.call(t, token, http.MethodPut, path, marshal(t, groupBody("ops", nil))); r.status != http.StatusOK {
		t.Fatalf("PUT = %d: %s", r.status, r.body)
	}
	e.wantRoles(t, "a PUT without the member", beta, dana, management.GrantSourceSso)

	admins := e.mustCreateGroup(t, token, groupBody("it-admins", map[string]any{"members": memberRefs(dana)}))
	if !e.member(t, dana).InstanceAdmin {
		t.Error("it-admins did not make the member an instance admin")
	}
	if r := e.call(t, token, http.MethodDelete, "/Groups/"+admins, ""); r.status != http.StatusNoContent {
		t.Fatalf("DELETE = %d", r.status)
	}
	if e.member(t, dana).InstanceAdmin {
		t.Error("deleting it-admins left the member an instance admin")
	}

	e.patch(t, token, path, map[string]any{"op": "replace", "path": "displayName", "value": "eng"})
	e.patch(t, token, path, map[string]any{"op": "add", "path": "members", "value": memberRefs(dana)})
	e.wantRoles(t, "back in eng", alpha, dana, management.GrantSourceSso, "developer")
	if r := e.call(t, token, http.MethodDelete, path, ""); r.status != http.StatusNoContent {
		t.Fatalf("DELETE = %d", r.status)
	}
	e.wantRoles(t, "eng deleted", alpha, dana, management.GrantSourceSso)
	e.wantRoles(t, "eng deleted", gamma, dana, management.GrantSourceManual, "owner")

	sales := e.mustCreateGroup(t, token, groupBody("sales", map[string]any{"members": memberRefs(dana)}))
	if got := e.call(t, token, http.MethodGet, "/Groups/"+sales, "").json(t); len(membersOf(got)) != 1 {
		t.Errorf("an unmapped group = %v, want its member stored", got)
	}
	e.wantRoles(t, "an unmapped group", alpha, dana, management.GrantSourceSso)
	e.wantRoles(t, "an unmapped group", beta, dana, management.GrantSourceSso)

	// Editing the mappings runs them for the members of SCIM groups too.
	run := e.mapGroups(t, slug, projectMapping("sales", gamma, "viewer"))
	if run.Granted != 1 {
		t.Errorf("mapping sales granted %d, want 1", run.Granted)
	}
	e.wantRoles(t, "sales mapped", gamma, dana, management.GrantSourceSso, "viewer")
}

func TestAGroupNameIsUniquePerProviderAndATokenReachesOnlyItsProvidersGroups(t *testing.T) {
	e := start(t)
	a, b := e.provider(t), e.provider(t)
	tokenA, tokenB := e.token(t, a), e.token(t, b)
	alpha := e.project(t)
	ofA, ofB := e.user(t, tokenA), e.user(t, tokenB)

	// The same name under two providers is two groups.
	groupA := e.mustCreateGroup(t, tokenA, groupBody("admins", map[string]any{"externalId": "same", "members": memberRefs(ofA)}))
	groupB := e.mustCreateGroup(t, tokenB, groupBody("admins", map[string]any{"externalId": "same", "members": memberRefs(ofB)}))
	if groupA == groupB {
		t.Fatal("the two providers share one group")
	}

	assertScimError(t, "reading B's group", e.call(t, tokenA, http.MethodGet, "/Groups/"+groupB, ""), http.StatusNotFound, "")
	assertScimError(t, "patching B's group", e.call(t, tokenA, http.MethodPatch, "/Groups/"+groupB, marshal(t, map[string]any{
		"Operations": []map[string]any{{"op": "remove", "path": "members"}},
	})), http.StatusNotFound, "")
	assertScimError(t, "replacing B's group", e.call(t, tokenA, http.MethodPut, "/Groups/"+groupB, marshal(t, groupBody("admins", nil))), http.StatusNotFound, "")
	assertScimError(t, "deleting B's group", e.call(t, tokenA, http.MethodDelete, "/Groups/"+groupB, ""), http.StatusNotFound, "")
	if list := e.call(t, tokenA, http.MethodGet, "/Groups", "").json(t); list["totalResults"] != float64(1) {
		t.Errorf("A lists %v, want its own group alone", list)
	}
	assertScimError(t, "adding B's member", e.call(t, tokenA, http.MethodPatch, "/Groups/"+groupA, marshal(t, map[string]any{
		"Operations": []map[string]any{{"op": "add", "path": "members", "value": memberRefs(ofB)}},
	})), http.StatusBadRequest, "invalidValue")

	// B maps admins to instance admin and A maps it to a project: A's group
	// grants only through A's mappings, whoever A's token puts in it.
	e.mapGroups(t, b, adminMapping("admins"))
	e.mapGroups(t, a, projectMapping("admins", alpha, "viewer"))
	if e.member(t, ofA).InstanceAdmin {
		t.Fatal("A's group made A's member an instance admin through B's mapping")
	}
	if !e.member(t, ofB).InstanceAdmin {
		t.Error("B's mapping did not make B's member an instance admin")
	}
	other := e.user(t, tokenA)
	e.patch(t, tokenA, "/Groups/"+groupA, map[string]any{"op": "add", "path": "members", "value": memberRefs(other)})
	if e.member(t, other).InstanceAdmin {
		t.Error("A's token made an instance admin through B's mapping")
	}
	e.wantRoles(t, "A's mapping", alpha, other, management.GrantSourceSso, "viewer")
	if g := e.call(t, tokenB, http.MethodGet, "/Groups/"+groupB, "").json(t); !slices.Equal(membersOf(g), []string{ofB}) {
		t.Errorf("B's group = %v, want B's member alone", g)
	}
}

func TestAMemberDeactivatedThroughSCIMLosesTheirMappedAccessAndRegainsItWhenReactivated(t *testing.T) {
	e := start(t)
	slug := e.provider(t)
	token := e.token(t, slug)
	alpha := e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"))
	kim := e.user(t, token)
	e.grantByHand(t, alpha, kim, "viewer")
	e.mustCreateGroup(t, token, groupBody("eng", map[string]any{"members": memberRefs(kim)}))
	e.wantRoles(t, "in eng", alpha, kim, management.GrantSourceSso, "developer")

	active := func(v any) {
		t.Helper()
		e.patch(t, token, "/Users/"+kim, map[string]any{"op": "replace", "value": map[string]any{"active": v}})
	}
	active(false)
	e.wantRoles(t, "deactivated", alpha, kim, management.GrantSourceSso)
	e.wantRoles(t, "deactivated", alpha, kim, management.GrantSourceManual, "viewer")
	active(true)
	e.wantRoles(t, "reactivated", alpha, kim, management.GrantSourceSso, "developer")

	if r := e.call(t, token, http.MethodPut, "/Users/"+kim, marshal(t, userBody(e.member(t, kim).Email, map[string]any{"active": false}))); r.status != http.StatusOK {
		t.Fatalf("PUT active false = %d: %s", r.status, r.body)
	}
	e.wantRoles(t, "deactivated by PUT", alpha, kim, management.GrantSourceSso)
	active("True")
	e.wantRoles(t, "reactivated by Entra's string", alpha, kim, management.GrantSourceSso, "developer")

	if r := e.call(t, token, http.MethodDelete, "/Users/"+kim, ""); r.status != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", r.status, r.body)
	}
	e.wantRoles(t, "deleted", alpha, kim, management.GrantSourceSso)
	if deleted := e.scimEntries(t, "grant.deleted"); len(deleted) == 0 || deleted[0].Actor.Kind != management.ActorKindScim {
		t.Errorf("grant.deleted = %v, want the removal recorded for the SCIM token", deleted)
	}
}

func TestAnInstanceAdminReadsAProvidersSCIMGroupsWithTheirMemberCounts(t *testing.T) {
	e := start(t)
	a, b := e.provider(t), e.provider(t)
	tokenA, tokenB := e.token(t, a), e.token(t, b)
	one, two := e.user(t, tokenA), e.user(t, tokenA)

	e.mustCreateGroup(t, tokenA, groupBody("platform", map[string]any{"externalId": "dir-7", "members": memberRefs(one, two)}))
	e.mustCreateGroup(t, tokenA, groupBody("admins", nil))
	e.mustCreateGroup(t, tokenB, groupBody("elsewhere", map[string]any{"members": memberRefs(e.user(t, tokenB))}))

	res, err := e.admin.ListAuthProviderScimGroupsWithResponse(t.Context(), a)
	if err != nil || res.JSON200 == nil {
		t.Fatalf("listing the SCIM groups of %s: %v, %d: %s", a, err, res.StatusCode(), res.Body)
	}
	type row struct {
		name     string
		external string
		members  int
	}
	var got []row
	for _, g := range res.JSON200.Items {
		got = append(got, row{g.DisplayName, deref(g.ExternalId), g.MemberCount})
	}
	want := []row{{"admins", "", 0}, {"platform", "dir-7", 2}}
	if !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v: the provider's own, by name, with their members counted", got, want)
	}

	// A member leaving a group is no longer counted.
	groupID := e.call(t, tokenA, http.MethodGet, `/Groups?filter=displayName%20eq%20%22platform%22`, "").json(t)["Resources"].([]any)[0].(map[string]any)["id"].(string)
	e.patch(t, tokenA, "/Groups/"+groupID, map[string]any{"op": "remove", "path": "members[value eq \"" + one + "\"]"})
	res, _ = e.admin.ListAuthProviderScimGroupsWithResponse(t.Context(), a)
	if res.JSON200 == nil || res.JSON200.Items[1].MemberCount != 1 {
		t.Errorf("after a removal = %d %s, want platform with 1 member", res.StatusCode(), res.Body)
	}

	empty, _ := e.admin.ListAuthProviderScimGroupsWithResponse(t.Context(), e.provider(t))
	if empty.JSON200 == nil || len(empty.JSON200.Items) != 0 {
		t.Errorf("a provider without groups = %d %s, want an empty list", empty.StatusCode(), empty.Body)
	}
	missing, _ := e.admin.ListAuthProviderScimGroupsWithResponse(t.Context(), "nowhere")
	if missing.StatusCode() != http.StatusNotFound {
		t.Errorf("an unknown provider = %d, want 404", missing.StatusCode())
	}

	member := testkit.NewMember(t, testkit.Pool(t))
	b2 := testkit.NewBrowser(t, e.srv.URL)
	b2.SignIn(t, member.Email, member.Password)
	if denied, _ := b2.ListAuthProviderScimGroupsWithResponse(t.Context(), a); denied.StatusCode() != http.StatusForbidden {
		t.Errorf("a member listing = %d, want 403", denied.StatusCode())
	}
}

func deref[T any](p *T) (v T) {
	if p != nil {
		v = *p
	}
	return v
}
