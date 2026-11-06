package management_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

const (
	resetLinkPrefix     = smtpBaseURL + "/password-reset/"
	mailedResetPassword = "meadow-copper-lantern-9031"
)

// resetInstance is an instance whose SMTP server is a Mailpit, with an
// anonymous browser asking for resets.
type resetInstance struct {
	smtpInstance
	mp    *testkit.Mailpit
	clock *clock.Fake
	anon  *testkit.Browser
}

func startResetInstance(t *testing.T, mode testkit.MailpitMode, serverOpts ...testkit.ServerOption) resetInstance {
	t.Helper()
	mp := testkit.StartMailpit(t, mode)
	c := testkit.Clock()
	in := startSMTPInstance(t, append([]testkit.ServerOption{testkit.WithClock(c)}, serverOpts...))
	putSMTP(t, in.admin, settingsFor(mp))
	return resetInstance{smtpInstance: in, mp: mp, clock: c, anon: testkit.NewBrowser(t, in.srv.URL)}
}

// auditOf returns the audit entries of action about the entity, read by the
// instance's own admin: the instance has a database of its own.
func (in smtpInstance) auditOf(t *testing.T, action, entityID string) []management.AuditEntry {
	t.Helper()
	res := do(in.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action, EntityId: &entityID}))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// auditOfQ returns the audit entries that mention q.
func (in smtpInstance) auditOfQ(t *testing.T, q string) []management.AuditEntry {
	t.Helper()
	res := do(in.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Q: &q}))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// ask asks for a password reset of email and fails unless the answer is 202.
func (in resetInstance) ask(t *testing.T, email string) {
	t.Helper()
	res := do(in.anon.RequestPasswordResetWithResponse(t.Context(), management.PasswordResetRequest{Email: email}))
	if res.StatusCode() != http.StatusAccepted {
		t.Fatalf("POST /auth/password-reset for %s = %d: %s", email, res.StatusCode(), res.Body)
	}
	if len(res.Body) != 0 {
		t.Errorf("the answer carries a body: %q", res.Body)
	}
}

// linkIn returns the password reset link the mail holds, in its text, and
// fails unless its HTML holds the same.
func linkIn(t *testing.T, m testkit.ReceivedMail) string {
	t.Helper()
	i := strings.Index(m.Text, resetLinkPrefix)
	if i < 0 {
		t.Fatalf("the mail %q holds no link under %s: %s", m.Subject, resetLinkPrefix, m.Text)
	}
	link := m.Text[i:]
	if end := strings.IndexAny(link, " \r\n"); end >= 0 {
		link = link[:end]
	}
	if len(link) != len(resetLinkPrefix)+43 {
		t.Fatalf("link %q, want the console's origin, /password-reset/ and 43 base64url characters", link)
	}
	if !strings.Contains(m.HTML, `href="`+link+`"`) {
		t.Errorf("the HTML does not link to %s: %s", link, m.HTML)
	}
	testkit.RecordSecret(secretOf(link))
	return link
}

// settle asks for a reset of a member of its own who does get one and
// awaits that mail: the replica has started the requests asked for before it,
// so, once the mail is in, a stray mail of theirs would be too.
func (in resetInstance) settle(t *testing.T) {
	t.Helper()
	m := testkit.NewMember(t, in.pool)
	in.ask(t, m.Email)
	in.mp.AwaitMails(t, m.Email, 1)
}

