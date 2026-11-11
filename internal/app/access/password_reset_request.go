package access

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SignInRules are what the sign-in page shows of the auth settings, for an
// email or for anybody.
type SignInRules struct {
	// LocalLoginEnabled is whether members sign in with a password at all.
	LocalLoginEnabled bool
	// PasswordResetAvailable is whether a reset can be asked for by mail.
	PasswordResetAvailable bool
	// EnforcedProvider is the slug of the auth provider the email's domain
	// is enforced to, "" when it is not.
	EnforcedProvider string
}

// GetSignInRules answers how the sign-in page offers to sign in.
type GetSignInRules struct {
	settings AuthSettingsReader
	smtp     SMTPReader
}

// NewGetSignInRules returns the query.
func NewGetSignInRules(settings AuthSettingsReader, smtp SMTPReader) *GetSignInRules {
	return &GetSignInRules{settings: settings, smtp: smtp}
}

// Handle returns the rules for email, "" for anybody. They are judged from
// the email's domain alone, so they never tell whether a member holds it. A
// reset by mail is available when the instance has an SMTP server, lets
// members ask, and accepts a password from the email: local login is on and
// its domain is not enforced. A reset that could not be completed is never
// offered.
func (q *GetSignInRules) Handle(ctx context.Context, email string) (SignInRules, error) {
	s, err := q.settings.AuthSettings(ctx)
	if err != nil {
		return SignInRules{}, err
	}
	out := SignInRules{LocalLoginEnabled: s.LocalLoginEnabled}
	if d, ok := s.EnforcedFor(email); ok {
		out.EnforcedProvider = d.Provider
	}
	out.PasswordResetAvailable, err = resetByMailAvailable(ctx, q.settings, q.smtp, email)
	return out, err
}

