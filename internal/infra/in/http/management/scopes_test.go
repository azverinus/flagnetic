package management_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestTheDocumentsScopesAreTheScopesATokenCanCarry(t *testing.T) {
	documented := []management.Scope{management.ScopeRead, management.ScopeFlagsWrite, management.ScopeExperimentsWrite,
		management.ScopeProjectAdmin, management.ScopeInstanceAdmin}
	var carried []management.Scope
	for _, s := range access.AllScopes() {
		carried = append(carried, management.Scope(s))
	}
	if !slices.Equal(documented, carried) {
		t.Errorf("the document's scopes %v, a token's %v", documented, carried)
	}
}

func TestATokenLackingTheOperationsScopeIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	token := func(scopes ...access.Scope) *management.ClientWithResponses {
		return testkit.Client(t, srv, testkit.PersonalToken(t, pool, id.FromBytes[access.Member](adminID), "script", scopes...))
	}
	readOnly := token()

	for name, tc := range map[string]struct {
		call  func(c *management.ClientWithResponses) (int, []byte)
		scope access.Scope
	}{
		"createProject": {func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: projectKey(), Name: "Scoped"}))
			return res.StatusCode(), res.Body
		}, access.ScopeInstanceAdmin},
		"createEnvironment": {func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: projectKey(), Name: "Scoped"}))
			return res.StatusCode(), res.Body
		}, access.ScopeProjectAdmin},
		"createUnitType": {func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.CreateUnitTypeWithResponse(t.Context(), p.Key, management.UnitTypeCreate{Key: projectKey(), Name: "Scoped"}))
			return res.StatusCode(), res.Body
		}, access.ScopeFlagsWrite},
		"createFlag": {func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.CreateFlagWithResponse(t.Context(), p.Key, management.FlagCreate{Key: projectKey(), Type: management.FlagTypeBoolean, Name: "Scoped"}))
			return res.StatusCode(), res.Body
		}, access.ScopeFlagsWrite},
	} {
		status, body := tc.call(readOnly)
		if pr := assertProblem(t, name, status, body, http.StatusForbidden, "insufficient_scope"); pr.Detail == nil ||
			!strings.Contains(*pr.Detail, string(tc.scope)) {
			t.Errorf("%s: detail = %v, want the scope %s named", name, pr.Detail, tc.scope)
		}
		if status, body := tc.call(token(tc.scope)); status != http.StatusCreated {
			t.Errorf("%s with %s = %d: %s", name, tc.scope, status, body)
		}
	}
}

func TestOneScopeDoesNotOpenAnother(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	flags := testkit.Client(t, srv, testkit.PersonalToken(t, testkit.Pool(t), id.FromBytes[access.Member](adminID), "flags", access.ScopeFlagsWrite))

	env := do(flags.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
	assertProblem(t, "flags:write creating an environment", env.StatusCode(), env.Body, http.StatusForbidden, "insufficient_scope")
	if listed := do(flags.ListEnvironmentsWithResponse(t.Context(), p.Key)); listed.JSON200 == nil {
		t.Errorf("listing environments = %d: %s", listed.StatusCode(), listed.Body)
	}
	if created := do(flags.CreateUnitTypeWithResponse(t.Context(), p.Key, management.UnitTypeCreate{Key: "device", Name: "Device"})); created.JSON201 == nil {
		t.Errorf("declaring a unit type = %d: %s", created.StatusCode(), created.Body)
	}
}