func TestAMemberAsksForAPasswordResetByMailAndCompletesIt(t *testing.T) {
	t.Parallel()
	in := startResetInstance(t, testkit.MailpitOpen)
	m := testkit.NewMember(t, in.pool, testkit.WithDisplayName("Grace Hopper"))
	b := testkit.NewBrowser(t, in.srv.URL)
	b.SignIn(t, m.Email, m.Password)

	in.ask(t, strings.ToUpper(m.Email))
	got := in.mp.AwaitMails(t, m.Email, 1)
	if len(got) != 1 {
		t.Fatalf("%d mails, want 1", len(got))
	}
	mail := got[0]
	if mail.Subject != "Reset your Flagnetic password" || !strings.Contains(mail.Text, "Grace Hopper") ||
		!strings.Contains(mail.Text, in.clock.Now().Add(time.Hour).UTC().Format("2006-01-02 15:04")) {
		t.Errorf("mail = %q %q, want the English reset naming the member and when it expires", mail.Subject, mail.Text)
	}
	link := linkIn(t, mail)

	opened := resetOpened(t, in.srv, link)
	if opened.JSON200 == nil || opened.JSON200.DisplayName != "Grace Hopper" {
		t.Fatalf("GET the mailed reset = %d: %s", opened.StatusCode(), opened.Body)
	}
	done := completeReset(t, testkit.NewBrowser(t, in.srv.URL), link, mailedResetPassword)
	if done.JSON200 == nil || done.JSON200.Member == nil || done.JSON200.Member.Email != m.Email {
		t.Fatalf("completing the mailed reset = %d: %s", done.StatusCode(), done.Body)
	}
	sessionIsDead(t, b, "the session open before the reset")
	if me := testkit.NewBrowser(t, in.srv.URL).SignIn(t, m.Email, mailedResetPassword); me.Member == nil {
		t.Error("the new password does not sign in")
	}
	wantResetGone(t, in.srv, link, "after it was used")

	entries := in.auditOf(t, "password.reset_requested", m.ID.String())
	if len(entries) != 1 {
		t.Fatalf("password.reset_requested entries = %d, want 1", len(entries))
	}
	if a := entries[0].Actor; a.Kind != management.ActorKindMember || a.Id != m.ID.String() {
		t.Errorf("actor = %+v, want the member who asked", a)
	}
	// No admin issued it: the member did.
	if n := len(in.auditOf(t, "password.reset_issued", m.ID.String())); n != 0 {
		t.Errorf("password.reset_issued entries = %d, want none for a reset the member asked for", n)
	}
}

func TestAResetIsMailedInTheMembersLanguage(t *testing.T) {
	t.Parallel()
	in := startResetInstance(t, testkit.MailpitOpen, testkit.WithDefaultLocale("ru"))
	// The member chose English, though the instance writes Russian.
	english := testkit.NewMember(t, in.pool, testkit.WithLocale("en"))
	russian := testkit.NewMember(t, in.pool, testkit.WithLocale("ru"))
	followsInstance := testkit.NewMember(t, in.pool)
	for _, m := range []testkit.Member{english, russian, followsInstance} {
		in.ask(t, m.Email)
	}
	for m, subject := range map[testkit.Member]string{
		english: "Reset your Flagnetic password", russian: "Сброс пароля Flagnetic", followsInstance: "Сброс пароля Flagnetic",
	} {
		got := in.mp.AwaitMails(t, m.Email, 1)
		if got[0].Subject != subject {
			t.Errorf("subject for %s = %q, want %q", m.Email, got[0].Subject, subject)
		}
		linkIn(t, got[0])
	}
}

func TestNothingIsMailedToAnEmailThatHasNoResetToAskFor(t *testing.T) {
	t.Parallel()
	in := startResetInstance(t, testkit.MailpitOpen)
	deactivated := testkit.NewMember(t, in.pool, testkit.WithStatus(access.MemberDeactivated))
	invited := testkit.NewMember(t, in.pool, testkit.WithStatus(access.MemberInvited))
	passwordless := testkit.NewPasswordlessMember(t, in.pool)
	unknown := "nobody-" + testkit.NewMember(t, in.pool).Email

	for _, email := range []string{unknown, invited.Email, deactivated.Email, passwordless.Email, "", "not an email"} {
		in.ask(t, email)
	}
	in.settle(t)

	for what, m := range map[string]string{
		"an unknown email": unknown, "an invited member": invited.Email, "a deactivated member": deactivated.Email,
		"a member with no password": passwordless.Email,
	} {
		if got := in.mp.MailsTo(t, m); len(got) != 0 {
			t.Errorf("%s: %d mails, want none", what, len(got))
		}
	}
	for _, m := range []testkit.Member{invited, deactivated, passwordless} {
		if n := len(in.auditOf(t, "password.reset_requested", m.ID.String())); n != 0 {
			t.Errorf("%s: password.reset_requested entries = %d, want none", m.Email, n)
		}
	}
	// None of them can complete anything either: nothing was issued.
	if n := len(in.auditOfQ(t, unknown)); n != 0 {
		t.Errorf("%d audit entries mention the unknown email", n)
	}
}

