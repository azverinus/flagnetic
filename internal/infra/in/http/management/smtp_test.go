package management_test

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

const smtpBaseURL = "https://flags.example.com"

// smtpInstance serves an instance of its own, so the SMTP settings it holds
// are no other test's, with an instance admin whose address is their own:
// the mail sent to them is theirs alone.
type smtpInstance struct {
	srv   *testkit.Server
	pool  *pgxpool.Pool
	admin *management.ClientWithResponses
	// member is the instance admin the client acts as.
	member testkit.Member
}

func startSMTPInstance(t *testing.T, serverOpts []testkit.ServerOption, memberOpts ...testkit.MemberOption) smtpInstance {
	t.Helper()
	pool := testkit.FreshPool(t)
	opts := append([]testkit.ServerOption{
		testkit.WithPool(pool), testkit.WithBaseURL(smtpBaseURL), testkit.WithMailRoots(testkit.MailpitRoots()),
		testkit.WithClock(testkit.Clock()),
	}, serverOpts...)
	srv := testkit.StartServer(t, opts...)
	member, token := testkit.NewInstanceAdmin(t, pool, memberOpts...)
	return smtpInstance{srv: srv, pool: pool, admin: testkit.Client(t, srv, token), member: member}
}

// settingsFor are the SMTP settings that reach m, with its credentials when it
// asks for them.
func settingsFor(m *testkit.Mailpit) management.SmtpSettingsUpdate {
	s := management.SmtpSettingsUpdate{
		Host: m.Host, Port: m.Port, Security: management.SmtpSecurity(m.Security()), From: "Flagnetic <flags@example.com>",
	}
	if m.Credentials() {
		s.Username, s.Password = new(testkit.MailpitUser), new(testkit.MailpitPassword)
	}
	return s
}

func putSMTP(t *testing.T, c *management.ClientWithResponses, body management.SmtpSettingsUpdate) management.SmtpSettings {
	t.Helper()
	res, err := c.UpdateMailSettingsWithResponse(t.Context(), body)
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON200 == nil {
		t.Fatalf("PUT /instance/smtp = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func smtpAudit(t *testing.T, c *management.ClientWithResponses, action string) []management.AuditEntry {
	t.Helper()
	res, err := c.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit = %v, %v", res.StatusCode(), err)
	}
	return res.JSON200.Items
}

func TestSMTPNeedsABaseURL(t *testing.T) {
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool))
	admin := testkit.Client(t, srv, testkit.Bootstrap(t, pool))

	res, err := admin.UpdateMailSettingsWithResponse(t.Context(), management.SmtpSettingsUpdate{
		Host: "smtp.example.com", Port: 587, Security: management.SmtpSecurityStarttls, From: "flags@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "SMTP without a base URL", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "base_url_required")
	get, err := admin.GetMailSettingsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "SMTP after the refusal", get.StatusCode(), get.Body, http.StatusNotFound, "not_found")
}

