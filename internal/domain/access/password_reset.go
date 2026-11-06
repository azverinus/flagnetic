package access

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// PasswordResetID identifies a password reset.
type PasswordResetID = id.Of[PasswordReset]

// PasswordResetHash is the SHA-256 of a password reset's secret, the only
// form in which the secret is stored.
type PasswordResetHash [sha256.Size]byte

// PasswordResetLifetime is how long a password reset can be completed: long
// enough to reach the member through whoever passes the link on, short
// enough that a link lying in a chat or a mailbox stops working soon.
const PasswordResetLifetime = time.Hour

// PasswordResetRetention is how long a password reset is kept once it can no
// longer be completed before the retention worker deletes it, so the table
// does not grow forever while a recent link can still be looked into.
const PasswordResetRetention = 30 * 24 * time.Hour

const resetSecretLength = 32

// PasswordReset is the one-time link that lets an active member set a new
// password.
type PasswordReset struct {
	ID     PasswordResetID
	Member MemberID
	Hash   PasswordResetHash
	// IssuedBy is the instance admin who issued the reset; zero for one the
	// member asked for themselves.
	IssuedBy  MemberID
	CreatedAt time.Time
	ExpiresAt time.Time
	// ConsumedAt is when the reset stopped being completable before it
	// expired: completed, or ended by the completion of another, by a
	// password change or by its member's deactivation. Nil while it is open.
	ConsumedAt *time.Time
}

// NewPasswordReset issues a password reset of member, by issuedBy (zero for
// the member themselves), and returns it with its secret: 32 bytes drawn
// from random in unpadded base64url, shown once to whoever the reset is
// handed to and never stored.
func NewPasswordReset(resetID PasswordResetID, member, issuedBy MemberID, now time.Time, random io.Reader) (PasswordReset, secret.String) {
	raw := make([]byte, resetSecretLength)
	if _, err := io.ReadFull(random, raw); err != nil {
		// The system's randomness never fails; an exhausted test source is
		// a programming error.
		panic("access: reading randomness: " + err.Error())
	}
	return PasswordReset{
		ID: resetID, Member: member, Hash: sha256.Sum256(raw), IssuedBy: issuedBy,
		CreatedAt: now, ExpiresAt: now.Add(PasswordResetLifetime),
	}, secret.New(base64.RawURLEncoding.EncodeToString(raw))
}

// ParsePasswordResetSecret returns the hash a password reset's secret is
// stored under; false for a value no reset was ever issued with, which needs
// no lookup.
func ParsePasswordResetSecret(value string) (PasswordResetHash, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(raw) != resetSecretLength {
		return PasswordResetHash{}, false
	}
	return sha256.Sum256(raw), true
}

// Open reports whether the reset can be completed at now by holder, its
// member: not consumed, not expired, and holder active. A member who was
// deactivated since the reset was issued has it consumed with their
// deactivation, and one still invited never holds a reset, so the status
// check only makes certain no link outlives either.
func (r PasswordReset) Open(now time.Time, holder Member) bool {
	return r.ConsumedAt == nil && now.Before(r.ExpiresAt) && holder.Status == MemberActive
}

// ErrPasswordResetGone answers a password reset that cannot be completed,
// whatever the reason, so a link tells nobody whether it ever existed.
var ErrPasswordResetGone = apperrors.Gone("reset_gone",
	"this password reset was never issued, has been used or ended, or has expired")

// ErrMemberNotActive refuses issuing a password reset for a member who is
// still invited: they hold no password to reset, and their invitation is the
// way to give them one.
var ErrMemberNotActive = apperrors.Conflict("member_not_active",
	"only an active member can be given a password reset; issue an invited member's invitation again instead")

// CheckPasswordResetIssuable refuses a password reset for a member who is
// not active: ErrMemberNotActive for one still invited, ErrMemberDeactivated
// for one deactivated.
func CheckPasswordResetIssuable(m Member) error {
	switch m.Status {
	case MemberActive:
		return nil
	case MemberDeactivated:
		return ErrMemberDeactivated
	default:
		return ErrMemberNotActive
	}
}

// PasswordResetCutoffs select the password resets the retention worker
// purges.
type PasswordResetCutoffs struct {
	// EndedBy purges a password reset consumed, or expired, at or before it.
	EndedBy time.Time
}

// PasswordResetCutoffsAt returns the cutoffs at now.
func PasswordResetCutoffsAt(now time.Time) PasswordResetCutoffs {
	return PasswordResetCutoffs{EndedBy: now.Add(-PasswordResetRetention)}
}
