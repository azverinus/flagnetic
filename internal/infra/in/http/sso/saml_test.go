package sso_test

import (
	"bytes"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// createSAML creates an enabled SAML provider whose metadata the forging
// identity provider serves, vouching for example.com and provisioning on first
// sign-in, changed by change.
func (e env) createSAML(t *testing.T, idp *testkit.ForgingSAMLIDP, change func(*management.AuthProviderCreate)) management.AuthProvider {
	t.Helper()
	body := management.AuthProviderCreate{
		Slug: slug(), Kind: management.AuthProviderKindSaml, DisplayName: "Corporate SAML", Domains: []string{"example.com"},
		IdpMetadataUrl: ptr(idp.MetadataURL), AutoProvision: ptr(true),
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

// samlTrip is a sign-in held at the identity provider's endpoint: the browser
// it started in, and the request it carried.
type samlTrip struct {
	b    *testkit.Browser
	slug string
	req  testkit.SAMLRequest
	sp   []byte
}

// start begins a sign-in through slug in b and holds it where the browser
// would be sent to the identity provider.
func (e env) samlStart(t *testing.T, b *testkit.Browser, idp *testkit.ForgingSAMLIDP, slug, returnTo string) samlTrip {
	t.Helper()
	idpURL, err := url.Parse(idp.SSOURL)
	if err != nil {
		t.Fatal(err)
	}
	o := e.opts()
	o.Stop = func(u *url.URL) bool { return u.Host == idpURL.Host }
	v := e.signIn(t, b, slug, returnTo, o)
	if v.URL.Host != idpURL.Host {
		t.Fatalf("the sign-in ended at %s (%d), not at the identity provider: %s", v.URL, v.Status, v.Body)
	}
	sp := e.spMetadata(t, slug)
	return samlTrip{b: b, slug: slug, req: idp.ParseRequest(t, v.URL, sp), sp: sp}
}

func (e env) spMetadata(t *testing.T, slug string) []byte {
	t.Helper()
	res, err := http.Get(e.srv.URL + "/sso/" + slug + "/metadata") //nolint:noctx // a test server on loopback
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/samlmetadata+xml" {
		t.Fatalf("GET /sso/%s/metadata = %d %s: %s", slug, res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	return body
}

// answer has the identity provider answer the trip with what a asserts and
// posts it to the ACS from the trip's browser.
func (e env) answer(t *testing.T, idp *testkit.ForgingSAMLIDP, trip samlTrip, a testkit.SAMLAssertion) testkit.Visit {
	t.Helper()
	if a.EncryptTo == nil {
		if certs := testkit.SPCertificates(trip.sp); len(certs) > 0 {
			a.EncryptTo = pemOf(certs[0].Raw)
		}
	}
	return trip.b.Post(t, trip.req.ACS, idp.Respond(t, trip.req, a), e.opts())
}

func (e env) samlSignIn(t *testing.T, idp *testkit.ForgingSAMLIDP, slug, returnTo string, a testkit.SAMLAssertion) (*testkit.Browser, testkit.Visit) {
	t.Helper()
	b := testkit.NewBrowser(t, e.srv.URL)
	return b, e.answer(t, idp, e.samlStart(t, b, idp, slug, returnTo), a)
}

func pemOf(der []byte) []byte { return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}) }

func samlPerson(email string) testkit.SAMLAssertion {
	return testkit.SAMLAssertion{Subject: "sub-" + email, Attributes: map[string][]string{"email": {email}, "name": {"Ada Lovelace"}}}
}

func TestSigningInThroughSAMLProvisionsAMemberAndOpensASessionForTheProvider(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	email := uniqueEmail()

	b, v := e.samlSignIn(t, idp, p.Slug, "/projects/x/flags?tab=a", samlPerson(email))

	if v.URL.Path != "/projects/x/flags" || v.URL.RawQuery != "tab=a" {
		t.Fatalf("the sign-in ended at %s (%d): %s", v.URL, v.Status, v.Body[max(0, len(v.Body)-300):])
	}
	me := signedIn(t, b)
	if me == nil || me.Member == nil || me.Member.Email != email || me.Member.DisplayName != "Ada Lovelace" {
		t.Fatalf("after signing in: %+v", me)
	}
	if b.Cookie("flagnetic_sso") != "" {
		t.Error("the attempt cookie outlived the sign-in")
	}
	sessions, err := b.ListMySessionsWithResponse(t.Context())
	if err != nil || sessions.JSON200 == nil || len(sessions.JSON200.Items) != 1 {
		t.Fatalf("GET /me/sessions = %v, %v", sessions.StatusCode(), err)
	}
	if s := sessions.JSON200.Items[0]; s.Method != management.SessionMethodSaml || s.Provider == nil || *s.Provider != p.Slug {
		t.Errorf("the session = method %s, provider %v; want saml through %s", s.Method, s.Provider, p.Slug)
	}
	if got := e.audited(t, "member.provisioned", "email", email); len(got) != 1 || got[0]["method"] != "saml" || got[0]["provider"] != p.Slug {
		t.Errorf("member.provisioned = %v", got)
	}
	if got := e.audited(t, "auth.signed_in", "email", email); len(got) != 1 || got[0]["method"] != "saml" || got[0]["provider"] != p.Slug {
		t.Errorf("auth.signed_in = %v", got)
	}
	if scrape := e.srv.Scrape(t); testkit.Metric(t, scrape, `flagnetic_sso_sign_ins_total{kind="saml",result="succeeded"}`) < 1 {
		t.Errorf("flagnetic_sso_sign_ins_total{kind=saml,result=succeeded} not counted:\n%s", scrape)
	}

	// The same person again, in a browser that holds nothing, is the member
	// through the identity that was linked, with nobody provisioned.
	again := testkit.NewBrowser(t, e.srv.URL)
	if v = e.answer(t, idp, e.samlStart(t, again, idp, p.Slug, ""), samlPerson(email)); v.URL.Path != "/" {
		t.Errorf("the second sign-in ended at %s", v.URL)
	}
	if other := signedIn(t, again); other == nil || other.Member == nil || other.Member.Id != me.Member.Id {
		t.Errorf("the second sign-in = %+v, want the same member", other)
	}
	if n := len(e.audited(t, "member.provisioned", "email", email)); n != 1 {
		t.Errorf("%d members provisioned for one person", n)
	}
}

func TestTheAuthnRequestIsSignedWhenTheProviderSignsRequests(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	for name, sign := range map[string]bool{"signed": true, "unsigned": false} {
		t.Run(name, func(t *testing.T) {
			p := e.createSAML(t, idp, func(b *management.AuthProviderCreate) { b.SignRequests = ptr(sign) })
			trip := e.samlStart(t, testkit.NewBrowser(t, e.srv.URL), idp, p.Slug, "")

			if trip.req.Signed != sign {
				t.Errorf("request signed = %v, want %v", trip.req.Signed, sign)
			}
			if trip.req.Issuer != "http://localhost:8080/sso/"+p.Slug+"/metadata" || trip.req.ACS != "http://localhost:8080/sso/"+p.Slug+"/acs" ||
				trip.req.Destination != idp.SSOURL || trip.req.RelayState == "" || !strings.HasPrefix(trip.req.ID, "_") {
				t.Errorf("request = %+v", trip.req)
			}
		})
	}
}

func TestAResponseTheProviderForgedOpensNoSessionAndCreatesNoMember(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	for _, tc := range []struct {
		name  string
		fault testkit.SAMLFault
		// cause is what the logged reason of the refusal holds: the check that
		// refused it, so a response is never refused for another reason than
		// the one it is meant to break.
		cause string
	}{
		{"unsigned", testkit.SAMLUnsigned, "Missing signature"},
		{"signed by another key", testkit.SAMLSignedByOtherKey, "certificate"},
		{"for another audience", testkit.SAMLWrongAudience, "another audience"},
		{"without an audience", testkit.SAMLNoAudience, "names no audience"},
		{"for another destination", testkit.SAMLWrongDestination, "Destination"},
		{"answering another request", testkit.SAMLWrongInResponseTo, "another request"},
		{"answering no request", testkit.SAMLNoInResponseTo, "another request"},
		{"expired", testkit.SAMLExpired, "NotOnOrAfter"},
		{"not valid yet", testkit.SAMLNotYetValid, "not valid yet"},
		{"by another issuer", testkit.SAMLWrongIssuer, "Issuer"},
		{"wrapped", testkit.SAMLWrapped, "Missing signature"},
		{"with a DOCTYPE", testkit.SAMLDocType, "DOCTYPE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := e.createSAML(t, idp, nil)
			email := uniqueEmail()
			a := samlPerson(email)
			a.Fault = tc.fault
			before := len(e.srv.Logs.Find("single sign-on failed"))

			b, v := e.samlSignIn(t, idp, p.Slug, "", a)

			wantError(t, v, b, "sso_failed")
			logged := e.srv.Logs.Find("single sign-on failed")
			if len(logged) != before+1 || !strings.Contains(fmt.Sprint(logged[len(logged)-1].Attrs["error"]), tc.cause) {
				t.Errorf("the refusal was logged as %v, want its cause to hold %q", logged[before:], tc.cause)
			}
			for _, who := range []string{email, "victim@example.com"} {
				if n := e.memberCount(t, who); n != 0 {
					t.Errorf("%d members created for %s by a forged response", n, who)
				}
			}
		})
	}
}

func TestAnIdentityProviderThatRefusedTheSignInIsReportedAsSuch(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	a := samlPerson(uniqueEmail())
	a.Fault = testkit.SAMLFailedStatus

	b, v := e.samlSignIn(t, idp, p.Slug, "", a)

	wantError(t, v, b, "idp_error")
}

func TestAssertionTimesAreJudgedWithTwoMinutesOfSkew(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	for name, tc := range map[string]struct {
		until time.Duration
		ok    bool
	}{
		"ended a minute ago":      {-time.Minute, true},
		"ended three minutes ago": {-3 * time.Minute, false},
	} {
		t.Run(name, func(t *testing.T) {
			p := e.createSAML(t, idp, nil)
			a := samlPerson(uniqueEmail())
			a.Until = tc.until

			b, v := e.samlSignIn(t, idp, p.Slug, "", a)

			if tc.ok && signedIn(t, b) == nil {
				t.Errorf("an assertion that ended %v ago, within the skew, was refused: %s", -tc.until, v.URL)
			}
			if !tc.ok {
				wantError(t, v, b, "sso_failed")
			}
		})
	}
}

func TestAnAssertionFromAProviderWhoseClockIsAheadWithinTheSkewIsAccepted(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	// The forging provider starts an assertion's validity a minute before its
	// clock, so two and a half minutes ahead starts it a minute and a half
	// after the instance's: within the skew.
	idp.Now = func() time.Time { return time.Now().Add(150 * time.Second) }
	e := start(t)
	p := e.createSAML(t, idp, nil)

	b, v := e.samlSignIn(t, idp, p.Slug, "", samlPerson(uniqueEmail()))

	if signedIn(t, b) == nil {
		t.Errorf("an assertion valid from within the skew was refused: %s", v.URL)
	}
}

func TestAResponseNoAttemptAskedForOpensNoSessionAndCreatesNoMember(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	email := uniqueEmail()
	a := samlPerson(email)
	a.Fault = testkit.SAMLNoInResponseTo
	b := testkit.NewBrowser(t, e.srv.URL)

	for name, relayState := range map[string]string{"without a relay state": "", "with an unknown relay state": "unknown"} {
		t.Run(name, func(t *testing.T) {
			// A response the identity provider sends of its own accord: no
			// AuthnRequest, so no attempt, preceded it.
			form := idp.Respond(t, testkit.SAMLRequest{Issuer: *p.EntityId, ACS: *p.AcsUrl, RelayState: relayState}, a)

			wantError(t, b.Post(t, *p.AcsUrl, form, e.opts()), b, "sso_failed")
			if n := e.memberCount(t, email); n != 0 {
				t.Errorf("%d members created by an unsolicited response", n)
			}
		})
	}
}

func TestAnAssertionAcceptedOnceIsRefusedAgainWhateverTheRequest(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	a := samlPerson(uniqueEmail())
	a.ID = "_assertion-" + strings.ToLower(rand.Text()[:10])

	first, v := e.samlSignIn(t, idp, p.Slug, "", a)
	if signedIn(t, first) == nil {
		t.Fatalf("the first sign-in failed: %s", v.URL)
	}

	// A response for a new request, repeating the assertion's ID: the identity
	// provider is faulty or its key is stolen; either way the instance has seen
	// this assertion.
	second, v := e.samlSignIn(t, idp, p.Slug, "", a)
	wantError(t, v, second, "sso_failed")
}

func TestAResponsePostedTwiceIsAnAttemptAlreadyTaken(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	b := testkit.NewBrowser(t, e.srv.URL)
	trip := e.samlStart(t, b, idp, p.Slug, "")
	form := idp.Respond(t, trip.req, samlPerson(uniqueEmail()))

	first := b.Post(t, trip.req.ACS, form, e.opts())
	if signedIn(t, b) == nil {
		t.Fatalf("the first post failed: %s", first.URL)
	}
	other := testkit.NewBrowser(t, e.srv.URL)
	wantError(t, other.Post(t, trip.req.ACS, form, e.opts()), other, "sso_failed")
}

func TestAnEncryptedAssertionIsDecryptedWithAnyOfTheSupportedEncryptions(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	for name, tc := range map[string]struct {
		encryption testkit.SAMLEncryption
		ok         bool
		// cause is what the logged reason of a refusal holds.
		cause string
	}{
		"AES-128-CBC with RSA-OAEP": {testkit.SAMLAES128CBC, true, ""},
		"AES-256-GCM with RSA-OAEP": {testkit.SAMLAES256GCM, true, ""},
		"RSA 1.5 key transport":     {testkit.SAMLRSA15, false, "RSA 1.5"},
		"Triple DES with RSA-OAEP":  {testkit.SAMLTripleDES, false, "Triple DES"},
	} {
		t.Run(name, func(t *testing.T) {
			p := e.createSAML(t, idp, nil)
			email := uniqueEmail()
			a := samlPerson(email)
			a.Encryption = tc.encryption
			before := len(e.srv.Logs.Find("single sign-on failed"))

			b, v := e.samlSignIn(t, idp, p.Slug, "", a)

			if tc.ok {
				if me := signedIn(t, b); me == nil || me.Member.Email != email {
					t.Fatalf("the encrypted assertion was refused: %s", v.URL)
				}
				return
			}
			wantError(t, v, b, "sso_failed")
			logged := e.srv.Logs.Find("single sign-on failed")
			if len(logged) != before+1 || !strings.Contains(fmt.Sprint(logged[len(logged)-1].Attrs["error"]), tc.cause) {
				t.Errorf("the refusal was logged as %v, want its cause to hold %q", logged[before:], tc.cause)
			}
			if n := e.memberCount(t, email); n != 0 {
				t.Errorf("%d members created through %s", n, name)
			}
		})
	}
}

func TestAResponseSignedAsAWholeIsAcceptedToo(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	a := samlPerson(uniqueEmail())
	a.SignResponse = true

	b, v := e.samlSignIn(t, idp, p.Slug, "", a)

	if signedIn(t, b) == nil {
		t.Errorf("a response signed as a whole was refused: %s", v.URL)
	}
}

func TestTheEmailAndNameAreReadFromTheAttributesTheProviderNames(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, func(b *management.AuthProviderCreate) {
		b.Claims = &management.AuthProviderClaimsPatch{Email: ptr("mail"), Name: ptr("displayName")}
	})
	email := uniqueEmail()

	b, _ := e.samlSignIn(t, idp, p.Slug, "", testkit.SAMLAssertion{
		Subject: "sub-attributes", Attributes: map[string][]string{"mail": {email}, "displayName": {"Grace Hopper"}, "email": {"other@example.com"}},
	})

	if me := signedIn(t, b); me == nil || me.Member.Email != email || me.Member.DisplayName != "Grace Hopper" {
		t.Errorf("after signing in: %+v", me)
	}
}

