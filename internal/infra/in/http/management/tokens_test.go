package management_test

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// tokenServer serves the package's instance on a fake clock, for tests
// judging expiries.
func tokenServer(t *testing.T) (*testkit.Server, *pgxpool.Pool, *clock.Fake) {
	t.Helper()
	c := testkit.Clock()
	return testkit.StartServer(t, testkit.WithClock(c)), testkit.Pool(t), c
}

// createToken asks for a personal access token through b's session.
func createToken(t *testing.T, b *testkit.Browser, body management.ApiTokenCreate) *management.CreateMyTokenResponse {
	t.Helper()
	return do(b.CreateMyTokenWithResponse(t.Context(), body))
}

// mustCreateToken creates a personal access token through b's session,
// failing the test unless it is created.
func mustCreateToken(t *testing.T, b *testkit.Browser, body management.ApiTokenCreate) management.CreatedApiToken {
	t.Helper()
	res := createToken(t, b, body)
	if res.JSON201 == nil {
		t.Fatalf("POST /me/tokens = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON201
}

// myTokens returns the tokens c's member holds, as c lists them.
func myTokens(t *testing.T, c interface {
	ListMyTokensWithResponse(ctx context.Context, reqEditors ...management.RequestEditorFn) (*management.ListMyTokensResponse, error)
},
) []management.ApiToken {
	t.Helper()
	res := do(c.ListMyTokensWithResponse(t.Context()))
	if res.JSON200 == nil {
		t.Fatalf("GET /me/tokens = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func scopes(ss ...management.Scope) []management.Scope { return ss }

func TestAPersonalAccessTokenIsShownOnce(t *testing.T) {
	srv, pool, c := tokenServer(t)
	m := testkit.NewMember(t, pool)
	b := signedIn(t, srv, m)
	expiry := c.Now().Add(30 * 24 * time.Hour)

	created := mustCreateToken(t, b, management.ApiTokenCreate{
		Name: "deploy", Scopes: ptr(scopes(management.ScopeFlagsWrite)), ExpiresAt: &expiry,
	})
	if !regexp.MustCompile(`^fn_pat_[0-9A-Za-z]{40}$`).MatchString(created.Value) {
		t.Errorf("value %q is not fn_pat_ and 40 base62 characters", created.Value)
	}
	tok := created.Token
	if tok.Hint != created.Value[:len("fn_pat_")+4] || tok.Name != "deploy" ||
		!slices.Equal(tok.Scopes, scopes(management.ScopeRead, management.ScopeFlagsWrite)) ||
		tok.ExpiresAt == nil || !tok.ExpiresAt.Equal(expiry) || !tok.CreatedAt.Equal(c.Now()) ||
		tok.LastUsedAt != nil || tok.LastUsedIp != nil || tok.RevokedAt != nil {
		t.Errorf("token = %+v, want the hint, read beside flags:write, the expiry and no use yet", tok)
	}
	listed := do(b.ListMyTokensWithResponse(t.Context()))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 1 || listed.JSON200.Items[0].Id != tok.Id {
		t.Errorf("listed = %d %s, want the token alone", listed.StatusCode(), listed.Body)
	}
	if regexp.MustCompile(created.Value).Match(listed.Body) {
		t.Error("the list shows the token's value")
	}
	if got := me(t, srv, created.Value); got.Credential.TokenId == nil || *got.Credential.TokenId != tok.Id ||
		got.Member == nil || got.Member.Id != m.ID.Bytes() {
		t.Errorf("GET /me with the token = %+v, want the token acting for its member", got)
	}
}

func TestATokenCarriesReadAloneByDefault(t *testing.T) {
	srv := testkit.StartServer(t)
	b := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t)))
	for name, asked := range map[string]*[]management.Scope{"no scopes": nil, "an empty list": ptr(scopes())} {
		tok := mustCreateToken(t, b, management.ApiTokenCreate{Name: name, Scopes: asked}).Token
		if !slices.Equal(tok.Scopes, scopes(management.ScopeRead)) || tok.ExpiresAt != nil {
			t.Errorf("%s: scopes %v, expiry %v; want read alone and no expiry", name, tok.Scopes, tok.ExpiresAt)
		}
	}
}

func TestEveryWriteScopeImpliesRead(t *testing.T) {
	srv := testkit.StartServer(t)
	b := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin()))
	for _, s := range scopes(management.ScopeFlagsWrite, management.ScopeExperimentsWrite, management.ScopeProjectAdmin,
		management.ScopeInstanceAdmin) {
		tok := mustCreateToken(t, b, management.ApiTokenCreate{Name: string(s), Scopes: ptr(scopes(s))}).Token
		if !slices.Equal(tok.Scopes, scopes(management.ScopeRead, s)) {
			t.Errorf("%s: scopes = %v, want read and %s", s, tok.Scopes, s)
		}
	}
}

