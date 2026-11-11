package management_test

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

const resetPassword = "copper-meadow-lantern-5521"

// issueReset issues a password reset of m through admin and returns the
// answer; the secret is recorded for the audit guard.
func issueReset(t *testing.T, admin *management.ClientWithResponses, m access.MemberID) management.IssuedPasswordReset {
	t.Helper()
	res := do(admin.IssuePasswordResetWithResponse(t.Context(), m.String()))
	if res.JSON201 == nil {
		t.Fatalf("POST /members/%s/password-reset = %d: %s", m, res.StatusCode(), res.Body)
	}
	testkit.RecordSecret(secretOf(res.JSON201.Url))
	return *res.JSON201
}

// completeReset completes the password reset link opens through b, setting
// password.
func completeReset(t *testing.T, b *testkit.Browser, link, password string) *management.CompletePasswordResetResponse {
	t.Helper()
	testkit.RecordSecret(password)
	return do(b.CompletePasswordResetWithResponse(t.Context(), secretOf(link), management.PasswordResetCompletion{Password: password}))
}

// resetOpened returns what link opens, read by anyone.
func resetOpened(t *testing.T, srv *testkit.Server, link string) *management.GetPasswordResetResponse {
	t.Helper()
	return do(testkit.NewBrowser(t, srv.URL).GetPasswordResetWithResponse(t.Context(), secretOf(link)))
}

// wantResetGone fails unless link opens nothing and cannot be completed.
func wantResetGone(t *testing.T, srv *testkit.Server, link, why string) {
	t.Helper()
	got := resetOpened(t, srv, link)
	assertProblem(t, why+": GET", got.StatusCode(), got.Body, http.StatusGone, "reset_gone")
	done := completeReset(t, testkit.NewBrowser(t, srv.URL), link, "a-perfectly-fine-passphrase-7")
	assertProblem(t, why+": complete", done.StatusCode(), done.Body, http.StatusGone, "reset_gone")
}

// sessionIsDead fails unless b's session no longer authenticates.
func sessionIsDead(t *testing.T, b *testkit.Browser, why string) {
	t.Helper()
	res := do(b.GetMeWithResponse(t.Context()))
	if res.StatusCode() != http.StatusUnauthorized {
		t.Errorf("%s: GET /me = %d, want 401", why, res.StatusCode())
	}
}