func TestNothingIsMailedWhileSelfServiceResetIsOff(t *testing.T) {
	t.Parallel()
	in := startResetInstance(t, testkit.MailpitOpen)
	m := testkit.NewMember(t, in.pool)
	settings := do(in.admin.GetAuthSettingsWithResponse(t.Context())).JSON200
	if settings == nil || !settings.SelfServicePasswordReset {
		t.Fatalf("auth settings = %+v, want self-service reset on in a fresh instance", settings)
	}
	off := *settings
	off.SelfServicePasswordReset = false
	if res := do(in.admin.UpdateAuthSettingsWithResponse(t.Context(), off)); res.JSON200 == nil || res.JSON200.SelfServicePasswordReset {
		t.Fatalf("turning it off = %d: %s", res.StatusCode(), res.Body)
	}
	if got := do(in.anon.GetAuthMethodsWithResponse(t.Context())).JSON200; got == nil || got.PasswordResetAvailable {
		t.Errorf("auth methods = %+v, want no reset available with it off", got)
	}

	in.ask(t, m.Email)
	in.ask(t, m.Email)
	// Turned back on, the same instance mails, and the request made while it
	// was off left no trace that would make this one wait.
	on := off
	on.SelfServicePasswordReset = true
	if res := do(in.admin.UpdateAuthSettingsWithResponse(t.Context(), on)); res.JSON200 == nil || !res.JSON200.SelfServicePasswordReset {
		t.Fatalf("turning it on = %d: %s", res.StatusCode(), res.Body)
	}
	if got := do(in.anon.GetAuthMethodsWithResponse(t.Context())).JSON200; got == nil || !got.PasswordResetAvailable {
		t.Errorf("auth methods = %+v, want a reset available again", got)
	}
	other := testkit.NewMember(t, in.pool)
	in.ask(t, other.Email)
	in.mp.AwaitMails(t, other.Email, 1)
	if got := in.mp.MailsTo(t, m.Email); len(got) != 0 {
		t.Errorf("%d mails sent while self-service reset was off, want none", len(got))
	}
	in.ask(t, m.Email)
	in.mp.AwaitMails(t, m.Email, 1)
}

func TestNothingIsMailedWhileTheInstanceHasNoSMTPServer(t *testing.T) {
	t.Parallel()
	mp := testkit.StartMailpit(t, testkit.MailpitOpen)
	in := startSMTPInstance(t, nil)
	anon := testkit.NewBrowser(t, in.srv.URL)
	m := testkit.NewMember(t, in.pool)

	if got := do(anon.GetAuthMethodsWithResponse(t.Context())).JSON200; got == nil || got.PasswordResetAvailable {
		t.Errorf("auth methods without SMTP = %+v, want no reset available", got)
	}
	res := do(anon.RequestPasswordResetWithResponse(t.Context(), management.PasswordResetRequest{Email: m.Email}))
	if res.StatusCode() != http.StatusAccepted {
		t.Fatalf("POST /auth/password-reset = %d: %s", res.StatusCode(), res.Body)
	}

	// Once SMTP is configured the request that went unanswered is no reason
	// to wait: the member is mailed at once.
	putSMTP(t, in.admin, settingsFor(mp))
	if got := do(anon.GetAuthMethodsWithResponse(t.Context())).JSON200; got == nil || !got.PasswordResetAvailable {
		t.Errorf("auth methods with SMTP = %+v, want a reset available", got)
	}
	other := testkit.NewMember(t, in.pool)
	do(anon.RequestPasswordResetWithResponse(t.Context(), management.PasswordResetRequest{Email: other.Email}))
	mp.AwaitMails(t, other.Email, 1)
	if got := mp.MailsTo(t, m.Email); len(got) != 0 {
		t.Errorf("%d mails sent while there was no SMTP server, want none", len(got))
	}
	if n := len(in.auditOf(t, "password.reset_requested", m.ID.String())); n != 0 {
		t.Errorf("password.reset_requested entries = %d, want none", n)
	}
}

