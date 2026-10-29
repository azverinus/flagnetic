package access_test

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/mail"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// counted records what a postman counts.
type counted struct {
	mu   sync.Mutex
	seen []string
}

func (c *counted) Count(kind access.MailKind, result string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, string(kind)+"/"+result)
}

func (c *counted) results() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.seen...)
}

// postman is a postman on a new instance, with what it logs and counts.
type postman struct {
	*appaccess.Postman
	store *pgaccess.Store
	tx    appaccess.Transactor
	logs  *testkit.LogSink
	count *counted
}

// newPostman returns a postman on a new instance whose SMTP settings are
// settings, sending through m, the real sender when nil.
func newPostman(t *testing.T, settings access.SMTPChange, m appaccess.Mailer) postman {
	t.Helper()
	pool := testkit.FreshPool(t)
	store, tx := pgaccess.NewStore(pool), postgres.NewTransactor(pool)
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	c := testkit.Clock()
	configure := appaccess.NewConfigureSMTP(appaccess.ConfigureSMTPDeps{
		Tx: tx, Store: store, Cache: nopCache{}, Sealer: keyring, Audit: testkit.Record(pool), Clock: c,
		IDs: testkit.IDs(c), BaseURLSet: true,
	})
	if _, err := configure.Handle(t.Context(), settings, governance.Request{Actor: cli}); err != nil {
		t.Fatal(err)
	}
	if m == nil {
		renderer, err := mail.NewRenderer()
		if err != nil {
			t.Fatal(err)
		}
		m = mail.NewSender(renderer, testkit.MailpitRoots())
	}
	logs, count := testkit.NewLogSink(), &counted{}
	return postman{
		Postman: appaccess.NewPostman(appaccess.PostmanDeps{
			Settings: store, Opener: keyring, Mailer: m, Counter: count, After: postgres.AfterCommit{}, Clock: c,
			Logger: logs.Logger(), DefaultLocale: "en",
		}),
		store: store, tx: tx, logs: logs, count: count,
	}
}

type nopCache struct{}

func (nopCache) Forget() {}

func smtpFor(mp *testkit.Mailpit) access.SMTPChange {
	c := access.SMTPChange{Host: mp.Host, Port: mp.Port, Security: access.SMTPSecurity(mp.Security()), From: "flags@example.com"}
	if mp.Credentials() {
		c.Username, c.Password = testkit.MailpitUser, new(testkit.MailpitPassword)
	}
	return c
}

// uniqueAddress is an address no other test sends mail to.
func uniqueAddress() string { return "ada-" + strings.ToLower(rand.Text()) + "@example.com" }

func testMail(to string) appaccess.Mail {
	return appaccess.Mail{
		Kind: access.MailTest, To: appaccess.MailRecipient{Name: "Ada", Email: to}, Locale: "en",
		Data: appaccess.TestMailData{Instance: "https://flags.example.com"}, RequestID: "req-" + rand.Text(),
	}
}