func TestSMTPSettingsAreWrittenReadAndRemoved(t *testing.T) {
	in := startSMTPInstance(t, nil)
	const password = "relay-password-4417"

	unset, err := in.admin.GetMailSettingsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "SMTP on a fresh instance", unset.StatusCode(), unset.Body, http.StatusNotFound, "not_found")
	gone, err := in.admin.DeleteMailSettingsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "removing what is not there", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")

	body := management.SmtpSettingsUpdate{
		Host: "smtp.example.com", Port: 587, Security: management.SmtpSecurityStarttls, Username: new("relay"), Password: new(password),
		From: "Flagnetic <flags@example.com>",
	}
	want := management.SmtpSettings{
		Host: "smtp.example.com", Port: 587, Security: management.SmtpSecurityStarttls, Username: "relay", PasswordSet: true,
		From: "Flagnetic <flags@example.com>",
	}
	check := func(what string, got management.SmtpSettings) {
		t.Helper()
		if !got.UpdatedAt.Equal(testkit.Start) {
			t.Errorf("%s: updated at %v, want the server's clock %v", what, got.UpdatedAt, testkit.Start)
		}
		got.UpdatedAt = time.Time{}
		if got != want {
			t.Errorf("%s = %+v, want %+v", what, got, want)
		}
	}
	check("the PUT's answer", putSMTP(t, in.admin, body))
	read, err := in.admin.GetMailSettingsWithResponse(t.Context())
	if err != nil || read.JSON200 == nil {
		t.Fatalf("GET = %v, %v", read.StatusCode(), err)
	}
	check("a fresh read", *read.JSON200)
	if strings.Contains(string(read.Body), password) {
		t.Error("GET answers the password")
	}

	// Left out, the password is kept; the other fields are replaced.
	body.Password, body.Port, want.Port = nil, 2525, 2525
	check("a PUT without a password", putSMTP(t, in.admin, body))

	// Emptied, it is removed, and a username then has none.
	body.Password = new("")
	res, err := in.admin.UpdateMailSettingsWithResponse(t.Context(), body)
	if err != nil {
		t.Fatal(err)
	}
	p := assertProblem(t, "a username without a password", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_smtp_settings")
	if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != "/password" {
		t.Errorf("violations %+v, want one at /password", p.Errors)
	}
	body.Username, want.Username, want.PasswordSet = new(""), "", false
	check("a PUT dropping the username and with it the password", putSMTP(t, in.admin, body))

	body.Username, body.Password = new("relay"), new(password)
	want.Username, want.PasswordSet = "relay", true
	putSMTP(t, in.admin, body)

	deleted, err := in.admin.DeleteMailSettingsWithResponse(t.Context())
	if err != nil || deleted.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE = %v, %v", deleted.StatusCode(), err)
	}
	after, err := in.admin.GetMailSettingsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "SMTP after removing it", after.StatusCode(), after.Body, http.StatusNotFound, "not_found")

	updates := smtpAudit(t, in.admin, "smtp.updated")
	if len(updates) != 4 {
		t.Fatalf("%d smtp.updated entries, want 4: the first PUT, the port, the dropped password, the new password", len(updates))
	}
	// Newest first.
	first := updates[3]
	firstAfter, _ := first.After.(map[string]any)
	if first.Before != nil || firstAfter["host"] != "smtp.example.com" || firstAfter["password_set"] != true {
		t.Errorf("first smtp.updated before = %v, after = %v; want nothing, then the settings with password_set", first.Before, first.After)
	}
	dropped, _ := updates[1].After.(map[string]any)
	if dropped["password_set"] != false || dropped["username"] != "" {
		t.Errorf("the entry dropping the password records %v, want password_set false", dropped)
	}
	deletes := smtpAudit(t, in.admin, "smtp.deleted")
	if len(deletes) != 1 {
		t.Fatalf("%d smtp.deleted entries, want 1", len(deletes))
	}
	if before, _ := deletes[0].Before.(map[string]any); before["host"] != "smtp.example.com" || deletes[0].After != nil {
		t.Errorf("smtp.deleted before = %v, after = %v", deletes[0].Before, deletes[0].After)
	}
}

func TestSettingsThatChangeNothingRecordNothing(t *testing.T) {
	in := startSMTPInstance(t, nil)
	body := management.SmtpSettingsUpdate{Host: "smtp.example.com", Port: 25, Security: management.SmtpSecurityNone, From: "flags@example.com"}
	putSMTP(t, in.admin, body)
	putSMTP(t, in.admin, body)
	if got := smtpAudit(t, in.admin, "smtp.updated"); len(got) != 1 {
		t.Errorf("%d smtp.updated entries after the same settings twice, want 1", len(got))
	}
}

