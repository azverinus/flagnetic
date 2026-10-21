package management_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management/operations"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// invite invites a new member called name through admin and returns the
// answer; the invitation's secret is recorded for the audit guard.
func invite(t *testing.T, admin *management.ClientWithResponses, name string) management.MemberInvitation {
	t.Helper()
	email := strings.ToLower(rand.Text()[:12]) + "@example.com"
	res := do(admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{Email: email, DisplayName: name}))
	if res.JSON201 == nil {
		t.Fatalf("POST /members = %d: %s", res.StatusCode(), res.Body)
	}
	recordLink(t, res.JSON201)
	return *res.JSON201
}

// recordLink records the secret of the link mi carries for the audit guard,
// failing unless it carries one.
func recordLink(t *testing.T, mi *management.MemberInvitation) {
	t.Helper()
	if mi.Invitation == nil {
		t.Fatalf("no invitation in %+v", mi)
	}
	testkit.RecordSecret(secretOf(mi.Invitation.Url))
}

// secretOf returns the secret an invitation link ends with.
func secretOf(url string) string { return url[strings.LastIndex(url, "/")+1:] }

// reissue reissues m's invitation through admin and returns the new link.
func reissue(t *testing.T, admin *management.ClientWithResponses, m management.Member) string {
	t.Helper()
	res := do(admin.ReissueInvitationWithResponse(t.Context(), m.Id.String()))
	if res.JSON201 == nil {
		t.Fatalf("POST /members/%s/invitation = %d: %s", m.Id, res.StatusCode(), res.Body)
	}
	recordLink(t, res.JSON201)
	return res.JSON201.Invitation.Url
}

// accept accepts the invitation link opens through b, setting password.
func accept(t *testing.T, b *testkit.Browser, link, password string) *management.AcceptInvitationResponse {
	t.Helper()
	testkit.RecordSecret(password)
	return do(b.AcceptInvitationWithResponse(t.Context(), secretOf(link), management.InvitationAcceptance{Password: password}))
}

// opened returns what link opens, read by anyone.
func opened(t *testing.T, srv *testkit.Server, link string) *management.GetInvitationResponse {
	t.Helper()
	return do(testkit.NewBrowser(t, srv.URL).GetInvitationWithResponse(t.Context(), secretOf(link)))
}

// wantGone fails unless link opens nothing and cannot be accepted.
func wantGone(t *testing.T, srv *testkit.Server, link, why string) {
	t.Helper()
	got := opened(t, srv, link)
	assertProblem(t, why+": GET", got.StatusCode(), got.Body, http.StatusGone, "invitation_gone")
	acc := accept(t, testkit.NewBrowser(t, srv.URL), link, "a-perfectly-fine-passphrase-7")
	assertProblem(t, why+": accept", acc.StatusCode(), acc.Body, http.StatusGone, "invitation_gone")
}

const newPassword = "harbour-lantern-quiet-4410"

