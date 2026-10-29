package management_test

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

const providerBaseURL = "https://flags.example.com"

func providerSlug() string { return "p" + strings.ToLower(rand.Text()[:12]) }

func corporate(slug string) management.AuthProviderCreate {
	return management.AuthProviderCreate{
		Slug: slug, Kind: management.AuthProviderKindOidc, DisplayName: "Corporate SSO", Domains: []string{"example.com"},
		Issuer: "https://idp.example.com/realms/corp", ClientId: "flagnetic", ClientSecret: ptr("client-secret-" + rand.Text()),
	}
}

func createProvider(t *testing.T, c *management.ClientWithResponses, body management.AuthProviderCreate) management.AuthProvider {
	t.Helper()
	if body.ClientSecret != nil {
		testkit.RecordSecret(*body.ClientSecret)
	}
	res := do(c.CreateAuthProviderWithResponse(t.Context(), body))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST /instance/auth-providers = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	return *res.JSON201
}

func patchProvider(t *testing.T, c *management.ClientWithResponses, slug string, version int64, body management.AuthProviderPatch) *management.UpdateAuthProviderResponse {
	t.Helper()
	return do(c.UpdateAuthProviderWithResponse(t.Context(), slug, &management.UpdateAuthProviderParams{IfMatch: ifMatch(version)}, body))
}

func providerSlugs(t *testing.T, c *management.ClientWithResponses) []string {
	t.Helper()
	res := do(c.ListAuthProvidersWithResponse(t.Context()))
	if res.JSON200 == nil {
		t.Fatalf("GET /instance/auth-providers = %d: %s", res.StatusCode(), res.Body)
	}
	out := make([]string, len(res.JSON200.Items))
	for i, p := range res.JSON200.Items {
		out[i] = p.Slug
	}
	return out
}

func offeredSlugs(t *testing.T, c *management.ClientWithResponses) []string {
	t.Helper()
	res := do(c.GetAuthMethodsWithResponse(t.Context()))
	if res.JSON200 == nil {
		t.Fatalf("GET /auth/methods = %d: %s", res.StatusCode(), res.Body)
	}
	out := make([]string, len(res.JSON200.Providers))
	for i, p := range res.JSON200.Providers {
		out[i] = p.Slug
	}
	return out
}

// providerEntry returns the one audit entry of action on provider, its before
// and after decoded, failing if it holds a value recorded as secret.
func providerEntry(t *testing.T, srv *testkit.Server, action string, p management.AuthProvider) (management.AuditEntry, map[string]any, map[string]any) {
	t.Helper()
	entries := auditOf(t, srv, action, p.Id.String())
	if len(entries) != 1 {
		t.Fatalf("%d entries of %s, want 1", len(entries), action)
	}
	e := entries[0]
	before, _ := e.Before.(map[string]any)
	after, _ := e.After.(map[string]any)
	return e, before, after
}

func TestCreatingAnAuthProviderAnswersItWithoutItsSecretAndWithTheRedirectURIToRegister(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	slug := providerSlug()
	body := corporate(slug)
	body.Domains = []string{" Example.COM ", "corp.example.org"}

	p := createProvider(t, admin, body)

	if p.Slug != slug || p.Kind != management.AuthProviderKindOidc || p.DisplayName != "Corporate SSO" || !p.Enabled ||
		!slices.Equal(p.Domains, []string{"example.com", "corp.example.org"}) || p.LinkByEmail || p.AutoProvision ||
		p.Version != 1 {
		t.Errorf("created %+v", p)
	}
	if p.Issuer == nil || *p.Issuer != body.Issuer || p.ClientId == nil || *p.ClientId != "flagnetic" ||
		p.ClientSecretSet == nil || !*p.ClientSecretSet || p.RequireVerifiedEmail == nil || !*p.RequireVerifiedEmail {
		t.Errorf("OIDC part %+v", p)
	}
	if p.Scopes == nil || !slices.Equal(*p.Scopes, []string{"openid", "email", "profile"}) {
		t.Errorf("scopes = %v", p.Scopes)
	}
	if p.Claims == nil || *p.Claims != (management.AuthProviderClaims{Email: "email", Name: "name", Groups: "groups"}) {
		t.Errorf("claims = %+v", p.Claims)
	}
	if p.RedirectUri == nil || *p.RedirectUri != providerBaseURL+"/sso/"+slug+"/callback" {
		t.Errorf("redirect_uri = %v", p.RedirectUri)
	}

	got := do(admin.GetAuthProviderWithResponse(t.Context(), slug))
	if got.JSON200 == nil || got.HTTPResponse.Header.Get("ETag") != `"1"` || got.JSON200.Id != p.Id {
		t.Fatalf("GET = %d: %s", got.StatusCode(), got.Body)
	}
	if strings.Contains(string(got.Body), *body.ClientSecret) {
		t.Error("the answer holds the client secret")
	}
	if !slices.Contains(providerSlugs(t, admin), slug) {
		t.Error("the list lacks the provider")
	}

	e, before, after := providerEntry(t, srv, "auth_provider.created", p)
	if e.ProjectId != nil || e.Entity.Key != slug || before != nil || after["client_secret"] != "[redacted]" ||
		after["issuer"] != body.Issuer {
		t.Errorf("auth_provider.created = project %v, entity %+v, before %v, after %v", e.ProjectId, e.Entity, before, after)
	}
}