func TestATokenRequestIsValidated(t *testing.T) {
	srv := testkit.StartServer(t)
	b := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t)))
	for name, tc := range map[string]struct {
		body              management.ApiTokenCreate
		pointer, violated string
	}{
		"an unknown scope": {management.ApiTokenCreate{Name: "ci", Scopes: ptr(scopes("read", "flags:delete"))}, "/scopes/1", "unknown_scope"},
		"a blank name":     {management.ApiTokenCreate{Name: "  "}, "/name", "invalid_name"},
	} {
		res := createToken(t, b, tc.body)
		p := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_token_request")
		if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != tc.pointer || (*p.Errors)[0].Code != tc.violated {
			t.Errorf("%s: violations = %+v, want %s at %s", name, p.Errors, tc.violated, tc.pointer)
		}
	}
}

func TestInstanceAdminScopeIsForInstanceAdminsOnly(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	asked := management.ApiTokenCreate{Name: "admin script", Scopes: ptr(scopes(management.ScopeInstanceAdmin))}

	res := createToken(t, signedIn(t, srv, testkit.NewMember(t, pool)), asked)
	assertProblem(t, "a member", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "scope_not_allowed")
	tok := mustCreateToken(t, signedIn(t, srv, testkit.NewMember(t, pool, testkit.AsInstanceAdmin())), asked).Token
	if !slices.Equal(tok.Scopes, scopes(management.ScopeRead, management.ScopeInstanceAdmin)) {
		t.Errorf("an instance admin's token carries %v", tok.Scopes)
	}
}

// Every bearer is refused, however wide, so a leaked token can never
// multiply itself or outlive its own revocation.
func TestNoTokenCreatesAToken(t *testing.T) {
	srv := testkit.StartServer(t)
	wide := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	res := do(wide.CreateMyTokenWithResponse(t.Context(), management.ApiTokenCreate{
		Name: "child", Scopes: ptr(scopes(management.ScopeRead)),
	}))
	assertProblem(t, "the bootstrap token", res.StatusCode(), res.Body, http.StatusForbidden, "session_required")
}

func TestAReadOnlyTokenCannotCreateOrRevokeTokensButLists(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := signedIn(t, srv, m)
	sibling := mustCreateToken(t, b, management.ApiTokenCreate{Name: "sibling"})
	script := testkit.Client(t, srv, mustCreateToken(t, b, management.ApiTokenCreate{Name: "script"}).Value)

	created := do(script.CreateMyTokenWithResponse(t.Context(), management.ApiTokenCreate{Name: "child"}))
	assertProblem(t, "creating", created.StatusCode(), created.Body, http.StatusForbidden, "session_required")
	revoked := do(script.RevokeMyTokenWithResponse(t.Context(), sibling.Token.Id.String()))
	assertProblem(t, "revoking a sibling", revoked.StatusCode(), revoked.Body, http.StatusForbidden, "session_required")
	if got := myTokens(t, script); len(got) != 2 || got[0].RevokedAt != nil || got[1].RevokedAt != nil {
		t.Errorf("the token lists %+v, want both tokens unrevoked", got)
	}
}

func TestAnExpiryInThePastOrBeyondTheMaximumLifetimeIsRefused(t *testing.T) {
	srv, pool, c := tokenServer(t)
	b := signedIn(t, srv, testkit.NewMember(t, pool))
	maxLifetime := 365 * 24 * time.Hour
	for name, tc := range map[string]struct {
		expiry   time.Time
		violated string
	}{
		"a day beyond the maximum lifetime": {c.Now().Add(maxLifetime + 24*time.Hour), "beyond_max_lifetime"},
		"an hour ago":                       {c.Now().Add(-time.Hour), "in_past"},
	} {
		res := createToken(t, b, management.ApiTokenCreate{Name: name, ExpiresAt: &tc.expiry})
		p := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_token_request")
		if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != "/expires_at" || (*p.Errors)[0].Code != tc.violated {
			t.Errorf("%s: violations = %+v, want %s at /expires_at", name, p.Errors, tc.violated)
		}
	}
	limit := c.Now().Add(maxLifetime)
	mustCreateToken(t, b, management.ApiTokenCreate{Name: "exactly the maximum lifetime", ExpiresAt: &limit})
}