func TestInviteThenSetAPasswordThenSignIn(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))

	mi := invite(t, admin, "Grace Hopper")
	m := mi.Member
	if m.Status != management.MemberStatusInvited || m.InstanceAdmin || m.LastSignInAt != nil || m.DisplayName != "Grace Hopper" ||
		m.ProjectCount != 0 {
		t.Errorf("invited = %+v", m)
	}
	link := mi.Invitation.Url
	if !regexp.MustCompile(`^` + regexp.QuoteMeta(srv.URL) + `/invitations/[A-Za-z0-9_-]{43}$`).MatchString(link) {
		t.Errorf("link = %q, want the console's origin, /invitations/ and 43 base64url characters", link)
	}
	if !mi.Invitation.ExpiresAt.Equal(c.Now().Add(7 * 24 * time.Hour)) {
		t.Errorf("expires at %v, want in seven days", mi.Invitation.ExpiresAt)
	}

	got := opened(t, srv, link)
	if got.JSON200 == nil {
		t.Fatalf("GET the invitation = %d: %s", got.StatusCode(), got.Body)
	}
	if want := m.Email[:1] + "***@example.com"; got.JSON200.Email != want || got.JSON200.DisplayName != "Grace Hopper" ||
		!got.JSON200.ExpiresAt.Equal(mi.Invitation.ExpiresAt) {
		t.Errorf("invitation = %+v, want %s, the name and the expiry", got.JSON200, want)
	}

	b := testkit.NewBrowser(t, srv.URL)
	accepted := accept(t, b, link, newPassword)
	if accepted.JSON200 == nil {
		t.Fatalf("accept = %d: %s", accepted.StatusCode(), accepted.Body)
	}
	if me := accepted.JSON200; me.Member == nil || me.Member.Email != m.Email || me.Member.Status != management.MemberStatusActive ||
		me.Credential.Method != management.CredentialMethodSession {
		t.Errorf("accepted = %+v, want the member, active, signed in to a session", me)
	}
	if b.Cookie(management.SessionCookie) == "" {
		t.Error("accepting set no session cookie")
	}
	wantSignedIn(t, b, "the session accepting opened")
	if read := memberOf(t, admin, id.FromBytes[access.Member](m.Id)); read.Status != management.MemberStatusActive || read.LastSignInAt == nil {
		t.Errorf("member after accepting = %+v, want active and signed in", read)
	}
	testkit.NewBrowser(t, srv.URL).SignIn(t, m.Email, newPassword)
	wantGone(t, srv, link, "a used link")

	id := m.Id.String()
	if invited := auditOf(t, srv, "member.invited", id); len(invited) != 1 || invited[0].After.(map[string]any)["status"] != "invited" {
		t.Errorf("member.invited = %+v, want one, invited", invited)
	}
	if acc := auditOf(t, srv, "member.invitation_accepted", id); len(acc) != 1 || acc[0].After.(map[string]any)["status"] != "active" ||
		acc[0].Actor.MemberId == nil || acc[0].Actor.MemberId.String() != id {
		t.Errorf("member.invitation_accepted = %+v, want one, active, by the member", acc)
	}
	if n := len(auditOf(t, srv, "auth.signed_in", id)); n != 2 {
		t.Errorf("auth.signed_in entries = %d, want 2: accepting and signing in", n)
	}
}

func TestAnInvitationLinkStartsWithTheBaseURL(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithBaseURL("https://flags.example.com/"))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	if link := invite(t, admin, "Behind A Proxy").Invitation.Url; !strings.HasPrefix(link, "https://flags.example.com/invitations/") {
		t.Errorf("link = %q, want it under the base URL", link)
	}
}

func TestAnInvitationExpiresAfterSevenDays(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Late Comer")

	c.Advance(7*24*time.Hour - time.Minute)
	if got := opened(t, srv, mi.Invitation.Url); got.StatusCode() != http.StatusOK {
		t.Errorf("a minute before it expires: GET = %d", got.StatusCode())
	}
	c.Advance(2 * time.Minute)
	wantGone(t, srv, mi.Invitation.Url, "an expired link")
	if got := memberOf(t, admin, id.FromBytes[access.Member](mi.Member.Id)); got.Status != management.MemberStatusInvited {
		t.Errorf("member = %s, want still invited", got.Status)
	}
}

func TestReissuingAnInvitationInvalidatesTheOldLink(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Second Chance")
	fresh := reissue(t, admin, mi.Member)
	if fresh == mi.Invitation.Url {
		t.Fatal("the reissued link is the old one")
	}
	wantGone(t, srv, mi.Invitation.Url, "a replaced link")
	if res := accept(t, testkit.NewBrowser(t, srv.URL), fresh, newPassword); res.JSON200 == nil ||
		res.JSON200.Member.Status != management.MemberStatusActive {
		t.Errorf("accepting the new link = %d %s", res.StatusCode(), res.Body)
	}
	if n := len(auditOf(t, srv, "member.invitation_reissued", mi.Member.Id.String())); n != 1 {
		t.Errorf("member.invitation_reissued entries = %d, want 1", n)
	}
}