func TestAMemberIsMailedOneResetEveryFiveMinutes(t *testing.T) {
	t.Parallel()
	in := startResetInstance(t, testkit.MailpitOpen)
	m := testkit.NewMember(t, in.pool)

	in.ask(t, m.Email)
	first := in.mp.AwaitMails(t, m.Email, 1)
	in.clock.Advance(access.PasswordResetInterval - time.Second)
	in.ask(t, m.Email)
	in.settle(t)
	if got := in.mp.MailsTo(t, m.Email); len(got) != 1 {
		t.Fatalf("%d mails within the interval, want 1", len(got))
	}

	in.clock.Advance(time.Second)
	in.ask(t, m.Email)
	got := in.mp.AwaitMails(t, m.Email, 2)
	if linkIn(t, got[0]) == linkIn(t, got[1]) || got[0].ID != first[0].ID {
		t.Error("the second mail repeats the first's link")
	}
	if n := len(in.auditOf(t, "password.reset_requested", m.ID.String())); n != 2 {
		t.Errorf("password.reset_requested entries = %d, want 2", n)
	}
	// Both links work: an earlier reset stays open until one is completed.
	if done := completeReset(t, testkit.NewBrowser(t, in.srv.URL), linkIn(t, got[0]), mailedResetPassword); done.JSON200 == nil {
		t.Errorf("completing the first = %d: %s", done.StatusCode(), done.Body)
	}
	wantResetGone(t, in.srv, linkIn(t, got[1]), "after the other was completed")
}

func TestRequestsMadeAtOnceMailOneReset(t *testing.T) {
	t.Parallel()
	in := startResetInstance(t, testkit.MailpitOpen)
	m := testkit.NewMember(t, in.pool)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { in.ask(t, m.Email) })
	}
	wg.Wait()
	in.mp.AwaitMails(t, m.Email, 1)
	in.settle(t)
	if got := in.mp.MailsTo(t, m.Email); len(got) != 1 {
		t.Errorf("%d mails for 8 requests at once, want 1", len(got))
	}
	if n := len(in.auditOf(t, "password.reset_requested", m.ID.String())); n != 1 {
		t.Errorf("password.reset_requested entries = %d, want 1", n)
	}
}

func TestAResetAnInstanceAdminIssuedDoesNotDelayOneTheMemberAsksFor(t *testing.T) {
	t.Parallel()
	in := startResetInstance(t, testkit.MailpitOpen)
	m := testkit.NewMember(t, in.pool)
	adminLink := issueReset(t, in.admin, m.ID)

	in.ask(t, m.Email)
	linkIn(t, in.mp.AwaitMails(t, m.Email, 1)[0])
	// The admin's link was never mailed, and stays open beside the mailed one.
	if done := completeReset(t, testkit.NewBrowser(t, in.srv.URL), adminLink.Url, mailedResetPassword); done.JSON200 == nil {
		t.Errorf("completing the admin's link = %d: %s", done.StatusCode(), done.Body)
	}
}

func TestAResetIsIssuedEvenWhenItsMailCannotBeSent(t *testing.T) {
	t.Parallel()
	// The picky server refuses every recipient outside its one domain.
	in := startResetInstance(t, testkit.MailpitPicky)
	m := testkit.NewMember(t, in.pool)

	in.ask(t, m.Email)
	series := `flagnetic_mail_sent_total{kind="password_reset",result="rejected"}`
	await(t, "the refused mail to be counted", func() bool { return testkit.Metric(t, in.srv.Scrape(t), series) >= 1 })
	// The answer was 202 whatever the relay said, and the attempt counts: a
	// request a minute later issues nothing, so a failing relay is not asked
	// again for every request.
	in.clock.Advance(time.Minute)
	in.ask(t, m.Email)
	in.ask(t, testkit.NewMember(t, in.pool, testkit.WithEmail("someone@"+testkit.MailpitAllowedDomain)).Email)
	await(t, "the accepted mail to be counted", func() bool {
		return testkit.Metric(t, in.srv.Scrape(t), `flagnetic_mail_sent_total{kind="password_reset",result="sent"}`) >= 1
	})
	if got := testkit.Metric(t, in.srv.Scrape(t), series); got != 1 {
		t.Errorf("%s = %v, want 1: the second request must issue nothing", series, got)
	}
	if n := len(in.auditOf(t, "password.reset_requested", m.ID.String())); n != 1 {
		t.Errorf("password.reset_requested entries = %d, want 1", n)
	}
}

