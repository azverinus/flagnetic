package sso_test

import (
	"cmp"
	"crypto/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// publicURL is the address the instance believes it is served at. It is on
// localhost, where a browser accepts cookies without Secure over plain http.
const publicURL = "http://localhost:8080"

type env struct {
	srv   *testkit.Server
	admin *management.ClientWithResponses
}

func start(t *testing.T, opts ...testkit.ServerOption) env {
	t.Helper()
	srv := testkit.StartServer(t, append([]testkit.ServerOption{testkit.WithBaseURL(publicURL)}, opts...)...)
	t.Cleanup(func() {
		if t.Failed() {
			for _, r := range srv.Logs.Find("single sign-on failed") {
				t.Logf("server: %+v", r)
			}
		}
	})
	return env{srv: srv, admin: testkit.Client(t, srv, testkit.InstanceAdminToken(t))}
}

func ptr[T any](v T) *T { return &v }

func slug() string { return "p" + strings.ToLower(rand.Text()[:12]) }

// provider describes an auth provider to create.
type provider struct {
	slug                           string
	issuer, clientID, clientSecret string
	domains                        []string
	linkByEmail, autoProvision     bool
	requireVerifiedEmail           *bool
}

func (e env) create(t *testing.T, p provider) string {
	t.Helper()
	testkit.RecordSecret(p.clientSecret)
	body := management.AuthProviderCreate{
		Slug: cmp.Or(p.slug, slug()), Kind: management.AuthProviderKindOidc, DisplayName: "Corporate SSO", Domains: p.domains,
		Issuer: p.issuer, ClientId: p.clientID, ClientSecret: &p.clientSecret,
		LinkByEmail: &p.linkByEmail, AutoProvision: &p.autoProvision,
	}
	res, err := e.admin.CreateAuthProviderWithResponse(t.Context(), body)
	if err != nil || res.JSON201 == nil {
		t.Fatalf("creating the provider: %v, %d: %s", err, res.StatusCode(), res.Body)
	}
	if p.requireVerifiedEmail != nil {
		patch := management.AuthProviderPatch{RequireVerifiedEmail: p.requireVerifiedEmail}
		upd, err := e.admin.UpdateAuthProviderWithResponse(t.Context(), body.Slug,
			&management.UpdateAuthProviderParams{IfMatch: ptr(`"1"`)}, patch)
		if err != nil || upd.JSON200 == nil {
			t.Fatalf("editing the provider: %v, %d: %s", err, upd.StatusCode(), upd.Body)
		}
	}
	return body.Slug
}

func (e env) forged(t *testing.T, idp *testkit.ForgingIDP, change func(*provider)) string {
	t.Helper()
	p := provider{
		issuer: idp.Issuer, clientID: idp.ClientID, clientSecret: idp.ClientSecret, domains: []string{"example.com"},
		autoProvision: true,
	}
	if change != nil {
		change(&p)
	}
	return e.create(t, p)
}

func (e env) opts() testkit.VisitOptions { return testkit.VisitOptions{PublicURL: publicURL} }

// signIn starts a sign-in through slug in b and follows it to its end.
func (e env) signIn(t *testing.T, b *testkit.Browser, slug, returnTo string, o testkit.VisitOptions) testkit.Visit {
	t.Helper()
	u := e.srv.URL + "/sso/" + slug + "/start"
	if returnTo != "" {
		u += "?return_to=" + url.QueryEscape(returnTo)
	}
	return b.Visit(t, u, o)
}

// signedIn reports whether b holds a session, and as whom.
func signedIn(t *testing.T, b *testkit.Browser) *management.Me {
	t.Helper()
	if b.Cookie(management.SessionCookie) == "" {
		return nil
	}
	res, err := b.GetMeWithResponse(t.Context())
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /me = %v, %v", res.StatusCode(), err)
	}
	return res.JSON200
}

func (e env) memberCount(t *testing.T, email string) int {
	t.Helper()
	res, err := e.admin.ListMembersWithResponse(t.Context(), &management.ListMembersParams{Q: &email})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /members = %v, %v", res.StatusCode(), err)
	}
	return len(res.JSON200.Items)
}

