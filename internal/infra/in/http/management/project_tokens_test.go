package management_test

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// grantedSession returns a browser signed in as a new member holding role
// in project, and the member.
func grantedSession(t *testing.T, srv *testkit.Server, project management.Project, role access.RoleName) (*testkit.Browser, testkit.Member) {
	t.Helper()
	m := testkit.NewMember(t, testkit.Pool(t))
	testkit.Grant(t, testkit.Pool(t), projectIDOf(project), m.ID, role)
	return signedIn(t, srv, m), m
}

// mustCreateProjectToken creates a token of project through b's session,
// failing the test unless it is created.
func mustCreateProjectToken(t *testing.T, b *testkit.Browser, project string, body management.ProjectTokenCreate) management.CreatedProjectToken {
	t.Helper()
	res := do(b.CreateProjectTokenWithResponse(t.Context(), project, body))
	if res.JSON201 == nil {
		t.Fatalf("POST /projects/%s/tokens = %d: %s", project, res.StatusCode(), res.Body)
	}
	return *res.JSON201
}

// pipeline returns a client presenting a new token of project holding role
// with scopes, created by an owner's session.
func pipeline(t *testing.T, srv *testkit.Server, project management.Project, role string, ss ...management.Scope) *management.ClientWithResponses {
	t.Helper()
	owner, _ := grantedSession(t, srv, project, access.RoleOwner)
	created := mustCreateProjectToken(t, owner, project.Key, management.ProjectTokenCreate{Name: "ci", Role: role, Scopes: &ss})
	return testkit.Client(t, srv, created.Value)
}

func TestAProjectTokenIsShownOnceAndActsForNoMember(t *testing.T) {
	srv, _, c := tokenServer(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	owner, creator := grantedSession(t, srv, p, access.RoleOwner)
	expiry := c.Now().Add(7 * 24 * time.Hour)

	created := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{
		Name: "ci-deploy", Role: "developer", Scopes: ptr(scopes(management.ScopeFlagsWrite)), ExpiresAt: &expiry,
	})
	if !regexp.MustCompile(`^fn_prj_[0-9A-Za-z]{40}$`).MatchString(created.Value) {
		t.Errorf("value %q is not fn_prj_ and 40 base62 characters", created.Value)
	}
	tok := created.Token
	if tok.Hint != created.Value[:len("fn_prj_")+4] || tok.Name != "ci-deploy" || tok.Role != management.RoleNameDeveloper ||
		!slices.Equal(tok.Scopes, scopes(management.ScopeRead, management.ScopeFlagsWrite)) ||
		tok.ExpiresAt == nil || !tok.ExpiresAt.Equal(expiry) || !tok.CreatedAt.Equal(c.Now()) ||
		tok.CreatedBy == nil || *tok.CreatedBy != creator.ID.Bytes() || tok.RevokedAt != nil {
		t.Errorf("token = %+v, want developer with read and flags:write, created by the owner", tok)
	}
	listed := do(owner.ListProjectTokensWithResponse(t.Context(), p.Key))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 1 || listed.JSON200.Items[0].Id != tok.Id ||
		regexp.MustCompile(created.Value).Match(listed.Body) {
		t.Errorf("listed = %d %s, want the token without its value", listed.StatusCode(), listed.Body)
	}
	if mine := myTokens(t, owner); len(mine) != 0 {
		t.Errorf("the creator's own tokens list %+v", mine)
	}
	got := me(t, srv, created.Value)
	cred := got.Credential
	if got.Member != nil || cred.Method != management.CredentialMethodToken || cred.TokenId == nil || *cred.TokenId != tok.Id ||
		cred.TokenName == nil || *cred.TokenName != "ci-deploy" || cred.Project == nil || *cred.Project != p.Key ||
		cred.Role == nil || *cred.Role != management.RoleNameDeveloper {
		t.Errorf("GET /me = %+v, credential %+v; want the token, its project and role, and no member", got, cred)
	}
}