func TestAMailQueuedInAUnitOfWorkLeavesOnlyOnceItHasCommitted(t *testing.T) {
	mp := testkit.StartMailpit(t, testkit.MailpitAuth)
	p := newPostman(t, smtpFor(mp), nil)
	to := uniqueAddress()

	err := p.tx.Within(t.Context(), func(ctx context.Context) error {
		p.Queue(ctx, testMail(to))
		time.Sleep(300 * time.Millisecond)
		mp.AssertNoMail(t, to)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mp.AwaitMails(t, to, 1)
	if got := p.count.results(); len(got) != 1 || got[0] != "test/sent" {
		t.Errorf("counted %v, want one test/sent", got)
	}
}

func TestAMailQueuedInAUnitOfWorkThatRollsBackIsNeverSent(t *testing.T) {
	mp := testkit.StartMailpit(t, testkit.MailpitOpen)
	p := newPostman(t, smtpFor(mp), nil)
	to := uniqueAddress()

	err := p.tx.Within(t.Context(), func(ctx context.Context) error {
		p.Queue(ctx, testMail(to))
		return errors.New("the change failed")
	})
	if err == nil {
		t.Fatal("the unit of work succeeded")
	}
	waitFor(t, p.Postman)
	time.Sleep(300 * time.Millisecond)
	mp.AssertNoMail(t, to)
	if got := p.count.results(); len(got) != 0 {
		t.Errorf("counted %v, want nothing", got)
	}
}

func waitFor(t *testing.T, p *appaccess.Postman) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p.Wait(ctx)
	if ctx.Err() != nil {
		t.Fatal("the postman still had mail in flight after 5 seconds")
	}
}

func TestWaitingAwaitsTheMailInFlight(t *testing.T) {
	mp := testkit.StartMailpit(t, testkit.MailpitOpen)
	p := newPostman(t, smtpFor(mp), nil)
	to := uniqueAddress()
	if err := p.tx.Within(t.Context(), func(ctx context.Context) error {
		p.Queue(ctx, testMail(to))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p.Postman)
	// No polling: what was in flight when the replica stopped has arrived.
	if got := mp.MailsTo(t, to); len(got) != 1 {
		t.Errorf("%d mails after waiting, want 1", len(got))
	}
}

func TestAMailThatFailsIsLoggedAndCountedAndFailsNothing(t *testing.T) {
	// Nothing listens where the settings point.
	p := newPostman(t, access.SMTPChange{Host: "127.0.0.1", Port: 1, Security: access.SMTPNone, From: "flags@example.com"}, nil)
	m := testMail("ada@example.com")
	m.Data = appaccess.TestMailData{Instance: "https://flags.example.com/reset/the-secret-link"}

	if err := p.tx.Within(t.Context(), func(ctx context.Context) error {
		p.Queue(ctx, m)
		return nil
	}); err != nil {
		t.Fatalf("the change failed with its mail: %v", err)
	}
	waitFor(t, p.Postman)

	logged := p.logs.Find("mail not sent")
	if len(logged) != 1 {
		t.Fatalf("%d failures logged, want 1: %+v", len(logged), p.logs.Records())
	}
	attrs := logged[0].Attrs
	if attrs["kind"] != "test" || attrs["failure"] != "connection" || attrs["request_id"] != m.RequestID {
		t.Errorf("logged %v, want the kind, the failure class and the request ID", attrs)
	}
	for k, v := range attrs {
		if s, ok := v.(string); ok && strings.Contains(s, "the-secret-link") {
			t.Errorf("the log holds the link in %s: %q", k, s)
		}
	}
	if got := p.count.results(); len(got) != 1 || got[0] != "test/connection" {
		t.Errorf("counted %v, want one test/connection", got)
	}
}

// deadlineMailer records the deadline of the context it is sent under.
type deadlineMailer struct {
	deadline chan time.Time
}

func (d deadlineMailer) Send(ctx context.Context, _ appaccess.MailServer, _ appaccess.Mail) error {
	at, _ := ctx.Deadline()
	d.deadline <- at
	return nil
}

func TestAMailInTheBackgroundIsBoundedByAMinuteWhateverTheRequestDoes(t *testing.T) {
	d := deadlineMailer{deadline: make(chan time.Time, 1)}
	p := newPostman(t, access.SMTPChange{Host: "127.0.0.1", Port: 25, Security: access.SMTPNone, From: "flags@example.com"}, d)

	// The request ends before its mail leaves.
	requestCtx, endRequest := context.WithCancel(t.Context())
	if err := p.tx.Within(requestCtx, func(ctx context.Context) error {
		p.Queue(ctx, testMail("ada@example.com"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	endRequest()
	waitFor(t, p.Postman)

	var at time.Time
	select {
	case at = <-d.deadline:
	case <-time.After(5 * time.Second):
		t.Fatal("the mail was never handed to the mailer")
	}
	if left := time.Until(at); left < 50*time.Second || left > time.Minute {
		t.Errorf("the mail is bounded by %v, want a minute", left)
	}
}

func TestNothingIsSentOnceSMTPIsRemoved(t *testing.T) {
	mp := testkit.StartMailpit(t, testkit.MailpitOpen)
	p := newPostman(t, smtpFor(mp), nil)
	to := uniqueAddress()
	if err := p.store.DeleteSMTPSettings(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := p.tx.Within(t.Context(), func(ctx context.Context) error {
		p.Queue(ctx, testMail(to))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p.Postman)
	mp.AssertNoMail(t, to)
	if got := p.count.results(); len(got) != 0 {
		t.Errorf("counted %v, want nothing for a mail with no server to send it through", got)
	}
}