// audited returns what entries of action hold in after, where the field key
// equals value.
func (e env) audited(t *testing.T, action, key, value string) []map[string]any {
	t.Helper()
	res, err := e.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit = %v, %v", res.StatusCode(), err)
	}
	var out []map[string]any
	for _, entry := range res.JSON200.Items {
		if after, _ := entry.After.(map[string]any); after != nil && after[key] == value {
			out = append(out, after)
		}
	}
	return out
}

func uniqueEmail() string { return strings.ToLower(rand.Text()[:10]) + "@example.com" }

func wantError(t *testing.T, v testkit.Visit, b *testkit.Browser, code string) {
	t.Helper()
	if v.URL.Path != "/sign-in" || v.Query("error") != code {
		t.Errorf("the sign-in ended at %s, want /sign-in?error=%s", v.URL, code)
	}
	if b.Cookie(management.SessionCookie) != "" {
		t.Errorf("a session cookie was set by a sign-in that failed with %s", code)
	}
}

func TestSigningInThroughDexProvisionsAMemberAndOpensASessionForTheProvider(t *testing.T) {
	dex := testkit.StartDex(t)
	e := start(t)
	slug := e.create(t, provider{
		slug: testkit.DexSlug(t), issuer: dex.Issuer, clientID: dex.ClientID, clientSecret: dex.ClientSecret, domains: []string{"example.com"},
		autoProvision: true,
	})
	ada := dex.Users[0]
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Login = testkit.DexLogin(ada)

	v := e.signIn(t, b, slug, "/projects/x/flags?tab=a", o)

	if v.URL.Path != "/projects/x/flags" || v.URL.RawQuery != "tab=a" {
		t.Fatalf("the sign-in ended at %s (%d): %s", v.URL, v.Status, v.Body[max(0, len(v.Body)-300):])
	}
	me := signedIn(t, b)
	if me == nil || me.Member == nil || me.Member.Email != ada.Email || me.Credential.Method != management.CredentialMethodSession {
		t.Fatalf("after signing in: %+v", me)
	}
	if b.Cookie("flagnetic_sso") != "" {
		t.Error("the attempt cookie outlived the sign-in")
	}
	sessions, err := b.ListMySessionsWithResponse(t.Context())
	if err != nil || sessions.JSON200 == nil || len(sessions.JSON200.Items) != 1 {
		t.Fatalf("GET /me/sessions = %v, %v", sessions.StatusCode(), err)
	}
	if s := sessions.JSON200.Items[0]; s.Method != management.SessionMethodOidc || s.Provider == nil || *s.Provider != slug {
		t.Errorf("the session = method %s, provider %v; want oidc through %s", s.Method, s.Provider, slug)
	}
	if got := e.audited(t, "member.provisioned", "email", ada.Email); len(got) != 1 || got[0]["provider"] != slug {
		t.Errorf("member.provisioned = %v", got)
	}
	if got := e.audited(t, "identity.linked", "member_email", ada.Email); len(got) != 1 || got[0]["provider"] != slug {
		t.Errorf("identity.linked = %v", got)
	}
	signedInEntries := e.audited(t, "auth.signed_in", "email", ada.Email)
	if len(signedInEntries) != 1 || signedInEntries[0]["method"] != "oidc" || signedInEntries[0]["provider"] != slug {
		t.Errorf("auth.signed_in = %v", signedInEntries)
	}
	scrape := e.srv.Scrape(t)
	if testkit.Metric(t, scrape, `flagnetic_sso_sign_ins_total{kind="oidc",result="succeeded"}`) < 1 {
		t.Errorf("flagnetic_sso_sign_ins_total{result=succeeded} not counted:\n%s", scrape)
	}

	// The next sign-in, in a browser that holds nothing, finds the member
	// through the identity it linked and provisions nobody.
	again := testkit.NewBrowser(t, e.srv.URL)
	if v = e.signIn(t, again, slug, "", o); v.URL.Path != "/" {
		t.Errorf("the second sign-in ended at %s", v.URL)
	}
	if other := signedIn(t, again); other == nil || other.Member == nil || other.Member.Id != me.Member.Id {
		t.Errorf("the second sign-in = %+v, want the same member", other)
	}
	if n := len(e.audited(t, "member.provisioned", "email", ada.Email)); n != 1 {
		t.Errorf("%d members provisioned for one person", n)
	}
}

