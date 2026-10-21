package access_test

import (
	"crypto/rand"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestTheCatalogNamesEveryPermissionOnceWithAScopeAndTheOwnerHoldsEach(t *testing.T) {
	owner, ok := access.RoleNamed(access.RoleOwner)
	if !ok {
		t.Fatal("no owner role")
	}
	seen := map[access.Permission]bool{}
	for _, p := range access.Catalog() {
		if seen[p.Name] {
			t.Errorf("%s listed twice", p.Name)
		}
		seen[p.Name] = true
		if !slices.Contains(access.AllScopes(), p.Scope) {
			t.Errorf("%s falls under %q, not a scope a token carries", p.Name, p.Scope)
		}
		if !slices.Contains(owner.Permissions, p.Name) {
			t.Errorf("the owner role does not hold %s", p.Name)
		}
	}
}

func TestTheBuiltInRolesAreFixed(t *testing.T) {
	var names []access.RoleName
	for _, r := range access.Roles() {
		names = append(names, r.Name)
		for _, p := range r.Permissions {
			if _, ok := access.PermissionNamed(string(p)); !ok {
				t.Errorf("role %s holds %s, which the catalog does not", r.Name, p)
			}
		}
	}
	want := []access.RoleName{"owner", "maintainer", "developer", "viewer"}
	if !slices.Equal(names, want) {
		t.Errorf("roles = %v, want %v", names, want)
	}
	if _, ok := access.RoleNamed("admin"); ok {
		t.Error("a role named admin exists")
	}
}

func TestARequirementIsACatalogPermissionOrAReservedValue(t *testing.T) {
	for _, r := range []access.Requirement{access.RequirePublic, access.RequireMember, access.RequireInstanceAdmin} {
		if !access.IsRequirement(r) {
			t.Errorf("%s is not accepted", r)
		}
	}
	for _, r := range []access.Requirement{"", "owner", "project.delete", "PUBLIC"} {
		if access.IsRequirement(r) {
			t.Errorf("%q is accepted", r)
		}
	}
}

func TestTheScopeOfAnOperationFollowsItsRequirement(t *testing.T) {
	for _, tc := range []struct {
		requirement access.Requirement
		read        bool
		want        access.Scope
	}{
		{"public", false, ""},
		{"member", false, ""},
		{"member", true, ""},
		{"instance_admin", true, access.ScopeRead},
		{"instance_admin", false, access.ScopeInstanceAdmin},
		{"project.read", true, access.ScopeRead},
		{"project.update", false, access.ScopeProjectAdmin},
		{"declaration.manage", false, access.ScopeFlagsWrite},
		// A name outside the catalog falls under no scope; the operation
		// table refuses such a requirement before any operation is served.
		{"project.delete", false, ""},
	} {
		if got := access.ScopeOf(tc.requirement, tc.read); got != tc.want {
			t.Errorf("ScopeOf(%s, read %v) = %q, want %q", tc.requirement, tc.read, got, tc.want)
		}
	}
}

// tokenCaller returns a caller presenting a personal access token with
// scopes, held by a member who is an instance admin or not.
func tokenCaller(t *testing.T, instanceAdmin bool, scopes ...access.Scope) access.Caller {
	t.Helper()
	ids := newIDs()
	member := access.Member{ID: id.New[access.Member](ids), Status: access.MemberActive, InstanceAdmin: instanceAdmin}
	tok, _, err := access.NewPersonalToken(id.New[access.APIToken](ids), access.NewPersonalTokenInput{
		Member: member.ID, Name: "t", Scopes: scopes, Now: now,
	}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return access.TokenCaller(tok, &member)
}

// projectTokenCaller returns a caller presenting a project token of
// project holding role, with scopes.
func projectTokenCaller(project workspace.ProjectID, role access.RoleName, scopes ...access.Scope) access.Caller {
	if len(scopes) == 0 {
		scopes = access.AllScopes()
	}
	tok := access.APIToken{Kind: access.TokenProject, Project: project, Role: role, Scopes: scopes}
	return access.TokenCaller(tok, nil)
}

// sessionCaller returns a caller signed in as a member, an instance admin or
// not.
func sessionCaller(instanceAdmin bool) access.Caller {
	m := access.Member{ID: id.New[access.Member](newIDs()), Status: access.MemberActive, InstanceAdmin: instanceAdmin}
	return access.Caller{Method: access.MethodSession, Member: &m}
}

func refusal(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := apperrors.From(err); ok && (e.Kind == apperrors.KindForbidden || e.Kind == apperrors.KindNotFound) {
		return e.Code
	}
	return "not forbidden: " + err.Error()
}

func TestTheSeamJudgesReachThenSessionThenScopeThenProjectThenPermission(t *testing.T) {
	getMe := access.Operation{Name: "getMe", Requirement: "member", Read: true, OpenToProjectTokens: true}
	authMethods := access.Operation{Name: "listAuthMethods", Requirement: "public", Read: true}
	updateProject := access.Operation{Name: "updateProject", Requirement: "project.update", Scope: access.ScopeProjectAdmin}
	createProjectToken := access.Operation{Name: "createProjectToken", Requirement: "project_token.manage",
		SessionRequired: true, Scope: access.ScopeProjectAdmin}
	updateMe := access.Operation{Name: "updateMe", Requirement: "member", SessionRequired: true}
	listMembers := access.Operation{Name: "listMembers", Requirement: "instance_admin", Read: true, Scope: access.ScopeRead}
	updateMember := access.Operation{Name: "updateMember", Requirement: "instance_admin", Scope: access.ScopeInstanceAdmin}
	listRoles := access.Operation{Name: "listRoles", Requirement: "member", Read: true}

	ids := newIDs()
	mobile, web := id.New[workspace.Project](ids), id.New[workspace.Project](ids)
	holding := func(p workspace.ProjectID, perms ...access.Permission) *access.ProjectAccess {
		return &access.ProjectAccess{Project: p, Permissions: perms}
	}
	unknown := &access.ProjectAccess{}
	ownerOfMobile := projectTokenCaller(mobile, access.RoleOwner)

	for name, tc := range map[string]struct {
		caller access.Caller
		op     access.Operation
		in     *access.ProjectAccess
		want   string
	}{
		"a token reads who it is":                       {tokenCaller(t, false), getMe, nil, ""},
		"a project token reads who it is":               {ownerOfMobile, getMe, nil, ""},
		"a project token reaches nothing else":          {ownerOfMobile, listRoles, nil, "forbidden"},
		"a project token is refused before the session": {ownerOfMobile, updateMe, nil, "forbidden"},
		"a token never writes to its member's account":  {tokenCaller(t, true, access.AllScopes()...), updateMe, nil, "session_required"},
		"an instance admin's read token reads members":  {tokenCaller(t, true), listMembers, nil, ""},
		"an instance admin's read token writes nothing": {tokenCaller(t, true), updateMember, nil, "insufficient_scope"},
		"a scope is judged before the permission":       {tokenCaller(t, false), updateMember, nil, "insufficient_scope"},
		"an instance admin's admin token writes":        {tokenCaller(t, true, access.ScopeInstanceAdmin), updateMember, nil, ""},
		"a member is no instance admin":                 {tokenCaller(t, false, access.ScopeInstanceAdmin), updateMember, nil, "forbidden"},
		"a member reads what every member reads":        {tokenCaller(t, false), listRoles, nil, ""},
		"anyone calls a public operation":               {access.Caller{}, authMethods, nil, ""},
		"a token calls a public operation":              {tokenCaller(t, false), authMethods, nil, ""},
		"a project token calls a public operation":      {ownerOfMobile, authMethods, nil, ""},

		"a scope is judged before the project": {tokenCaller(t, true), updateProject, unknown, "insufficient_scope"},
		"an unknown project is not found":      {tokenCaller(t, false, access.ScopeProjectAdmin), updateProject, unknown, "not_found"},
		"the permission held is enough":        {sessionCaller(false), updateProject, holding(mobile, "project.update"), ""},
		"a permission not held is refused": {sessionCaller(false), updateProject,
			holding(mobile, "project.read"), "forbidden"},
		"a project token acts in its project": {ownerOfMobile, updateProject,
			holding(mobile, "project.update"), ""},
		"a project token reaches no other project": {ownerOfMobile, updateProject,
			holding(web, "project.update"), "forbidden"},
		// Answered like another project's, so a project token learns
		// nothing of which keys exist.
		"a project token reaches no unknown project": {ownerOfMobile, updateProject, unknown, "forbidden"},
		"a session is judged before the project": {tokenCaller(t, true, access.AllScopes()...), createProjectToken,
			unknown, "session_required"},
		// The project is judged in the seam; an operation needing a catalog
		// permission judged without it is open to nobody.
		"a catalog permission without its project is refused": {sessionCaller(true), updateProject, nil, "forbidden"},
	} {
		if got := refusal(access.Authorize(tc.caller, tc.op, tc.in)); got != tc.want {
			t.Errorf("%s: refusal = %q, want %q", name, got, tc.want)
		}
	}
}

func TestARefusedPermissionIsNamed(t *testing.T) {
	op := access.Operation{Name: "updateMember", Requirement: "instance_admin", Scope: access.ScopeInstanceAdmin}
	e, _ := apperrors.From(access.Authorize(tokenCaller(t, false, access.ScopeInstanceAdmin), op, nil))
	if e == nil || e.Extensions["permission"] != "instance_admin" {
		t.Errorf("refusal = %+v, want the permission instance_admin named", e)
	}
}

func TestACatalogPermissionIsRefusedNamingIt(t *testing.T) {
	op := access.Operation{Name: "updateProject", Requirement: "project.update", Scope: access.ScopeProjectAdmin}
	in := &access.ProjectAccess{Project: id.New[workspace.Project](newIDs()), Permissions: []access.Permission{"project.read"}}
	e, _ := apperrors.From(access.Authorize(sessionCaller(false), op, in))
	if e == nil || e.Code != "forbidden" || e.Extensions["permission"] != "project.update" {
		t.Errorf("refusal = %+v, want forbidden naming project.update", e)
	}
}