func TestTheEmailIsTheNameIDWhenItsFormatIsEmailAddress(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	email := uniqueEmail()

	b, _ := e.samlSignIn(t, idp, p.Slug, "", testkit.SAMLAssertion{
		Subject: email, NameIDFormat: "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress", Attributes: map[string][]string{"name": {"Ada"}},
	})

	if me := signedIn(t, b); me == nil || me.Member.Email != email {
		t.Errorf("after signing in: %+v", me)
	}
}

func TestAnEmailOutsideTheProvidersDomainsIsNeverVerifiedBySAML(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, func(b *management.AuthProviderCreate) { b.LinkByEmail = ptr(true) })
	outside := strings.ToLower(rand.Text()[:10]) + "@example.org"
	b, v := e.samlSignIn(t, idp, p.Slug, "", samlPerson(outside))
	wantError(t, v, b, "member_not_found")
	if n := e.memberCount(t, outside); n != 0 {
		t.Errorf("%d members provisioned for an email the provider does not vouch for", n)
	}

	member := testkit.NewMember(t, testkit.Pool(t), testkit.WithEmail(strings.ToLower(rand.Text()[:10])+"@example.org"))
	b, v = e.samlSignIn(t, idp, p.Slug, "", samlPerson(member.Email))
	wantError(t, v, b, "member_not_found")
}

