package identity

import (
	"crypto/sha256"
	"crypto/subtle"
	"io"
	"strings"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// SCIMTokenID identifies a SCIM token.
type SCIMTokenID = id.Of[SCIMToken]

const (
	scimTokenPrefix     = "fn_scim_" //nolint:gosec // a prefix, not a credential
	scimTokenBodyLength = 40
	scimHintBodyLength  = 4
)

// SCIMTokenHash is the SHA-256 of a SCIM token's whole value. The store seals
// it under the instance's secret key rather than index it, so a database dump
// is no offline guessing target.
type SCIMTokenHash [sha256.Size]byte

// Equal reports whether h and other are the same hash, in constant time.
func (h SCIMTokenHash) Equal(other SCIMTokenHash) bool {
	return subtle.ConstantTimeCompare(h[:], other[:]) == 1
}

// SCIMToken is the credential an identity provider presents to /scim/v2. It
// belongs for life to one auth provider and reaches only that provider's
// provisioned members and groups.
type SCIMToken struct {
	ID       SCIMTokenID
	Provider ProviderID
	// Hint is the value's prefix and first four characters, enough to tell
	// tokens apart and to name one in the audit log.
	Hint string
	Hash SCIMTokenHash
	// ExpiresAt is nil for a token that does not expire.
	ExpiresAt *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
	// LastUsedAt is when the token was last presented, as last recorded; nil
	// until then.
	LastUsedAt *time.Time
}

// SCIMTokenValue is a newly drawn SCIM token value: shown once, then only its
// hint and hash remain.
type SCIMTokenValue struct {
	Value secret.String
	Hint  string
	Hash  SCIMTokenHash
}

// NewSCIMToken returns a SCIM token bound to provider and its value, drawn
// from random: the prefix fn_scim_ and 40 base62 characters, about 238 bits.
// A token that expires does so in the future of now.
func NewSCIMToken(tokenID SCIMTokenID, provider ProviderID, expiresAt *time.Time, now time.Time, random io.Reader) (SCIMToken, SCIMTokenValue, error) {
	if expiresAt != nil && !expiresAt.After(now) {
		return SCIMToken{}, SCIMTokenValue{}, apperrors.Invalid("invalid_scim_token", "the SCIM token is not valid", apperrors.Violation{
			Pointer: "/expires_at", Code: "in_past", Detail: "a token's expiry lies in the future",
		})
	}
	v := scimTokenPrefix + secret.Base62(random, scimTokenBodyLength)
	value := SCIMTokenValue{
		Value: secret.New(v), Hint: v[:len(scimTokenPrefix)+scimHintBodyLength], Hash: sha256.Sum256([]byte(v)),
	}
	return SCIMToken{
		ID: tokenID, Provider: provider, Hint: value.Hint, Hash: value.Hash, ExpiresAt: expiresAt, CreatedAt: now,
	}, value, nil
}

// ErrInvalidSCIMToken refuses a bearer value that is no SCIM token,
// malformed, unknown, expired, revoked or of a disabled provider alike, so a
// caller cannot tell which.
var ErrInvalidSCIMToken = apperrors.Unauthenticated("invalid_scim_token", "the SCIM token is not accepted")

// HashSCIMTokenValue returns the hash of a well-formed SCIM token value; any
// other value is refused without a lookup.
func HashSCIMTokenValue(value string) (SCIMTokenHash, error) {
	body, ok := strings.CutPrefix(value, scimTokenPrefix)
	if !ok || len(body) != scimTokenBodyLength || !secret.IsBase62(body) {
		return SCIMTokenHash{}, ErrInvalidSCIMToken
	}
	return sha256.Sum256([]byte(value)), nil
}

// SCIMTokenUseInterval is how often a SCIM token's use is recorded at most.
const SCIMTokenUseInterval = time.Minute

// UseToRecord reports whether presenting the token at now is a use worth
// recording: its first, or a minute or more after the last one recorded.
func (t SCIMToken) UseToRecord(now time.Time) bool {
	return t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= SCIMTokenUseInterval
}

// Usable reports whether the token is accepted at now: it is not revoked,
// has not expired, and its provider is enabled.
func (t SCIMToken) Usable(now time.Time, providerEnabled bool) bool {
	if t.RevokedAt != nil || !providerEnabled {
		return false
	}
	return t.ExpiresAt == nil || now.Before(*t.ExpiresAt)
}