func TestTheClientSecretIsSealedAndAnEditKeepsItUnlessItNamesANewOne(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	slug := providerSlug()
	body := corporate(slug)
	createProvider(t, admin, body)
	store := pgidentity.NewStore(testkit.Pool(t))
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	opened := func() string {
		t.Helper()
		p, err := store.Provider(t.Context(), slug)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p.OIDC.SealedClientSecret, *body.ClientSecret) {
			t.Fatal("the stored secret is not sealed")
		}
		plain, err := keyring.Open(secret.PurposeOIDCClientSecret, p.OIDC.SealedClientSecret)
		if err != nil {
			t.Fatal(err)
		}
		return string(plain)
	}
	if opened() != *body.ClientSecret {
		t.Fatal("the sealed secret does not open to the secret sent")
	}

	if res := patchProvider(t, admin, slug, 1, management.AuthProviderPatch{DisplayName: ptr("Renamed")}); res.JSON200 == nil || res.JSON200.Version != 2 {
		t.Fatalf("PATCH = %d: %s", res.StatusCode(), res.Body)
	}
	if opened() != *body.ClientSecret {
		t.Error("an edit naming no secret changed it")
	}

	replacement := "replacement-" + rand.Text()
	testkit.RecordSecret(replacement)
	res := patchProvider(t, admin, slug, 2, management.AuthProviderPatch{ClientSecret: &replacement})
	if res.JSON200 == nil || res.JSON200.Version != 3 || strings.Contains(string(res.Body), replacement) {
		t.Fatalf("PATCH with a secret = %d: %s", res.StatusCode(), res.Body)
	}
	if opened() != replacement {
		t.Error("the new secret was not stored")
	}
	blank := patchProvider(t, admin, slug, 3, management.AuthProviderPatch{ClientSecret: ptr(" ")})
	assertProblem(t, "a blank secret", blank.StatusCode(), blank.Body, http.StatusUnprocessableEntity, "invalid_auth_provider")
}