func TestAnInstanceAdminIssuesAPasswordResetTheMemberCompletes(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, testkit.Pool(t), testkit.WithDisplayName("Grace Hopper"))

	issued := issueReset(t, admin, m.ID)
	if !regexp.MustCompile(`^` + regexp.QuoteMeta(srv.URL) + `/password-reset/[A-Za-z0-9_-]{43}$`).MatchString(issued.Url) {
		t.Errorf("link = %q, want the console's origin, /password-reset/ and 43 base64url characters", issued.Url)
	}
	if !issued.ExpiresAt.Equal(c.Now().Add(time.Hour)) {
		t.Errorf("expires at %v, want in an hour", issued.ExpiresAt)
	}

	got := resetOpened(t, srv, issued.Url)
	if got.JSON200 == nil {
		t.Fatalf("GET the password reset = %d: %s", got.StatusCode(), got.Body)
	}
	if want := m.Email[:1] + "***@example.com"; got.JSON200.Email != want || got.JSON200.DisplayName != "Grace Hopper" ||
		!got.JSON200.ExpiresAt.Equal(issued.ExpiresAt) {
		t.Errorf("password reset = %+v, want %s, the name and the expiry", got.JSON200, want)
	}

	b := testkit.NewBrowser(t, srv.URL)
	done := completeReset(t, b, issued.Url, resetPassword)
	if done.JSON200 == nil {
		t.Fatalf("complete = %d: %s", done.StatusCode(), done.Body)
	}
	if me := done.JSON200; me.Member == nil || me.Member.Email != m.Email || me.Member.Status != management.MemberStatusActive ||
		me.Credential.Method != management.CredentialMethodSession {
		t.Errorf("completed = %+v, want the member, active, signed in to a session", me)
	}
	if b.Cookie(management.SessionCookie) == "" {
		t.Error("completing set no session cookie")
	}
	if res := do(b.GetMeWithResponse(t.Context())); res.StatusCode() != http.StatusOK {
		t.Errorf("GET /me with the new session = %d, want 200", res.StatusCode())
	}

	// The new password signs in; the old one no longer does.
	if me := testkit.NewBrowser(t, srv.URL).SignIn(t, m.Email, resetPassword); me.Member == nil || me.Member.Email != m.Email {
		t.Errorf("sign-in with the new password = %+v", me)
	}
	if status, code, _ := signInAs(t, testkit.NewBrowser(t, srv.URL), m.Email, m.Password); status != http.StatusUnauthorized ||
		code != "invalid_credentials" {
		t.Errorf("sign-in with the old password = %d %s, want 401 invalid_credentials", status, code)
	}

	// The link is single use.
	wantResetGone(t, srv, issued.Url, "after it was used")

	for action, n := range map[string]int{"password.reset_issued": 1, "password.reset": 1, "auth.signed_in": 2} {
		if got := len(auditOf(t, srv, action, m.ID.String())); got != n {
			t.Errorf("%s entries = %d, want %d", action, got, n)
		}
	}
	for _, e := range auditOf(t, srv, "password.reset", m.ID.String()) {
		if e.Actor.MemberId == nil || e.Actor.MemberId.String() != m.ID.String() {
			t.Errorf("password.reset actor = %+v, want the member who completed it", e.Actor)
		}
		if after := e.After.(map[string]any); after["email"] != m.Email || after["sessions_revoked"] != float64(0) {
			t.Errorf("password.reset after = %v", after)
		}
	}
	if entries := auditOfQ(t, srv, secretOf(issued.Url)); len(entries) != 0 {
		t.Errorf("%d audit entries hold the link's secret, want none", len(entries))
	}
}

func TestCompletingAPasswordResetSignsOutEveryDeviceAndClearsALockout(t *testing.T) {
	srv := testkit.StartServer(t, trustLoopback())
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, testkit.Pool(t))
	phone, laptop := signedIn(t, srv, m), signedIn(t, srv, m)
	lockEmail(t, srv, m.Email)

	issued := issueReset(t, admin, m.ID)
	b := fromAddress(t, srv, someAddress())
	if done := completeReset(t, b, issued.Url, resetPassword); done.JSON200 == nil {
		t.Fatalf("complete = %d: %s", done.StatusCode(), done.Body)
	}

	sessionIsDead(t, phone, "the phone")
	sessionIsDead(t, laptop, "the laptop")
	if res := do(b.GetMeWithResponse(t.Context())); res.StatusCode() != http.StatusOK {
		t.Errorf("the session the reset opened: GET /me = %d, want 200", res.StatusCode())
	}
	for _, e := range auditOf(t, srv, "password.reset", m.ID.String()) {
		if after := e.After.(map[string]any); after["sessions_revoked"] != float64(2) {
			t.Errorf("password.reset after = %v, want two sessions revoked", after)
		}
	}

	// The lockout is gone: a wrong password is an ordinary first failure,
	// and the new password signs in from an address that was locked out.
	other := fromAddress(t, srv, someAddress())
	if status, code, _ := signInAs(t, other, m.Email, wrongPassword); status != http.StatusUnauthorized || code != "invalid_credentials" {
		t.Errorf("a wrong password after the reset = %d %s, want 401 invalid_credentials, not locked", status, code)
	}
	if status, code, _ := signInAs(t, other, m.Email, resetPassword); status != http.StatusOK {
		t.Errorf("the new password after the reset = %d %s, want 200", status, code)
	}
}