// resetByMailAvailable reports whether a reset by mail can be asked for
// email: the instance has an SMTP server, lets members ask, and the settings
// accept a password from the email.
func resetByMailAvailable(ctx context.Context, settings AuthSettingsReader, smtp SMTPReader, email string) (bool, error) {
	s, err := settings.AuthSettings(ctx)
	if err != nil || !s.SelfServicePasswordReset || s.PasswordRefusal(email) != nil {
		return false, err
	}
	if _, err = smtp.SMTPSettings(ctx); errors.Is(err, ErrSMTPNotConfigured) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// RequestPasswordResetStore is what asking for a password reset needs.
type RequestPasswordResetStore interface {
	PasswordResetStore
	// MemberByEmail returns the member whose email is email whatever its
	// case, with their password hash ("" for none); ErrNotFound when there
	// is none.
	MemberByEmail(ctx context.Context, email string) (access.Member, string, error)
	// LockMember returns the member, locked until the unit of work ends, and
	// whether they hold a password; ErrNotFound when none has the identifier.
	LockMember(ctx context.Context, member access.MemberID) (access.Member, bool, error)
	// LatestSelfServicePasswordReset returns when the latest password reset
	// member asked for was issued, open or not; false when they never asked.
	LatestSelfServicePasswordReset(ctx context.Context, member access.MemberID) (time.Time, bool, error)
}

// PasswordResetMailData is what the password reset mail's template reads.
type PasswordResetMailData struct {
	// URL is the link that opens the reset, carrying its secret.
	URL string
	// ExpiresAt is when the link stops working.
	ExpiresAt time.Time
}

// RequestPasswordResetDeps are what asking for a password reset works with.
type RequestPasswordResetDeps struct {
	Tx       Transactor
	Store    RequestPasswordResetStore
	Settings AuthSettingsReader
	SMTP     SMTPReader
	Postman  *Postman
	// Throttle is the source throttle sign-in, acceptance and completion
	// share; every request counts against it.
	Throttle *SourceThrottle
	// Background runs the request after its answer.
	Background *Background
	Audit      AuditLog
	Clock      clock.Clock
	IDs        *id.Source
	Random     io.Reader
	Logger     *slog.Logger
	// ConsoleURL is the address the console is served at, "" when the
	// instance has none, which a link cannot be written without: the mail
	// would otherwise carry whatever address the request claimed to come to.
	ConsoleURL string
}

// RequestPasswordReset mails a member a link to set a new password, when they
// ask for one without being signed in.
type RequestPasswordReset struct{ d RequestPasswordResetDeps }

// NewRequestPasswordReset returns the handler.
func NewRequestPasswordReset(d RequestPasswordResetDeps) *RequestPasswordReset {
	return &RequestPasswordReset{d: d}
}

// RequestPasswordResetCommand is one request for a password reset.
type RequestPasswordResetCommand struct {
	Email string
	// ClientIP is the address the request comes from.
	ClientIP  netip.Addr
	RequestID string
}

// passwordResetLinkPath is where the console opens a password reset,
// followed by its secret.
const passwordResetLinkPath = "/password-reset/"

// requestBound bounds the lookup and the issuing behind a request.
const requestBound = 30 * time.Second

// Handle counts the request against the throttle of its source and starts
// it, returning before anything is looked up, whatever the email is: neither
// what comes of the request nor how long it takes tells who holds an
// account. A source past the throttle is refused rate_limited, whatever the
// email; every request counts, so a source cannot make the instance mail or
// audit at wire speed, and the count is kept, as the answer is the same
// whether or not the email exists. Behind the answer, a password reset is
// issued and mailed only when the instance has an SMTP server and lets
// members ask, the email belongs to an active member who holds a password,
// and the member has not been issued a reset they asked for in the last
// PasswordResetInterval, whether or not its mail was sent. An email the auth
// settings refuse a password, while local login is disabled or in an
// enforced domain, is issued none. Whatever stops it does so silently; a
// failure is logged, and so is a request dropped because the replica is
// running too many background jobs.
func (h *RequestPasswordReset) Handle(ctx context.Context, cmd RequestPasswordResetCommand) error {
	if _, err := h.d.Throttle.Reserve(ctx, cmd.ClientIP, h.d.Clock.Now()); err != nil {
		return err
	}
	h.d.Background.Go(ctx, "password reset request", requestBound, func(ctx context.Context) {
		if err := h.issue(ctx, cmd); err != nil {
			h.d.Logger.WarnContext(ctx, "password reset request failed", "request_id", cmd.RequestID, "error", err.Error())
		}
	})
	return nil
}

// issue issues and queues the password reset the request leads to, if any.
func (h *RequestPasswordReset) issue(ctx context.Context, cmd RequestPasswordResetCommand) error {
	email := strings.TrimSpace(cmd.Email)
	if email == "" || h.d.ConsoleURL == "" {
		return nil
	}
	if on, err := resetByMailAvailable(ctx, h.d.Settings, h.d.SMTP, email); err != nil || !on {
		return err
	}
	found, hash, err := h.d.Store.MemberByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) || (err == nil && !access.CanAskForPasswordReset(found, hash != "")) {
		return nil
	}
	if err != nil {
		return err
	}
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		// Locked, the member is read again: another request for them, a
		// deactivation or a password change may have come in between.
		m, holdsPassword, err := h.d.Store.LockMember(ctx, found.ID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil || !access.CanAskForPasswordReset(m, holdsPassword) {
			return err
		}
		last, asked, err := h.d.Store.LatestSelfServicePasswordReset(ctx, m.ID)
		if err != nil {
			return err
		}
		now := h.d.Clock.Now()
		if asked && !access.SelfServicePasswordResetDue(&last, now) {
			return nil
		}
		reset, s := access.NewPasswordReset(id.New[access.PasswordReset](h.d.IDs), m.ID, access.MemberID{}, now, h.d.Random)
		if err = h.d.Store.CreatePasswordReset(ctx, reset); err != nil {
			return err
		}
		if err = recordAll(ctx, h.d.Audit, h.d.IDs, now, governance.Request{Actor: memberActor(m), ID: cmd.RequestID}, governance.Record{
			Action: "password.reset_requested", Entity: memberEntity(m),
			After: map[string]any{"email": m.Email, "expires_at": reset.ExpiresAt.UTC().Format(time.RFC3339)},
		}); err != nil {
			return err
		}
		h.d.Postman.Queue(ctx, Mail{
			Kind: access.MailPasswordReset, To: MailRecipient{Name: m.DisplayName, Email: m.Email},
			Locale:    h.d.Postman.Locale(m.Locale),
			Data:      PasswordResetMailData{URL: h.d.ConsoleURL + passwordResetLinkPath + s.Reveal(), ExpiresAt: reset.ExpiresAt},
			RequestID: cmd.RequestID, At: now,
		})
		return nil
	})
}