func TestAMemberIsLinkedByEmailOnlyWhenTheProviderLinksByEmail(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	member := testkit.NewMember(t, testkit.Pool(t))
	linking := e.createSAML(t, idp, func(b *management.AuthProviderCreate) {
		b.LinkByEmail, b.Domains = ptr(true), []string{"example.com"}
	})
	notLinking := e.createSAML(t, idp, func(b *management.AuthProviderCreate) { b.AutoProvision = ptr(false) })

	b, v := e.samlSignIn(t, idp, notLinking.Slug, "", samlPerson(member.Email))
	wantError(t, v, b, "member_not_found")

	b, _ = e.samlSignIn(t, idp, linking.Slug, "", samlPerson(member.Email))
	if me := signedIn(t, b); me == nil || me.Member.Email != member.Email {
		t.Errorf("after linking by email: %+v", me)
	}
}

func TestADeactivatedMemberCannotSignInThroughSAML(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	member := testkit.NewMember(t, testkit.Pool(t))
	p := e.createSAML(t, idp, func(b *management.AuthProviderCreate) { b.LinkByEmail = ptr(true) })
	if b, _ := e.samlSignIn(t, idp, p.Slug, "", samlPerson(member.Email)); signedIn(t, b) == nil {
		t.Fatal("the member could not sign in before being deactivated")
	}
	res, err := e.admin.DeactivateMemberWithResponse(t.Context(), member.ID.String())
	if err != nil || res.StatusCode() != http.StatusOK {
		t.Fatalf("deactivating the member: %v, %d: %s", err, res.StatusCode(), res.Body)
	}

	b, v := e.samlSignIn(t, idp, p.Slug, "", samlPerson(member.Email))

	wantError(t, v, b, "member_deactivated")
}

