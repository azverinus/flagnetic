package management_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// matrixRow is one permission of the catalog, exercised through one
// operation requiring it.
type matrixRow struct {
	permission string
	operation  string
	// call performs the operation in project as c and returns its status
	// and body; each call changes something new, so the cells of a row do
	// not depend on one another.
	call func(t *testing.T, c *management.ClientWithResponses, project management.Project) (int, []byte)
}

func matrix(t *testing.T) []matrixRow {
	t.Helper()
	target := testkit.NewMember(t, testkit.Pool(t))
	return []matrixRow{
		{"project.read", "getProject", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.GetProjectWithResponse(t.Context(), p.Key))
			return res.StatusCode(), res.Body
		}},
		{"project.read", "listProjectMembers", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.ListProjectMembersWithResponse(t.Context(), p.Key))
			return res.StatusCode(), res.Body
		}},
		{"project.update", "updateProject", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.UpdateProjectWithResponse(t.Context(), p.Key, management.ProjectPatch{Name: ptr("Renamed " + projectKey())}))
			return res.StatusCode(), res.Body
		}},
		{"environment.manage", "createEnvironment", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: projectKey(), Name: "Matrix"}))
			return res.StatusCode(), res.Body
		}},
		{"declaration.manage", "createUnitType", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.CreateUnitTypeWithResponse(t.Context(), p.Key, management.UnitTypeCreate{Key: projectKey(), Name: "Matrix"}))
			return res.StatusCode(), res.Body
		}},
		{"grant.manage", "createGrant", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.CreateGrantWithResponse(t.Context(), p.Key, management.GrantCreate{Member: target.ID.Bytes(), Role: "viewer"}))
			if res.JSON201 != nil {
				// Removed again, so the next cell can grant it anew.
				do(c.DeleteGrantWithResponse(t.Context(), p.Key, res.JSON201.Id.String()))
			}
			return res.StatusCode(), res.Body
		}},
		{"project_token.manage", "listProjectTokens", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.ListProjectTokensWithResponse(t.Context(), p.Key))
			return res.StatusCode(), res.Body
		}},
	}
}

// Each permission is held by the roles the permission table gives it and by
// an instance admin, and refused, naming it, to every other role and to a
// member holding no grant.
func TestThePermissionMatrixHoldsRowByRow(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	holders := map[string][]access.RoleName{
		"project.read":         {access.RoleViewer, access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"project.update":       {access.RoleOwner},
		"environment.manage":   {access.RoleMaintainer, access.RoleOwner},
		"declaration.manage":   {access.RoleMaintainer, access.RoleOwner},
		"grant.manage":         {access.RoleOwner},
		"project_token.manage": {access.RoleOwner},
	}
	callers := map[string]*management.ClientWithResponses{
		"instance admin": testkit.Client(t, srv, testkit.InstanceAdminToken(t)),
		"no grant":       testkit.Client(t, srv, testkit.MemberToken(t, pool, access.ScopeRead, access.ScopeFlagsWrite, access.ScopeProjectAdmin)),
	}
	for _, r := range access.Roles() {
		callers[string(r.Name)] = testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), r.Name))
	}

	for _, row := range matrix(t) {
		for who, c := range callers {
			holds := who == "instance admin" || slices.Contains(holders[row.permission], access.RoleName(who))
			status, body := row.call(t, c, p)
			if holds {
				if status == http.StatusForbidden {
					t.Errorf("%s by %s = 403, want it allowed: %s", row.operation, who, body)
				}
				continue
			}
			pr := assertProblem(t, row.operation+" by "+who, status, body, http.StatusForbidden, "forbidden")
			if pr.Permission == nil || *pr.Permission != row.permission {
				t.Errorf("%s by %s: refusal names %v, want %s", row.operation, who, pr.Permission, row.permission)
			}
		}
	}
}

func TestProjectMeAnswersGrantsAndPermissionsPerRole(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	for role, want := range map[access.RoleName][]string{
		access.RoleViewer:     {"project.read"},
		access.RoleDeveloper:  {"project.read"},
		access.RoleMaintainer: {"project.read", "environment.manage", "declaration.manage"},
		access.RoleOwner:      {"project.read", "project.update", "environment.manage", "declaration.manage", "grant.manage", "project_token.manage"},
	} {
		got := projectMe(t, testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), role)), p.Key)
		if !slices.Equal(got.Permissions, want) || !slices.Equal(rolesOf(got.Grants), []string{string(role)}) {
			t.Errorf("%s: me = %+v, want the %s grant and %v", role, got, role, want)
		}
	}
	if got := projectMe(t, admin, p.Key); len(got.Permissions) != len(access.Catalog()) || len(got.Grants) != 0 {
		t.Errorf("an instance admin: me = %+v, want every permission and no grant", got)
	}
}

func TestProjectsAreListedWhereTheMemberHoldsAGrant(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mine := createProject(t, admin, projectKey())
	createProject(t, admin, projectKey())
	m := testkit.NewMember(t, pool)
	testkit.Grant(t, pool, projectIDOf(mine), m.ID, access.RoleDeveloper, access.RoleViewer)

	res := do(testkit.Client(t, srv, testkit.PersonalToken(t, pool, m.ID, "lister")).ListProjectsWithResponse(t.Context()))
	if res.JSON200 == nil || len(res.JSON200.Items) != 1 || res.JSON200.Items[0].Id != mine.Id {
		t.Errorf("a developer lists %d %s, want their one project once", res.StatusCode(), res.Body)
	}
}
