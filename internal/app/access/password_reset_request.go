package access

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// PasswordResetAvailable answers whether a member can ask for a password reset
// by mail: the instance has an SMTP server and lets them.
type PasswordResetAvailable struct {
	settings AuthSettingsReader
	smtp     SMTPReader
}

// NewPasswordResetAvailable returns the query.
func NewPasswordResetAvailable(settings AuthSettingsReader, smtp SMTPReader) *PasswordResetAvailable {
	return &PasswordResetAvailable{settings: settings, smtp: smtp}
}

// Handle reports whether a reset can be asked for by mail.
func (q *PasswordResetAvailable) Handle(ctx context.Context) (bool, error) {
	return resetByMailAvailable(ctx, q.settings, q.smtp)
}

func resetByMailAvailable(ctx context.Context, settings AuthSettingsReader, smtp SMTPReader) (bool, error) {
	s, err := settings.AuthSettings(ctx)
	if err != nil || !s.SelfServicePasswordReset {
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
	Email     string
	RequestID string
}

// passwordResetLinkPath is where the console opens a password reset,
// followed by its secret.
const passwordResetLinkPath = "/password-reset/"

// requestBound bounds the lookup and the issuing behind a request.
const requestBound = 30 * time.Second

// Handle starts the request and returns at once, whatever the email is: it
// is answered before anything is looked up, so neither what comes of the
// request nor how long it takes tells who holds an account. Behind the
// answer, a password reset is issued and mailed only when the instance has an
// SMTP server and lets members ask, the email belongs to an active member
// who holds a password, and the member has not been issued a reset they asked
// for in the last PasswordResetInterval, whether or not its mail was sent.
// Whatever stops it does so silently; a failure is logged.
func (h *RequestPasswordReset) Handle(ctx context.Context, cmd RequestPasswordResetCommand) {
	h.d.Background.Go(ctx, "password reset request", requestBound, func(ctx context.Context) {
		if err := h.issue(ctx, cmd); err != nil {
			h.d.Logger.WarnContext(ctx, "password reset request failed", "request_id", cmd.RequestID, "error", err.Error())
		}
	})
}

// issue issues and queues the password reset the request leads to, if any.
func (h *RequestPasswordReset) issue(ctx context.Context, cmd RequestPasswordResetCommand) error {
	email := strings.TrimSpace(cmd.Email)
	if email == "" || h.d.ConsoleURL == "" {
		return nil
	}
	if on, err := resetByMailAvailable(ctx, h.d.Settings, h.d.SMTP); err != nil || !on {
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