func TestACompletionFromAnotherBrowserChangesNothingForSAMLToo(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	email := uniqueEmail()
	victim := testkit.NewBrowser(t, e.srv.URL)
	trip := e.samlStart(t, victim, idp, p.Slug, "")
	o := e.opts()
	o.Stop = func(u *url.URL) bool { return strings.HasPrefix(u.Path, "/sso/complete/") }
	held := victim.Post(t, trip.req.ACS, idp.Respond(t, trip.req, samlPerson(email)), o)
	if !strings.HasPrefix(held.URL.Path, "/sso/complete/") {
		t.Fatalf("the sign-in reached %s before completing", held.URL)
	}

	attacker := testkit.NewBrowser(t, e.srv.URL)
	got := attacker.Visit(t, held.URL.String(), testkit.VisitOptions{})

	wantError(t, got, attacker, "attempt_mismatch")
	if n := e.memberCount(t, email); n != 0 {
		t.Errorf("%d members created by a completion from another browser", n)
	}
}

func TestTheACSTakesACrossSitePostAndOnlyThatProvidersResponses(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	b := testkit.NewBrowser(t, e.srv.URL)
	b.Header.Set("Sec-Fetch-Site", "cross-site")
	b.Header.Set("Origin", idp.URL(""))
	trip := e.samlStart(t, b, idp, p.Slug, "")

	e.answer(t, idp, trip, samlPerson(uniqueEmail()))
	// The console's own calls come from its own origin.
	b.Header.Del("Sec-Fetch-Site")
	b.Header.Del("Origin")
	if signedIn(t, b) == nil {
		t.Error("a response posted cross-site, as an identity provider does, was refused")
	}

	// A relay state of another provider's attempt names no attempt of this one.
	other := e.createSAML(t, idp, nil)
	b2 := testkit.NewBrowser(t, e.srv.URL)
	trip2 := e.samlStart(t, b2, idp, other.Slug, "")
	trip2.req.ACS = strings.Replace(trip2.req.ACS, "/"+other.Slug+"/", "/"+p.Slug+"/", 1)
	form := idp.Respond(t, trip2.req, samlPerson(uniqueEmail()))
	wantError(t, b2.Post(t, trip2.req.ACS, form, e.opts()), b2, "sso_failed")
}