func TestCompletingAPasswordResetEndsEveryOtherOpenResetAndInvitation(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, pool)

	first, second, third := issueReset(t, admin, m.ID), issueReset(t, admin, m.ID), issueReset(t, admin, m.ID)
	// Issuing leaves the earlier ones open.
	for _, link := range []string{first.Url, second.Url, third.Url} {
		if got := resetOpened(t, srv, link); got.JSON200 == nil {
			t.Fatalf("an open reset before any completes = %d: %s", got.StatusCode(), got.Body)
		}
	}
	// An invitation the member still holds: the store is the only way to
	// see it, as no page opens an invitation of an active member.
	store := pgaccess.NewStore(pool)
	inv, _ := access.NewInvitation(id.New[access.Invitation](id.NewSource(testkit.Clock())), m.ID, testkit.Clock().Now(), rand.Reader)
	if err := store.CreateInvitation(t.Context(), inv); err != nil {
		t.Fatal(err)
	}

	if done := completeReset(t, testkit.NewBrowser(t, srv.URL), second.Url, resetPassword); done.JSON200 == nil {
		t.Fatalf("complete = %d: %s", done.StatusCode(), done.Body)
	}
	for name, link := range map[string]string{"the first": first.Url, "the second": second.Url, "the third": third.Url} {
		wantResetGone(t, srv, link, name)
	}
	got, _, err := store.InvitationByHash(t.Context(), inv.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConsumedAt == nil {
		t.Error("the member's invitation is still open after a password reset")
	}
}

func TestAPasswordResetExpiresAfterAnHour(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	issued := issueReset(t, admin, testkit.NewMember(t, testkit.Pool(t)).ID)

	c.Advance(time.Hour - time.Minute)
	if got := resetOpened(t, srv, issued.Url); got.JSON200 == nil {
		t.Fatalf("a minute before it expires = %d: %s", got.StatusCode(), got.Body)
	}
	c.Advance(time.Minute)
	wantResetGone(t, srv, issued.Url, "when it expires")
}

func TestAnUnknownPasswordResetSecretIsGone(t *testing.T) {
	srv := testkit.StartServer(t)
	never := rand.Text() + rand.Text()
	for _, s := range []string{never[:43], "not-base64url!", strings.Repeat("a", 43)} {
		wantResetGone(t, srv, "/password-reset/"+s, s)
	}
}

func TestCompletingAPasswordResetAppliesThePasswordPolicy(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, testkit.Pool(t), testkit.WithEmail("weak.reset@example.com"))
	issued := issueReset(t, admin, m.ID)
	b := testkit.NewBrowser(t, srv.URL)

	for name, password := range map[string]string{
		"too short":         "short",
		"too long":          strings.Repeat("a", 129),
		"holding the email": "prefix-weak.reset-suffix",
		"a common password": "administrator",
	} {
		res := completeReset(t, b, issued.Url, password)
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "weak_password")
	}
	if b.Cookie(management.SessionCookie) != "" {
		t.Error("a refused password opened a session")
	}
	// A refused password changes nothing and leaves the reset usable.
	if status, _, _ := signInAs(t, testkit.NewBrowser(t, srv.URL), m.Email, m.Password); status != http.StatusOK {
		t.Errorf("the old password after refused ones = %d, want 200", status)
	}
	if res := completeReset(t, b, issued.Url, resetPassword); res.JSON200 == nil {
		t.Fatalf("complete with a good password = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestIssuingAPasswordResetIsRefusedForAMemberWhoIsNotActive(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))

	invited := testkit.NewMember(t, pool, testkit.WithStatus(access.MemberInvited))
	res := do(admin.IssuePasswordResetWithResponse(t.Context(), invited.ID.String()))
	assertProblem(t, "an invited member", res.StatusCode(), res.Body, http.StatusConflict, "member_not_active")

	gone := testkit.NewMember(t, pool)
	if res := do(admin.DeactivateMemberWithResponse(t.Context(), gone.ID.String())); res.StatusCode() != http.StatusOK {
		t.Fatalf("deactivate = %d: %s", res.StatusCode(), res.Body)
	}
	res = do(admin.IssuePasswordResetWithResponse(t.Context(), gone.ID.String()))
	assertProblem(t, "a deactivated member", res.StatusCode(), res.Body, http.StatusConflict, "member_deactivated")

	res = do(admin.IssuePasswordResetWithResponse(t.Context(), id.New[access.Member](id.NewSource(testkit.Clock())).String()))
	assertProblem(t, "an unknown member", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")

	for _, m := range []testkit.Member{invited, gone} {
		if n := len(auditOf(t, srv, "password.reset_issued", m.ID.String())); n != 0 {
			t.Errorf("%d password.reset_issued entries for a refused member, want none", n)
		}
	}
}

func TestIssuingAPasswordResetNeedsAnInstanceAdmin(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	target := testkit.NewMember(t, pool)
	caller := signedIn(t, srv, testkit.NewMember(t, pool))
	res := do(caller.IssuePasswordResetWithResponse(t.Context(), target.ID.String()))
	if res.StatusCode() != http.StatusForbidden {
		t.Errorf("a member issues = %d, want 403: %s", res.StatusCode(), res.Body)
	}
	anonymous := do(testkit.NewBrowser(t, srv.URL).IssuePasswordResetWithResponse(t.Context(), target.ID.String()))
	if anonymous.StatusCode() != http.StatusUnauthorized {
		t.Errorf("no credential issues = %d, want 401", anonymous.StatusCode())
	}
}

func TestAPasswordChangeEndsTheMembersOpenPasswordResets(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, testkit.Pool(t))
	issued := issueReset(t, admin, m.ID)

	b := signedIn(t, srv, m)
	if res := changePassword(t, b, m.Password, changedPassword); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("change the password = %d: %s", res.StatusCode(), res.Body)
	}
	wantResetGone(t, srv, issued.Url, "after a password change")
}