func TestAnExpiredTokenIsRefused(t *testing.T) {
	srv, pool, c := tokenServer(t)
	b := signedIn(t, srv, testkit.NewMember(t, pool))
	expiry := c.Now().Add(time.Hour)
	value := mustCreateToken(t, b, management.ApiTokenCreate{Name: "short", ExpiresAt: &expiry}).Value

	me(t, srv, value)
	c.Set(expiry)
	res := get(t, srv.URL+"/api/v1/me", map[string]string{"Authorization": "Bearer " + value})
	assertRefused(t, "at its expiry", res, "token_expired")
}

func TestRevokingATokenEndsItAndIsAudited(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	m := testkit.NewMember(t, pool)
	b := signedIn(t, srv, m)
	created := mustCreateToken(t, b, management.ApiTokenCreate{Name: "to revoke"})
	id := created.Token.Id.String()

	if res := do(b.RevokeMyTokenWithResponse(t.Context(), id)); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /me/tokens/{token} = %d: %s", res.StatusCode(), res.Body)
	}
	assertRefused(t, "the revoked token", get(t, srv.URL+"/api/v1/me", map[string]string{"Authorization": "Bearer " + created.Value}),
		"token_revoked")
	if got := myTokens(t, b); len(got) != 1 || got[0].RevokedAt == nil {
		t.Errorf("listed = %+v, want the token revoked", got)
	}
	again := do(b.RevokeMyTokenWithResponse(t.Context(), id))
	assertProblem(t, "revoking again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
	other := mustCreateToken(t, signedIn(t, srv, testkit.NewMember(t, pool)), management.ApiTokenCreate{Name: "another's"})
	theirs := do(b.RevokeMyTokenWithResponse(t.Context(), other.Token.Id.String()))
	assertProblem(t, "another member's token", theirs.StatusCode(), theirs.Body, http.StatusNotFound, "not_found")
	malformed := do(b.RevokeMyTokenWithResponse(t.Context(), "not-an-identifier"))
	assertProblem(t, "no identifier", malformed.StatusCode(), malformed.Body, http.StatusNotFound, "not_found")

	for action, revokedAt := range map[string]bool{"api_token.created": false, "api_token.revoked": true} {
		entries := auditOf(t, srv, action, id)
		if len(entries) != 1 {
			t.Errorf("%s recorded %d times, want once", action, len(entries))
			continue
		}
		e := entries[0]
		after, _ := e.After.(map[string]any)
		if e.Actor.Kind != management.ActorKindMember || e.Actor.MemberId == nil || *e.Actor.MemberId != m.ID.Bytes() ||
			after["hint"] != created.Token.Hint || after["member"] != m.ID.String() || (after["revoked_at"] != nil) != revokedAt {
			t.Errorf("%s = actor %+v, after %v; want the member, the hint and revoked_at %v", action, e.Actor, after, revokedAt)
		}
	}
}

func TestATokenOfADeactivatedMemberIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	value := mustCreateToken(t, signedIn(t, srv, m), management.ApiTokenCreate{Name: "script"}).Value
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	if res := do(admin.DeactivateMemberWithResponse(t.Context(), m.ID.String())); res.JSON200 == nil {
		t.Fatalf("deactivating = %d: %s", res.StatusCode(), res.Body)
	}
	assertRefused(t, "after deactivation", get(t, srv.URL+"/api/v1/me", map[string]string{"Authorization": "Bearer " + value}),
		"token_revoked")
}

