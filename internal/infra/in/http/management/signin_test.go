package management_test

import (
	"crypto/rand"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// clearedCookie is the Set-Cookie of an ended session on a plain HTTP
// localhost.
const clearedCookie = "flagnetic_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax"

// auditOf returns the audit entries of action on the entity entityID, newest
// first, as an instance admin reads them.
func auditOf(t *testing.T, srv *testkit.Server, action, entityID string) []management.AuditEntry {
	t.Helper()
	res, err := testkit.Client(t, srv, testkit.InstanceAdminToken(t)).ListAuditEntriesWithResponse(t.Context(),
		&management.ListAuditEntriesParams{Action: &action, EntityId: &entityID})
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func TestSignInOpensASessionWhoseWritesAreRefusedCrossOriginUntilSignOut(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := testkit.NewBrowser(t, srv.URL)

	me := b.SignIn(t, admin.Email, admin.Password)
	if me.Credential.Method != management.CredentialMethodSession || me.Credential.TokenId != nil ||
		me.Member == nil || me.Member.Email != admin.Email || !me.Member.InstanceAdmin {
		t.Fatalf("sign-in answered %+v, want the instance admin signed in to a session", me)
	}
	cookie := b.Cookie(management.SessionCookie)
	if cookie == "" {
		t.Fatal("no session cookie after sign-in")
	}
	if res, err := b.GetMeWithResponse(t.Context()); err != nil || res.JSON200 == nil ||
		res.JSON200.Credential.Method != management.CredentialMethodSession {
		t.Fatalf("GET /me with the cookie = %v, %v; want the session", res.StatusCode(), err)
	}

	b.Header.Set("Sec-Fetch-Site", "cross-site")
	refused, err := b.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: projectKey(), Name: "Cross"})
	if err != nil {
		t.Fatal(err)
	}
	if refused.StatusCode() != http.StatusForbidden || refused.ApplicationproblemJSON403 == nil ||
		refused.ApplicationproblemJSON403.Code != "cross_origin" {
		t.Errorf("cross-site write = %d %s, want 403 cross_origin", refused.StatusCode(), refused.Body)
	}
	b.Header.Set("Sec-Fetch-Site", "same-origin")
	created, err := b.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: projectKey(), Name: "Same"})
	if err != nil || created.StatusCode() != http.StatusCreated {
		t.Fatalf("same-origin write = %d %s, want 201", created.StatusCode(), created.Body)
	}

	out, err := b.SignOutWithResponse(t.Context())
	if err != nil || out.StatusCode() != http.StatusNoContent {
		t.Fatalf("sign-out = %d %s, want 204", out.StatusCode(), out.Body)
	}
	if b.Cookie(management.SessionCookie) != "" {
		t.Error("the session cookie is still held after sign-out")
	}
	if res, _ := b.GetMeWithResponse(t.Context()); res.StatusCode() != http.StatusUnauthorized {
		t.Errorf("GET /me after sign-out = %d, want 401", res.StatusCode())
	}

	b.SetCookie(management.SessionCookie, cookie)
	res, err := b.GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p := res.ApplicationproblemJSON401; p == nil || p.Code != "session_expired" {
		t.Errorf("GET /me with the old cookie = %d %s, want 401 session_expired", res.StatusCode(), res.Body)
	}
	if got := res.HTTPResponse.Header.Get("Set-Cookie"); got != clearedCookie {
		t.Errorf("Set-Cookie = %q, want %q", got, clearedCookie)
	}
}

