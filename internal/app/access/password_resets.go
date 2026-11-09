package access

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// PasswordResetStore keeps password resets.
type PasswordResetStore interface {
	CreatePasswordReset(ctx context.Context, reset access.PasswordReset) error
	// ConsumePasswordResets ends every open password reset of member, and
	// returns how many it ended.
	ConsumePasswordResets(ctx context.Context, member access.MemberID, at time.Time) (int, error)
}

// IssuedPasswordReset is a password reset just issued, with its secret,
// shown once to whoever issued it.
type IssuedPasswordReset struct {
	access.PasswordReset
	Secret secret.String
}

// IssuePasswordResetStore is what issuing a password reset needs.
type IssuePasswordResetStore interface {
	PasswordResetStore
	// LockMember returns the member, locked until the unit of work ends,
	// and whether they hold a password; ErrNotFound when none has the
	// identifier.
	LockMember(ctx context.Context, member access.MemberID) (access.Member, bool, error)
}

// IssuePasswordReset hands an instance admin a password reset to pass on to
// an active member.
type IssuePasswordReset struct {
	tx     Transactor
	store  IssuePasswordResetStore
	audit  AuditLog
	clock  clock.Clock
	ids    *id.Source
	random io.Reader
}

// NewIssuePasswordReset returns the handler; random draws reset secrets.
func NewIssuePasswordReset(tx Transactor, store IssuePasswordResetStore, audit AuditLog, c clock.Clock, ids *id.Source, random io.Reader,
) *IssuePasswordReset {
	return &IssuePasswordReset{tx: tx, store: store, audit: audit, clock: c, ids: ids, random: random}
}

// IssuePasswordResetCommand names the member to issue a password reset for.
type IssuePasswordResetCommand struct {
	// Caller is the instance admin issuing.
	Caller  access.Caller
	Member  access.MemberID
	Request governance.Request
}

// Handle issues a password reset for the member, recording
// password.reset_issued without its secret, which the answer carries and
// nothing else does. An unknown member is not found; one still invited is
// refused member_not_active, as there is no password to reset until they
// accept their invitation, and one deactivated member_deactivated. The
// member's earlier password resets stay open.
func (h *IssuePasswordReset) Handle(ctx context.Context, cmd IssuePasswordResetCommand) (IssuedPasswordReset, error) {
	var res IssuedPasswordReset
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		m, _, err := h.store.LockMember(ctx, cmd.Member)
		if errors.Is(err, ErrNotFound) {
			return ErrMemberNotFound
		}
		if err != nil {
			return err
		}
		if err = access.CheckPasswordResetIssuable(m); err != nil {
			return err
		}
		now := h.clock.Now()
		reset, s := access.NewPasswordReset(id.New[access.PasswordReset](h.ids), m.ID, creatorOf(cmd.Caller), now, h.random)
		if err = h.store.CreatePasswordReset(ctx, reset); err != nil {
			return err
		}
		res = IssuedPasswordReset{PasswordReset: reset, Secret: s}
		return recordAll(ctx, h.audit, h.ids, now, cmd.Request, governance.Record{
			Action: "password.reset_issued", Entity: memberEntity(m),
			After: map[string]any{"email": m.Email, "expires_at": reset.ExpiresAt.UTC().Format(time.RFC3339)},
		})
	})
	if err != nil {
		return IssuedPasswordReset{}, err
	}
	return res, nil
}

// PasswordResetReader finds password resets by the hash of their secret.
type PasswordResetReader interface {
	// PasswordResetByHash returns the password reset and its member;
	// ErrNotFound when no password reset has the hash.
	PasswordResetByHash(ctx context.Context, hash access.PasswordResetHash) (access.PasswordReset, access.Member, error)
}

// PasswordResetView is what the page a password reset opens shows.
type PasswordResetView struct {
	// MaskedEmail is the member's email with most of its local part hidden.
	MaskedEmail string
	DisplayName string
	ExpiresAt   time.Time
}

// GetPasswordReset answers what a password reset's link opens, before anyone
// is signed in.
type GetPasswordReset struct {
	store PasswordResetReader
	clock clock.Clock
}

// NewGetPasswordReset returns the query.
func NewGetPasswordReset(store PasswordResetReader, c clock.Clock) *GetPasswordReset {
	return &GetPasswordReset{store: store, clock: c}
}

// Handle returns the password reset the secret opens; reset_gone when it
// opens none that can be completed, whatever the reason.
func (h *GetPasswordReset) Handle(ctx context.Context, s secret.String) (PasswordResetView, error) {
	reset, m, err := openPasswordReset(ctx, h.store, s, h.clock.Now())
	if err != nil {
		return PasswordResetView{}, err
	}
	return PasswordResetView{MaskedEmail: access.MaskEmail(m.Email), DisplayName: m.DisplayName, ExpiresAt: reset.ExpiresAt}, nil
}