func TestAPersonOutsideTheProvidersDomainsIsNotProvisioned(t *testing.T) {
	dex := testkit.StartDex(t)
	e := start(t)
	slug := e.create(t, provider{
		slug: testkit.DexSlug(t), issuer: dex.Issuer, clientID: dex.ClientID, clientSecret: dex.ClientSecret, domains: []string{"example.com"}, autoProvision: true, linkByEmail: true,
	})
	alan := dex.Users[2] // alan@example.org
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Login = testkit.DexLogin(alan)

	v := e.signIn(t, b, slug, "", o)

	wantError(t, v, b, "member_not_found")
	if n := e.memberCount(t, alan.Email); n != 0 {
		t.Errorf("%d members named after a person the provider does not vouch for", n)
	}
}

func TestATokenTheProviderForgedOpensNoSessionAndCreatesNoMember(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	for _, tc := range []struct {
		name  string
		fault testkit.Fault
	}{
		{"signed by another key", testkit.SignedByOtherKey},
		{"for another audience", testkit.WrongAudience},
		{"by another issuer", testkit.WrongIssuer},
		{"expired", testkit.Expired},
		{"answering another nonce", testkit.WrongNonce},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := e.forged(t, idp, nil)
			email := uniqueEmail()
			idp.NextClaims(map[string]any{"email": email})
			idp.FailNext(tc.fault)
			b := testkit.NewBrowser(t, e.srv.URL)

			wantError(t, e.signIn(t, b, slug, "", e.opts()), b, "sso_failed")
			if n := e.memberCount(t, email); n != 0 {
				t.Errorf("%d members created by a forged token", n)
			}
		})
	}
}

func TestAClientSecretTheProviderDoesNotKnowFailsTheSignIn(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.clientSecret = "not-the-secret" })
	b := testkit.NewBrowser(t, e.srv.URL)

	wantError(t, e.signIn(t, b, slug, "", e.opts()), b, "sso_failed")
}

func TestAnAnswerOfTheProviderIsTakenOnce(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	idp.NextClaims(map[string]any{"email": uniqueEmail()})
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Stop = func(u *url.URL) bool { return strings.HasPrefix(u.Path, "/sso/complete/") }
	held := e.signIn(t, b, slug, "", o)
	if !strings.HasPrefix(held.URL.Path, "/sso/complete/") {
		t.Fatalf("the sign-in reached %s before completing", held.URL)
	}
	callback := callbackOf(t, e, slug, b)

	// The provider's answer, delivered again, is a state already used.
	replay := b.Visit(t, callback, testkit.VisitOptions{Stop: func(*url.URL) bool { return false }})

	if replay.URL.Path != "/sign-in" || replay.Query("error") != "sso_failed" {
		t.Errorf("the replayed answer ended at %s, want /sign-in?error=sso_failed", replay.URL)
	}
}

// callbackOf starts a second sign-in in b and returns the address the
// provider sends the browser back to, not yet fetched.
func callbackOf(t *testing.T, e env, slug string, b *testkit.Browser) string {
	t.Helper()
	o := e.opts()
	var callback string
	o.Stop = func(u *url.URL) bool {
		if strings.HasSuffix(u.Path, "/callback") {
			callback = u.String()
			return true
		}
		return false
	}
	e.signIn(t, b, slug, "", o)
	// Take the answer once, as the browser should.
	first := b.Visit(t, callback, testkit.VisitOptions{Stop: func(u *url.URL) bool { return strings.HasPrefix(u.Path, "/sso/complete/") }})
	if !strings.HasPrefix(first.URL.Path, "/sso/complete/") {
		t.Fatalf("the first answer reached %s", first.URL)
	}
	return callback
}

