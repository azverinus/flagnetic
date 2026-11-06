package management_test

import (
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// scimAdmin returns a browser signed in as an instance admin, the only one
// who creates SCIM tokens.
func scimAdmin(t *testing.T, srv *testkit.Server) *testkit.Browser {
	t.Helper()
	testkit.InstanceAdminToken(t) // the package's instance is bootstrapped before it has any other admin
	return signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin()))
}

func mustCreateScimToken(t *testing.T, b *testkit.Browser, body management.ScimTokenCreate) management.CreatedScimToken {
	t.Helper()
	res := do(b.CreateScimTokenWithResponse(t.Context(), body))
	if res.JSON201 == nil {
		t.Fatalf("POST /instance/scim-tokens = %d: %s", res.StatusCode(), res.Body)
	}
	testkit.RecordSecret(res.JSON201.Value)
	return *res.JSON201
}

// listedScimToken returns the token with the identifier among items, nil when
// none has it: the tests of a package share one instance.
func listedScimToken(items []management.ScimToken, token [16]byte) *management.ScimToken {
	for i := range items {
		if items[i].Id == token {
			return &items[i]
		}
	}
	return nil
}

func TestAScimTokenIsBoundToOneProviderAndShownOnce(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c), testkit.WithBaseURL(providerBaseURL))
	admin := scimAdmin(t, srv)
	provider := createProvider(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), corporate(providerSlug()))
	expiry := c.Now().Add(30 * 24 * time.Hour)

	created := mustCreateScimToken(t, admin, management.ScimTokenCreate{Provider: provider.Slug, ExpiresAt: &expiry})
	if !regexp.MustCompile(`^fn_scim_[0-9A-Za-z]{40}$`).MatchString(created.Value) {
		t.Errorf("value %q is not fn_scim_ and 40 base62 characters", created.Value)
	}
	tok := created.Token
	if tok.Provider != provider.Slug || tok.Hint != created.Value[:len("fn_scim_")+4] || tok.ExpiresAt == nil ||
		!tok.ExpiresAt.Equal(expiry) || !tok.CreatedAt.Equal(c.Now()) || tok.LastUsedAt != nil || tok.RevokedAt != nil {
		t.Errorf("token = %+v, want one bound to %s, hinted, expiring %s, unused and unrevoked", tok, provider.Slug, expiry)
	}

	listed := do(admin.ListScimTokensWithResponse(t.Context()))
	if listed.JSON200 == nil || listedScimToken(listed.JSON200.Items, tok.Id) == nil || regexp.MustCompile(created.Value).Match(listed.Body) {
		t.Errorf("listed = %d %s, want the token without its value", listed.StatusCode(), listed.Body)
	}

	entries := auditOf(t, srv, "scim_token.created", tok.Id.String())
	if len(entries) != 1 {
		t.Fatalf("%d scim_token.created entries, want 1", len(entries))
	}
	after, _ := entries[0].After.(map[string]any)
	if entries[0].Actor.Kind != management.ActorKindMember || after["hint"] != tok.Hint || after["provider"] != provider.Slug {
		t.Errorf("entry = actor %+v after %+v, want the admin creating the hinted token for %s", entries[0].Actor, after, provider.Slug)
	}
}

func TestOnlyAnInstanceAdminsSessionCreatesAScimToken(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	provider := createProvider(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), corporate(providerSlug()))
	body := management.ScimTokenCreate{Provider: provider.Slug}

	res := do(testkit.Client(t, srv, testkit.InstanceAdminToken(t)).CreateScimTokenWithResponse(t.Context(), body))
	assertProblem(t, "an instance admin's token", res.StatusCode(), res.Body, http.StatusForbidden, "session_required")

	member := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t)))
	res = do(member.CreateScimTokenWithResponse(t.Context(), body))
	assertProblem(t, "a member", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
	if res := do(member.ListScimTokensWithResponse(t.Context())); res.StatusCode() != http.StatusForbidden {
		t.Errorf("a member listing = %d, want 403", res.StatusCode())
	}
}

func TestAScimTokenNamesAnExistingProviderAndAFutureExpiry(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c), testkit.WithBaseURL(providerBaseURL))
	admin := scimAdmin(t, srv)
	provider := createProvider(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), corporate(providerSlug()))
	past := c.Now().Add(-time.Hour)

	for name, body := range map[string]struct {
		body    management.ScimTokenCreate
		pointer string
	}{
		"an unknown provider": {management.ScimTokenCreate{Provider: "nowhere"}, "/provider"},
		"a past expiry":       {management.ScimTokenCreate{Provider: provider.Slug, ExpiresAt: &past}, "/expires_at"},
	} {
		res := do(admin.CreateScimTokenWithResponse(t.Context(), body.body))
		p := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_scim_token")
		if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != body.pointer {
			t.Errorf("%s: violations = %+v, want one at %s", name, p.Errors, body.pointer)
		}
	}
}

func TestRevokingAScimTokenIsAuditedAndFinal(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := scimAdmin(t, srv)
	provider := createProvider(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), corporate(providerSlug()))
	created := mustCreateScimToken(t, admin, management.ScimTokenCreate{Provider: provider.Slug})

	if res := do(admin.RevokeScimTokenWithResponse(t.Context(), created.Token.Id.String())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", res.StatusCode(), res.Body)
	}
	listed := do(admin.ListScimTokensWithResponse(t.Context()))
	if listed.JSON200 == nil || listedScimToken(listed.JSON200.Items, created.Token.Id) == nil ||
		listedScimToken(listed.JSON200.Items, created.Token.Id).RevokedAt == nil {
		t.Errorf("listed = %s, want the token revoked", listed.Body)
	}
	if entries := auditOf(t, srv, "scim_token.revoked", created.Token.Id.String()); len(entries) != 1 {
		t.Errorf("%d scim_token.revoked entries, want 1", len(entries))
	}
	res := do(admin.RevokeScimTokenWithResponse(t.Context(), created.Token.Id.String()))
	assertProblem(t, "revoking again", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
	res = do(admin.RevokeScimTokenWithResponse(t.Context(), "00000000-0000-0000-0000-000000000000"))
	assertProblem(t, "revoking an unknown token", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
}
