package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// Mail is one mail to one recipient, before it is written: the kind and
// the language choose the template, Data fills it.
type Mail struct {
	Kind access.MailKind
	// To is the recipient.
	To MailRecipient
	// Locale is the language the mail is written in, one of access.MailLocales.
	Locale string
	// Data is what the kind's template reads: the type its kind documents.
	Data any
	// RequestID is the request that caused the mail, for the log, "" when
	// none did.
	RequestID string
	// At is when the mail is written, its Date header.
	At time.Time
}

// MailRecipient is who a mail is for.
type MailRecipient struct {
	Name  string
	Email string
}

// MailServer is an SMTP server to send through, its password in the clear.
type MailServer struct {
	Host     string
	Port     int
	Security access.SMTPSecurity
	Username string
	Password secret.String
	// From is the sender, one address, optionally with a display name.
	From string
}

// Mailer writes a mail and sends it through a server, returning a
// *MailError naming the failed step's class when the exchange fails.
type Mailer interface {
	Send(ctx context.Context, server MailServer, m Mail) error
}

// MailError is a failed send. It never carries the password or the mail.
type MailError struct {
	Failure access.MailFailure
	Err     error
}

func (e *MailError) Error() string { return fmt.Sprintf("send mail: %s: %v", e.Failure, e.Err) }

// Unwrap returns the cause.
func (e *MailError) Unwrap() error { return e.Err }

// MailCounter counts the mails sent.
type MailCounter interface {
	// Count counts one mail of kind with result: "sent", the class of the
	// failure (an access.MailFailure), or "error" when it failed before the
	// exchange with the server began.
	Count(kind access.MailKind, result string)
}

// Mail results that are no failure class.
const (
	mailSent  = "sent"
	mailError = "error"
)

// MailBound bounds a mail sent in the background, from the commit of the
// change causing it.
const MailBound = time.Minute

// AfterCommit runs a function once the unit of work a context carries has
// committed, and never if it rolls back.
type AfterCommit interface {
	// AfterCommit calls fn with the context the unit of work was given,
	// without the unit of work itself: it has ended, and fn's statements
	// run on their own.
	AfterCommit(ctx context.Context, fn func(committed context.Context))
}

// PostmanDeps are what a Postman needs.
type PostmanDeps struct {
	Settings SMTPReader
	Opener   Opener
	Mailer   Mailer
	Counter  MailCounter
	After    AfterCommit
	Clock    clock.Clock
	Logger   *slog.Logger
	// DefaultLocale is the language of a mail whose recipient has not chosen
	// one (FLAGNETIC_DEFAULT_LOCALE).
	DefaultLocale string
}

// Postman sends the instance's mail. A use case queues a mail inside its
// unit of work and the mail goes out only once that has committed, in the
// background, so a slow or failing relay never delays or undoes the change
// that caused it. A mail in flight when the replica dies is lost: every
// mail has a cheap human retry.
type Postman struct {
	d  PostmanDeps
	wg sync.WaitGroup
	// mu orders each mail's place in wg before Wait begins: once waiting is
	// set, a mail still goes out but is not awaited, since adding to an empty
	// wait group while it is being waited on is not allowed.
	mu      sync.Mutex
	waiting bool
}

// NewPostman returns the service.
func NewPostman(d PostmanDeps) *Postman { return &Postman{d: d} }

// Locale returns the language a mail is written in: the first of preferred
// that mail is written in, else the instance's default.
func (p *Postman) Locale(preferred ...string) string {
	return access.MailLocale(p.d.DefaultLocale, preferred...)
}

// Queue sends m once the unit of work ctx carries has committed, in a
// goroutine bounded by MailBound, with m.At and m.RequestID filled in when
// the caller left them out. Nothing is sent while the instance has no SMTP
// server. A failure is logged with the request ID, the kind and the failure
// class, never the link, and counted; it fails nothing.
func (p *Postman) Queue(ctx context.Context, m Mail) {
	if m.At.IsZero() {
		m.At = p.d.Clock.Now()
	}
	p.d.After.AfterCommit(ctx, func(committed context.Context) {
		// Counted once the change has committed, not when queued: a change
		// that rolls back sends nothing and has nothing to await. The mail
		// outlives the request that caused it.
		done := p.track()
		bg, cancel := context.WithTimeout(context.WithoutCancel(committed), MailBound)
		safego.Go(bg, "mail "+string(m.Kind), p.d.Logger, func(ctx context.Context) {
			defer done()
			defer cancel()
			p.sendInBackground(ctx, m)
		})
	})
}