func TestACompletionFromAnotherBrowserChangesNothing(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.linkByEmail = true })
	email := uniqueEmail()
	idp.NextClaims(map[string]any{"email": email})
	victim := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Stop = func(u *url.URL) bool { return strings.HasPrefix(u.Path, "/sso/complete/") }
	held := e.signIn(t, victim, slug, "", o)
	if !strings.HasPrefix(held.URL.Path, "/sso/complete/") {
		t.Fatalf("the sign-in reached %s before completing", held.URL)
	}

	// Another browser, which holds no attempt cookie, completes it.
	attacker := testkit.NewBrowser(t, e.srv.URL)
	got := attacker.Visit(t, held.URL.String(), testkit.VisitOptions{})

	wantError(t, got, attacker, "attempt_mismatch")
	if n := e.memberCount(t, email); n != 0 {
		t.Errorf("%d members provisioned by a completion from another browser", n)
	}
	if n := len(e.audited(t, "identity.linked", "member_email", email)); n != 0 {
		t.Errorf("%d identities linked by a completion from another browser", n)
	}
	// The attempt is gone: the browser that started it cannot complete it.
	wantError(t, victim.Visit(t, held.URL.String(), testkit.VisitOptions{}), victim, "sso_failed")
	if n := e.memberCount(t, email); n != 0 {
		t.Errorf("%d members provisioned after the attempt was spent", n)
	}
}

func TestACompletionPresentingAnotherAttemptsCookieChangesNothing(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	email := uniqueEmail()
	idp.NextClaims(map[string]any{"email": email})
	victim := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Stop = func(u *url.URL) bool { return strings.HasPrefix(u.Path, "/sso/complete/") }
	held := e.signIn(t, victim, slug, "", o)

	// The other browser has started an attempt of its own, so it holds a
	// cookie, but not the victim's.
	attacker := testkit.NewBrowser(t, e.srv.URL)
	attacker.Visit(t, e.srv.URL+"/sso/"+slug+"/start", testkit.VisitOptions{Stop: func(u *url.URL) bool { return u.Host != strings.TrimPrefix(e.srv.URL, "http://") }})
	got := attacker.Visit(t, held.URL.String(), testkit.VisitOptions{})

	wantError(t, got, attacker, "attempt_mismatch")
	if n := e.memberCount(t, email); n != 0 {
		t.Errorf("%d members provisioned", n)
	}
}

func TestAnAttemptExpiresAfterTenMinutes(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	c := testkit.Clock()
	e := start(t, testkit.WithClock(c))
	slug := e.forged(t, idp, nil)
	idp.NextClaims(map[string]any{"email": uniqueEmail()})
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Stop = func(u *url.URL) bool { return strings.HasSuffix(u.Path, "/callback") }
	held := e.signIn(t, b, slug, "", o)

	c.Advance(11 * time.Minute)

	wantError(t, b.Visit(t, held.URL.String(), testkit.VisitOptions{}), b, "sso_failed")
}

func TestTheProvidersRefusalSurfacesAsAnIDPError(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	idp.DenyNext("access_denied")
	b := testkit.NewBrowser(t, e.srv.URL)

	wantError(t, e.signIn(t, b, slug, "", e.opts()), b, "idp_error")
}

func TestOnlyAPathOnTheInstanceIsReturnedTo(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	for _, tc := range []struct{ returnTo, want string }{
		{"/projects/x/flags?tab=a", "/projects/x/flags"},
		{"https://evil.com", "/"},
		{"//evil.com", "/"},
		{`/\evil.com`, "/"},
		{"/%5Cevil.com", "/"},
		{"/a\r\nSet-Cookie: x=1", "/"},
	} {
		t.Run(tc.returnTo, func(t *testing.T) {
			idp.NextClaims(map[string]any{"email": uniqueEmail()})
			b := testkit.NewBrowser(t, e.srv.URL)

			v := e.signIn(t, b, slug, tc.returnTo, e.opts())

			if v.URL.Host != strings.TrimPrefix(e.srv.URL, "http://") || v.URL.Path != tc.want {
				t.Errorf("returning to %q ended at %s, want %s on the instance", tc.returnTo, v.URL, tc.want)
			}
			if signedIn(t, b) == nil {
				t.Error("no session")
			}
		})
	}
}