func TestAnAuthProviderIsRefusedForEachRuleItBreaks(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	for name, tc := range map[string]struct {
		change  func(*management.AuthProviderCreate)
		code    string
		pointer string
	}{
		"a slug starting with a digit": {func(b *management.AuthProviderCreate) { b.Slug = "1corp" }, "invalid_auth_provider", "/slug"},
		"an uppercase slug":            {func(b *management.AuthProviderCreate) { b.Slug = "Corp" }, "invalid_auth_provider", "/slug"},
		"another kind":                 {func(b *management.AuthProviderCreate) { b.Kind = "saml" }, "invalid_auth_provider", "/kind"},
		"no domain":                    {func(b *management.AuthProviderCreate) { b.Domains = nil }, "invalid_auth_provider", "/domains"},
		"an email as a domain":         {func(b *management.AuthProviderCreate) { b.Domains = []string{"a@example.com"} }, "invalid_auth_provider", "/domains/0"},
		"a relative issuer":            {func(b *management.AuthProviderCreate) { b.Issuer = "idp.example.com" }, "invalid_auth_provider", "/issuer"},
		"no client secret":             {func(b *management.AuthProviderCreate) { b.ClientSecret = nil }, "invalid_auth_provider", "/client_secret"},
		"a plain http issuer":          {func(b *management.AuthProviderCreate) { b.Issuer = "http://idp.example.com" }, "insecure_url", "/issuer"},
		"a plain http issuer on a private network": {
			func(b *management.AuthProviderCreate) { b.Issuer = "http://10.0.0.5:8080" }, "insecure_url", "/issuer",
		},
	} {
		body := corporate(providerSlug())
		tc.change(&body)
		res := do(admin.CreateAuthProviderWithResponse(t.Context(), body))
		p := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, tc.code)
		if p.Errors == nil || !slices.ContainsFunc(*p.Errors, func(v management.Violation) bool { return v.Pointer == tc.pointer }) {
			t.Errorf("%s: violations %s, want one at %s", name, raw(p.Errors), tc.pointer)
		}
	}

	// The same rules judge a loopback http issuer and a private https one fit.
	for _, issuer := range []string{"http://localhost:5556/dex", "http://127.0.0.1:8080", "http://[::1]:8080", "https://10.0.0.5/realms/a"} {
		body := corporate(providerSlug())
		body.Issuer = issuer
		createProvider(t, admin, body)
	}
}

func TestAnAuthProviderSlugIsTakenOnce(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	slug := providerSlug()
	createProvider(t, admin, corporate(slug))
	res := do(admin.CreateAuthProviderWithResponse(t.Context(), corporate(slug)))
	assertProblem(t, "the same slug", res.StatusCode(), res.Body, http.StatusConflict, "already_exists")
}

func TestAnInstanceWithoutABaseURLConfiguresNoAuthProvider(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	res := do(admin.CreateAuthProviderWithResponse(t.Context(), corporate(providerSlug())))
	assertProblem(t, "create", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "base_url_required")

	// One configured while the instance knew its address still reads, without
	// a redirect URI it cannot build, and neither edits nor enables.
	with := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	slug := providerSlug()
	disabled := corporate(slug)
	disabled.Enabled = ptr(false)
	createProvider(t, testkit.Client(t, with, testkit.InstanceAdminToken(t)), disabled)
	got := do(admin.GetAuthProviderWithResponse(t.Context(), slug))
	if got.JSON200 == nil || got.JSON200.RedirectUri != nil {
		t.Fatalf("GET without a base URL = %d: %s", got.StatusCode(), got.Body)
	}
	edit := patchProvider(t, admin, slug, 1, management.AuthProviderPatch{DisplayName: ptr("Renamed")})
	assertProblem(t, "edit", edit.StatusCode(), edit.Body, http.StatusUnprocessableEntity, "base_url_required")
	enable := do(admin.EnableAuthProviderWithResponse(t.Context(), slug))
	assertProblem(t, "enable", enable.StatusCode(), enable.Body, http.StatusUnprocessableEntity, "base_url_required")
	if off := do(admin.DisableAuthProviderWithResponse(t.Context(), slug)); off.StatusCode() != http.StatusOK {
		t.Errorf("disabling a disabled provider = %d: %s", off.StatusCode(), off.Body)
	}
}

