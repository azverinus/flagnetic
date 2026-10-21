package management_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// grant grants m role in project through c, failing the test unless it is
// granted.
func grant(t *testing.T, c *management.ClientWithResponses, project string, m access.MemberID, role string) management.Grant {
	t.Helper()
	res := do(c.CreateGrantWithResponse(t.Context(), project, management.GrantCreate{Member: m.Bytes(), Role: role}))
	if res.JSON201 == nil {
		t.Fatalf("POST /projects/%s/grants (%s) = %d: %s", project, role, res.StatusCode(), res.Body)
	}
	return *res.JSON201
}

// projectMembers returns the members holding grants in project, as c lists
// them.
func projectMembers(t *testing.T, c *management.ClientWithResponses, project string) []management.ProjectMember {
	t.Helper()
	res := do(c.ListProjectMembersWithResponse(t.Context(), project))
	if res.JSON200 == nil {
		t.Fatalf("GET /projects/%s/members = %d: %s", project, res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// projectMe returns what c's caller holds in project.
func projectMe(t *testing.T, c *management.ClientWithResponses, project string) management.ProjectMe {
	t.Helper()
	res := do(c.GetProjectMeWithResponse(t.Context(), project))
	if res.JSON200 == nil {
		t.Fatalf("GET /projects/%s/me = %d: %s", project, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func rolesOf(grants []management.Grant) []string {
	out := make([]string, len(grants))
	for i, g := range grants {
		out[i] = string(g.Role)
	}
	return out
}

func TestEveryRoleIsGrantedAndListed(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	p := createProject(t, admin, projectKey())
	byRole := map[string]testkit.Member{}
	for _, role := range []string{"owner", "maintainer", "developer", "viewer"} {
		byRole[role] = testkit.NewMember(t, pool, testkit.WithEmail(role+"-"+strings.ToLower(projectKey())+"@example.com"))
		g := grant(t, admin, p.Key, byRole[role].ID, role)
		if g.Member != byRole[role].ID.Bytes() || string(g.Role) != role || g.Source != management.GrantSourceManual ||
			g.CreatedBy == nil || *g.CreatedBy != adminID || g.CreatedAt.IsZero() {
			t.Errorf("%s: grant = %+v, want it made by hand by the instance admin", role, g)
		}
	}
	// A second grant sits beside the first.
	grant(t, admin, p.Key, byRole["viewer"].ID, "developer")

	members := projectMembers(t, admin, p.Key)
	var emails []string
	for _, m := range members {
		emails = append(emails, m.Email)
		if m.Status != management.MemberStatusActive || m.DisplayName != "Test Member" {
			t.Errorf("%s listed as %+v", m.Email, m)
		}
	}
	want := []string{byRole["developer"].Email, byRole["maintainer"].Email, byRole["owner"].Email, byRole["viewer"].Email}
	if !slices.Equal(emails, want) {
		t.Errorf("members = %v, want %v ordered by email", emails, want)
	}
	if got := rolesOf(members[3].Grants); !slices.Equal(got, []string{"viewer", "developer"}) {
		t.Errorf("the viewer's grants = %v, want viewer then developer", got)
	}
	grants := do(admin.ListGrantsWithResponse(t.Context(), p.Key))
	if grants.JSON200 == nil || len(grants.JSON200.Items) != 5 {
		t.Errorf("GET grants = %d %s, want five grants", grants.StatusCode(), grants.Body)
	}
	if other := projectMembers(t, admin, createProject(t, admin, projectKey()).Key); len(other) != 0 {
		t.Errorf("another project lists %+v", other)
	}
}

func TestGrantingIsRefusedTwiceAndForUnknownMembersAndRoles(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	m := testkit.NewMember(t, pool)
	grant(t, admin, p.Key, m.ID, "developer")
	gone := testkit.NewMember(t, pool, testkit.WithStatus(access.MemberDeactivated))

	for name, tc := range map[string]struct {
		body               management.GrantCreate
		status             int
		code               string
		pointer, violation string
	}{
		"the same role twice":  {management.GrantCreate{Member: m.ID.Bytes(), Role: "developer"}, http.StatusConflict, "already_granted", "", ""},
		"an unknown member":    {management.GrantCreate{Member: openapi_types.UUID{1}, Role: "viewer"}, http.StatusUnprocessableEntity, "invalid_grant", "/member", "unknown_member"},
		"an unknown role":      {management.GrantCreate{Member: m.ID.Bytes(), Role: "admin"}, http.StatusUnprocessableEntity, "invalid_grant", "/role", "unknown_role"},
		"a deactivated member": {management.GrantCreate{Member: gone.ID.Bytes(), Role: "viewer"}, http.StatusConflict, "member_deactivated", "", ""},
	} {
		res := do(admin.CreateGrantWithResponse(t.Context(), p.Key, tc.body))
		pr := assertProblem(t, name, res.StatusCode(), res.Body, tc.status, tc.code)
		if tc.pointer != "" && (pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != tc.pointer ||
			(*pr.Errors)[0].Code != tc.violation) {
			t.Errorf("%s: violations = %+v, want %s at %s", name, pr.Errors, tc.violation, tc.pointer)
		}
	}
	if got := projectMembers(t, admin, p.Key); len(got) != 1 || len(got[0].Grants) != 1 {
		t.Errorf("members = %+v, want the one grant made", got)
	}
	unknown := do(admin.ListProjectMembersWithResponse(t.Context(), "no-such-project"))
	assertProblem(t, "an unknown project", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
	unknownGrant := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, "not-an-identifier"))
	assertProblem(t, "an unknown grant", unknownGrant.StatusCode(), unknownGrant.Body, http.StatusNotFound, "not_found")
}

func TestOnlyAnOwnerManagesGrantsAndEveryGrantHolderReadsThem(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p, other := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	target := testkit.NewMember(t, pool)

	for _, role := range []access.RoleName{access.RoleViewer, access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner} {
		c := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), role))
		if res := do(c.ListProjectMembersWithResponse(t.Context(), p.Key)); res.JSON200 == nil {
			t.Errorf("%s listing members = %d: %s", role, res.StatusCode(), res.Body)
		}
		res := do(c.CreateGrantWithResponse(t.Context(), p.Key, management.GrantCreate{Member: target.ID.Bytes(), Role: "viewer"}))
		if role != access.RoleOwner {
			pr := assertProblem(t, string(role), res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
			if pr.Permission == nil || *pr.Permission != "grant.manage" {
				t.Errorf("%s: refusal names %v, want grant.manage", role, pr.Permission)
			}
			continue
		}
		if res.JSON201 == nil {
			t.Fatalf("an owner granting = %d: %s", res.StatusCode(), res.Body)
		}
		if del := do(c.DeleteGrantWithResponse(t.Context(), p.Key, res.JSON201.Id.String())); del.StatusCode() != http.StatusNoContent {
			t.Errorf("an owner removing a grant = %d: %s", del.StatusCode(), del.Body)
		}
	}
	stranger := testkit.Client(t, srv, testkit.MemberToken(t, pool))
	res := do(stranger.ListProjectMembersWithResponse(t.Context(), p.Key))
	if pr := assertProblem(t, "a stranger", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden"); pr.Permission == nil ||
		*pr.Permission != "project.read" {
		t.Errorf("a stranger's refusal names %v, want project.read", pr.Permission)
	}
	elsewhere := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(other), access.RoleOwner))
	cross := do(elsewhere.CreateGrantWithResponse(t.Context(), p.Key, management.GrantCreate{Member: target.ID.Bytes(), Role: "viewer"}))
	assertProblem(t, "the owner of another project", cross.StatusCode(), cross.Body, http.StatusForbidden, "forbidden")
}

func TestAnInstanceAdminWithoutAGrantActsAsOwner(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	a := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
	b := signedIn(t, srv, a)
	p := createProject(t, b.ClientWithResponses, projectKey())
	m := testkit.NewMember(t, pool)

	grant(t, b.ClientWithResponses, p.Key, m.ID, "maintainer")
	own := grant(t, b.ClientWithResponses, p.Key, a.ID, "viewer")
	if e := auditOf(t, srv, "grant.created", own.Id.String()); len(e) != 1 || e[0].Entity.Key != a.Email {
		t.Errorf("the instance admin's own grant audited as %+v", e)
	}
	if got := projectMe(t, b.ClientWithResponses, p.Key); len(got.Permissions) != len(access.Catalog()) ||
		!slices.Equal(rolesOf(got.Grants), []string{"viewer"}) {
		t.Errorf("the instance admin holds %+v, want every permission beside their viewer grant", got)
	}
	// Holding a viewer grant takes nothing away from an instance admin.
	grant(t, b.ClientWithResponses, p.Key, m.ID, "owner")
	if got := projectMembers(t, b.ClientWithResponses, p.Key); len(got) != 2 {
		t.Errorf("members = %+v, want the member and the instance admin", got)
	}
}

func TestAMembersPermissionsAreTheUnionOfTheirGrantsAndRemovingOneKeepsTheOthers(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	m := testkit.NewMember(t, pool)
	mc := testkit.Client(t, srv, testkit.PersonalToken(t, pool, m.ID, "script", access.ScopeRead, access.ScopeFlagsWrite,
		access.ScopeProjectAdmin))

	grant(t, admin, p.Key, m.ID, "developer")
	maintainer := grant(t, admin, p.Key, m.ID, "maintainer")
	if got := projectMe(t, mc, p.Key); !slices.Equal(got.Permissions, []string{"project.read", "environment.manage", "declaration.manage", "flag.edit", "flag.archive", "flag_configuration.update", "flag_configuration.kill", "segment.edit", "segment.delete"}) ||
		!slices.Equal(rolesOf(got.Grants), []string{"developer", "maintainer"}) {
		t.Errorf("with two grants: %+v, want the union of developer and maintainer", got)
	}
	if res := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, maintainer.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("removing the maintainer grant = %d: %s", res.StatusCode(), res.Body)
	}
	if got := projectMe(t, mc, p.Key); !slices.Equal(got.Permissions, []string{"project.read", "flag.edit", "flag_configuration.update", "flag_configuration.kill", "segment.edit"}) ||
		!slices.Equal(rolesOf(got.Grants), []string{"developer"}) {
		t.Errorf("after removing one: %+v, want the developer grant alone", got)
	}
	// The last owner's grant may go: an instance admin can always step in.
	owner := grant(t, admin, p.Key, m.ID, "owner")
	if res := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, owner.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Errorf("removing the last owner grant = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestGrantWritesAreAudited(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	p := createProject(t, admin, projectKey())
	m := testkit.NewMember(t, pool)
	g := grant(t, admin, p.Key, m.ID, "developer")
	if res := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, g.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("removing = %d: %s", res.StatusCode(), res.Body)
	}

	for action, removed := range map[string]bool{"grant.created": false, "grant.deleted": true} {
		entries := auditOf(t, srv, action, g.Id.String())
		if len(entries) != 1 {
			t.Errorf("%s recorded %d times, want once", action, len(entries))
			continue
		}
		e := entries[0]
		values, _ := e.After.(map[string]any)
		if removed {
			values, _ = e.Before.(map[string]any)
		}
		if e.ProjectId == nil || *e.ProjectId != p.Id || e.Entity.Kind != "grant" || e.Entity.Key != m.Email ||
			e.Actor.MemberId == nil || *e.Actor.MemberId != adminID ||
			values["member"] != m.ID.String() || values["role"] != "developer" || values["source"] != "manual" {
			t.Errorf("%s = %+v, want the grant in its project, by the instance admin", action, e)
		}
	}
}

// A credential cannot hand out what it does not hold: an owner's token
// narrowed by its scopes is capped by what the scopes leave it.
func TestAScopedTokenGrantsOnlyRolesWithinWhatItsScopesLeaveIt(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	owner := testkit.NewMember(t, pool)
	testkit.Grant(t, pool, projectIDOf(p), owner.ID, access.RoleOwner)
	noFlags := testkit.Client(t, srv, testkit.PersonalToken(t, pool, owner.ID, "no flags", access.ScopeProjectAdmin))
	m := testkit.NewMember(t, pool)

	res := do(noFlags.CreateGrantWithResponse(t.Context(), p.Key, management.GrantCreate{Member: m.ID.Bytes(), Role: "maintainer"}))
	if pr := assertProblem(t, "maintainer without flags:write", res.StatusCode(), res.Body, http.StatusUnprocessableEntity,
		"role_not_allowed"); pr.Permission == nil || *pr.Permission != "declaration.manage" {
		t.Errorf("the refusal names %v, want declaration.manage", pr.Permission)
	}
	dev := do(noFlags.CreateGrantWithResponse(t.Context(), p.Key, management.GrantCreate{Member: m.ID.Bytes(), Role: "developer"}))
	if pr := assertProblem(t, "developer without flags:write", dev.StatusCode(), dev.Body, http.StatusUnprocessableEntity,
		"role_not_allowed"); pr.Permission == nil || *pr.Permission != "flag.edit" {
		t.Errorf("the refusal names %v, want flag.edit", pr.Permission)
	}
	if res := do(noFlags.CreateGrantWithResponse(t.Context(), p.Key, management.GrantCreate{Member: m.ID.Bytes(), Role: "viewer"})); res.JSON201 == nil {
		t.Errorf("viewer = %d: %s", res.StatusCode(), res.Body)
	}
}