func TestSAMLEndpointsAnswerNotFoundForWhatIsNotAnEnabledSAMLProvider(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	oidc := testkit.StartForgingIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	oidcSlug := e.forged(t, oidc, nil)
	disabled := e.createSAML(t, idp, func(b *management.AuthProviderCreate) { b.Enabled = ptr(false) })

	for name, tc := range map[string]struct{ method, path string }{
		"the metadata of an unknown provider":   {http.MethodGet, "/sso/nobody/metadata"},
		"the metadata of a disabled provider":   {http.MethodGet, "/sso/" + disabled.Slug + "/metadata"},
		"the metadata of an OIDC provider":      {http.MethodGet, "/sso/" + oidcSlug + "/metadata"},
		"the ACS of an unknown provider":        {http.MethodPost, "/sso/nobody/acs"},
		"the ACS of a disabled provider":        {http.MethodPost, "/sso/" + disabled.Slug + "/acs"},
		"the ACS of an OIDC provider":           {http.MethodPost, "/sso/" + oidcSlug + "/acs"},
		"the callback of a SAML provider":       {http.MethodGet, "/sso/" + p.Slug + "/callback"},
		"the start of a disabled SAML provider": {http.MethodGet, "/sso/" + disabled.Slug + "/start"},
	} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tc.method, e.srv.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404", tc.method, tc.path, res.StatusCode)
			}
		})
	}
}

