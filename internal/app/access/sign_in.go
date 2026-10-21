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
	// MemberByEmail returns the member whose email is email whatever its
	// case, with their password hash ("" for none); ErrNotFound when none.
	MemberByEmail(ctx context.Context, email string) (access.Member, string, error)
	SetPasswordHash(ctx context.Context, member access.MemberID, hash string, at time.Time) error
	RecordSignIn(ctx context.Context, member access.MemberID, at time.Time) error
	CreateSession(ctx context.Context, s access.Session) error
	AttemptStore
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
	tx       Transactor
	store    SignInStore
	hasher   PasswordVerifier
	audit    AuditLog
	clock    clock.Clock
	ids      *id.Source
	random   io.Reader
	key      []byte
	timeouts access.SessionTimeouts
}

// NewSignIn returns the handler. Session cookies are drawn from random and
// signed with key.
func NewSignIn(tx Transactor, store SignInStore, hasher PasswordVerifier, audit AuditLog, c clock.Clock, ids *id.Source,
	random io.Reader, key []byte,
) *SignIn {
	return &SignIn{
		tx: tx, store: store, hasher: hasher, audit: audit, clock: c, ids: ids, random: random, key: key,
		timeouts: access.DefaultSessionTimeouts,
	}
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
// status instead.
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
	reserved, err := h.store.ReserveAttempt(ctx, access.NewAttempt(cmd.Email, cmd.ClientIP, h.clock.Now()))
	if err != nil {
		return SignInResult{}, err
	}
	if refusal := reserved.Refusal(); refusal != nil {
		// Refused before any hash: a locked email or a throttled source
		// takes no hashing slot from the members signing in.
		return SignInResult{}, refusal
	}
	member, hash, err := h.store.MemberByEmail(ctx, cmd.Email)
	known := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return SignInResult{}, err
	}
	ok, outdated := false, false
	// No password this long is ever set, so it is refused before any hash:
	// a huge password cannot cost one.
	if utf8.RuneCountInString(cmd.Password.Reveal()) <= access.MaxPasswordLength {
		if ok, outdated, err = h.hasher.Verify(ctx, cmd.Password, hash); err != nil {
			// Busy, or the stored hash unreadable: nothing was guessed.
			return SignInResult{}, errors.Join(err, h.store.ReleaseAttempt(context.WithoutCancel(ctx), reserved))
		}
	}
	switch {
	case !known:
		return SignInResult{}, ErrInvalidCredentials
	case !ok || hash == "":
		return SignInResult{}, h.fail(ctx, cmd, member, reserved, reasonWrongPassword)
	case member.Status != access.MemberActive:
		return SignInResult{}, h.fail(ctx, cmd, member, reserved, string(member.Status))
	}

	res, err := h.succeed(ctx, cmd, member, reserved, outdated)
	if err != nil {
		// The password proved right, so whatever failed after was no wrong
		// guess: a member who knows their password is never locked by it.
		return SignInResult{}, errors.Join(err, h.store.ReleaseAttempt(context.WithoutCancel(ctx), reserved))
	}
	return res, nil
}

// succeed opens a session for member, whose password proved right, rehashing
// it when outdated, and clears the attempt reserved in the same unit of work.
func (h *SignIn) succeed(ctx context.Context, cmd SignInCommand, member access.Member, reserved access.Reservation, outdated bool,
) (SignInResult, error) {
	var (
		rehash string
		err    error
	)
	if outdated {
		if rehash, err = h.hasher.Hash(ctx, cmd.Password); err != nil {
			return SignInResult{}, err
		}
	}
	now := h.clock.Now()
	session, cookie := access.NewSession(id.New[access.Session](h.ids), access.NewSessionInput{
		Member: member.ID, Method: access.SignInLocal, ClientIP: cmd.ClientIP, UserAgent: cmd.UserAgent,
		Timeouts: h.timeouts, Now: now,
	}, h.random, h.key)
	entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, governance.Request{
		Actor: memberActor(member), ID: cmd.RequestID,
	}, signInRecord("auth.signed_in", member, nil))
	if err != nil {
		return SignInResult{}, err
	}
	if err := h.tx.Within(ctx, func(ctx context.Context) error {
		if err := h.store.ClearAttempt(ctx, reserved); err != nil {
			return err
		}
		return h.open(ctx, session, rehash, entry)
	}); err != nil {
		return SignInResult{}, err
	}
	return SignInResult{Caller: access.SessionCaller(session, member), Cookie: cookie}, nil
}

// open stores session, the rehashed password of its member when rehash is
// not "", the member's sign-in and its audit entry, in the unit of work ctx
// carries.
func (h *SignIn) open(ctx context.Context, session access.Session, rehash string, entry governance.Entry) error {
	if rehash != "" {
		if err := h.store.SetPasswordHash(ctx, session.Member, rehash, session.CreatedAt); err != nil {
			return err
		}
	}
	if err := h.store.RecordSignIn(ctx, session.Member, session.CreatedAt); err != nil {
		return err
	}
	if err := h.store.CreateSession(ctx, session); err != nil {
		return err
	}
	return h.audit.Record(ctx, entry)
}

// fail records the failed sign-in of member, with its true reason, and the
// lockout the failure starts, and returns the refusal every failure gets.
func (h *SignIn) fail(ctx context.Context, cmd SignInCommand, member access.Member, reserved access.Reservation, reason string) error {
	now := h.clock.Now()
	req := governance.Request{Actor: memberActor(member), ID: cmd.RequestID}
	records := []governance.Record{signInRecord("auth.sign_in_failed", member, map[string]any{"reason": reason})}
	if locks, wait := reserved.Locks(); locks {
		records = append(records, governance.Record{
			Action: "auth.locked_out",
			Entity: governance.Entity{Kind: "member", ID: member.ID.String(), Key: member.Email},
			After: map[string]any{
				"email": member.Email, "failures": reserved.Failures, "retry_after_seconds": int(wait / time.Second),
			},
		})
	}
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		for _, r := range records {
			entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, req, r)
			if err != nil {
				return err
			}
			if err := h.audit.Record(ctx, entry); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return ErrInvalidCredentials
}

// memberActor is the actor of a member's own sign-in and sign-out: the
// member whose email the attempt names.
func memberActor(m access.Member) governance.Actor {
	return ActorOf(access.Caller{Method: access.MethodSession, Member: &m})
}

// signInRecord is the audit record of action on member's sign-in. It
// names the method and the email, never the password.
func signInRecord(action string, m access.Member, more map[string]any) governance.Record {
	after := map[string]any{"method": access.SignInLocal, "email": m.Email}
	for k, v := range more {
		after[k] = v
	}
	return governance.Record{
		Action: action,
		Entity: governance.Entity{Kind: "member", ID: m.ID.String(), Key: m.Email},
		After:  after,
	}
}