// openPasswordReset returns the password reset s opens and its member, when
// it can be completed at now; ErrPasswordResetGone otherwise.
func openPasswordReset(ctx context.Context, store PasswordResetReader, s secret.String, now time.Time,
) (access.PasswordReset, access.Member, error) {
	hash, ok := access.ParsePasswordResetSecret(s.Reveal())
	if !ok {
		return access.PasswordReset{}, access.Member{}, access.ErrPasswordResetGone
	}
	reset, m, err := store.PasswordResetByHash(ctx, hash)
	switch {
	case errors.Is(err, ErrNotFound):
		return access.PasswordReset{}, access.Member{}, access.ErrPasswordResetGone
	case err != nil:
		return access.PasswordReset{}, access.Member{}, err
	case !reset.Open(now, m):
		return access.PasswordReset{}, access.Member{}, access.ErrPasswordResetGone
	}
	return reset, m, nil
}

// CompletePasswordResetStore is what completing a password reset needs.
type CompletePasswordResetStore interface {
	PasswordResetReader
	PasswordResetStore
	InvitationStore
	// LockMember returns the member, locked until the unit of work ends;
	// ErrNotFound when none has the identifier.
	LockMember(ctx context.Context, member access.MemberID) (access.Member, bool, error)
	// LockPasswordResetByHash returns the password reset, locked until the
	// unit of work ends, and its member; ErrNotFound when none has the hash.
	LockPasswordResetByHash(ctx context.Context, hash access.PasswordResetHash) (access.PasswordReset, access.Member, error)
	SetPasswordHash(ctx context.Context, member access.MemberID, hash string, at time.Time) error
	// RevokeMemberSessions ends every session of member not ended yet, and
	// returns how many it ended.
	RevokeMemberSessions(ctx context.Context, member access.MemberID, at time.Time) (int, error)
	// ClearSignInFailures ends the consecutive failed sign-ins counted
	// under email, which lifts any lockout of it.
	ClearSignInFailures(ctx context.Context, email string) error
	RecordSignIn(ctx context.Context, member access.MemberID, at time.Time) error
	knownSourceRecorder
	CreateSession(ctx context.Context, s access.Session) error
	// ReserveSourceAttempt counts a as a failure of its source alone,
	// before what it presents is checked, and returns what it found.
	ReserveSourceAttempt(ctx context.Context, a access.Attempt) (access.Reservation, error)
	// ReleaseAttempt takes back the failure r reserved.
	ReleaseAttempt(ctx context.Context, r access.Reservation) error
}

// CompletePasswordReset lets a member set a new password with a password
// reset, and signs them in.
type CompletePasswordReset struct {
	d CompletePasswordResetDeps
}

// CompletePasswordResetDeps are what completing a password reset works with.
type CompletePasswordResetDeps struct {
	Tx    Transactor
	Store CompletePasswordResetStore
	// Settings give the password policy's minimum length and the timeouts
	// the session opens with.
	Settings AuthSettingsReader
	Hasher   PasswordHasher
	Audit    AuditLog
	Clock    clock.Clock
	IDs      *id.Source
	// Random draws session cookies, which are signed with Key.
	Random io.Reader
	Key    []byte
}

// NewCompletePasswordReset returns the handler.
func NewCompletePasswordReset(d CompletePasswordResetDeps) *CompletePasswordReset {
	return &CompletePasswordReset{d: d}
}

// CompletePasswordResetCommand is one attempt at completing a password reset.
type CompletePasswordResetCommand struct {
	Secret   secret.String
	Password secret.String
	// ClientIP and UserAgent describe the device signing in.
	ClientIP  netip.Addr
	UserAgent string
	RequestID string
}

// Handle sets the member's new password, clears the lockout of their email,
// revokes every session of theirs, ends every open password reset and
// invitation of theirs, this one included, and opens a session, in one unit
// of work, recording password.reset and auth.signed_in. The attempt counts
// as a failure of its source before the secret is looked up, so a source
// hammering completion is throttled with its sign-ins; a throttled source is
// refused rate_limited before anything else. A secret that opens no password
// reset keeps its failure and is answered reset_gone; one that does takes it
// back, whatever happens next. A password the auth settings refuse the
// member's email is refused, local_login_disabled or sso_required naming the
// provider, and the reset stays open. The password policy applies
// (weak_password), before any hash.
func (h *CompletePasswordReset) Handle(ctx context.Context, cmd CompletePasswordResetCommand) (SignInResult, error) {
	now := h.d.Clock.Now()
	reserved, err := h.d.Store.ReserveSourceAttempt(ctx, access.NewAttempt("", cmd.ClientIP, now))
	if err != nil {
		return SignInResult{}, err
	}
	if refusal := reserved.Refusal(); refusal != nil {
		return SignInResult{}, h.release(ctx, reserved, refusal)
	}
	reset, m, err := openPasswordReset(ctx, h.d.Store, cmd.Secret, now)
	if errors.Is(err, access.ErrPasswordResetGone) {
		return SignInResult{}, err
	}
	if err != nil {
		return SignInResult{}, h.release(ctx, reserved, err)
	}
	res, err := h.complete(ctx, cmd, reset, m, reserved)
	if err != nil {
		return SignInResult{}, h.release(ctx, reserved, err)
	}
	return res, nil
}