func TestAProjectTokensPermissionsAreItsRoleNarrowedByItsScopes(t *testing.T) {
	srv := testkit.StartServer(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	unitType := func(c *management.ClientWithResponses) (int, []byte) {
		res := do(c.CreateUnitTypeWithResponse(t.Context(), p.Key, management.UnitTypeCreate{Key: projectKey(), Name: "Unit"}))
		return res.StatusCode(), res.Body
	}
	environment := func(c *management.ClientWithResponses) (int, []byte) {
		res := do(c.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: projectKey(), Name: "Env"}))
		return res.StatusCode(), res.Body
	}

	maintainer := pipeline(t, srv, p, "maintainer", management.ScopeFlagsWrite)
	if status, body := unitType(maintainer); status != http.StatusCreated {
		t.Errorf("a maintainer with flags:write declaring = %d: %s", status, body)
	}
	status, body := environment(maintainer)
	assertProblem(t, "a maintainer with flags:write managing environments", status, body, http.StatusForbidden, "insufficient_scope")
	status, body = environment(pipeline(t, srv, p, "developer", management.ScopeProjectAdmin))
	if pr := assertProblem(t, "a developer with project:admin", status, body, http.StatusForbidden, "forbidden"); pr.Permission == nil ||
		*pr.Permission != "environment.manage" {
		t.Errorf("the refusal names %v, want environment.manage", pr.Permission)
	}
	reader := pipeline(t, srv, p, "owner")
	status, body = unitType(reader)
	assertProblem(t, "an owner with read declaring", status, body, http.StatusForbidden, "insufficient_scope")
	if res := do(reader.ListUnitTypesWithResponse(t.Context(), p.Key)); res.JSON200 == nil {
		t.Errorf("an owner with read listing = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestAProjectTokenReachesNothingOutsideItsProject(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p, other := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	c := pipeline(t, srv, p, "owner", management.ScopeFlagsWrite, management.ScopeExperimentsWrite, management.ScopeProjectAdmin)

	for name, call := range map[string]func() (int, []byte){
		"another project": func() (int, []byte) {
			r := do(c.GetProjectWithResponse(t.Context(), other.Key))
			return r.StatusCode(), r.Body
		},
		"an unknown project": func() (int, []byte) {
			r := do(c.GetProjectWithResponse(t.Context(), "no-such-project"))
			return r.StatusCode(), r.Body
		},
		"listProjects": func() (int, []byte) { r := do(c.ListProjectsWithResponse(t.Context())); return r.StatusCode(), r.Body },
		"listMembers": func() (int, []byte) {
			r := do(c.ListMembersWithResponse(t.Context(), nil))
			return r.StatusCode(), r.Body
		},
		"getAuthSettings": func() (int, []byte) {
			r := do(c.GetAuthSettingsWithResponse(t.Context()))
			return r.StatusCode(), r.Body
		},
		"listAuditEntries": func() (int, []byte) {
			r := do(c.ListAuditEntriesWithResponse(t.Context(), nil))
			return r.StatusCode(), r.Body
		},
		"listMyTokens": func() (int, []byte) { r := do(c.ListMyTokensWithResponse(t.Context())); return r.StatusCode(), r.Body },
		"createMyToken": func() (int, []byte) {
			r := do(c.CreateMyTokenWithResponse(t.Context(), management.ApiTokenCreate{Name: "x"}))
			return r.StatusCode(), r.Body
		},
		"listMySessions": func() (int, []byte) {
			r := do(c.ListMySessionsWithResponse(t.Context()))
			return r.StatusCode(), r.Body
		},
		"a token of another project": func() (int, []byte) {
			r := do(c.CreateProjectTokenWithResponse(t.Context(), other.Key, management.ProjectTokenCreate{Name: "x", Role: "viewer"}))
			return r.StatusCode(), r.Body
		},
	} {
		status, body := call()
		assertProblem(t, name, status, body, http.StatusForbidden, "forbidden")
	}
}

func TestOnlyAnOwnerManagesProjectTokens(t *testing.T) {
	srv := testkit.StartServer(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	maintainer, _ := grantedSession(t, srv, p, access.RoleMaintainer)
	owner, _ := grantedSession(t, srv, p, access.RoleOwner)
	tok := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{Name: "ci", Role: "viewer"})

	created := do(maintainer.CreateProjectTokenWithResponse(t.Context(), p.Key, management.ProjectTokenCreate{Name: "ci", Role: "viewer"}))
	listed := do(maintainer.ListProjectTokensWithResponse(t.Context(), p.Key))
	revoked := do(maintainer.RevokeProjectTokenWithResponse(t.Context(), p.Key, tok.Token.Id.String()))
	for name, res := range map[string]struct {
		status int
		body   []byte
	}{
		"creating": {created.StatusCode(), created.Body}, "listing": {listed.StatusCode(), listed.Body},
		"revoking": {revoked.StatusCode(), revoked.Body},
	} {
		pr := assertProblem(t, "a maintainer "+name, res.status, res.body, http.StatusForbidden, "forbidden")
		if pr.Permission == nil || *pr.Permission != "project_token.manage" {
			t.Errorf("a maintainer %s: refusal names %v, want project_token.manage", name, pr.Permission)
		}
	}
	admin := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin()))
	mustCreateProjectToken(t, admin, p.Key, management.ProjectTokenCreate{Name: "admin's", Role: "owner"})
	unknown := do(admin.ListProjectTokensWithResponse(t.Context(), "no-such-project"))
	assertProblem(t, "an unknown project", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
}