func TestTheACSRefusesABodyOverAMebibyte(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	body := "SAMLResponse=" + strings.Repeat("A", 1<<20+1)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.srv.URL+"/sso/"+p.Slug+"/acs", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("a body of 1 MiB and a byte = %d, want 413", res.StatusCode)
	}
}

func TestSAMLProvidersAreOfferedOnTheSignInPage(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)

	res, err := testkit.NewBrowser(t, e.srv.URL).GetAuthMethodsWithResponse(t.Context())
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /auth/methods = %v, %v", res.StatusCode(), err)
	}
	for _, m := range res.JSON200.Providers {
		if m.Slug == p.Slug {
			if m.Kind != management.AuthProviderKindSaml {
				t.Errorf("kind = %s, want saml", m.Kind)
			}
			return
		}
	}
	t.Errorf("providers = %+v, want %s among them", res.JSON200.Providers, p.Slug)
}

func TestSAMLGroupsMapToGrantsAndAnAbsentAttributeKeepsThem(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	alpha, beta := e.project(t), e.project(t)
	e.mapGroups(t, p.Slug, projectMapping("eng", alpha, "maintainer"), projectMapping("ops", beta, "viewer"), adminMapping("it-admins"))
	email := uniqueEmail()
	person := func(groups map[string][]string) testkit.SAMLAssertion {
		a := samlPerson(email)
		for name, values := range groups {
			a.Attributes[name] = values
		}
		return a
	}

	b, _ := e.samlSignIn(t, idp, p.Slug, "", person(map[string][]string{"groups": {"eng", "ops", "it-admins"}}))
	member := idOf(t, b)
	e.wantGrants(t, alpha, member, management.GrantSourceSso, p.Slug, "maintainer")
	e.wantGrants(t, beta, member, management.GrantSourceSso, p.Slug, "viewer")
	if !signedIn(t, b).Member.InstanceAdmin {
		t.Error("the mapped group did not make the member an instance admin")
	}

	// No attribute at all keeps everything.
	e.samlSignIn(t, idp, p.Slug, "", person(nil))
	e.wantGrants(t, alpha, member, management.GrantSourceSso, p.Slug, "maintainer")

	// An attribute without a value is an empty list: the person is in no group.
	b, _ = e.samlSignIn(t, idp, p.Slug, "", person(map[string][]string{"groups": {}}))
	e.wantGrants(t, alpha, member, management.GrantSourceSso, p.Slug)
	e.wantGrants(t, beta, member, management.GrantSourceSso, p.Slug)
	if signedIn(t, b).Member.InstanceAdmin {
		t.Error("an empty groups attribute left the member an instance admin")
	}
}
