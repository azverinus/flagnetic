package access_test

import (
	"crypto/rand"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
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
		// A name outside the catalog falls under no scope; the operation
		// table refuses such a requirement before any operation is served.
		{"project.update", false, ""},
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

func projectTokenCaller() access.Caller {
	tok := access.APIToken{Kind: access.TokenProject, Scopes: access.AllScopes()}
	return access.TokenCaller(tok, nil)
}

func refusal(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := apperrors.From(err); ok && e.Kind == apperrors.KindForbidden {
		return e.Code
	}
	return "not forbidden: " + err.Error()
}

func TestTheSeamJudgesReachThenSessionThenScopeThenPermission(t *testing.T) {
	getMe := access.Operation{Name: "getMe", Requirement: "member", Read: true, OpenToProjectTokens: true}
	authMethods := access.Operation{Name: "listAuthMethods", Requirement: "public", Read: true}
	updateProject := access.Operation{Name: "updateProject", Requirement: "project.update", Scope: access.ScopeProjectAdmin}
	updateMe := access.Operation{Name: "updateMe", Requirement: "member", SessionRequired: true}
	listMembers := access.Operation{Name: "listMembers", Requirement: "instance_admin", Read: true, Scope: access.ScopeRead}
	updateMember := access.Operation{Name: "updateMember", Requirement: "instance_admin", Scope: access.ScopeInstanceAdmin}
	listRoles := access.Operation{Name: "listRoles", Requirement: "member", Read: true}

	for name, tc := range map[string]struct {
		caller access.Caller
		op     access.Operation
		want   string
	}{
		"a token reads who it is":                       {tokenCaller(t, false), getMe, ""},
		"a project token reads who it is":               {projectTokenCaller(), getMe, ""},
		"a project token reaches nothing else":          {projectTokenCaller(), listRoles, "forbidden"},
		"a project token is refused before the session": {projectTokenCaller(), updateMe, "forbidden"},
		"a token never writes to its member's account":  {tokenCaller(t, true, access.AllScopes()...), updateMe, "session_required"},
		"an instance admin's read token reads members":  {tokenCaller(t, true), listMembers, ""},
		"an instance admin's read token writes nothing": {tokenCaller(t, true), updateMember, "insufficient_scope"},
		"a scope is judged before the permission":       {tokenCaller(t, false), updateMember, "insufficient_scope"},
		"an instance admin's admin token writes":        {tokenCaller(t, true, access.ScopeInstanceAdmin), updateMember, ""},
		"a member is no instance admin":                 {tokenCaller(t, false, access.ScopeInstanceAdmin), updateMember, "forbidden"},
		"a member reads what every member reads":        {tokenCaller(t, false), listRoles, ""},
		"anyone calls a public operation":               {access.Caller{}, authMethods, ""},
		"a token calls a public operation":              {tokenCaller(t, false), authMethods, ""},
		"a project token calls a public operation":      {projectTokenCaller(), authMethods, ""},
		"a scope is judged before a catalog permission": {tokenCaller(t, true), updateProject, "insufficient_scope"},
		"a project token's project is judged later":     {projectTokenCaller(), updateProject, "forbidden"},
		// Until a project is looked up and its grants judged, an operation
		// needing a catalog permission is open to nobody, an instance admin
		// included.
		"a catalog permission is refused for now": {tokenCaller(t, true, access.AllScopes()...), updateProject, "forbidden"},
	} {
		if got := refusal(access.Authorize(tc.caller, tc.op)); got != tc.want {
			t.Errorf("%s: refusal = %q, want %q", name, got, tc.want)
		}
	}
}

func TestARefusedPermissionIsNamed(t *testing.T) {
	op := access.Operation{Name: "updateMember", Requirement: "instance_admin", Scope: access.ScopeInstanceAdmin}
	e, _ := apperrors.From(access.Authorize(tokenCaller(t, false, access.ScopeInstanceAdmin), op))
	if e == nil || e.Extensions["permission"] != "instance_admin" {
		t.Errorf("refusal = %+v, want the permission instance_admin named", e)
	}
}

func TestACatalogPermissionIsRefusedNamingIt(t *testing.T) {
	op := access.Operation{Name: "updateProject", Requirement: "project.update", Scope: access.ScopeProjectAdmin}
	e, _ := apperrors.From(access.Authorize(tokenCaller(t, true, access.AllScopes()...), op))
	if e == nil || e.Code != "forbidden" || e.Extensions["permission"] != "project.update" {
		t.Errorf("refusal = %+v, want forbidden naming project.update", e)
	}
}