func TestAProjectTokensCreationIsValidated(t *testing.T) {
	srv, _, c := tokenServer(t)
	admin := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin()))
	p := createProject(t, admin.ClientWithResponses, projectKey())
	beyond := c.Now().Add(366 * 24 * time.Hour)

	for name, tc := range map[string]struct {
		body               management.ProjectTokenCreate
		code               string
		pointer, violation string
	}{
		"an unknown scope": {management.ProjectTokenCreate{Name: "ci", Role: "viewer", Scopes: ptr(scopes("flags:delete"))},
			"invalid_token_request", "/scopes/0", "unknown_scope"},
		"instance:admin, even from an instance admin": {management.ProjectTokenCreate{Name: "ci", Role: "owner",
			Scopes: ptr(scopes(management.ScopeInstanceAdmin))}, "scope_not_allowed", "/scopes/0", "scope_not_allowed"},
		"an expiry beyond the maximum lifetime": {management.ProjectTokenCreate{Name: "ci", Role: "viewer", ExpiresAt: &beyond},
			"invalid_token_request", "/expires_at", "beyond_max_lifetime"},
		"a role that is no role": {management.ProjectTokenCreate{Name: "ci", Role: "admin"}, "invalid_token_request", "/role", "unknown_role"},
		"a blank name":           {management.ProjectTokenCreate{Name: " ", Role: "viewer"}, "invalid_token_request", "/name", "invalid_name"},
	} {
		res := do(admin.CreateProjectTokenWithResponse(t.Context(), p.Key, tc.body))
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, tc.code)
		if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != tc.pointer || (*pr.Errors)[0].Code != tc.violation {
			t.Errorf("%s: violations = %+v, want %s at %s", name, pr.Errors, tc.violation, tc.pointer)
		}
	}
	tok := mustCreateProjectToken(t, admin, p.Key, management.ProjectTokenCreate{Name: "defaults", Role: "viewer"}).Token
	if !slices.Equal(tok.Scopes, scopes(management.ScopeRead)) || tok.ExpiresAt != nil {
		t.Errorf("defaults: scopes %v, expiry %v; want read alone and none", tok.Scopes, tok.ExpiresAt)
	}
}

