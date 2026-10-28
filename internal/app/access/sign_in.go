package access

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// PasswordVerifier checks a password against the string stored for it.
type PasswordVerifier interface {
	PasswordHasher
	// Verify reports whether password matches encoded, and whether encoded
	// was made under parameters other than the current ones. An empty
	// encoded is verified against a hash no password matches, at the same
	// cost, so an unknown email takes as long as a wrong password.
	Verify(ctx context.Context, password secret.String, encoded string) (ok, outdated bool, err error)
}

// SignInStore is what signing in needs of the stored members and sessions.
type SignInStore interface {
	passwordStore
	// LockMemberPassword returns member with their password hash ("" for
	// none), locked until the unit of work ends, so no password change
	// commits between the check and the session it opens; ErrNotFound when
	// there is none.
	LockMemberPassword(ctx context.Context, member access.MemberID) (access.Member, string, error)
	SetPasswordHash(ctx context.Context, member access.MemberID, hash string, at time.Time) error
	RecordSignIn(ctx context.Context, member access.MemberID, at time.Time) error
	CreateSession(ctx context.Context, s access.Session) error
}

// AttemptStore counts the failed attempts at a password, per email and per
// source, which lock an email and throttle a source.
type AttemptStore interface {
	// ReserveAttempt counts a as a failure before its password is verified,
	// in one atomic step, and returns what it found.
	ReserveAttempt(ctx context.Context, a access.Attempt) (access.Reservation, error)
	// ClearAttempt takes back the failure r reserved, and ends the email's
	// consecutive failures, once the password proved right.
	ClearAttempt(ctx context.Context, r access.Reservation) error
	// ReleaseAttempt takes back the failure r reserved when the attempt
	// guessed nothing: its password could not be verified, or proved right.
	ReleaseAttempt(ctx context.Context, r access.Reservation) error
}

// ErrInvalidCredentials refuses a sign-in whatever went wrong with the email
// or the password, so nobody learns from it which emails exist, nor whether
// the password of a member who may not sign in was right.
var ErrInvalidCredentials = apperrors.Unauthenticated("invalid_credentials", "the email or password is wrong")

// SignIn opens a session for a member who proves who they are with their
// email and password.
type SignIn struct {
	d SignInDeps
}

// SignInDeps are what signing in works with.
type SignInDeps struct {
	Tx    Transactor
	Store SignInStore
	// Settings give the timeouts a session opens with.
	Settings AuthSettingsReader
	Hasher   PasswordVerifier
	Audit    AuditLog
	Clock    clock.Clock
	IDs      *id.Source
	// Random draws session cookies, which are signed with Key.
	Random io.Reader
	Key    []byte
}

// NewSignIn returns the handler.
func NewSignIn(d SignInDeps) *SignIn {
	return &SignIn{d: d}
}

// SignInCommand is one attempt to sign in.
type SignInCommand struct {
	Email    string
	Password secret.String
	// ClientIP and UserAgent describe the device signing in.
	ClientIP  netip.Addr
	UserAgent string
	// RequestID is the request attempting, for the audit entries.
	RequestID string
}

// SignInResult is the session opened and the value of its cookie, which is
// handed to the browser and never stored.
type SignInResult struct {
	Caller access.Caller
	Cookie secret.String
}

// reasonWrongPassword is why a sign-in failed when its password was wrong,
// recorded in its audit entry while the caller is told only
// invalid_credentials; a member who is not active is recorded with their
// status instead when the password was right, or when they hold none.
const reasonWrongPassword = "wrong_password"

// Handle signs the member in, opening a fresh session, or refuses with
// invalid_credentials. The attempt is counted as a failure of its email and
// its source before the password is verified, so attempts made at once
// cannot all pass the lockout; a locked email (locked) or a throttled source
// (rate_limited) is refused before any hash, and a right password takes the
// failure back. An unknown email is verified against a hash no
// password matches, so it takes as long as a wrong password; a member who
// is not active is refused like a wrong password even when the password is
// right. Failures of a known member are audited, attempts on unknown emails
// are not: the log records people, not an attacker's input.
func (h *SignIn) Handle(ctx context.Context, cmd SignInCommand) (SignInResult, error) {
	a, err := attemptPassword(ctx, h.d.Tx, h.d.Store, h.d.Hasher, access.NewAttempt(cmd.Email, cmd.ClientIP, h.d.Clock.Now()), cmd.Password)
	if err != nil {
		return SignInResult{}, err
	}
	member, reserved := a.member, a.reserved
	switch {
	case !a.known:
		return SignInResult{}, ErrInvalidCredentials
	case a.hash == "" && member.Status != access.MemberActive:
		// An invited member holds no password until they accept: no
		// password was wrong, they may not sign in yet.
		return SignInResult{}, h.fail(ctx, cmd, member, reserved, string(member.Status))
	case !a.right():
		return SignInResult{}, h.fail(ctx, cmd, member, reserved, reasonWrongPassword)
	case member.Status != access.MemberActive:
		return SignInResult{}, h.fail(ctx, cmd, member, reserved, string(member.Status))
	}

	res, err := h.succeed(ctx, cmd, a)
	if err != nil {
		// The password proved right, so whatever failed after was no wrong
		// guess: a member who knows their password is never locked by it.
		return SignInResult{}, h.release(ctx, reserved, err)
	}
	return res, nil
}