func TestAnInvitationIsRefusedForAMemberWhoIsNotInvited(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	for _, s := range []access.MemberStatus{access.MemberActive, access.MemberDeactivated} {
		m := testkit.NewMember(t, pool, testkit.WithStatus(s))
		res := do(admin.ReissueInvitationWithResponse(t.Context(), m.ID.String()))
		assertProblem(t, string(s), res.StatusCode(), res.Body, http.StatusConflict, "member_not_invited")
	}
	res := do(admin.ReissueInvitationWithResponse(t.Context(), "0198a3f0-0000-7000-8000-000000000000"))
	assertProblem(t, "unknown member", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
}

func TestInvitingAnEmailTheInstanceAlreadyHolds(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Only Once")
	for _, email := range []string{mi.Member.Email, strings.ToUpper(mi.Member.Email), testkit.InstanceAdminEmail} {
		res := do(admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{Email: email, DisplayName: "Twice"}))
		assertProblem(t, email, res.StatusCode(), res.Body, http.StatusConflict, "already_exists")
	}
	for name, body := range map[string]management.MemberInvite{
		"malformed email": {Email: "not an email", DisplayName: "Someone"},
		"blank name":      {Email: "blank-" + mi.Member.Email, DisplayName: " "},
	} {
		res := do(admin.InviteMemberWithResponse(t.Context(), body))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_member")
	}
}

func TestAcceptingAnInvitationAppliesThePasswordPolicy(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Careful Chooser")
	local, _, _ := strings.Cut(mi.Member.Email, "@")
	for password, rule := range map[string]string{
		"short":                  "length",
		strings.Repeat("a", 129): "length",
		"my-" + local + "-pass":  "email",
		"administrator":          "common",
	} {
		res := accept(t, testkit.NewBrowser(t, srv.URL), mi.Invitation.Url, password)
		p := assertProblem(t, rule, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "weak_password")
		if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Params == nil || (*(*p.Errors)[0].Params)["rule"] != rule {
			t.Errorf("%s: violations = %+v, want the rule %s", rule, p.Errors, rule)
		}
		if len(res.HTTPResponse.Cookies()) != 0 {
			t.Errorf("%s: a refused acceptance set a cookie", rule)
		}
	}
	if got := memberOf(t, admin, id.FromBytes[access.Member](mi.Member.Id)); got.Status != management.MemberStatusInvited {
		t.Errorf("member = %s, want still invited", got.Status)
	}
	if res := accept(t, testkit.NewBrowser(t, srv.URL), mi.Invitation.Url, newPassword); res.StatusCode() != http.StatusOK {
		t.Errorf("a good password = %d %s, want 200", res.StatusCode(), res.Body)
	}
}

func TestAnUnknownInvitationSecretIsGone(t *testing.T) {
	srv := testkit.StartServer(t)
	never := make([]byte, 32)
	_, _ = rand.Read(never)
	for _, s := range []string{base64.RawURLEncoding.EncodeToString(never), "not-base64url!", strings.Repeat("a", 43)} {
		wantGone(t, srv, "/invitations/"+s, s)
	}
}

func TestAcceptingAnInvitationIsRefusedAsAForm(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Form Filler")
	b := testkit.NewBrowser(t, srv.URL)
	b.Header.Set("Sec-Fetch-Site", "cross-site")
	b.Header.Set("Origin", "https://evil.example")
	for _, contentType := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
		res, body := b.Do(t, http.MethodPost, management.BaseURL+"/auth/invitations/"+secretOf(mi.Invitation.Url), contentType,
			`{"password":"`+newPassword+`"}`)
		if res.StatusCode != http.StatusUnsupportedMediaType || !strings.Contains(body, "unsupported_media_type") || len(res.Cookies()) != 0 {
			t.Errorf("accepting as %q = %d %s, want 415 unsupported_media_type and no cookie", contentType, res.StatusCode, body)
		}
	}
	if got := opened(t, srv, mi.Invitation.Url); got.StatusCode() != http.StatusOK {
		t.Errorf("the link after refused forms = %d, want still open", got.StatusCode())
	}
}

func TestAnInvitationSecretNeverReachesALogLine(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Quiet One")
	secret := secretOf(mi.Invitation.Url)
	opened(t, srv, mi.Invitation.Url)
	b := testkit.NewBrowser(t, srv.URL)
	b.Do(t, http.MethodPut, management.BaseURL+"/auth/invitations/"+secret, "application/json", `{}`)
	accept(t, b, mi.Invitation.Url, newPassword)

	paths := map[string]bool{}
	for _, r := range srv.Logs.Records() {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(line), secret) {
			t.Errorf("a log line holds the secret: %s", line)
		}
		if p, ok := r.Attrs["path"].(string); ok {
			paths[p] = true
		}
	}
	if !paths[management.BaseURL+"/auth/invitations/{secret}"] {
		t.Errorf("logged paths = %v, want the route's pattern in place of the secret", paths)
	}
}