func TestAProjectTokenSurvivesItsCreatorLeaving(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	owner, creator := grantedSession(t, srv, p, access.RoleOwner)
	value := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{Name: "ci", Role: "viewer"}).Value

	for _, m := range projectMembers(t, admin, p.Key) {
		for _, g := range m.Grants {
			do(admin.DeleteGrantWithResponse(t.Context(), p.Key, g.Id.String()))
		}
	}
	if res := do(admin.DeactivateMemberWithResponse(t.Context(), creator.ID.String())); res.JSON200 == nil {
		t.Fatalf("deactivating the creator = %d: %s", res.StatusCode(), res.Body)
	}
	wantSignedOut(t, owner, "the creator")
	if res := do(testkit.Client(t, srv, value).ListUnitTypesWithResponse(t.Context(), p.Key)); res.JSON200 == nil {
		t.Errorf("the project token after its creator left = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestAProjectTokenIsRefusedWithEachCode(t *testing.T) {
	srv, _, c := tokenServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p, other := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	owner, ownerMember := grantedSession(t, srv, p, access.RoleOwner)
	testkit.Grant(t, testkit.Pool(t), projectIDOf(other), ownerMember.ID, access.RoleOwner)
	bearer := func(v string) map[string]string { return map[string]string{"Authorization": "Bearer " + v} }

	assertRefused(t, "an unknown token", get(t, srv.URL+"/api/v1/me", bearer("fn_prj_"+strings.Repeat("Z", 40))), "invalid_token")

	expiry := c.Now().Add(time.Hour)
	short := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{Name: "short", Role: "viewer", ExpiresAt: &expiry})
	c.Set(expiry)
	assertRefused(t, "an expired token", get(t, srv.URL+"/api/v1/me", bearer(short.Value)), "token_expired")

	tok := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{Name: "revoked", Role: "viewer"})
	elsewhere := do(owner.RevokeProjectTokenWithResponse(t.Context(), other.Key, tok.Token.Id.String()))
	assertProblem(t, "revoking through another project", elsewhere.StatusCode(), elsewhere.Body, http.StatusNotFound, "not_found")
	if res := do(owner.RevokeProjectTokenWithResponse(t.Context(), p.Key, tok.Token.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("revoking = %d: %s", res.StatusCode(), res.Body)
	}
	assertRefused(t, "a revoked token", get(t, srv.URL+"/api/v1/me", bearer(tok.Value)), "token_revoked")
	again := do(owner.RevokeProjectTokenWithResponse(t.Context(), p.Key, tok.Token.Id.String()))
	assertProblem(t, "revoking again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
	personal := mustCreateToken(t, owner, management.ApiTokenCreate{Name: "mine"})
	asProject := do(owner.RevokeProjectTokenWithResponse(t.Context(), p.Key, personal.Token.Id.String()))
	assertProblem(t, "a personal token through the project", asProject.StatusCode(), asProject.Body, http.StatusNotFound, "not_found")
	listed := do(owner.ListProjectTokensWithResponse(t.Context(), p.Key))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 2 || listed.JSON200.Items[0].RevokedAt == nil {
		t.Errorf("listed = %d %s, want the revoked token still listed, newest first", listed.StatusCode(), listed.Body)
	}
}

func TestAProjectTokensLastUseIsRecorded(t *testing.T) {
	srv, _, c := tokenServer(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	owner, _ := grantedSession(t, srv, p, access.RoleOwner)
	value := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{Name: "ci", Role: "viewer"}).Value
	lastUse := func() *time.Time {
		t.Helper()
		if err := srv.Tick(t, worker.TokenLastUseName); err != nil {
			t.Fatal(err)
		}
		res := do(owner.ListProjectTokensWithResponse(t.Context(), p.Key))
		if res.JSON200 == nil {
			t.Fatalf("listing = %d: %s", res.StatusCode(), res.Body)
		}
		return res.JSON200.Items[0].LastUsedAt
	}

	first := c.Now()
	me(t, srv, value)
	if at := lastUse(); at == nil || !at.Equal(first) {
		t.Errorf("after the first use: %v, want %v", at, first)
	}
	c.Advance(time.Second)
	me(t, srv, value)
	if at := lastUse(); at == nil || !at.Equal(first) {
		t.Errorf("a second later: %v, want %v unchanged", at, first)
	}
	c.Advance(time.Minute)
	me(t, srv, value)
	if at := lastUse(); at == nil || !at.Equal(c.Now()) {
		t.Errorf("a minute later: %v, want %v", at, c.Now())
	}
}

func TestAProjectTokenIsAuditedInItsProject(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	owner, creator := grantedSession(t, srv, p, access.RoleOwner)
	created := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{
		Name: "ci-deploy", Role: "maintainer", Scopes: ptr(scopes(management.ScopeFlagsWrite)),
	})
	ut := do(testkit.Client(t, srv, created.Value).CreateUnitTypeWithResponse(t.Context(), p.Key,
		management.UnitTypeCreate{Key: "device", Name: "Device"}))
	if ut.JSON201 == nil {
		t.Fatalf("the token declaring = %d: %s", ut.StatusCode(), ut.Body)
	}
	if res := do(owner.RevokeProjectTokenWithResponse(t.Context(), p.Key, created.Token.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("revoking = %d: %s", res.StatusCode(), res.Body)
	}

	log := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, nil))
	if log.JSON200 == nil {
		t.Fatalf("project audit = %d: %s", log.StatusCode(), log.Body)
	}
	seen := map[string]management.AuditEntry{}
	for _, e := range log.JSON200.Items {
		seen[e.Action] = e
	}
	for _, action := range []string{"api_token.created", "api_token.revoked"} {
		e, ok := seen[action]
		after, _ := e.After.(map[string]any)
		if !ok || e.Entity.Id != created.Token.Id.String() || e.Actor.Kind != management.ActorKindMember ||
			e.Actor.MemberId == nil || *e.Actor.MemberId != creator.ID.Bytes() ||
			after["kind"] != "project" || after["hint"] != created.Token.Hint || after["role"] != "maintainer" ||
			(action == "api_token.revoked") != (after["revoked_at"] != nil) {
			t.Errorf("%s = %+v, want the token by its hint, recorded in its project by its creator", action, e)
		}
	}
	if e := seen["unit_type.created"]; e.Actor.Kind != management.ActorKindToken || e.Actor.Id != created.Token.Id.String() ||
		e.Actor.Name != "ci-deploy" || e.Actor.MemberId != nil {
		t.Errorf("the token's own write is audited as %+v, want the token acting for no member", e.Actor)
	}
}