func TestSignInCookieAttributes(t *testing.T) {
	attrs := regexp.MustCompile(`^flagnetic_session=[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}; Path=/; HttpOnly(; Secure)?; SameSite=Lax$`)
	for name, tc := range map[string]struct {
		opts   []testkit.ServerOption
		header map[string]string
		secure bool
	}{
		"plain HTTP on localhost":              {},
		"an HTTPS base URL":                    {opts: []testkit.ServerOption{testkit.WithBaseURL("https://flags.example.com")}, secure: true},
		"an HTTP base URL on localhost":        {opts: []testkit.ServerOption{testkit.WithBaseURL("http://localhost:8080")}},
		"an HTTP base URL elsewhere":           {opts: []testkit.ServerOption{testkit.WithBaseURL("http://flags.internal")}, secure: true},
		"elsewhere with insecure cookies":      {opts: []testkit.ServerOption{testkit.WithBaseURL("http://flags.internal"), testkit.WithInsecureCookies()}},
		"HTTPS forwarded by a trusted proxy":   {opts: []testkit.ServerOption{trustLoopback()}, header: map[string]string{"X-Forwarded-Proto": "https"}, secure: true},
		"HTTPS forwarded by an untrusted peer": {header: map[string]string{"X-Forwarded-Proto": "https"}},
	} {
		srv := testkit.StartServer(t, tc.opts...)
		admin := testkit.NewMember(t, testkit.Pool(t))
		b := testkit.NewBrowser(t, srv.URL)
		for k, v := range tc.header {
			b.Header.Set(k, v)
		}
		res, err := b.SignInWithResponse(t.Context(), management.SignInRequest{Email: admin.Email, Password: admin.Password})
		if err != nil {
			t.Fatal(err)
		}
		got := res.HTTPResponse.Header.Values("Set-Cookie")
		if len(got) != 1 || !attrs.MatchString(got[0]) {
			t.Errorf("%s: Set-Cookie = %q, want one session cookie", name, got)
			continue
		}
		if secure := strings.Contains(got[0], "; Secure"); secure != tc.secure {
			t.Errorf("%s: Secure = %v, want %v", name, secure, tc.secure)
		}
	}
}

func trustLoopback() testkit.ServerOption {
	return testkit.WithEdge(func(o *flaghttp.Options) {
		o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	})
}

func TestWrongEmailAndWrongPasswordAreTheSame401(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := testkit.NewBrowser(t, srv.URL)
	var details []string
	for name, attempt := range map[string]management.SignInRequest{
		"wrong password": {Email: m.Email, Password: "not-the-password-of-anyone"},
		"unknown email":  {Email: "nobody-" + m.Email, Password: m.Password},
		"empty password": {Email: m.Email, Password: ""},
	} {
		res, err := b.SignInWithResponse(t.Context(), attempt)
		if err != nil {
			t.Fatal(err)
		}
		p := res.ApplicationproblemJSON401
		if p == nil || p.Code != "invalid_credentials" || p.Detail == nil {
			t.Errorf("%s: %d %s, want 401 invalid_credentials", name, res.StatusCode(), res.Body)
			continue
		}
		if c := res.HTTPResponse.Header.Values("Set-Cookie"); len(c) != 0 {
			t.Errorf("%s: Set-Cookie = %q, want none", name, c)
		}
		details = append(details, *p.Detail)
	}
	for _, d := range details {
		if d != details[0] {
			t.Errorf("details differ: %q", details)
			break
		}
	}
	failed := auditOf(t, srv, "auth.sign_in_failed", m.ID.String())
	if len(failed) != 2 {
		t.Fatalf("%d sign_in_failed entries for the member, want 2", len(failed))
	}
	for _, e := range failed {
		after, _ := e.After.(map[string]any)
		if e.Actor.Kind != "member" || e.Actor.MemberId == nil || e.Actor.MemberId.String() != m.ID.String() ||
			after["method"] != "local" || after["email"] != m.Email || after["reason"] != "wrong_password" {
			t.Errorf("entry = actor %+v, after %v; want the member's failed local sign-in, wrong password", e.Actor, e.After)
		}
	}
	if unknown := auditOfQ(t, srv, "nobody-"+m.Email); len(unknown) != 0 {
		t.Errorf("%d entries name the unknown email, want none", len(unknown))
	}
}

// auditOfQ returns the instance audit entries whose text holds q.
func auditOfQ(t *testing.T, srv *testkit.Server, q string) []management.AuditEntry {
	t.Helper()
	res, err := testkit.Client(t, srv, testkit.InstanceAdminToken(t)).ListAuditEntriesWithResponse(t.Context(),
		&management.ListAuditEntriesParams{Q: &q})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit: %v", err)
	}
	return res.JSON200.Items
}

func TestSignInAndSignOutAreAudited(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	m := testkit.NewMember(t, testkit.Pool(t))
	b := testkit.NewBrowser(t, srv.URL)
	b.SignIn(t, strings.ToUpper(m.Email), m.Password)
	c.Advance(time.Hour)
	if res, err := b.SignOutWithResponse(t.Context()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("sign-out = %v, %v", res.StatusCode(), err)
	}
	for action, at := range map[string]time.Time{"auth.signed_in": testkit.Start, "auth.signed_out": testkit.Start.Add(time.Hour)} {
		entries := auditOf(t, srv, action, m.ID.String())
		if len(entries) != 1 {
			t.Errorf("%s: %d entries, want 1", action, len(entries))
			continue
		}
		e := entries[0]
		after, _ := e.After.(map[string]any)
		if e.Actor.Kind != "member" || e.Actor.Id != m.ID.String() || e.Entity.Kind != "member" || e.Entity.Key != m.Email ||
			!e.OccurredAt.Equal(at) || after["method"] != "local" || after["email"] != m.Email || e.ProjectId != nil {
			t.Errorf("%s: entry = %+v, want the member's own instance entry at %v", action, e, at)
		}
	}
}

