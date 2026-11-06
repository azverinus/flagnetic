package identity

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

// AttemptID identifies a sign-in attempt.
type AttemptID = id.Of[Attempt]

// AttemptLifetime is how long a sign-in attempt lives from the moment a
// browser starts it: long enough to type a password and a second factor at
// the identity provider, short enough that an abandoned attempt, and the
// claims it may hold, vanish quickly.
const AttemptLifetime = 10 * time.Minute

// Intent is what a sign-in attempt is for.
type Intent string

const (
	// IntentLogin signs a person in.
	IntentLogin Intent = "login"
	// IntentLink links the subject to the member signed in, in the session
	// that started the attempt.
	IntentLink Intent = "link"
)

// Session is the identity context's view of a session of the access context.
type Session struct{}

// SessionID identifies a session.
type SessionID = id.Of[Session]

// SecretHash is the SHA-256 of a random value, the only form in which the
// attempt keeps what its browser or the identity provider will present
// back: a state, a cookie, a completion token.
type SecretHash [sha256.Size]byte

// HashSecret returns the hash an attempt keeps of value.
func HashSecret(value string) SecretHash { return sha256.Sum256([]byte(value)) }

// Equal reports whether h and other are the same hash, in constant time.
func (h SecretHash) Equal(other SecretHash) bool {
	return subtle.ConstantTimeCompare(h[:], other[:]) == 1
}

// VerifiedClaims are what an identity provider asserted of a person and the
// instance verified: signature, issuer, audience, expiry and nonce.
type VerifiedClaims struct {
	// Subject identifies the person at the provider; it never changes.
	Subject string
	Email   string
	// EmailVerified is whether the provider says it verified the email.
	EmailVerified bool
	Name          string
	// Groups are the directory groups the provider claimed for the person.
	// HasGroups is false when the claim was absent or not a list of strings
	// (Groups is then nil), so the person's earlier groups stand.
	Groups    []string
	HasGroups bool
}

// Attempt is one sign-in, from the moment a browser starts it to the moment
// it completes it. The callback only verifies what the identity provider
// answers and stores the claims on the attempt; nothing about any member
// changes until the browser that started it completes it.
type Attempt struct {
	ID       AttemptID
	Provider ProviderID
	Intent   Intent
	// StateHash, Nonce and CodeVerifier belong to the OIDC authorization code
	// flow: the state comes back on the callback and names the attempt, the
	// nonce must be in the ID token and the verifier proves the code exchange
	// is the one that started it.
	StateHash    SecretHash
	Nonce        string
	CodeVerifier string
	// RequestID is the ID of the AuthnRequest of a SAML sign-in, which the
	// response must answer; the state is the request's RelayState, the nonce
	// and the verifier are unused. Empty for OIDC.
	RequestID string
	// CookieHash binds the attempt to the browser that started it.
	CookieHash SecretHash
	// Member and Session bind a link attempt to the member who started it and
	// the session they started it in; zero for a login.
	Member  MemberID
	Session SessionID
	// ReturnTo is where the browser lands after signing in, already validated.
	ReturnTo  string
	CreatedAt time.Time
	ExpiresAt time.Time
	// CallbackAt is when the identity provider's answer was taken, once: an
	// answer replayed finds it set.
	CallbackAt *time.Time
	// CompletionHash and Claims are set by a callback that verified.
	CompletionHash *SecretHash
	Claims         *VerifiedClaims
}

// Live reports whether the attempt may still be answered or completed at now.
func (a Attempt) Live(now time.Time) bool { return now.Before(a.ExpiresAt) }

// Secrets are the random values a sign-in start draws; the values go to the
// browser and the identity provider, the attempt keeps hashes and what the
// callback needs.
type Secrets struct {
	State, Nonce, CodeVerifier, Cookie string
}

const secretBytes = 32

// NewSecrets draws a sign-in's random values from random.
func NewSecrets(random io.Reader) Secrets {
	return Secrets{State: NewToken(random), Nonce: NewToken(random), CodeVerifier: NewToken(random), Cookie: NewToken(random)}
}

// NewToken draws a random value from random: 32 bytes in unpadded base64url.
func NewToken(random io.Reader) string {
	raw := make([]byte, secretBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		// The system's randomness never fails; an exhausted test source is a
		// programming error.
		panic("identity: reading randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// NewLoginAttempt returns the attempt a browser starts to sign in through
// provider at now, returning to returnTo, which it validates.
func NewLoginAttempt(attemptID AttemptID, provider ProviderID, returnTo string, s Secrets, now time.Time) Attempt {
	return Attempt{
		ID: attemptID, Provider: provider, Intent: IntentLogin,
		StateHash: HashSecret(s.State), Nonce: s.Nonce, CodeVerifier: s.CodeVerifier, CookieHash: HashSecret(s.Cookie),
		ReturnTo: ValidReturnTo(returnTo), CreatedAt: now, ExpiresAt: now.Add(AttemptLifetime),
	}
}

// NewSAMLLoginAttempt returns the attempt a browser starts to sign in through
// a SAML provider at now: the state is the RelayState the identity provider
// hands back, and requestID the AuthnRequest its response must answer.
func NewSAMLLoginAttempt(attemptID AttemptID, provider ProviderID, returnTo, requestID string, s Secrets, now time.Time) Attempt {
	a := NewLoginAttempt(attemptID, provider, returnTo, s, now)
	a.Nonce, a.CodeVerifier, a.RequestID = "", "", requestID
	return a
}

// DefaultLinkReturnTo is where the browser lands after linking when the member
// named no page of this site: their settings, where the identities are.
const DefaultLinkReturnTo = "/settings"

// NewLinkAttempt returns the attempt a browser starts to link the subject of
// provider to member, signed in in session, at now, returning to returnTo
// when it is a path of this site and to the settings otherwise. requestID is
// the AuthnRequest's of a SAML provider, empty for OIDC.
func NewLinkAttempt(attemptID AttemptID, provider ProviderID, member MemberID, session SessionID, returnTo, requestID string,
	s Secrets, now time.Time,
) Attempt {
	if returnTo == "" || ValidReturnTo(returnTo) == "/" && returnTo != "/" {
		returnTo = DefaultLinkReturnTo
	}
	a := NewLoginAttempt(attemptID, provider, returnTo, s, now)
	a.Intent, a.Member, a.Session = IntentLink, member, session
	if requestID != "" {
		a.Nonce, a.CodeVerifier, a.RequestID = "", "", requestID
	}
	return a
}