// Every bearer is refused, so no token mints another, however wide.
func TestAProjectTokenCannotCreateAnother(t *testing.T) {
	srv := testkit.StartServer(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	c := pipeline(t, srv, p, "owner", management.ScopeProjectAdmin)

	res := do(c.CreateProjectTokenWithResponse(t.Context(), p.Key, management.ProjectTokenCreate{Name: "child", Role: "viewer"}))
	assertProblem(t, "creating", res.StatusCode(), res.Body, http.StatusForbidden, "session_required")
	if listed := do(c.ListProjectTokensWithResponse(t.Context(), p.Key)); listed.StatusCode() != http.StatusForbidden {
		t.Errorf("an owner's project token listing project tokens = %d, want 403: it never manages them", listed.StatusCode())
	}
}

// Whatever its role, a project token never changes who has access.
func TestAnOwnersProjectTokenNeverManagesGrants(t *testing.T) {
	srv := testkit.StartServer(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	c := pipeline(t, srv, p, "owner", management.ScopeProjectAdmin)
	m := testkit.NewMember(t, testkit.Pool(t))

	res := do(c.CreateGrantWithResponse(t.Context(), p.Key, management.GrantCreate{Member: m.ID.Bytes(), Role: "viewer"}))
	if pr := assertProblem(t, "granting", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden"); pr.Permission == nil ||
		*pr.Permission != "grant.manage" {
		t.Errorf("the refusal names %v, want grant.manage", pr.Permission)
	}
	if listed := do(c.ListGrantsWithResponse(t.Context(), p.Key)); listed.JSON200 == nil {
		t.Errorf("listing grants = %d: %s", listed.StatusCode(), listed.Body)
	}
}
