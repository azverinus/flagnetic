package access

import (
	"context"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// UpdateMe lets a member rename themself and choose their console language.
type UpdateMe struct {
	tx      Transactor
	store   MemberChanger
	audit   AuditLog
	clock   clock.Clock
	ids     *id.Source
	locales []string
}

// NewUpdateMe returns the handler; locales are the console's languages, the
// ones a member may choose.
func NewUpdateMe(tx Transactor, store MemberChanger, audit AuditLog, c clock.Clock, ids *id.Source, locales []string) *UpdateMe {
	return &UpdateMe{tx: tx, store: store, audit: audit, clock: c, ids: ids, locales: locales}
}

// UpdateMeCommand is a member's edit of themself.
type UpdateMeCommand struct {
	Caller  access.Caller
	Change  access.ProfileChange
	Request governance.Request
}

// Handle applies the edit to the caller's member and records member.updated
// naming the fields it changed; an edit changing nothing records nothing.
// Only a session edits its member, so a leaked token cannot rename its
// owner.
func (h *UpdateMe) Handle(ctx context.Context, cmd UpdateMeCommand) (MemberView, error) {
	if cmd.Caller.Member == nil || cmd.Caller.Session == nil {
		return MemberView{}, access.ErrSessionRequired
	}
	return changeMember(ctx, h.tx, h.store, h.audit, h.clock, h.ids, cmd.Request, cmd.Caller.Member.ID,
		func(_ context.Context, before access.Member, _ bool, _ time.Time) (access.Member, []governance.Record, error) {
			after, err := before.Profiled(cmd.Change, h.locales)
			if err != nil {
				return access.Member{}, nil, err
			}
			return after, profileEdited(before, after), nil
		})
}

// profileEdited returns the audit record of a member editing themself from
// before into after, naming only the fields changed; none when nothing
// changed. A locale that follows the browser is recorded as null.
func profileEdited(before, after access.Member) []governance.Record {
	was, is := map[string]any{}, map[string]any{}
	if before.DisplayName != after.DisplayName {
		was["display_name"], is["display_name"] = before.DisplayName, after.DisplayName
	}
	if before.Locale != after.Locale {
		was["locale"], is["locale"] = localeOrNil(before.Locale), localeOrNil(after.Locale)
	}
	if len(is) == 0 {
		return nil
	}
	return []governance.Record{{Action: "member.updated", Entity: memberEntity(after), Before: was, After: is}}
}

func localeOrNil(locale string) any {
	if locale == "" {
		return nil
	}
	return locale
}

// PasswordChangeStore is what changing one's password needs.
type PasswordChangeStore interface {
	passwordStore
	SetPasswordHash(ctx context.Context, member access.MemberID, hash string, at time.Time) error
	MemberSessions(ctx context.Context, member access.MemberID) ([]access.Session, error)
	RevokeSessions(ctx context.Context, sessions []access.SessionID, at time.Time) ([]access.SessionID, error)
	ConsumeInvitations(ctx context.Context, member access.MemberID, at time.Time) (int, error)
}

// ErrWrongCurrentPassword refuses a password change whose current password
// is wrong, with the code a wrong sign-in gets, as a violation of the field
// that holds it. It is no 401: the caller is signed in and stays so, and a
// 401 would send the console to its sign-in page.
var ErrWrongCurrentPassword = apperrors.Invalid("invalid_credentials", "the current password is wrong",
	apperrors.Violation{Pointer: "/current_password", Code: "invalid_credentials", Detail: "the current password is wrong"})

// ChangeMyPassword lets a signed-in member set a new password.
type ChangeMyPassword struct {
	d ChangeMyPasswordDeps
}

// ChangeMyPasswordDeps are what changing one's password works with.
type ChangeMyPasswordDeps struct {
	Tx    Transactor
	Store PasswordChangeStore
	// Settings give the password policy's minimum length and the timeouts
	// that tell the live sessions from the ended ones.
	Settings AuthSettingsReader
	Hasher   PasswordVerifier
	Audit    AuditLog
	Clock    clock.Clock
	IDs      *id.Source
}

// NewChangeMyPassword returns the handler.
func NewChangeMyPassword(d ChangeMyPasswordDeps) *ChangeMyPassword { return &ChangeMyPassword{d: d} }

// ChangeMyPasswordCommand is one attempt at changing one's password.
type ChangeMyPasswordCommand struct {
	Caller          access.Caller
	CurrentPassword secret.String
	NewPassword     secret.String
	// ClientIP is the source of the attempt, which a wrong current password
	// counts against.
	ClientIP netip.Addr
	Request  governance.Request
}

// Handle sets the caller's new password, revokes every other live session
// of theirs and consumes their open invitations, in one unit of work, and
// records password.changed with how many sessions it revoked. The session
// the caller is signed in to stays.
//
// The new password is held to the policy (weak_password at /new_password)
// before the current one is checked, so a weak one costs no attempt. The
// current one is checked as a sign-in checks a password, counting against
// the member's email and the source: a right one takes the failure back, a
// wrong one keeps it and is refused invalid_credentials, so a session left
// open on a shared device cannot be used to guess the password, and the
// email locks after the same failures a sign-in locks it after, recording
// auth.locked_out.
func (h *ChangeMyPassword) Handle(ctx context.Context, cmd ChangeMyPasswordCommand) error {
	if cmd.Caller.Member == nil || cmd.Caller.Session == nil {
		return access.ErrSessionRequired
	}
	member := *cmd.Caller.Member
	settings, err := h.d.Settings.AuthSettings(ctx)
	if err != nil {
		return err
	}
	if err = access.CheckPasswordAt("/new_password", cmd.NewPassword.Reveal(), member.Email, settings.PasswordMinLength); err != nil {
		return err
	}
	a, err := attemptPassword(ctx, h.d.Tx, h.d.Store, h.d.Hasher, access.NewAttempt(member.Email, cmd.ClientIP, h.d.Clock.Now()), cmd.CurrentPassword)
	if err != nil {
		return err
	}
	if !a.right() {
		return h.wrong(ctx, cmd, member, a.reserved)
	}
	if err := h.change(ctx, cmd, member, a.reserved, settings); err != nil {
		// The current password proved right, so whatever failed after was
		// no wrong guess.
		return releaseAttempt(ctx, h.d.Tx, h.d.Store, a.reserved, err)
	}
	return nil
}

// change hashes the new password and stores it, revoking the caller's other
// sessions and consuming their invitations, in one unit of work that also
// takes back the failure reserved.
func (h *ChangeMyPassword) change(ctx context.Context, cmd ChangeMyPasswordCommand, member access.Member,
	reserved access.Reservation, settings access.AuthSettings,
) error {
	hash, err := h.d.Hasher.Hash(ctx, cmd.NewPassword)
	if err != nil {
		return err
	}
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		return h.apply(ctx, cmd, member, reserved, settings, hash)
	})
}