// passwordStore is what checking a password needs: the member an email
// names, and the failed attempts counted against it.
type passwordStore interface {
	// MemberByEmail returns the member whose email is email whatever its
	// case, with their password hash ("" for none); ErrNotFound when none.
	MemberByEmail(ctx context.Context, email string) (access.Member, string, error)
	AttemptStore
}

// passwordAttempt is a password verified against the member its email
// names, with the failure reserved for it, which the caller keeps for a
// wrong password and takes back otherwise.
type passwordAttempt struct {
	reserved access.Reservation
	member   access.Member
	// known is whether the email names a member.
	known bool
	// hash is the member's stored password hash, "" when they hold none.
	hash string
	// ok is whether the password matched hash, and outdated whether hash
	// was made under parameters other than the current ones.
	ok, outdated bool
}

// right reports whether the password proved right: it matched a password
// the member holds.
func (a passwordAttempt) right() bool { return a.ok && a.hash != "" }

// attemptPassword counts attempt as a failure of its email and its source
// before its password is verified, so attempts made at once cannot all pass
// the lockout, then verifies password against the member the email names.
// A locked email (locked) or a throttled source (rate_limited) is refused
// before any hash: it takes no hashing slot from the members signing in
// and, since no password was checked, counts as no failure either. An
// unknown email is verified against a hash no password matches, so it takes
// as long as a wrong password; a password longer than any ever set is
// refused before any hash, so a huge one cannot cost one. On an error,
// nothing was guessed and the failure is taken back.
func attemptPassword(ctx context.Context, tx Transactor, store passwordStore, hasher PasswordVerifier,
	attempt access.Attempt, password secret.String,
) (passwordAttempt, error) {
	reserved, err := store.ReserveAttempt(ctx, attempt)
	if err != nil {
		return passwordAttempt{}, err
	}
	a := passwordAttempt{reserved: reserved}
	if refusal := reserved.Refusal(); refusal != nil {
		return a, releaseAttempt(ctx, tx, store, reserved, refusal)
	}
	a.member, a.hash, err = store.MemberByEmail(ctx, attempt.Email)
	a.known = err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return a, releaseAttempt(ctx, tx, store, reserved, err)
	}
	if utf8.RuneCountInString(password.Reveal()) <= access.MaxPasswordLength {
		if a.ok, a.outdated, err = hasher.Verify(ctx, password, a.hash); err != nil {
			// Busy, or the stored hash unreadable: nothing was guessed.
			return a, releaseAttempt(ctx, tx, store, reserved, err)
		}
	}
	return a, nil
}

// release takes back the failure reserved for an attempt that guessed
// nothing, and returns why the attempt ended, err, with any error taking it
// back.
func (h *SignIn) release(ctx context.Context, reserved access.Reservation, err error) error {
	return releaseAttempt(ctx, h.d.Tx, h.d.Store, reserved, err)
}

// attemptReleaser takes back a reserved failure.
type attemptReleaser interface {
	ReleaseAttempt(ctx context.Context, r access.Reservation) error
}

// releaseAttempt takes back the failure reserved, and returns err, with any
// error taking it back. It runs even when the request was cancelled, so an
// abandoned attempt leaves no failure behind, and in a unit of work of its
// own, so the source and the email are taken back together.
func releaseAttempt(ctx context.Context, tx Transactor, store attemptReleaser, reserved access.Reservation, err error) error {
	return errors.Join(err, tx.Within(context.WithoutCancel(ctx), func(ctx context.Context) error {
		return store.ReleaseAttempt(ctx, reserved)
	}))
}

