package sso_test

import (
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// createKeycloak creates a SAML provider whose slug Keycloak has a client
// for, with the metadata Keycloak serves for its realm.
func (e env) createKeycloak(t *testing.T, kc *testkit.Keycloak, change func(*management.AuthProviderCreate)) management.AuthProvider {
	t.Helper()
	slug := testkit.KeycloakSlug(t)
	return e.createSAMLAt(t, kc.MetadataURL, func(b *management.AuthProviderCreate) {
		b.Slug = slug
		if change != nil {
			change(b)
		}
	})
}

func (e env) createSAMLAt(t *testing.T, metadataURL string, change func(*management.AuthProviderCreate)) management.AuthProvider {
	t.Helper()
	body := management.AuthProviderCreate{
		Slug: slug(), Kind: management.AuthProviderKindSaml, DisplayName: "Keycloak", Domains: []string{"saml.example.com"},
		IdpMetadataUrl: &metadataURL, AutoProvision: ptr(true),
	}
	if change != nil {
		change(&body)
	}
	res, err := e.admin.CreateAuthProviderWithResponse(t.Context(), body)
	if err != nil || res.JSON201 == nil {
		t.Fatalf("creating the SAML provider: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	return *res.JSON201
}

func TestSigningInThroughKeycloakProvisionsAMemberAndOpensASessionForTheProvider(t *testing.T) {
	kc := testkit.StartKeycloak(t)
	e := start(t)
	p := e.createKeycloak(t, kc, nil)
	ada := kc.Users[0]
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Login = testkit.KeycloakLogin(ada)

	v := e.signIn(t, b, p.Slug, "/projects/x/flags?tab=a", o)

	if v.URL.Path != "/projects/x/flags" || v.URL.RawQuery != "tab=a" {
		t.Fatalf("the sign-in ended at %s (%d): %s", v.URL, v.Status, v.Body[max(0, len(v.Body)-400):])
	}
	me := signedIn(t, b)
	if me == nil || me.Member == nil || me.Member.Email != ada.Email || me.Member.DisplayName != ada.FirstName {
		t.Fatalf("after signing in: %+v", me)
	}
	sessions, err := b.ListMySessionsWithResponse(t.Context())
	if err != nil || sessions.JSON200 == nil || len(sessions.JSON200.Items) != 1 {
		t.Fatalf("GET /me/sessions = %v, %v", sessions.StatusCode(), err)
	}
	if s := sessions.JSON200.Items[0]; s.Method != management.SessionMethodSaml || s.Provider == nil || *s.Provider != p.Slug {
		t.Errorf("the session = method %s, provider %v; want saml through %s", s.Method, s.Provider, p.Slug)
	}

	// The next sign-in finds the member through the identity it linked.
	again := testkit.NewBrowser(t, e.srv.URL)
	if v = e.signIn(t, again, p.Slug, "", o); v.URL.Path != "/" {
		t.Errorf("the second sign-in ended at %s", v.URL)
	}
	if other := signedIn(t, again); other == nil || other.Member == nil || other.Member.Id != me.Member.Id {
		t.Errorf("the second sign-in = %+v, want the same member", other)
	}
	if n := len(e.audited(t, "member.provisioned", "email", ada.Email)); n != 1 {
		t.Errorf("%d members provisioned for one person", n)
	}
}

func TestAPersonKeycloakKnowsOutsideTheProvidersDomainsIsNotProvisioned(t *testing.T) {
	kc := testkit.StartKeycloak(t)
	e := start(t)
	p := e.createKeycloak(t, kc, func(b *management.AuthProviderCreate) { b.LinkByEmail = ptr(true) })
	alan := kc.Users[2] // alan@saml.example.org
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Login = testkit.KeycloakLogin(alan)

	v := e.signIn(t, b, p.Slug, "", o)

	wantError(t, v, b, "member_not_found")
	if n := e.memberCount(t, alan.Email); n != 0 {
		t.Errorf("%d members named after a person the provider does not vouch for", n)
	}
}

func TestAnUnsignedRequestIsAcceptedByKeycloakToo(t *testing.T) {
	kc := testkit.StartKeycloak(t)
	e := start(t)
	p := e.createKeycloak(t, kc, func(b *management.AuthProviderCreate) { b.SignRequests = ptr(false) })
	grace := kc.Users[1]
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Login = testkit.KeycloakLogin(grace)

	e.signIn(t, b, p.Slug, "", o)

	if me := signedIn(t, b); me == nil || me.Member.Email != grace.Email {
		t.Errorf("after signing in: %+v", me)
	}
}
