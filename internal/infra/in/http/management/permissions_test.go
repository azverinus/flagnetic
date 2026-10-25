package management_test

import (
	"net/http"
	"slices"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

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
		{"project.read", "listDiscoveredAttributes", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.ListDiscoveredAttributesWithResponse(t.Context(), p.Key))
			return res.StatusCode(), res.Body
		}},
		{"declaration.manage", "dismissDiscoveredAttribute", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// Nothing was discovered, so a caller holding the permission is
			// answered not_found, and one lacking it forbidden first.
			res := do(c.DismissDiscoveredAttributeWithResponse(t.Context(), p.Key, "absent"))
			return res.StatusCode(), res.Body
		}},
		{"declaration.manage", "declareDiscoveredAttribute", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.DeclareDiscoveredAttributeWithResponse(t.Context(), p.Key, "absent",
				management.DiscoveredAttributeDeclaration{Type: management.AttributeTypeString}))
			return res.StatusCode(), res.Body
		}},
		{"project.update", "updateProject", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.UpdateProjectWithResponse(t.Context(), p.Key, &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Name: ptr("Renamed " + projectKey())}))
			return res.StatusCode(), res.Body
		}},
		{"environment.manage", "createEnvironment", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: projectKey(), Name: "Matrix"}))
			return res.StatusCode(), res.Body
		}},
		{"environment.protect", "setEnvironmentProtection", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// Production is protected already, so a caller holding the
			// permission changes nothing and every call is based on version 1.
			res := do(c.SetEnvironmentProtectionWithResponse(t.Context(), p.Key, "production",
				&management.SetEnvironmentProtectionParams{IfMatch: ifMatch(1)}, management.EnvironmentProtection{Protected: true}))
			return res.StatusCode(), res.Body
		}},
		{"declaration.manage", "createUnitType", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.CreateUnitTypeWithResponse(t.Context(), p.Key, management.UnitTypeCreate{Key: projectKey(), Name: "Matrix"}))
			return res.StatusCode(), res.Body
		}},
		{"flag.edit", "createFlag", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{
				Key: projectKey(), Type: management.FlagTypeBoolean, Name: "Matrix", Owner: ptr(openapi_types.UUID(target.ID.Bytes())),
			}))
			return res.StatusCode(), res.Body
		}},
		{"flag.archive", "archiveFlag", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No flag has the key, so a caller holding the permission is
			// answered not_found, and one lacking it forbidden first.
			res := do(c.ArchiveFlagWithResponse(t.Context(), p.Key, "absent", &management.ArchiveFlagParams{IfMatch: ptr("1")}))
			return res.StatusCode(), res.Body
		}},
		{"flag_configuration.update", "putFlagConfiguration", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No flag has the key, so a caller holding the permission is
			// answered not_found, and one lacking it forbidden first.
			res := do(c.PutFlagConfigurationWithResponse(t.Context(), p.Key, "absent", "production",
				&management.PutFlagConfigurationParams{IfMatch: ptr("1")}, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "true"}))
			return res.StatusCode(), res.Body
		}},
		{"flag_configuration.kill", "killFlag", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No flag has the key, so a caller holding the permission is
			// answered not_found, and one lacking it forbidden first.
			res := do(c.KillFlagWithResponse(t.Context(), p.Key, "absent", "production", management.Kill{}))
			return res.StatusCode(), res.Body
		}},
		{"flag_configuration.revert", "revertFlagConfiguration", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No flag has the key, so a caller holding the permission is
			// answered not_found, and one lacking it forbidden first.
			res := do(c.RevertFlagConfigurationWithResponse(t.Context(), p.Key, "absent", "production",
				&management.RevertFlagConfigurationParams{IfMatch: ptr("1")}, management.FlagConfigurationRevert{Version: 1}))
			return res.StatusCode(), res.Body
		}},
		{"segment.edit", "createSegment", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			res := do(c.CreateSegmentWithResponse(t.Context(), p.Key, management.SegmentCreate{Key: projectKey(), Name: "Matrix"}))
			return res.StatusCode(), res.Body
		}},
		{"segment.delete", "deleteSegment", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No segment has the key, so a caller holding the permission is
			// answered not_found, and one lacking it forbidden first.
			res := do(c.DeleteSegmentWithResponse(t.Context(), p.Key, "absent", &management.DeleteSegmentParams{IfMatch: ptr("1")}))
			return res.StatusCode(), res.Body
		}},
		{"experiment.edit", "createExperiment", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// Nothing to compare, so a caller holding the permission is
			// answered invalid, and one lacking it forbidden first.
			res := do(c.CreateExperimentWithResponse(t.Context(), p.Key, management.ExperimentCreate{
				Key: projectKey(), Name: "Matrix", Flag: "absent", UnitType: "user", Variations: weights("a", 5000, "b", 5000),
			}))
			return res.StatusCode(), res.Body
		}},
		{"experiment.delete", "deleteExperiment", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No experiment has the key, so a caller holding the permission
			// is answered not_found, and one lacking it forbidden first.
			res := do(c.DeleteExperimentWithResponse(t.Context(), p.Key, "absent", &management.DeleteExperimentParams{IfMatch: ptr("1")}))
			return res.StatusCode(), res.Body
		}},
		{"change_request.propose", "createChangeRequest", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No flag has the key, so a caller holding the permission is
			// answered not_found, and one lacking it forbidden first.
			res := do(c.CreateChangeRequestWithResponse(t.Context(), p.Key, management.ChangeRequestCreate{
				Kind: management.ChangeRequestCreateKindFlagConfiguration, Flag: "absent", Environment: "production",
				Title: "Matrix", Proposed: management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a"},
			}))
			return res.StatusCode(), res.Body
		}},
		{"change_request.approve", "approveChangeRequest", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No change request has the identifier, so a caller holding the
			// permission is answered not_found, and one lacking it
			// forbidden first.
			res := do(c.ApproveChangeRequestWithResponse(t.Context(), p.Key, openapi_types.UUID{1},
				&management.ApproveChangeRequestParams{IfMatch: ptr("1")}, management.ChangeRequestApprove{}))
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
		{"sdk_key.manage", "createSdkKey", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// A client key per call, in an environment of its own, so the
			// cells never meet the limit of active keys.
			res := do(c.CreateSdkKeyWithResponse(t.Context(), p.Key, "development", management.SdkKeyCreate{Kind: "client", Name: "Matrix"}))
			return res.StatusCode(), res.Body
		}},
		{"sdk_key.manage", "revokeSdkKey", func(t *testing.T, c *management.ClientWithResponses, p management.Project) (int, []byte) {
			// No key has the identifier, so a caller holding the permission
			// is answered not_found, and one lacking it forbidden first.
			res := do(c.RevokeSdkKeyWithResponse(t.Context(), p.Key, "staging", "0190d2a0-0000-7000-8000-000000000000"))
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
		"project.read":              {access.RoleViewer, access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"project.update":            {access.RoleOwner},
		"environment.manage":        {access.RoleMaintainer, access.RoleOwner},
		"environment.protect":       {access.RoleOwner},
		"declaration.manage":        {access.RoleMaintainer, access.RoleOwner},
		"grant.manage":              {access.RoleOwner},
		"project_token.manage":      {access.RoleOwner},
		"flag.edit":                 {access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"flag.archive":              {access.RoleMaintainer, access.RoleOwner},
		"flag_configuration.update": {access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"flag_configuration.kill":   {access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"flag_configuration.revert": {access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"segment.edit":              {access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"segment.delete":            {access.RoleMaintainer, access.RoleOwner},
		"sdk_key.manage":            {access.RoleMaintainer, access.RoleOwner},
		"experiment.edit":           {access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"experiment.delete":         {access.RoleMaintainer, access.RoleOwner},
		"change_request.propose":    {access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner},
		"change_request.approve":    {access.RoleMaintainer, access.RoleOwner},
	}
	callers := map[string]*management.ClientWithResponses{
		"instance admin": testkit.Client(t, srv, testkit.InstanceAdminToken(t)),
		"no grant":       testkit.Client(t, srv, testkit.MemberToken(t, pool, access.ScopeRead, access.ScopeFlagsWrite, access.ScopeExperimentsWrite, access.ScopeProjectAdmin)),
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
		access.RoleViewer:    {"project.read"},
		access.RoleDeveloper: {"project.read", "flag.edit", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "experiment.edit", "change_request.propose"},
		access.RoleMaintainer: {"project.read", "environment.manage", "declaration.manage", "flag.edit", "flag.archive",
			"flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage", "experiment.edit", "experiment.delete", "change_request.propose", "change_request.approve"},
		access.RoleOwner: {"project.read", "project.update", "environment.manage", "environment.protect", "declaration.manage", "grant.manage", "project_token.manage",
			"flag.edit", "flag.archive", "flag_configuration.update", "flag_configuration.kill", "flag_configuration.revert", "segment.edit", "segment.delete", "sdk_key.manage", "experiment.edit", "experiment.delete", "change_request.propose", "change_request.approve"},
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