func TestThePublicOperationsAreTheOnesDeclaredPublic(t *testing.T) {
	var public []string
	for pattern, op := range operations.Table {
		if op.Requirement == access.RequirePublic {
			public = append(public, pattern)
		}
	}
	slices.Sort(public)
	want := []string{
		"GET /auth/invitations/{secret}", "GET /auth/methods", "POST /auth/invitations/{secret}", "POST /auth/sign-in",
	}
	if !slices.Equal(public, want) {
		t.Fatalf("public operations = %v, want %v", public, want)
	}
	srv := testkit.StartServer(t)
	b := testkit.NewBrowser(t, srv.URL)
	for _, pattern := range public {
		method, path, _ := strings.Cut(pattern, " ")
		path = strings.ReplaceAll(path, "{secret}", rand.Text())
		res, body := b.Do(t, method, management.BaseURL+path, "application/json", `{}`)
		if res.StatusCode == http.StatusUnauthorized && strings.Contains(body, `"unauthenticated"`) {
			t.Errorf("%s answers unauthenticated without a credential", pattern)
		}
	}
}

func TestReactivatingAMemberWhoNeverSetAPasswordInvitesThemAgain(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Back Again")
	id := mi.Member.Id.String()
	if res := do(admin.DeactivateMemberWithResponse(t.Context(), id)); res.StatusCode() != http.StatusOK {
		t.Fatalf("deactivate = %d: %s", res.StatusCode(), res.Body)
	}
	wantGone(t, srv, mi.Invitation.Url, "the link of a deactivated member")

	res := do(admin.ReactivateMemberWithResponse(t.Context(), id))
	if res.JSON200 == nil {
		t.Fatalf("reactivate = %d: %s", res.StatusCode(), res.Body)
	}
	if res.JSON200.Member.Status != management.MemberStatusInvited || res.JSON200.Invitation == nil {
		t.Fatalf("reactivated = %+v, want invited with a fresh invitation", res.JSON200)
	}
	recordLink(t, res.JSON200)
	wantGone(t, srv, mi.Invitation.Url, "the link consumed at deactivation, after reactivation")
	if acc := accept(t, testkit.NewBrowser(t, srv.URL), res.JSON200.Invitation.Url, newPassword); acc.StatusCode() != http.StatusOK {
		t.Errorf("accepting the fresh link = %d %s", acc.StatusCode(), acc.Body)
	}
	if again := reissueStatus(t, admin, mi.Member); again != http.StatusConflict {
		t.Errorf("reissuing once accepted = %d, want 409", again)
	}
}

func reissueStatus(t *testing.T, admin *management.ClientWithResponses, m management.Member) int {
	t.Helper()
	return do(admin.ReissueInvitationWithResponse(t.Context(), m.Id.String())).StatusCode()
}

func TestEveryInvitationOperationOfMembersNeedsAnInstanceAdmin(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	target := invite(t, admin, "Target").Member
	caller := signedIn(t, srv, testkit.NewMember(t, pool))
	if res := do(caller.InviteMemberWithResponse(t.Context(), management.MemberInvite{
		Email: "x-" + target.Email, DisplayName: "X",
	})); res.StatusCode() != http.StatusForbidden {
		t.Errorf("a member invites = %d, want 403", res.StatusCode())
	}
	if status := reissueStatus(t, caller.ClientWithResponses, target); status != http.StatusForbidden {
		t.Errorf("a member reissues = %d, want 403", status)
	}
}