// apply stores the new password hash of member, revokes their other live
// sessions, consumes their invitations, takes back the failure reserved and
// records password.changed with how many sessions it revoked, in the unit
// of work ctx carries.
func (h *ChangeMyPassword) apply(ctx context.Context, cmd ChangeMyPasswordCommand, member access.Member,
	reserved access.Reservation, settings access.AuthSettings, hash string,
) error {
	now := h.d.Clock.Now()
	if err := h.d.Store.ClearAttempt(ctx, reserved); err != nil {
		return err
	}
	if err := h.d.Store.SetPasswordHash(ctx, member.ID, hash, now); err != nil {
		return err
	}
	sessions, err := h.d.Store.MemberSessions(ctx, member.ID)
	if err != nil {
		return err
	}
	var others, revoked []access.SessionID
	for _, s := range access.LiveSessions(sessions, member, now, settings.SessionTimeouts()) {
		if s.ID != cmd.Caller.Session.ID {
			others = append(others, s.ID)
		}
	}
	if len(others) > 0 {
		if revoked, err = h.d.Store.RevokeSessions(ctx, others, now); err != nil {
			return err
		}
	}
	if _, err := h.d.Store.ConsumeInvitations(ctx, member.ID, now); err != nil {
		return err
	}
	return recordAll(ctx, h.d.Audit, h.d.IDs, now, cmd.Request, governance.Record{
		Action: "password.changed", Entity: memberEntity(member),
		After: map[string]any{"email": member.Email, "sessions_revoked": len(revoked)},
	})
}

// wrong keeps the failure reserved for a wrong current password, records
// the lockout it starts, if any, and returns the refusal.
func (h *ChangeMyPassword) wrong(ctx context.Context, cmd ChangeMyPasswordCommand, member access.Member,
	reserved access.Reservation,
) error {
	locked, locks := lockedOut(member, reserved)
	if !locks {
		return ErrWrongCurrentPassword
	}
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		return recordAll(ctx, h.d.Audit, h.d.IDs, h.d.Clock.Now(), cmd.Request, locked)
	})
	if err != nil {
		return err
	}
	return ErrWrongCurrentPassword
}
