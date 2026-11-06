package sso_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/scim"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// scimClient returns a client of the SCIM API presenting a new token bound to
// provider, created in an instance admin's session.
func (e env) scimClient(t *testing.T, provider string) *scim.ClientWithResponses {
	t.Helper()
	admin := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := testkit.NewBrowser(t, e.srv.URL)
	b.SignIn(t, admin.Email, admin.Password)
	res, err := b.CreateScimTokenWithResponse(t.Context(), management.ScimTokenCreate{Provider: provider})
	if err != nil || res.JSON201 == nil {
		t.Fatalf("creating a SCIM token: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	testkit.RecordSecret(res.JSON201.Value)
	c, err := scim.NewClientWithResponses(e.srv.URL+scim.BaseURL, scim.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+res.JSON201.Value)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAProvisionedMemberSignsInByTheirExternalIDAtTheirProviderAlone(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	noFallback := func(p *provider) { p.autoProvision, p.linkByEmail = false, false }
	bound, other := e.forged(t, idp, noFallback), e.forged(t, idp, noFallback)
	directory := e.scimClient(t, bound)
	address := uniqueEmail()
	created, err := directory.CreateUserWithApplicationScimPlusJSONBodyWithResponse(t.Context(), scim.User{UserName: &address, ExternalId: ptr("sub-ext-1")})
	if err != nil || created.ApplicationscimJSON201 == nil {
		t.Fatalf("provisioning: %v, %d: %s", err, created.StatusCode(), created.Body)
	}
	signIn := func(slug string) (testkit.Visit, *testkit.Browser) {
		t.Helper()
		// The email is another person's, and nothing the provider vouches for
		// links by: only the external ID can be what lets this person in.
		idp.NextClaims(map[string]any{"sub": "sub-ext-1", "email": uniqueEmail()})
		b := testkit.NewBrowser(t, e.srv.URL)
		return e.signIn(t, b, slug, "", e.opts()), b
	}

	v, b := signIn(other)
	wantError(t, v, b, "member_not_found")

	v, b = signIn(bound)
	me := signedIn(t, b)
	if v.URL.Path != "/" || me == nil || me.Member == nil || me.Member.Email != address {
		t.Fatalf("the sign-in at the member's provider ended at %s as %+v, want %s", v.URL, me, address)
	}
	sessions, err := b.ListMySessionsWithResponse(t.Context())
	if err != nil || sessions.JSON200 == nil || len(sessions.JSON200.Items) != 1 ||
		sessions.JSON200.Items[0].Provider == nil || *sessions.JSON200.Items[0].Provider != bound {
		t.Errorf("the session = %v, want one through %s", sessions.JSON200, bound)
	}
	if got := e.audited(t, "identity.linked", "member_email", address); len(got) != 1 || got[0]["provider"] != bound {
		t.Errorf("identity.linked = %v, want the subject linked at %s", got, bound)
	}

	// The link is made: the next sign-in finds the member through it.
	if v, b = signIn(bound); v.URL.Path != "/" || signedIn(t, b) == nil {
		t.Errorf("the second sign-in ended at %s", v.URL)
	}
	if n := len(e.audited(t, "identity.linked", "member_email", address)); n != 1 {
		t.Errorf("%d identities linked, want 1", n)
	}

	// The directory takes the member away.
	if res, err := directory.DeleteUserWithResponse(t.Context(), *created.ApplicationscimJSON201.Id); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("deprovisioning: %v, %d", err, res.StatusCode())
	}
	v, b = signIn(bound)
	wantError(t, v, b, "member_deactivated")
}

func TestAProvisionedMemberNobodyLinkedIsRefusedWhenTheyAreDeactivatedBeforeTheirFirstSignIn(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.autoProvision, p.linkByEmail = false, false })
	directory := e.scimClient(t, slug)
	address := uniqueEmail()
	created, err := directory.CreateUserWithApplicationScimPlusJSONBodyWithResponse(t.Context(), scim.User{
		UserName: &address, ExternalId: ptr("sub-ext-2"), Active: ptr(false),
	})
	if err != nil || created.ApplicationscimJSON201 == nil {
		t.Fatalf("provisioning: %v, %d: %s", err, created.StatusCode(), created.Body)
	}
	if created.ApplicationscimJSON201.Active == nil || *created.ApplicationscimJSON201.Active {
		t.Errorf("a user created inactive = %+v", created.ApplicationscimJSON201)
	}
	idp.NextClaims(map[string]any{"sub": "sub-ext-2", "email": address})
	b := testkit.NewBrowser(t, e.srv.URL)
	wantError(t, e.signIn(t, b, slug, "", e.opts()), b, "member_deactivated")
}