func TestEditingAnAuthProviderNeedsItsVersionAndRecordsWhatChanged(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	slug := providerSlug()
	p := createProvider(t, admin, corporate(slug))

	missing := do(admin.UpdateAuthProviderWithResponse(t.Context(), slug, &management.UpdateAuthProviderParams{},
		management.AuthProviderPatch{DisplayName: ptr("Late")}))
	assertProblem(t, "no If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")

	res := patchProvider(t, admin, slug, 1, management.AuthProviderPatch{
		DisplayName: ptr("Acme SSO"), Domains: &[]string{"Acme.com"}, LinkByEmail: ptr(true), AutoProvision: ptr(true),
		Scopes: &[]string{"openid", "email"}, Claims: &management.AuthProviderClaimsPatch{Groups: ptr("roles")},
		RequireVerifiedEmail: ptr(false),
	})
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("PATCH = %d: %s", res.StatusCode(), res.Body)
	}
	got := res.JSON200
	if got.DisplayName != "Acme SSO" || !slices.Equal(got.Domains, []string{"acme.com"}) || !got.LinkByEmail || !got.AutoProvision ||
		!slices.Equal(*got.Scopes, []string{"openid", "email"}) || *got.RequireVerifiedEmail ||
		*got.Claims != (management.AuthProviderClaims{Email: "email", Name: "name", Groups: "roles"}) ||
		got.Slug != slug || got.Kind != management.AuthProviderKindOidc || got.Enabled != p.Enabled {
		t.Errorf("edited %+v", got)
	}

	stale := patchProvider(t, admin, slug, 1, management.AuthProviderPatch{DisplayName: ptr("Stale")})
	assertProblem(t, "a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	insecure := patchProvider(t, admin, slug, 2, management.AuthProviderPatch{Issuer: ptr("http://idp.example.com")})
	assertProblem(t, "an http issuer", insecure.StatusCode(), insecure.Body, http.StatusUnprocessableEntity, "insecure_url")
	empty := patchProvider(t, admin, slug, 2, management.AuthProviderPatch{Domains: &[]string{}})
	assertProblem(t, "no domain", empty.StatusCode(), empty.Body, http.StatusUnprocessableEntity, "invalid_auth_provider")

	same := patchProvider(t, admin, slug, 2, management.AuthProviderPatch{DisplayName: ptr("Acme SSO")})
	if same.JSON200 == nil || same.JSON200.Version != 2 {
		t.Fatalf("an edit changing nothing = %d: %s", same.StatusCode(), same.Body)
	}

	_, before, after := providerEntry(t, srv, "auth_provider.updated", p)
	if before["display_name"] != "Corporate SSO" || after["display_name"] != "Acme SSO" || after["client_secret"] != "[redacted]" {
		t.Errorf("auth_provider.updated = before %v, after %v", before, after)
	}
}

func TestEnablingAndDisablingAnAuthProviderChangesWhatSignInOffers(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	anyone := testkit.Client(t, srv, "")
	slug := providerSlug()
	body := corporate(slug)
	body.Enabled = ptr(false)
	p := createProvider(t, admin, body)
	if slices.Contains(offeredSlugs(t, anyone), slug) {
		t.Fatal("a disabled provider is offered")
	}

	on := do(admin.EnableAuthProviderWithResponse(t.Context(), slug))
	if on.JSON200 == nil || !on.JSON200.Enabled || on.JSON200.Version != 2 || on.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("enable = %d: %s", on.StatusCode(), on.Body)
	}
	if again := do(admin.EnableAuthProviderWithResponse(t.Context(), slug)); again.JSON200 == nil || again.JSON200.Version != 2 {
		t.Errorf("enabling an enabled provider = %d: %s", again.StatusCode(), again.Body)
	}
	methods := do(anyone.GetAuthMethodsWithResponse(t.Context()))
	if methods.JSON200 == nil || !methods.JSON200.LocalLoginEnabled {
		t.Fatalf("GET /auth/methods = %d: %s", methods.StatusCode(), methods.Body)
	}
	i := slices.IndexFunc(methods.JSON200.Providers, func(m management.AuthMethodProvider) bool { return m.Slug == slug })
	if i < 0 || methods.JSON200.Providers[i] != (management.AuthMethodProvider{Slug: slug, DisplayName: "Corporate SSO", Kind: management.AuthProviderKindOidc}) {
		t.Fatalf("an enabled provider is offered as %+v", methods.JSON200.Providers)
	}
	if strings.Contains(string(methods.Body), "idp.example.com") {
		t.Error("the sign-in methods say more than slug, name and kind")
	}

	off := do(admin.DisableAuthProviderWithResponse(t.Context(), slug))
	if off.JSON200 == nil || off.JSON200.Enabled || off.JSON200.Version != 3 {
		t.Fatalf("disable = %d: %s", off.StatusCode(), off.Body)
	}
	do(admin.DisableAuthProviderWithResponse(t.Context(), slug))
	if slices.Contains(offeredSlugs(t, anyone), slug) {
		t.Error("a disabled provider is offered")
	}

	for action, count := range map[string]int{"auth_provider.enabled": 1, "auth_provider.disabled": 1} {
		if n := len(auditOf(t, srv, action, p.Id.String())); n != count {
			t.Errorf("%d entries of %s, want %d", n, action, count)
		}
	}
	missing := do(admin.EnableAuthProviderWithResponse(t.Context(), providerSlug()))
	assertProblem(t, "enabling an unknown provider", missing.StatusCode(), missing.Body, http.StatusNotFound, "not_found")
}