func TestAnEmailIsLinkedToAnExistingMemberOnlyWhenTheProviderVouchesForIt(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	pool := testkit.Pool(t)
	for _, tc := range []struct {
		name     string
		change   func(*provider)
		claims   map[string]any
		userInfo map[string]any
		want     string // "" for a sign-in
	}{
		{"a verified email in the domains", func(p *provider) { p.linkByEmail = true }, nil, nil, ""},
		{"linking by email off", func(*provider) {}, nil, nil, "member_not_found"},
		{"an unverified email", func(p *provider) { p.linkByEmail = true }, map[string]any{"email_verified": false}, nil, "member_not_found"},
		{"a verification the token does not state", func(p *provider) { p.linkByEmail = true }, map[string]any{"email_verified": nil}, nil, "member_not_found"},
		{"a verification stated as a string", func(p *provider) { p.linkByEmail = true }, map[string]any{"email_verified": "true"}, nil, "member_not_found"},
		{"an unverified email where verification is not required", func(p *provider) { p.linkByEmail, p.requireVerifiedEmail = true, ptr(false) },
			map[string]any{"email_verified": false}, nil, ""},
		{"an email outside the domains", func(p *provider) { p.linkByEmail, p.domains = true, []string{"corp.example.net"} }, nil, nil, "member_not_found"},
		{"an email only UserInfo holds", func(p *provider) { p.linkByEmail = true }, map[string]any{"email": nil},
			map[string]any{"sub": "subject-1", "email": "{email}", "email_verified": true}, ""},
		{"a UserInfo email the token's verification does not vouch for", func(p *provider) { p.linkByEmail = true },
			map[string]any{"email": nil, "email_verified": true}, map[string]any{"sub": "subject-1", "email": "{email}"}, "member_not_found"},
		{"a UserInfo answer for another subject", func(p *provider) { p.linkByEmail = true }, map[string]any{"email": nil},
			map[string]any{"sub": "someone-else", "email": "{email}", "email_verified": true}, "member_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := e.forged(t, idp, func(p *provider) { p.autoProvision = false; tc.change(p) })
			m := testkit.NewMember(t, pool)
			claims := map[string]any{"email": m.Email, "sub": "subject-" + m.Email}
			for k, v := range tc.claims {
				claims[k] = v
			}
			if tc.userInfo != nil {
				info := map[string]any{}
				for k, v := range tc.userInfo {
					if v == "{email}" {
						v = m.Email
					}
					info[k] = v
				}
				if info["sub"] == "subject-1" {
					info["sub"] = claims["sub"]
				}
				idp.NextUserInfo(info)
			}
			idp.NextClaims(claims)
			b := testkit.NewBrowser(t, e.srv.URL)

			v := e.signIn(t, b, slug, "", e.opts())

			if tc.want != "" {
				wantError(t, v, b, tc.want)
				return
			}
			me := signedIn(t, b)
			if me == nil || me.Member == nil || me.Member.Email != m.Email {
				t.Fatalf("signed in as %+v, want the existing member %s", me, m.Email)
			}
			if n := e.memberCount(t, m.Email); n != 1 {
				t.Errorf("%d members hold %s", n, m.Email)
			}
		})
	}
}

func TestAnInvitedMemberDoesNotSignInThroughAProvider(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.linkByEmail = true })
	invited := testkit.NewMember(t, testkit.Pool(t), testkit.WithStatus(access.MemberInvited))
	idp.NextClaims(map[string]any{"email": invited.Email})
	b := testkit.NewBrowser(t, e.srv.URL)

	wantError(t, e.signIn(t, b, slug, "", e.opts()), b, "member_not_found")
}

func TestADeactivatedMemberCannotSignInAndTheFailureIsAudited(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	email := uniqueEmail()
	idp.NextClaims(map[string]any{"email": email})
	first := testkit.NewBrowser(t, e.srv.URL)
	e.signIn(t, first, slug, "", e.opts())
	me := signedIn(t, first)
	if me == nil || me.Member == nil {
		t.Fatal("the first sign-in failed")
	}
	if res, err := e.admin.DeactivateMemberWithResponse(t.Context(), me.Member.Id.String()); err != nil || res.StatusCode() != http.StatusOK {
		t.Fatalf("deactivating = %v, %v", res.StatusCode(), err)
	}
	idp.NextClaims(map[string]any{"email": email})
	b := testkit.NewBrowser(t, e.srv.URL)

	wantError(t, e.signIn(t, b, slug, "", e.opts()), b, "member_deactivated")
	failed := e.audited(t, "auth.sign_in_failed", "email", email)
	if len(failed) != 1 || failed[0]["method"] != "oidc" || failed[0]["provider"] != slug || failed[0]["reason"] != "deactivated" {
		t.Errorf("auth.sign_in_failed = %v", failed)
	}
}