// succeed opens a session for the member whose password a proved right,
// rehashing it when outdated, and clears the attempt reserved in the same
// unit of work. The password is verified outside any unit of work, so a
// change of it may commit before the session is opened: the member is
// locked and read again, and a password or status no longer the one
// verified refuses the sign-in, so the old password opens no session and
// the rehash never writes it over the new one.
func (h *SignIn) succeed(ctx context.Context, cmd SignInCommand, a passwordAttempt) (SignInResult, error) {
	var (
		rehash string
		err    error
	)
	if a.outdated {
		if rehash, err = h.d.Hasher.Hash(ctx, cmd.Password); err != nil {
			return SignInResult{}, err
		}
	}
	settings, err := h.d.Settings.AuthSettings(ctx)
	if err != nil {
		return SignInResult{}, err
	}
	member := a.member
	now := h.d.Clock.Now()
	session, cookie := access.NewSession(id.New[access.Session](h.d.IDs), access.NewSessionInput{
		Member: member.ID, Method: access.SignInLocal, ClientIP: cmd.ClientIP, UserAgent: cmd.UserAgent,
		Timeouts: settings.SessionTimeouts(), Now: now,
	}, h.d.Random, h.d.Key)
	entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, governance.Request{
		Actor: memberActor(member), ID: cmd.RequestID,
	}, authRecord("auth.signed_in", member, nil))
	if err != nil {
		return SignInResult{}, err
	}
	if err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.stillHolds(ctx, member, a.hash); err != nil {
			return err
		}
		if err := h.d.Store.ClearAttempt(ctx, a.reserved); err != nil {
			return err
		}
		return h.open(ctx, session, rehash, entry)
	}); err != nil {
		return SignInResult{}, err
	}
	return SignInResult{Caller: access.SessionCaller(session, member), Cookie: cookie}, nil
}

// stillHolds locks member and refuses, as invalid credentials, a sign-in
// whose verified password hash is no longer the one stored, or whose member
// is no longer active, in the unit of work ctx carries.
func (h *SignIn) stillHolds(ctx context.Context, member access.Member, verified string) error {
	current, hash, err := h.d.Store.LockMemberPassword(ctx, member.ID)
	if errors.Is(err, ErrNotFound) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if hash != verified || current.Status != access.MemberActive {
		return ErrInvalidCredentials
	}
	return nil
}

// open stores session, the rehashed password of its member when rehash is
// not "", the member's sign-in and its audit entry, in the unit of work ctx
// carries.
func (h *SignIn) open(ctx context.Context, session access.Session, rehash string, entry governance.Entry) error {
	if rehash != "" {
		if err := h.d.Store.SetPasswordHash(ctx, session.Member, rehash, session.CreatedAt); err != nil {
			return err
		}
	}
	if err := h.d.Store.RecordSignIn(ctx, session.Member, session.CreatedAt); err != nil {
		return err
	}
	if err := h.d.Store.CreateSession(ctx, session); err != nil {
		return err
	}
	return h.d.Audit.Record(ctx, entry)
}

// fail records the failed sign-in of member, with its true reason, and the
// lockout the failure starts, and returns the refusal every failure gets.
func (h *SignIn) fail(ctx context.Context, cmd SignInCommand, member access.Member, reserved access.Reservation, reason string) error {
	now := h.d.Clock.Now()
	req := governance.Request{Actor: memberActor(member), ID: cmd.RequestID}
	records := []governance.Record{authRecord("auth.sign_in_failed", member, map[string]any{"reason": reason})}
	if locked, locks := lockedOut(member, reserved); locks {
		records = append(records, locked)
	}
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		return recordAll(ctx, h.d.Audit, h.d.IDs, now, req, records...)
	})
	if err != nil {
		return err
	}
	return ErrInvalidCredentials
}

// lockedOut is the audit record of the lockout of member's email that the
// failure reserved starts, and whether it starts one.
func lockedOut(member access.Member, reserved access.Reservation) (governance.Record, bool) {
	locks, wait := reserved.Locks()
	if !locks {
		return governance.Record{}, false
	}
	return governance.Record{
		Action: "auth.locked_out",
		Entity: memberEntity(member),
		After: map[string]any{
			"email": member.Email, "failures": reserved.Failures, "retry_after_seconds": int(wait / time.Second),
		},
	}, true
}

// memberActor is the actor of a member's own sign-in and sign-out: the
// member whose email the attempt names.
func memberActor(m access.Member) governance.Actor {
	return ActorOf(access.Caller{Method: access.MethodSession, Member: &m})
}

// authRecord is the audit record of action, a sign-in, a failed one or a
// sign-out, of member. It names the method and the email, never the
// password.
func authRecord(action string, m access.Member, more map[string]any) governance.Record {
	after := map[string]any{"method": access.SignInLocal, "email": m.Email}
	for k, v := range more {
		after[k] = v
	}
	return governance.Record{
		Action: action,
		Entity: memberEntity(m),
		After:  after,
	}
}

// memberEntity is m as the entity of an audit entry, keyed by their email.
func memberEntity(m access.Member) governance.Entity {
	return governance.Entity{Kind: "member", ID: m.ID.String(), Key: m.Email}
}