func TestThePerAddressThrottleCoversInvitationAcceptance(t *testing.T) {
	srv := testkit.StartServer(t, trustLoopback())
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	mi := invite(t, admin, "Throttled Joiner")
	b := fromAddress(t, srv, someAddress())
	for i := range access.SourceFailureLimit - 1 {
		if code, problem, _ := signInAs(t, b, "nobody-"+rand.Text()+"@example.com", "not-the-password-9"); code != http.StatusUnauthorized {
			t.Fatalf("wrong sign-in %d = %d %s", i, code, problem)
		}
	}
	if res := accept(t, b, "/invitations/"+strings.Repeat("a", 43), newPassword); res.StatusCode() != http.StatusGone {
		t.Fatalf("the hundredth failure, an unknown link = %d %s", res.StatusCode(), res.Body)
	}
	if code, problem, _ := signInAs(t, b, "nobody@example.com", "not-the-password-9"); code != http.StatusTooManyRequests || problem != "rate_limited" {
		t.Errorf("the next sign-in = %d %s, want 429 rate_limited", code, problem)
	}
	res := accept(t, b, mi.Invitation.Url, newPassword)
	assertProblem(t, "accepting a good link while throttled", res.StatusCode(), res.Body, http.StatusTooManyRequests, "rate_limited")
	if got := memberOf(t, admin, id.FromBytes[access.Member](mi.Member.Id)); got.Status != management.MemberStatusInvited {
		t.Errorf("member = %s, want still invited", got.Status)
	}
}

func TestAnAcceptedInvitationCountsNoFailureOfItsAddress(t *testing.T) {
	srv := testkit.StartServer(t, trustLoopback())
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	b := fromAddress(t, srv, someAddress())
	for range access.SourceFailureLimit - 1 {
		signInAs(t, b, "nobody-"+rand.Text()+"@example.com", "not-the-password-9")
	}
	mi := invite(t, admin, "Weak Then Strong")
	if res := accept(t, b, mi.Invitation.Url, "short"); res.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("a weak password = %d %s", res.StatusCode(), res.Body)
	}
	if res := accept(t, b, mi.Invitation.Url, newPassword); res.StatusCode() != http.StatusOK {
		t.Fatalf("accepting = %d %s", res.StatusCode(), res.Body)
	}
	if code, problem, _ := signInAs(t, b, "nobody@example.com", "not-the-password-9"); code != http.StatusUnauthorized {
		t.Errorf("the hundredth failure = %d %s, want 401: a link that opened an invitation guessed nothing", code, problem)
	}
}

func TestTheRetentionWorkerPurgesInvitationsLongPastUse(t *testing.T) {
	c := testkit.Clock()
	srv, pool := retentionServer(t, c)
	admin := testkit.Client(t, srv, testkit.Bootstrap(t, pool))

	accepted := invite(t, admin, "Accepted")
	c.Advance(time.Minute)
	acceptedLatest := reissue(t, admin, accepted.Member)
	if res := accept(t, testkit.NewBrowser(t, srv.URL), acceptedLatest, newPassword); res.StatusCode() != http.StatusOK {
		t.Fatalf("accept = %d %s", res.StatusCode(), res.Body)
	}
	expired := invite(t, admin, "Never Came")
	expiredLatest := reissue(t, admin, expired.Member)

	c.Advance(7*24*time.Hour + 30*24*time.Hour + 24*time.Hour - 2*24*time.Hour)
	recent := invite(t, admin, "Recently Replaced")
	reissue(t, admin, recent.Member)
	c.Advance(2 * 24 * time.Hour)
	purge(t, srv)

	store := pgaccess.NewStore(pool)
	kept := func(link string) bool {
		hash, ok := access.ParseInvitationSecret(secretOf(link))
		if !ok {
			t.Fatalf("link %q holds no secret", link)
		}
		_, _, err := store.InvitationByHash(t.Context(), hash)
		if err != nil && !errors.Is(err, appaccess.ErrNotFound) {
			t.Fatal(err)
		}
		return err == nil
	}
	for name, tc := range map[string]struct {
		link string
		kept bool
	}{
		"replaced before it was accepted, long ago": {accepted.Invitation.Url, false},
		"replaced long ago":                         {expired.Invitation.Url, false},
		"accepted long ago, the member's latest":    {acceptedLatest, true},
		"expired long ago, the member's latest":     {expiredLatest, true},
		"replaced two days ago":                     {recent.Invitation.Url, true},
	} {
		if got := kept(tc.link); got != tc.kept {
			t.Errorf("%s: kept = %t, want %t", name, got, tc.kept)
		}
	}
	if n := purged(t, srv, "invitations"); n != 2 {
		t.Errorf("purged invitations = %d, want 2", n)
	}
}