func TestADeactivationEndsTheMembersOpenPasswordResets(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, testkit.Pool(t))
	issued := issueReset(t, admin, m.ID)

	if res := do(admin.DeactivateMemberWithResponse(t.Context(), m.ID.String())); res.StatusCode() != http.StatusOK {
		t.Fatalf("deactivate = %d: %s", res.StatusCode(), res.Body)
	}
	wantResetGone(t, srv, issued.Url, "after a deactivation")

	// A reactivated member does not get the old link back.
	if res := do(admin.ReactivateMemberWithResponse(t.Context(), m.ID.String())); res.StatusCode() != http.StatusOK {
		t.Fatalf("reactivate = %d: %s", res.StatusCode(), res.Body)
	}
	wantResetGone(t, srv, issued.Url, "after a reactivation")
}

func TestCompletingAPasswordResetIsRefusedAsAForm(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	issued := issueReset(t, admin, testkit.NewMember(t, testkit.Pool(t)).ID)
	b := testkit.NewBrowser(t, srv.URL)
	b.Header.Set("Sec-Fetch-Site", "cross-site")
	b.Header.Set("Origin", "https://evil.example")
	for _, contentType := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
		res, body := b.Do(t, http.MethodPost, management.BaseURL+"/auth/password-reset/"+secretOf(issued.Url), contentType,
			`{"password":"`+resetPassword+`"}`)
		if res.StatusCode != http.StatusUnsupportedMediaType || !strings.Contains(body, "unsupported_media_type") || len(res.Cookies()) != 0 {
			t.Errorf("completing as %q = %d %s, want 415 unsupported_media_type and no cookie", contentType, res.StatusCode, body)
		}
	}
	if got := resetOpened(t, srv, issued.Url); got.StatusCode() != http.StatusOK {
		t.Errorf("the link after refused forms = %d, want still open", got.StatusCode())
	}
}

func TestAPasswordResetBodyOver8KiBIsRefusedBeforeAnything(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	issued := issueReset(t, admin, testkit.NewMember(t, testkit.Pool(t)).ID)
	body := `{"password":"` + strings.Repeat("x", 8<<10) + `"}`
	res, err := testkit.NewBrowser(t, srv.URL).CompletePasswordResetWithBodyWithResponse(t.Context(), secretOf(issued.Url),
		"application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if p := res.ApplicationproblemJSON413; p == nil || p.Code != "request_too_large" {
		t.Errorf("a 8 KiB password: %d %s, want 413 request_too_large", res.StatusCode(), res.Body)
	}
}