// track counts a mail Wait awaits and returns what ends it, or nothing to
// end once Wait has begun.
func (p *Postman) track() (done func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiting {
		return func() {}
	}
	p.wg.Add(1)
	return p.wg.Done
}

func (p *Postman) sendInBackground(ctx context.Context, m Mail) {
	if err := p.Deliver(ctx, m); err != nil {
		if errors.Is(err, ErrSMTPNotConfigured) {
			// SMTP was removed between the change and its mail.
			p.d.Logger.InfoContext(ctx, "mail not sent: SMTP is no longer configured",
				"kind", string(m.Kind), "request_id", m.RequestID)
			return
		}
		p.d.Logger.WarnContext(ctx, "mail not sent",
			"kind", string(m.Kind), "request_id", m.RequestID, "failure", failureOf(err), "error", err.Error())
	}
}

// Deliver sends m now, through the SMTP server in force, and returns what
// came of it: ErrSMTPNotConfigured while there is none, a *MailError when
// the exchange failed. It counts the mail.
func (p *Postman) Deliver(ctx context.Context, m Mail) error {
	settings, err := p.d.Settings.SMTPSettings(ctx)
	if err != nil {
		if !errors.Is(err, ErrSMTPNotConfigured) {
			// A mail that could not be tried at all is still one the replica
			// failed to send; one with no server to send through is not.
			p.d.Counter.Count(m.Kind, mailError)
		}
		return err
	}
	server := MailServer{
		Host: settings.Host, Port: settings.Port, Security: settings.Security, Username: settings.Username, From: settings.From,
	}
	if settings.PasswordSet() {
		plain, err := p.d.Opener.Open(secret.PurposeSMTPPassword, settings.PasswordSealed)
		if err != nil {
			p.d.Counter.Count(m.Kind, mailError)
			return fmt.Errorf("opening the SMTP password: %w", err)
		}
		server.Password = secret.New(string(plain))
	}
	if err := p.d.Mailer.Send(ctx, server, m); err != nil {
		p.d.Counter.Count(m.Kind, failureOf(err))
		return err
	}
	p.d.Counter.Count(m.Kind, mailSent)
	return nil
}

// failureOf is the result a failed send is counted and logged under.
func failureOf(err error) string {
	var failed *MailError
	if errors.As(err, &failed) {
		return string(failed.Failure)
	}
	return mailError
}

// Wait returns once every mail queued so far has been sent or has failed,
// or ctx ends. A replica stopping calls it after its requests are done, so
// the mails they caused leave with it.
func (p *Postman) Wait(ctx context.Context) {
	p.mu.Lock()
	p.waiting = true
	p.mu.Unlock()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// TestMailData is what the test mail's template reads.
type TestMailData struct {
	// Instance is the instance's public address, "" when it has none.
	Instance string
}

// SendTestMail sends the caller a mail, while they wait.
type SendTestMail struct {
	postman *Postman
	// instance is the instance's public address ("" when unset).
	instance string
}

// NewSendTestMail returns the handler; instance is the instance's public
// address, which the mail names, "" when the instance has none.
func NewSendTestMail(postman *Postman, instance string) *SendTestMail {
	return &SendTestMail{postman: postman, instance: instance}
}

var errTestMailNotConfigured = apperrors.Conflict("smtp_not_configured",
	"SMTP is not configured: set the SMTP server first")

// Handle sends the test mail to the member calling, in their language, and
// answers once the server accepted it. It is refused smtp_not_configured
// while there is no SMTP server, and smtp_failed naming the failure class
// when the server could not be reached, would not secure the connection,
// refused the credentials or refused the mail.
func (h *SendTestMail) Handle(ctx context.Context, caller access.Caller, requestID string) error {
	member := caller.Member
	if member == nil {
		return errors.New("a test mail has no member to be sent to")
	}
	err := h.postman.Deliver(ctx, Mail{
		Kind: access.MailTest, To: MailRecipient{Name: member.DisplayName, Email: member.Email},
		Locale: h.postman.Locale(member.Locale), Data: TestMailData{Instance: h.instance},
		RequestID: requestID, At: h.postman.d.Clock.Now(),
	})
	var failed *MailError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrSMTPNotConfigured):
		return errTestMailNotConfigured
	case errors.As(err, &failed):
		return apperrors.BadGateway("smtp_failed",
			"the SMTP server could not be reached or refused the mail: "+string(failed.Failure)).
			WithExtension("failure", string(failed.Failure)).WithCause(err)
	}
	return err
}