func TestAFailedResetMailIsLoggedWithoutItsLink(t *testing.T) {
	t.Parallel()
	logs := testkit.NewLogSink()
	in := startResetInstance(t, testkit.MailpitPicky, testkit.WithEdge(func(o *flaghttp.Options) { o.Logger = logs.Logger() }))
	m := testkit.NewMember(t, in.pool)

	res := do(in.anon.RequestPasswordResetWithResponse(t.Context(), management.PasswordResetRequest{Email: m.Email}))
	if res.StatusCode() != http.StatusAccepted {
		t.Fatalf("POST /auth/password-reset = %d: %s, want 202 whatever the mail server says", res.StatusCode(), res.Body)
	}
	await(t, "the failed mail to be logged", func() bool { return len(logs.Find("mail not sent")) > 0 })
	rec := logs.Find("mail not sent")[0]
	if rec.Attrs["kind"] != "password_reset" || rec.Attrs["failure"] != "rejected" ||
		rec.Attrs["request_id"] != res.HTTPResponse.Header.Get("X-Request-ID") {
		t.Errorf("log record = %+v, want the kind, the failure class and the request ID", rec.Attrs)
	}
	for _, r := range logs.Records() {
		for k, v := range r.Attrs {
			if strings.Contains(r.Message+k+fmt.Sprint(v), "/password-reset/") {
				t.Errorf("a log record carries the reset link: %+v", r)
			}
		}
	}
}

func TestAMailedResetExpiresAfterAnHour(t *testing.T) {
	t.Parallel()
	in := startResetInstance(t, testkit.MailpitOpen)
	m := testkit.NewMember(t, in.pool)
	in.ask(t, m.Email)
	link := linkIn(t, in.mp.AwaitMails(t, m.Email, 1)[0])

	in.clock.Advance(time.Hour - time.Minute)
	if got := resetOpened(t, in.srv, link); got.StatusCode() != http.StatusOK {
		t.Errorf("a minute before it expires: GET = %d", got.StatusCode())
	}
	in.clock.Advance(2 * time.Minute)
	wantResetGone(t, in.srv, link, "after an hour")
}

func await(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !done(); time.Sleep(25 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestAskingForAPasswordResetTakesOnlySmallJSON(t *testing.T) {
	t.Parallel()
	srv := testkit.StartServer(t)
	post := func(contentType string, body []byte) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+management.BaseURL+"/auth/password-reset", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", contentType)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		return res
	}
	if res := post("application/json", []byte(`{"email":"someone@example.com"}`)); res.StatusCode != http.StatusAccepted {
		t.Errorf("JSON = %d, want 202", res.StatusCode)
	}
	for name, tc := range map[string]struct {
		contentType string
		body        []byte
		want        int
	}{
		"a form":       {"application/x-www-form-urlencoded", []byte("email=someone%40example.com"), http.StatusUnsupportedMediaType},
		"plain text":   {"text/plain", []byte(`{"email":"someone@example.com"}`), http.StatusUnsupportedMediaType},
		"not an email": {"application/json", []byte(`{"email":7}`), http.StatusBadRequest},
		"too large":    {"application/json", []byte(`{"email":"` + strings.Repeat("a", 8<<10) + `@example.com"}`), http.StatusRequestEntityTooLarge},
	} {
		if res := post(tc.contentType, tc.body); res.StatusCode != tc.want {
			t.Errorf("%s = %d, want %d", name, res.StatusCode, tc.want)
		}
	}
}

func TestTheAuthSettingsCarrySelfServicePasswordResetAndAuditIt(t *testing.T) {
	t.Parallel()
	in := startSMTPInstance(t, nil)
	current := do(in.admin.GetAuthSettingsWithResponse(t.Context())).JSON200
	if current == nil || !current.SelfServicePasswordReset {
		t.Fatalf("settings of a fresh instance = %+v, want self-service reset on", current)
	}
	off := *current
	off.SelfServicePasswordReset = false
	if res := do(in.admin.UpdateAuthSettingsWithResponse(t.Context(), off)); res.JSON200 == nil || res.JSON200.SelfServicePasswordReset {
		t.Fatalf("PUT = %d: %s", res.StatusCode(), res.Body)
	}
	if got := do(in.admin.GetAuthSettingsWithResponse(t.Context())).JSON200; got == nil || got.SelfServicePasswordReset {
		t.Errorf("settings after turning it off = %+v", got)
	}
	entries := smtpAudit(t, in.admin, "auth_settings.updated")
	if len(entries) != 1 {
		t.Fatalf("auth_settings.updated entries = %d, want 1", len(entries))
	}
	before, _ := json.Marshal(entries[0].Before)
	after, _ := json.Marshal(entries[0].After)
	if !strings.Contains(string(before), `"self_service_password_reset":true`) ||
		!strings.Contains(string(after), `"self_service_password_reset":false`) {
		t.Errorf("auth_settings.updated before %s, after %s; want the setting on, then off", before, after)
	}
}