func TestAPasswordResetSecretNeverReachesALogLine(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	issued := issueReset(t, admin, testkit.NewMember(t, testkit.Pool(t)).ID)
	secret := secretOf(issued.Url)
	resetOpened(t, srv, issued.Url)
	b := testkit.NewBrowser(t, srv.URL)
	b.Do(t, http.MethodPut, management.BaseURL+"/auth/password-reset/"+secret, "application/json", `{}`)
	b.Do(t, http.MethodGet, strings.TrimPrefix(issued.Url, srv.URL), "", "") // the link itself, as the member opens it
	completeReset(t, b, issued.Url, resetPassword)

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
	if !paths[management.BaseURL+"/auth/password-reset/{secret}"] || !paths["/password-reset/{secret}"] {
		t.Errorf("logged paths = %v, want the route's pattern in place of the secret", paths)
	}
}

func TestThePerAddressThrottleCoversPasswordResetCompletion(t *testing.T) {
	srv := testkit.StartServer(t, trustLoopback())
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	m := testkit.NewMember(t, testkit.Pool(t))
	issued := issueReset(t, admin, m.ID)
	b := fromAddress(t, srv, someAddress())
	for i := range access.SourceFailureLimit - 1 {
		if code, problem, _ := signInAs(t, b, "nobody-"+rand.Text()+"@example.com", wrongPassword); code != http.StatusUnauthorized {
			t.Fatalf("wrong sign-in %d = %d %s", i, code, problem)
		}
	}
	if res := completeReset(t, b, "/password-reset/"+strings.Repeat("a", 43), resetPassword); res.StatusCode() != http.StatusGone {
		t.Fatalf("the hundredth failure, an unknown link = %d %s", res.StatusCode(), res.Body)
	}
	if code, problem, _ := signInAs(t, b, "nobody@example.com", wrongPassword); code != http.StatusTooManyRequests || problem != "rate_limited" {
		t.Errorf("the next sign-in = %d %s, want 429 rate_limited", code, problem)
	}
	res := completeReset(t, b, issued.Url, resetPassword)
	assertProblem(t, "completing a good link while throttled", res.StatusCode(), res.Body, http.StatusTooManyRequests, "rate_limited")
	if got := resetOpened(t, srv, issued.Url); got.StatusCode() != http.StatusOK {
		t.Errorf("the link after a throttled completion = %d, want still open", got.StatusCode())
	}
}

func TestACompletedPasswordResetCountsNoFailureOfItsAddress(t *testing.T) {
	srv := testkit.StartServer(t, trustLoopback())
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	issued := issueReset(t, admin, testkit.NewMember(t, testkit.Pool(t)).ID)
	b := fromAddress(t, srv, someAddress())
	for range access.SourceFailureLimit - 1 {
		signInAs(t, b, "nobody-"+rand.Text()+"@example.com", wrongPassword)
	}
	if res := completeReset(t, b, issued.Url, "short"); res.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("a weak password = %d %s", res.StatusCode(), res.Body)
	}
	if res := completeReset(t, b, issued.Url, resetPassword); res.StatusCode() != http.StatusOK {
		t.Fatalf("completing = %d %s", res.StatusCode(), res.Body)
	}
	if code, problem, _ := signInAs(t, b, "nobody@example.com", wrongPassword); code != http.StatusUnauthorized {
		t.Errorf("the hundredth failure = %d %s, want 401: a link that opened a reset guessed nothing", code, problem)
	}
}