func TestDeletingAnAuthProviderRemovesItAndIsAudited(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	slug := providerSlug()
	p := createProvider(t, admin, corporate(slug))

	if res := do(admin.DeleteAuthProviderWithResponse(t.Context(), slug)); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", res.StatusCode(), res.Body)
	}
	read := do(admin.GetAuthProviderWithResponse(t.Context(), slug))
	assertProblem(t, "reading it", read.StatusCode(), read.Body, http.StatusNotFound, "not_found")
	again := do(admin.DeleteAuthProviderWithResponse(t.Context(), slug))
	assertProblem(t, "deleting it again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
	if slices.Contains(providerSlugs(t, admin), slug) {
		t.Error("the list holds a deleted provider")
	}
	_, before, after := providerEntry(t, srv, "auth_provider.deleted", p)
	if before["slug"] != slug || after != nil || before["client_secret"] != "[redacted]" {
		t.Errorf("auth_provider.deleted = before %v, after %v", before, after)
	}
	// Its slug is free again.
	createProvider(t, admin, corporate(slug))
}

func TestOnlyAnInstanceAdminManagesAuthProviders(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	slug := providerSlug()
	createProvider(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), corporate(slug))
	member := testkit.Client(t, srv, testkit.MemberToken(t, testkit.Pool(t)))
	anonymous := testkit.Client(t, srv, "")
	for name, c := range map[string]*management.ClientWithResponses{"a member": member, "nobody": anonymous} {
		want := http.StatusForbidden
		if c == anonymous {
			want = http.StatusUnauthorized
		}
		for op, res := range map[string]*http.Response{
			"list":    do(c.ListAuthProvidersWithResponse(t.Context())).HTTPResponse,
			"create":  do(c.CreateAuthProviderWithResponse(t.Context(), corporate(providerSlug()))).HTTPResponse,
			"get":     do(c.GetAuthProviderWithResponse(t.Context(), slug)).HTTPResponse,
			"update":  do(c.UpdateAuthProviderWithResponse(t.Context(), slug, &management.UpdateAuthProviderParams{IfMatch: ifMatch(1)}, management.AuthProviderPatch{})).HTTPResponse,
			"delete":  do(c.DeleteAuthProviderWithResponse(t.Context(), slug)).HTTPResponse,
			"enable":  do(c.EnableAuthProviderWithResponse(t.Context(), slug)).HTTPResponse,
			"disable": do(c.DisableAuthProviderWithResponse(t.Context(), slug)).HTTPResponse,
			"test":    do(c.TestAuthProviderWithResponse(t.Context(), slug)).HTTPResponse,
		} {
			if res.StatusCode != want {
				t.Errorf("%s: %s = %d, want %d", name, op, res.StatusCode, want)
			}
		}
	}
}

func TestTestingAnAuthProviderAnswersWhatItsDiscoveryDocumentSays(t *testing.T) {
	dex := testkit.StartDex(t)
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	slug := providerSlug()
	body := corporate(slug)
	body.Issuer, body.ClientId, body.Enabled = dex.Issuer, dex.ClientID, ptr(false)
	createProvider(t, admin, body)

	res := do(admin.TestAuthProviderWithResponse(t.Context(), slug))
	if res.JSON200 == nil {
		t.Fatalf("POST .../test = %d: %s", res.StatusCode(), res.Body)
	}
	d := res.JSON200
	if d.Issuer != dex.Issuer || d.AuthorizationEndpoint != dex.Issuer+"/auth" || d.TokenEndpoint != dex.Issuer+"/token" ||
		d.JwksUri != dex.Issuer+"/keys" || d.UserinfoEndpoint == nil || *d.UserinfoEndpoint != dex.Issuer+"/userinfo" {
		t.Errorf("found %+v", d)
	}
	if d.CodeChallengeMethodsSupported == nil || !slices.Contains(*d.CodeChallengeMethodsSupported, "S256") {
		t.Errorf("code challenge methods = %v, want S256 among them", d.CodeChallengeMethodsSupported)
	}
	if len(auditOf(t, srv, "auth_provider.updated", mustProvider(t, admin, slug).Id.String())) != 0 {
		t.Error("testing a provider was audited as an edit")
	}
}