func TestSMTPSettingsRefuseWhatCannotSend(t *testing.T) {
	in := startSMTPInstance(t, nil)
	valid := management.SmtpSettingsUpdate{Host: "smtp.example.com", Port: 587, Security: management.SmtpSecurityStarttls, From: "flags@example.com"}
	for name, tc := range map[string]struct {
		edit    func(*management.SmtpSettingsUpdate)
		pointer string
	}{
		"a blank host":              {func(b *management.SmtpSettingsUpdate) { b.Host = "  " }, "/host"},
		"a host with a space":       {func(b *management.SmtpSettingsUpdate) { b.Host = "smtp example.com" }, "/host"},
		"port zero":                 {func(b *management.SmtpSettingsUpdate) { b.Port = 0 }, "/port"},
		"a port beyond 65535":       {func(b *management.SmtpSettingsUpdate) { b.Port = 65536 }, "/port"},
		"an unknown security":       {func(b *management.SmtpSettingsUpdate) { b.Security = "ssl" }, "/security"},
		"a from that is no address": {func(b *management.SmtpSettingsUpdate) { b.From = "Flagnetic" }, "/from"},
		"a from with two addresses": {func(b *management.SmtpSettingsUpdate) { b.From = "a@example.com, b@example.com" }, "/from"},
		"a username with no password": {
			func(b *management.SmtpSettingsUpdate) { b.Username = new("relay") }, "/password"},
		"a password with no username": {
			func(b *management.SmtpSettingsUpdate) { b.Password = new("secret-value") }, "/username"},
		"credentials in the clear to another host": {func(b *management.SmtpSettingsUpdate) {
			b.Security, b.Username, b.Password = management.SmtpSecurityNone, new("relay"), new("secret-value")
		}, "/security"},
	} {
		t.Run(name, func(t *testing.T) {
			body := valid
			tc.edit(&body)
			res, err := in.admin.UpdateMailSettingsWithResponse(t.Context(), body)
			if err != nil {
				t.Fatal(err)
			}
			p := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_smtp_settings")
			if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != tc.pointer {
				t.Errorf("violations %+v, want one at %s", p.Errors, tc.pointer)
			}
		})
	}
	if got := smtpAudit(t, in.admin, "smtp.updated"); len(got) != 0 {
		t.Errorf("%d smtp.updated entries after refusals only, want none", len(got))
	}
}

func TestCredentialsMayCrossNoSecurityToThisMachineAlone(t *testing.T) {
	in := startSMTPInstance(t, nil)
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		putSMTP(t, in.admin, management.SmtpSettingsUpdate{
			Host: host, Port: 1025, Security: management.SmtpSecurityNone, Username: new("relay"), Password: new("secret-value"),
			From: "flags@example.com",
		})
	}
}

func TestSMTPIsForInstanceAdminsAlone(t *testing.T) {
	in := startSMTPInstance(t, nil)
	member := testkit.Client(t, in.srv, testkit.MemberToken(t, in.pool, access.ScopeRead, access.ScopeInstanceAdmin))
	body := management.SmtpSettingsUpdate{Host: "smtp.example.com", Port: 25, Security: management.SmtpSecurityNone, From: "flags@example.com"}

	get, err := member.GetMailSettingsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "GET as a member", get.StatusCode(), get.Body, http.StatusForbidden, "forbidden")
	put, err := member.UpdateMailSettingsWithResponse(t.Context(), body)
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "PUT as a member", put.StatusCode(), put.Body, http.StatusForbidden, "forbidden")
	del, err := member.DeleteMailSettingsWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "DELETE as a member", del.StatusCode(), del.Body, http.StatusForbidden, "forbidden")
	test, err := member.SendTestMailWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "a test mail as a member", test.StatusCode(), test.Body, http.StatusForbidden, "forbidden")
}