func TestOnlyOneOfConcurrentCompletionsSucceeds(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	issued := issueReset(t, admin, testkit.NewMember(t, testkit.Pool(t)).ID)

	var wg sync.WaitGroup
	statuses := make([]int, 4)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := testkit.NewBrowser(t, srv.URL).CompletePasswordResetWithResponse(t.Context(), secretOf(issued.Url),
				management.PasswordResetCompletion{Password: resetPassword + "-" + string(rune('a'+i))})
			if err != nil {
				t.Error(err)
				return
			}
			statuses[i] = res.StatusCode()
		}()
	}
	wg.Wait()
	ok, gone := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusGone:
			gone++
		}
	}
	if ok != 1 || gone != len(statuses)-1 {
		t.Errorf("statuses = %v, want one 200 and the rest 410", statuses)
	}
}

func TestTheRetentionWorkerPurgesPasswordResetsThirtyDaysAfterUseOrExpiry(t *testing.T) {
	c := testkit.Clock()
	srv, pool := retentionServer(t, c)
	admin := testkit.Client(t, srv, testkit.Bootstrap(t, pool))
	store := pgaccess.NewStore(pool)
	kept := func(link string) bool {
		hash, ok := access.ParsePasswordResetSecret(secretOf(link))
		if !ok {
			t.Fatalf("link %q holds no secret", link)
		}
		_, _, err := store.PasswordResetByHash(t.Context(), hash)
		if err != nil && !errors.Is(err, appaccess.ErrNotFound) {
			t.Fatal(err)
		}
		return err == nil
	}

	used := issueReset(t, admin, testkit.NewMember(t, pool).ID)
	if res := completeReset(t, testkit.NewBrowser(t, srv.URL), used.Url, resetPassword); res.StatusCode() != http.StatusOK {
		t.Fatalf("complete = %d %s", res.StatusCode(), res.Body)
	}
	expired := issueReset(t, admin, testkit.NewMember(t, pool).ID)

	// A day short of thirty after the first two ended: nothing is purged.
	c.Advance(29 * 24 * time.Hour)
	recent := issueReset(t, admin, testkit.NewMember(t, pool).ID)
	purge(t, srv)
	if n := purged(t, srv, "password_resets"); n != 0 {
		t.Fatalf("purged password resets = %d a day short of the retention, want 0", n)
	}

	// Past it, the used one and the expired ones go; the recent stays.
	c.Advance(2 * 24 * time.Hour)
	purge(t, srv)
	for name, tc := range map[string]struct {
		link string
		kept bool
	}{
		"used thirty days ago":    {used.Url, false},
		"expired thirty days ago": {expired.Url, false},
		"expired two days ago":    {recent.Url, true},
	} {
		if got := kept(tc.link); got != tc.kept {
			t.Errorf("%s: kept = %t, want %t", name, got, tc.kept)
		}
	}
	if n := purged(t, srv, "password_resets"); n != 2 {
		t.Errorf("purged password resets = %d, want 2", n)
	}
}

// Asking for a password reset counts against the address's throttle before it
// is answered, whether or not the email exists, so the 429 tells nothing about
// which addresses are members.
func TestAskingForAPasswordResetIsThrottledPerAddress(t *testing.T) {
	srv := testkit.StartServer(t, trustLoopback())
	b := fromAddress(t, srv, someAddress())
	body := func(email string) (int, []byte) {
		res := do(b.RequestPasswordResetWithResponse(t.Context(), management.PasswordResetRequest{Email: email}))
		return res.StatusCode(), res.Body
	}
	for i := range access.SourceFailureLimit {
		if code, out := body("nobody-" + rand.Text() + "@example.com"); code != http.StatusAccepted {
			t.Fatalf("request %d = %d %s, want 202", i+1, code, out)
		}
	}
	member := testkit.NewMember(t, testkit.Pool(t))
	for name, email := range map[string]string{"an unknown email": "nobody@example.com", "a member's email": member.Email} {
		code, out := body(email)
		assertProblem(t, name+" past the throttle", code, out, http.StatusTooManyRequests, "rate_limited")
	}
	if code, problem, _ := signInAs(t, b, "nobody@example.com", wrongPassword); code != http.StatusTooManyRequests || problem != "rate_limited" {
		t.Errorf("sign-in from the same address = %d %s, want 429 rate_limited: the throttle is shared", code, problem)
	}
}