// release takes back the source's failure reserved for an attempt that
// guessed nothing, and returns err, with any error taking it back.
func (h *CompletePasswordReset) release(ctx context.Context, reserved access.Reservation, err error) error {
	return releaseAttempt(ctx, h.d.Tx, h.d.Store, reserved, err)
}

// complete sets the password of m, whose reset opened, and signs them in,
// taking back the source's failure in the same unit of work.
func (h *CompletePasswordReset) complete(ctx context.Context, cmd CompletePasswordResetCommand, opened access.PasswordReset,
	member access.Member, reserved access.Reservation,
) (SignInResult, error) {
	settings, err := h.d.Settings.AuthSettings(ctx)
	if err != nil {
		return SignInResult{}, err
	}
	if err = settings.PasswordRefusal(member.Email); err != nil {
		return SignInResult{}, err
	}
	if err = access.CheckPassword(cmd.Password.Reveal(), member.Email, settings.PasswordMinLength); err != nil {
		return SignInResult{}, err
	}
	hash, err := h.d.Hasher.Hash(ctx, cmd.Password)
	if err != nil {
		return SignInResult{}, err
	}
	now := h.d.Clock.Now()
	var res SignInResult
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		res, err = h.reset(ctx, cmd, opened, hash, settings, reserved, now)
		return err
	})
	if err != nil {
		return SignInResult{}, err
	}
	return res, nil
}

// reset stores the password hash of the member of opened, ends their
// sessions, password resets and invitations, clears the lockout of their
// email and opens their session, in the unit of work ctx carries;
// reset_gone when the password reset was completed, ended or expired since
// it was read, or its member left the active status. The member is locked
// before their password reset, the order deactivating them and changing
// their password take, so none of them waits on another in a deadlock.
func (h *CompletePasswordReset) reset(ctx context.Context, cmd CompletePasswordResetCommand, opened access.PasswordReset, hash string,
	settings access.AuthSettings, reserved access.Reservation, now time.Time,
) (SignInResult, error) {
	st := h.d.Store
	if _, _, err := st.LockMember(ctx, opened.Member); err != nil {
		return SignInResult{}, err
	}
	reset, m, err := st.LockPasswordResetByHash(ctx, opened.Hash)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return SignInResult{}, err
	}
	if err != nil || !reset.Open(now, m) {
		// Completed, ended or deactivated since it was read.
		return SignInResult{}, access.ErrPasswordResetGone
	}
	if err = st.SetPasswordHash(ctx, m.ID, hash, now); err != nil {
		return SignInResult{}, err
	}
	if err = st.ClearSignInFailures(ctx, m.Email); err != nil {
		return SignInResult{}, err
	}
	revoked, err := st.RevokeMemberSessions(ctx, m.ID, now)
	if err != nil {
		return SignInResult{}, err
	}
	if _, err = st.ConsumePasswordResets(ctx, m.ID, now); err != nil {
		return SignInResult{}, err
	}
	if _, err = st.ConsumeInvitations(ctx, m.ID, now); err != nil {
		return SignInResult{}, err
	}
	if err = st.ReleaseAttempt(ctx, reserved); err != nil {
		return SignInResult{}, err
	}
	session, cookie := access.NewSession(id.New[access.Session](h.d.IDs), access.NewSessionInput{
		Member: m.ID, Method: access.SignInLocal, ClientIP: cmd.ClientIP, UserAgent: cmd.UserAgent,
		Timeouts: settings.SessionTimeouts(), Now: now,
	}, h.d.Random, h.d.Key)
	if err = st.RecordSignIn(ctx, m.ID, now); err != nil {
		return SignInResult{}, err
	}
	// Completing signs the member in, so where they completed from is a
	// source they sign in from, as after a sign-in.
	if err = recordKnownSource(ctx, st, m.ID, cmd.ClientIP, now); err != nil {
		return SignInResult{}, err
	}
	if err = st.CreateSession(ctx, session); err != nil {
		return SignInResult{}, err
	}
	res := SignInResult{Caller: access.SessionCaller(session, m), Cookie: cookie}
	return res, recordAll(ctx, h.d.Audit, h.d.IDs, now, governance.Request{Actor: memberActor(m), ID: cmd.RequestID},
		governance.Record{
			Action: "password.reset", Entity: memberEntity(m),
			After: map[string]any{"email": m.Email, "sessions_revoked": revoked},
		},
		authRecord("auth.signed_in", m, nil))
}