func TestATestMailReachesTheCaller(t *testing.T) {
	for _, mode := range []testkit.MailpitMode{testkit.MailpitOpen, testkit.MailpitAuth, testkit.MailpitStartTLS, testkit.MailpitTLS} {
		t.Run(string(mode), func(t *testing.T) {
			mp := testkit.StartMailpit(t, mode)
			in := startSMTPInstance(t, nil)
			putSMTP(t, in.admin, settingsFor(mp))

			res, err := in.admin.SendTestMailWithResponse(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode() != http.StatusNoContent {
				t.Fatalf("test mail = %d: %s", res.StatusCode(), res.Body)
			}
			// The mail was accepted when the request answered.
			got := mp.MailsTo(t, in.member.Email)
			if len(got) != 1 {
				t.Fatalf("%d mails to the caller, want 1 already accepted when the request answers", len(got))
			}
			m := got[0]
			if m.From != "flags@example.com" || len(m.To) != 1 || m.To[0] != in.member.Email {
				t.Errorf("from %q to %q, want flags@example.com to the caller", m.From, m.To)
			}
			if m.Subject != "Flagnetic test mail" || !strings.Contains(m.Text, smtpBaseURL) || !strings.Contains(m.Text, in.member.Email) {
				t.Errorf("subject %q, text %q; want the English test mail naming the instance", m.Subject, m.Text)
			}
			if !strings.Contains(m.HTML, "<p>") || !strings.Contains(m.HTML, smtpBaseURL) {
				t.Errorf("HTML part %q, want the same message as markup", m.HTML)
			}
			if !m.Date.Equal(testkit.Start) {
				t.Errorf("Date = %v, want the server's clock %v", m.Date, testkit.Start)
			}
		})
	}
}

func TestATestMailIsWrittenInTheCallersLanguage(t *testing.T) {
	mp := testkit.StartMailpit(t, testkit.MailpitOpen)
	for name, tc := range map[string]struct {
		server []testkit.ServerOption
		member []testkit.MemberOption
		want   string
	}{
		"the member's own":                    {nil, []testkit.MemberOption{testkit.WithLocale("ru")}, "Тестовое письмо Flagnetic"},
		"the member's over the instance's":    {[]testkit.ServerOption{testkit.WithDefaultLocale("ru")}, []testkit.MemberOption{testkit.WithLocale("en")}, "Flagnetic test mail"},
		"the instance's when they chose none": {[]testkit.ServerOption{testkit.WithDefaultLocale("ru")}, nil, "Тестовое письмо Flagnetic"},
		"English by default":                  {nil, nil, "Flagnetic test mail"},
	} {
		t.Run(name, func(t *testing.T) {
			in := startSMTPInstance(t, tc.server, tc.member...)
			putSMTP(t, in.admin, settingsFor(mp))
			res, err := in.admin.SendTestMailWithResponse(t.Context())
			if err != nil || res.StatusCode() != http.StatusNoContent {
				t.Fatalf("test mail = %v, %v: %s", res.StatusCode(), err, res.Body)
			}
			got := mp.MailsTo(t, in.member.Email)
			if len(got) != 1 || got[0].Subject != tc.want {
				t.Errorf("mails = %+v, want one with subject %q", got, tc.want)
			}
		})
	}
}

func TestATestMailThatFailsNamesTheFailure(t *testing.T) {
	closedPort := func(t *testing.T) int {
		l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		return port
	}
	const wrongPassword = "not-the-relay-password"
	for name, tc := range map[string]struct {
		mode    testkit.MailpitMode
		edit    func(t *testing.T, s *management.SmtpSettingsUpdate)
		server  []testkit.ServerOption
		failure string
	}{
		"nothing listening": {mode: testkit.MailpitOpen, failure: "connection",
			edit: func(t *testing.T, s *management.SmtpSettingsUpdate) { s.Port = closedPort(t) }},
		"STARTTLS not offered": {mode: testkit.MailpitOpen, failure: "tls",
			edit: func(_ *testing.T, s *management.SmtpSettingsUpdate) { s.Security = management.SmtpSecurityStarttls }},
		"TLS against a plain server": {mode: testkit.MailpitOpen, failure: "tls",
			edit: func(_ *testing.T, s *management.SmtpSettingsUpdate) { s.Security = management.SmtpSecurityTls }},
		"a certificate nobody trusts": {mode: testkit.MailpitStartTLS, failure: "tls",
			server: []testkit.ServerOption{testkit.WithMailRoots(nil)}},
		"a wrong password": {mode: testkit.MailpitStartTLS, failure: "authentication",
			edit: func(_ *testing.T, s *management.SmtpSettingsUpdate) { s.Password = new(wrongPassword) }},
		"a refused recipient": {mode: testkit.MailpitPicky, failure: "rejected"},
	} {
		t.Run(name, func(t *testing.T) {
			mp := testkit.StartMailpit(t, tc.mode)
			in := startSMTPInstance(t, tc.server)
			body := settingsFor(mp)
			if tc.edit != nil {
				tc.edit(t, &body)
			}
			putSMTP(t, in.admin, body)

			res, err := in.admin.SendTestMailWithResponse(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			p := assertProblem(t, "the test mail", res.StatusCode(), res.Body, http.StatusBadGateway, "smtp_failed")
			if p.Failure == nil || *p.Failure != tc.failure {
				t.Errorf("failure = %v, want %q: %s", p.Failure, tc.failure, res.Body)
			}
			if strings.Contains(string(res.Body), testkit.MailpitPassword) || strings.Contains(string(res.Body), wrongPassword) {
				t.Errorf("the failure answers a password: %s", res.Body)
			}
		})
	}
}

func TestATestMailWithoutSMTPIsRefused(t *testing.T) {
	in := startSMTPInstance(t, nil)
	res, err := in.admin.SendTestMailWithResponse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, "a test mail with no SMTP server", res.StatusCode(), res.Body, http.StatusConflict, "smtp_not_configured")
}

func TestMailsAreCountedByKindAndResult(t *testing.T) {
	open := testkit.StartMailpit(t, testkit.MailpitOpen)
	picky := testkit.StartMailpit(t, testkit.MailpitPicky)
	in := startSMTPInstance(t, nil)
	send := func() {
		t.Helper()
		if _, err := in.admin.SendTestMailWithResponse(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	putSMTP(t, in.admin, settingsFor(open))
	send()
	send()
	putSMTP(t, in.admin, settingsFor(picky))
	send()

	scrape := in.srv.Scrape(t)
	for series, want := range map[string]float64{
		`flagnetic_mail_sent_total{kind="test",result="sent"}`:     2,
		`flagnetic_mail_sent_total{kind="test",result="rejected"}`: 1,
	} {
		if got := testkit.Metric(t, scrape, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
}

func TestEveryReplicaLearnsTheSMTPSettingsOfAnother(t *testing.T) {
	pool := testkit.FreshPool(t)
	_, token := testkit.NewInstanceAdmin(t, pool)
	opts := []testkit.ServerOption{testkit.WithPool(pool), testkit.WithBaseURL(smtpBaseURL)}
	a, b := testkit.StartServer(t, opts...), testkit.StartServer(t, opts...)
	a.AwaitListening(t, "smtp_settings")
	b.AwaitListening(t, "smtp_settings")
	adminA, adminB := testkit.Client(t, a, token), testkit.Client(t, b, token)

	status := func(c *management.ClientWithResponses) int {
		t.Helper()
		res, err := c.GetMailSettingsWithResponse(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode()
	}
	await := func(what string, want int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); status(adminB) != want; {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the other replica answers %d after 5 seconds, want %d", what, status(adminB), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if status(adminB) != http.StatusNotFound {
		t.Fatal("the other replica sees settings on a fresh instance")
	}
	putSMTP(t, adminA, management.SmtpSettingsUpdate{Host: "one.example.com", Port: 25, Security: management.SmtpSecurityNone, From: "flags@example.com"})
	await("after configuring", http.StatusOK)
	if res, err := adminA.DeleteMailSettingsWithResponse(t.Context()); err != nil || res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE = %v, %v", res.StatusCode(), err)
	}
	await("after removing", http.StatusNotFound)
}