func TestATokensLastUseIsRecordedAtMostOnceAMinute(t *testing.T) {
	srv, pool, c := tokenServer(t)
	b := signedIn(t, srv, testkit.NewMember(t, pool))
	value := mustCreateToken(t, b, management.ApiTokenCreate{Name: "script"}).Value
	lastUse := func() (*time.Time, *string) {
		t.Helper()
		if err := srv.Tick(t, worker.TokenLastUseName); err != nil {
			t.Fatal(err)
		}
		tok := myTokens(t, b)[0]
		return tok.LastUsedAt, tok.LastUsedIp
	}

	first := c.Now()
	me(t, srv, value)
	if at, ip := lastUse(); at == nil || !at.Equal(first) || ip == nil || *ip != "127.0.0.1" {
		t.Errorf("after the first use: %v from %v, want %v from 127.0.0.1", at, ip, first)
	}
	c.Advance(time.Second)
	me(t, srv, value)
	if at, _ := lastUse(); at == nil || !at.Equal(first) {
		t.Errorf("a second later: last use %v, want %v unchanged", at, first)
	}
	c.Advance(time.Minute)
	me(t, srv, value)
	if at, _ := lastUse(); at == nil || !at.Equal(c.Now()) {
		t.Errorf("a minute later: last use %v, want %v", at, c.Now())
	}
}

func TestAnInstanceAdminSeesAndRevokesAMembersTokens(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	m := testkit.NewMember(t, pool)
	created := mustCreateToken(t, signedIn(t, srv, m), management.ApiTokenCreate{Name: "script"})
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))

	listed := do(admin.ListMemberTokensWithResponse(t.Context(), m.ID.String()))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 1 || listed.JSON200.Items[0].Id != created.Token.Id {
		t.Errorf("listed = %d %s, want the member's token", listed.StatusCode(), listed.Body)
	}
	revoked := do(admin.RevokeMemberTokenWithResponse(t.Context(), m.ID.String(), created.Token.Id.String()))
	if revoked.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /members/{member}/tokens/{token} = %d: %s", revoked.StatusCode(), revoked.Body)
	}
	assertRefused(t, "the revoked token", get(t, srv.URL+"/api/v1/me", map[string]string{"Authorization": "Bearer " + created.Value}),
		"token_revoked")

	member := testkit.Client(t, srv, testkit.MemberToken(t, pool))
	refused := do(member.ListMemberTokensWithResponse(t.Context(), m.ID.String()))
	assertProblem(t, "a member", refused.StatusCode(), refused.Body, http.StatusForbidden, "forbidden")
	unknown := do(admin.ListMemberTokensWithResponse(t.Context(), access.MemberID{}.String()))
	assertProblem(t, "an unknown member", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
}

// An instance admin's own tokens are revoked only from a session, whichever
// route names them, so a leaked token cannot hide its siblings.
func TestAnInstanceAdminsTokenCannotRevokeItsOwnersTokensThroughTheMembersRoute(t *testing.T) {
	srv := testkit.StartServer(t)
	a := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := signedIn(t, srv, a)
	sibling := mustCreateToken(t, b, management.ApiTokenCreate{Name: "sibling"})
	script := testkit.Client(t, srv, mustCreateToken(t, b, management.ApiTokenCreate{
		Name: "admin script", Scopes: ptr(scopes(management.ScopeInstanceAdmin)),
	}).Value)

	res := do(script.RevokeMemberTokenWithResponse(t.Context(), a.ID.String(), sibling.Token.Id.String()))
	assertProblem(t, "its own member's token", res.StatusCode(), res.Body, http.StatusForbidden, "session_required")
	if res := do(b.RevokeMemberTokenWithResponse(t.Context(), a.ID.String(), sibling.Token.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Errorf("from a session = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestAPersonalTokensPermissionsAreNarrowedByItsScopes(t *testing.T) {
	srv := testkit.StartServer(t)
	a := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := signedIn(t, srv, a)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	reader := testkit.Client(t, srv, mustCreateToken(t, b, management.ApiTokenCreate{Name: "reader"}).Value)

	res := do(reader.GetProjectMeWithResponse(t.Context(), p.Key))
	if res.JSON200 == nil || !slices.Equal(res.JSON200.Permissions, []string{"project.read"}) {
		t.Errorf("GET me with a read-only token = %d %s, want project.read alone", res.StatusCode(), res.Body)
	}
	// The scope is judged before the project, so a token learns nothing of
	// which keys exist from a write it cannot make.
	name := "x"
	for _, key := range []string{p.Key, "no-such-project"} {
		patch := do(reader.UpdateProjectWithResponse(t.Context(), key, &management.UpdateProjectParams{IfMatch: ifMatch(1)}, management.ProjectPatch{Name: &name}))
		assertProblem(t, key, patch.StatusCode(), patch.Body, http.StatusForbidden, "insufficient_scope")
	}
}