func TestTheCredentialIsArgon2idAndOlderParametersAreRehashedOnSignIn(t *testing.T) {
	pool := testkit.Pool(t)
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, pool)
	store := pgaccess.NewStore(pool)
	_, stored, err := store.MemberByEmail(t.Context(), m.Email)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=2\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`).MatchString(stored) {
		t.Fatalf("stored credential %q is not argon2id under the current parameters", stored)
	}
	older, err := password.NewHasher(password.Params{MemoryKiB: 19456, Time: 2, Threads: 1, SaltLength: 16, KeyLength: 32}).
		Hash(t.Context(), secret.New(m.Password))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetPasswordHash(t.Context(), m.ID, older, testkit.Start); err != nil {
		t.Fatal(err)
	}
	testkit.NewBrowser(t, srv.URL).SignIn(t, m.Email, m.Password)
	_, rehashed, err := store.MemberByEmail(t.Context(), m.Email)
	if err != nil {
		t.Fatal(err)
	}
	if rehashed == older || !strings.HasPrefix(rehashed, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("credential after sign-in = %q, want rehashed under the current parameters", rehashed)
	}
	testkit.NewBrowser(t, srv.URL).SignIn(t, m.Email, m.Password)
}

func TestAForgedSessionCookieIsDropped(t *testing.T) {
	// The server's database is unreachable: a cookie looked up would answer
	// 500, one dropped by its tag answers 401 at once.
	unreachable, err := pgxpool.New(t.Context(), "postgres://flagnetic@127.0.0.1:1/flagnetic?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unreachable.Close)
	srv := testkit.StartServer(t, testkit.WithPool(unreachable))
	issuer := testkit.NewBrowser(t, testkit.StartServer(t).URL)
	m := testkit.NewMember(t, testkit.Pool(t))
	issuer.SignIn(t, m.Email, m.Password)
	value := issuer.Cookie(management.SessionCookie)
	rawPart, tagPart, _ := strings.Cut(value, ".")
	flipped := []byte(tagPart)
	flipped[0] ^= 1

	for name, forged := range map[string]string{
		"tag altered":   rawPart + "." + string(flipped),
		"tag missing":   rawPart,
		"random absent": "." + tagPart,
		"not base64":    "forged.cookie",
	} {
		b := testkit.NewBrowser(t, srv.URL)
		b.SetCookie(management.SessionCookie, forged)
		start := time.Now()
		res, err := b.GetMeWithResponse(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if p := res.ApplicationproblemJSON401; p == nil || p.Code != "unauthenticated" || time.Since(start) > time.Second {
			t.Errorf("%s: %d %s after %v, want 401 unauthenticated at once", name, res.StatusCode(), res.Body, time.Since(start))
		}
	}
	other := testkit.NewBrowser(t, testkit.StartServer(t).URL)
	other.SignIn(t, m.Email, m.Password)
	b := testkit.NewBrowser(t, srv.URL)
	b.SetCookie(management.SessionCookie, rawPart+"."+strings.Split(other.Cookie(management.SessionCookie), ".")[1])
	if res, _ := b.GetMeWithResponse(t.Context()); res.ApplicationproblemJSON401 == nil || res.ApplicationproblemJSON401.Code != "unauthenticated" {
		t.Errorf("another cookie's tag: %d %s, want 401 unauthenticated", res.StatusCode(), res.Body)
	}
}

func TestASessionEndsAfter24HoursIdleOr30DaysInAll(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	m := testkit.NewMember(t, testkit.Pool(t))
	status := func(b *testkit.Browser) (int, string) {
		res, err := b.GetMeWithResponse(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if p := res.ApplicationproblemJSON401; p != nil {
			return res.StatusCode(), p.Code
		}
		return res.StatusCode(), ""
	}

	idle := testkit.NewBrowser(t, srv.URL)
	idle.SignIn(t, m.Email, m.Password)
	for _, step := range []time.Duration{23 * time.Hour, 23 * time.Hour} {
		c.Advance(step)
		if code, _ := status(idle); code != http.StatusOK {
			t.Fatalf("after %v idle: %d, want 200", step, code)
		}
	}
	c.Advance(24 * time.Hour)
	if code, problem := status(idle); code != http.StatusUnauthorized || problem != "session_expired" {
		t.Errorf("after 24 hours idle: %d %s, want 401 session_expired", code, problem)
	}

	c.Set(testkit.Start.Add(100 * 24 * time.Hour))
	busy := testkit.NewBrowser(t, srv.URL)
	busy.SignIn(t, m.Email, m.Password)
	for i := range 35 {
		c.Advance(20 * time.Hour)
		if code, _ := status(busy); code != http.StatusOK {
			t.Fatalf("after %d steps of 20 hours in use: %d, want 200", i+1, code)
		}
	}
	c.Advance(20 * time.Hour)
	if code, problem := status(busy); code != http.StatusUnauthorized || problem != "session_expired" {
		t.Errorf("after 30 days 20 hours in use: %d %s, want 401 session_expired", code, problem)
	}
}

func TestABearerTokenWinsOverASessionCookieAndSkipsCrossOriginProtection(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := testkit.NewBrowser(t, srv.URL)
	b.SignIn(t, m.Email, m.Password)
	b.Header.Set("Authorization", "Bearer "+testkit.InstanceAdminToken(t))
	me, err := b.GetMeWithResponse(t.Context())
	if err != nil || me.JSON200 == nil {
		t.Fatalf("GET /me = %v, %v", me.StatusCode(), err)
	}
	if me.JSON200.Credential.Method != management.CredentialMethodToken || me.JSON200.Member.Email != testkit.InstanceAdminEmail {
		t.Errorf("GET /me with a token and a cookie = %+v, want the token's instance admin", me.JSON200)
	}
	b.Header.Set("Sec-Fetch-Site", "cross-site")
	res, err := b.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: projectKey(), Name: "By token"})
	if err != nil || res.StatusCode() != http.StatusCreated {
		t.Errorf("cross-site write with a token = %d %s, want 201", res.StatusCode(), res.Body)
	}
}

func TestCrossOriginProtectionGuardsCookieRequests(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL("https://flags.example.com"), testkit.WithInsecureCookies())
	admin := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := testkit.NewBrowser(t, srv.URL)
	b.SignIn(t, admin.Email, admin.Password)
	write := func(header map[string]string) (int, string) {
		b.Header = http.Header{}
		for k, v := range header {
			b.Header.Set(k, v)
		}
		res, err := b.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: projectKey(), Name: "Guarded"})
		if err != nil {
			t.Fatal(err)
		}
		if p := res.ApplicationproblemJSON403; p != nil {
			return res.StatusCode(), p.Code
		}
		return res.StatusCode(), ""
	}
	for name, h := range map[string]map[string]string{
		"cross-site":                     {"Sec-Fetch-Site": "cross-site"},
		"same-site":                      {"Sec-Fetch-Site": "same-site"},
		"another origin from an old one": {"Origin": "https://evil.example"},
	} {
		if code, problem := write(h); code != http.StatusForbidden || problem != "cross_origin" {
			t.Errorf("%s write: %d %s, want 403 cross_origin", name, code, problem)
		}
	}
	for name, h := range map[string]map[string]string{
		"same-origin":                  {"Sec-Fetch-Site": "same-origin"},
		"typed by the user":            {"Sec-Fetch-Site": "none"},
		"no fetch metadata":            {},
		"the base URL's origin":        {"Origin": "https://flags.example.com"},
		"the request's own host":       {"Origin": srv.URL},
		"cross-site from the base URL": {"Sec-Fetch-Site": "cross-site", "Origin": "https://flags.example.com"},
	} {
		if code, problem := write(h); code != http.StatusCreated {
			t.Errorf("%s write: %d %s, want 201", name, code, problem)
		}
	}
	b.Header = http.Header{"Sec-Fetch-Site": {"cross-site"}}
	// A session is only ever used by the console on its own origin, so a
	// read from another is refused too: what it answers is no other page's
	// business.
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		if res, body := b.Do(t, method, "/api/v1/me", "", ""); res.StatusCode != http.StatusForbidden ||
			(method == http.MethodGet && !strings.Contains(body, `"cross_origin"`)) {
			t.Errorf("cross-site %s = %d %s, want 403 cross_origin", method, res.StatusCode, body)
		}
	}
	b.Header = http.Header{"Sec-Fetch-Site": {"same-origin"}}
	if res, _ := b.GetMeWithResponse(t.Context()); res.StatusCode() != http.StatusOK {
		t.Errorf("same-origin read = %d, want 200", res.StatusCode())
	}
	b.Header = http.Header{"Sec-Fetch-Site": {"cross-site"}}
	if res, _ := b.SignOutWithResponse(t.Context()); res.StatusCode() != http.StatusForbidden {
		t.Errorf("cross-site sign-out = %d, want 403", res.StatusCode())
	}

	signIn := `{"email":"` + admin.Email + `","password":"` + admin.Password + `"}`
	other := testkit.NewBrowser(t, srv.URL)
	for _, contentType := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		res, body := other.Do(t, http.MethodPost, "/api/v1/auth/sign-in", contentType, signIn)
		if res.StatusCode != http.StatusForbidden || !strings.Contains(body, `"cross_origin"`) || len(res.Cookies()) != 0 {
			t.Errorf("sign-in as %q: %d %s, want 403 cross_origin and no cookie", contentType, res.StatusCode, body)
		}
	}
	if res, body := other.Do(t, http.MethodPost, "/api/v1/auth/sign-in", "application/json; charset=utf-8", signIn); res.StatusCode != http.StatusOK {
		t.Errorf("sign-in as JSON: %d %s, want 200", res.StatusCode, body)
	}
}

func TestAuthMethodsAreServedWithoutAuthentication(t *testing.T) {
	srv := testkit.StartServer(t)
	for name, cookie := range map[string]string{"no cookie": "", "a forged cookie": "forged.cookie"} {
		b := testkit.NewBrowser(t, srv.URL)
		if cookie != "" {
			b.SetCookie(management.SessionCookie, cookie)
		}
		res, body := b.Do(t, http.MethodGet, "/api/v1/auth/methods", "", "")
		if res.StatusCode != http.StatusOK || body != `{"local_login_enabled":true,"providers":[]}`+"\n" {
			t.Errorf("%s: %d %q, want local sign-in alone", name, res.StatusCode, body)
		}
	}
}

func TestASessionHoldsEveryScope(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin())
	b := testkit.NewBrowser(t, srv.URL)
	b.SignIn(t, admin.Email, admin.Password)
	res, err := b.CreateProjectWithResponse(t.Context(), management.ProjectCreate{Key: projectKey(), Name: "By session"})
	if err != nil || res.StatusCode() != http.StatusCreated {
		t.Errorf("a write signed in by a session = %d %s, want 201: a session is as wide as its member", res.StatusCode(), res.Body)
	}
}

func TestSigningOutNeedsASessionAndATokenEndsNone(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	b := testkit.NewBrowser(t, srv.URL)
	b.SignIn(t, m.Email, m.Password)
	res, err := testkit.Client(t, srv, testkit.PersonalToken(t, testkit.Pool(t), m.ID, "script")).SignOutWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p := res.ApplicationproblemJSON403; p == nil || p.Code != "session_required" {
		t.Fatalf("sign-out with a token = %d %s, want 403 session_required: a script must not believe it ended something", res.StatusCode(), res.Body)
	}
	me, err := b.GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if me.StatusCode() != http.StatusOK {
		t.Errorf("GET /me with the session after a token tried to sign out = %d, want 200", me.StatusCode())
	}
	if n := len(auditOf(t, srv, "auth.signed_out", m.ID.String())); n != 0 {
		t.Errorf("%d signed_out entries, want none", n)
	}
}

func TestACookieOfASessionTheServerNoLongerHoldsHasExpired(t *testing.T) {
	srv := testkit.StartServer(t)
	m := testkit.NewMember(t, testkit.Pool(t))
	// Signed under the instance's key, so its tag holds, for a session
	// never stored, as one purged after it ended.
	_, value := access.NewSession(id.New[access.Session](testkit.IDs(testkit.Clock())), access.NewSessionInput{
		Member: m.ID, Method: access.SignInLocal, Timeouts: access.DefaultSessionTimeouts, Now: testkit.Start,
	}, rand.Reader, secret.NewKeyring(secret.New(testkit.SecretKey)).Key(secret.UseSessionCookie))
	b := testkit.NewBrowser(t, srv.URL)
	b.SetCookie(management.SessionCookie, value.Reveal())
	res, err := b.GetMeWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p := res.ApplicationproblemJSON401; p == nil || p.Code != "session_expired" || res.HTTPResponse.Header.Get("Set-Cookie") != clearedCookie {
		t.Errorf("GET /me = %d %s, want 401 session_expired clearing the cookie", res.StatusCode(), res.Body)
	}
}