func TestADisabledOrUnknownProviderAnswers404(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	if res, err := e.admin.DisableAuthProviderWithResponse(t.Context(), slug); err != nil || res.StatusCode() != http.StatusOK {
		t.Fatalf("disabling = %v, %v", res.StatusCode(), err)
	}
	b := testkit.NewBrowser(t, e.srv.URL)

	for _, path := range []string{
		"/sso/" + slug + "/start", "/sso/" + slug + "/callback?state=x&code=y",
		"/sso/nobody-" + slug + "/start", "/sso/nobody-" + slug + "/callback?state=x&code=y",
	} {
		if v := b.Visit(t, e.srv.URL+path, testkit.VisitOptions{}); v.Status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, v.Status)
		}
	}
}

func TestACompletionNamingNoAttemptRedirectsToTheSignInPage(t *testing.T) {
	e := start(t)
	b := testkit.NewBrowser(t, e.srv.URL)

	v := b.Visit(t, e.srv.URL+"/sso/complete/"+rand.Text(), testkit.VisitOptions{})

	wantError(t, v, b, "sso_failed")
}

func TestAProviderWithIdentitiesCannotBeDeleted(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, nil)
	idp.NextClaims(map[string]any{"email": uniqueEmail()})
	b := testkit.NewBrowser(t, e.srv.URL)
	e.signIn(t, b, slug, "", e.opts())

	res, err := e.admin.DeleteAuthProviderWithResponse(t.Context(), slug)

	if err != nil || res.StatusCode() != http.StatusConflict || !strings.Contains(string(res.Body), `"provider_in_use"`) ||
		!strings.Contains(string(res.Body), "identities") {
		t.Errorf("DELETE = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestTheAttemptCookieIsHostBoundWhereCookiesAreSecure(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	for _, tc := range []struct {
		name, baseURL string
		options       []testkit.ServerOption
		wantName      string
		wantSecure    bool
	}{
		{"https", "https://flags.example.com", nil, "__Host-flagnetic_sso", true},
		{"localhost over http", "http://localhost:8080", nil, "flagnetic_sso", false},
		{"insecure cookies allowed", "http://flags.example.com", []testkit.ServerOption{testkit.WithInsecureCookies()}, "flagnetic_sso", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := testkit.StartServer(t, append([]testkit.ServerOption{testkit.WithBaseURL(tc.baseURL)}, tc.options...)...)
			e := env{srv: srv, admin: testkit.Client(t, srv, testkit.InstanceAdminToken(t))}
			slug := e.forged(t, idp, nil)
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/sso/"+slug+"/start", nil)
			if err != nil {
				t.Fatal(err)
			}

			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()

			var found *http.Cookie
			for _, c := range res.Cookies() {
				if strings.HasSuffix(c.Name, "flagnetic_sso") {
					found = c
				}
			}
			if found == nil || found.Name != tc.wantName || found.Secure != tc.wantSecure || !found.HttpOnly || found.Path != "/" ||
				found.Domain != "" || found.SameSite != http.SameSiteLaxMode || found.MaxAge != 600 {
				t.Errorf("the attempt cookie = %+v, want %s (secure %v), HttpOnly, Lax, path /, no domain, 10 minutes", found, tc.wantName, tc.wantSecure)
			}
			if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), idp.Issuer+"/authorize?") {
				t.Errorf("start = %d to %s", res.StatusCode, res.Header.Get("Location"))
			}
			loc, _ := url.Parse(res.Header.Get("Location"))
			q := loc.Query()
			if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" ||
				q.Get("response_type") != "code" || q.Get("redirect_uri") != tc.baseURL+"/sso/"+slug+"/callback" {
				t.Errorf("authorization request = %v", q)
			}
		})
	}
}