func mustProvider(t *testing.T, c *management.ClientWithResponses, slug string) management.AuthProvider {
	t.Helper()
	res := do(c.GetAuthProviderWithResponse(t.Context(), slug))
	if res.JSON200 == nil {
		t.Fatalf("GET provider = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func TestTestingAnAuthProviderThatCannotBeReachedIsABadGatewaySayingWhy(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))

	notADocument := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "<html>"+strings.Repeat("not here ", 8<<10)+"</html>", http.StatusNotFound)
	}))
	t.Cleanup(notADocument.Close)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":"x","token_endpoint":"y","jwks_uri":"z"}`, "https://someone-else.example")
	}))
	t.Cleanup(other.Close)
	tooLarge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issuer":"x","padding":"`)
		for range 2 << 20 {
			fmt.Fprint(w, "a")
		}
		fmt.Fprint(w, `"}`)
	}))
	t.Cleanup(tooLarge.Close)

	for name, issuer := range map[string]string{
		"nothing listening":             "http://127.0.0.1:1",
		"an answer that is no document": notADocument.URL,
		"a document of another issuer":  other.URL,
		"an answer over a megabyte":     tooLarge.URL,
	} {
		slug := providerSlug()
		body := corporate(slug)
		body.Issuer = issuer
		createProvider(t, admin, body)
		res := do(admin.TestAuthProviderWithResponse(t.Context(), slug))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusBadGateway, "provider_unreachable")
		var ext struct {
			Failure string `json:"failure"`
		}
		if err := json.Unmarshal(res.Body, &ext); err != nil || ext.Failure == "" {
			t.Errorf("%s: no failure in %s (%v)", name, res.Body, err)
		}
		if len(ext.Failure) > 1<<10 {
			t.Errorf("%s: failure of %d bytes, want it cut short", name, len(ext.Failure))
		}
	}
	unknown := do(admin.TestAuthProviderWithResponse(t.Context(), providerSlug()))
	assertProblem(t, "an unknown provider", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
}

// redirecting answers the discovery document of its own URL after hops
// redirects, each to the next path on the same server.
func redirecting(t *testing.T, hops int) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
		if n < hops {
			http.Redirect(w, r, fmt.Sprintf("/hop/%d", n+1), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":"%[1]s/auth","token_endpoint":"%[1]s/token","jwks_uri":"%[1]s/keys"}`, srv.URL)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTestingAnAuthProviderFollowsThreeRedirectsAndNeverToPlainHTTPAwayFromLoopback(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL(providerBaseURL))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	test := func(issuer string) *management.TestAuthProviderResponse {
		t.Helper()
		slug := providerSlug()
		body := corporate(slug)
		body.Issuer = issuer
		createProvider(t, admin, body)
		return do(admin.TestAuthProviderWithResponse(t.Context(), slug))
	}

	if res := test(redirecting(t, 3).URL); res.JSON200 == nil {
		t.Errorf("three redirects = %d: %s", res.StatusCode(), res.Body)
	}
	res := test(redirecting(t, 4).URL)
	assertProblem(t, "four redirects", res.StatusCode(), res.Body, http.StatusBadGateway, "provider_unreachable")

	downgrade := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://idp.example.com"+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(downgrade.Close)
	res = test(downgrade.URL)
	assertProblem(t, "a redirect to plain http away from loopback", res.StatusCode(), res.Body, http.StatusBadGateway, "provider_unreachable")
	if !strings.Contains(string(res.Body), "plain http") {
		t.Errorf("the failure does not say the redirect went to plain http: %s", res.Body)
	}
}
