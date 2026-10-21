package access

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/netip"
	"strings"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// SessionID identifies a session.
type SessionID = id.Of[Session]

// SessionHash is the SHA-256 of the random part of a session cookie, the
// only form in which the session's secret is stored.
type SessionHash [sha256.Size]byte

// SignInMethod is how a member proved who they are when a session opened.
type SignInMethod string

// SignInLocal is signing in with an email and a password. Identity
// providers join it with single sign-on.
const SignInLocal SignInMethod = "local"

// SessionTimeouts end a session: after Idle without a request, and after
// Absolute in all, whichever comes first.
type SessionTimeouts struct {
	Idle     time.Duration
	Absolute time.Duration
}

// DefaultSessionTimeouts end a session a day after its last request or
// thirty days after it opened, so a forgotten browser does not stay signed
// in.
var DefaultSessionTimeouts = SessionTimeouts{Idle: 24 * time.Hour, Absolute: 30 * 24 * time.Hour}

// Bounds of what a session keeps about its device.
const (
	maxUserAgentBytes = 256
	// touchInterval is how stale the last request of a session may be
	// before it is written again: idle timeouts are hours, and writing on
	// every request would turn each read into a write.
	touchInterval = time.Minute
)

// Session is a member's sign-in to the console, held server-side and
// referred to by a cookie.
type Session struct {
	ID     SessionID
	Hash   SessionHash
	Member MemberID
	Method SignInMethod
	// ClientIP and UserAgent describe the device the session opened on, so
	// a member notices a session they do not recognise.
	ClientIP   netip.Addr
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	// ExpiresAt is when the absolute timeout in force at sign-in ends the
	// session; a lower timeout set later ends it sooner.
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// NewSessionInput is what a session is opened with.
type NewSessionInput struct {
	Member    MemberID
	Method    SignInMethod
	ClientIP  netip.Addr
	UserAgent string
	Timeouts  SessionTimeouts
	Now       time.Time
}

// NewSession opens a session and returns it with the value of its cookie:
// 32 bytes drawn from random and their HMAC-SHA256 tag under key, each in
// unpadded base64url, joined by a dot. The tag tells a cookie the server
// did not issue apart without a lookup.
func NewSession(sessionID SessionID, in NewSessionInput, random io.Reader, key []byte) (Session, secret.String) {
	raw := make([]byte, sessionSecretLength)
	if _, err := io.ReadFull(random, raw); err != nil {
		// The system's randomness never fails; an exhausted test source is
		// a programming error.
		panic("access: reading randomness: " + err.Error())
	}
	ua := in.UserAgent
	if len(ua) > maxUserAgentBytes {
		ua = truncateUTF8(ua, maxUserAgentBytes)
	}
	s := Session{
		ID: sessionID, Hash: sha256.Sum256(raw), Member: in.Member, Method: in.Method,
		ClientIP: in.ClientIP, UserAgent: ua, CreatedAt: in.Now, LastSeenAt: in.Now,
		ExpiresAt: in.Now.Add(in.Timeouts.Absolute),
	}
	return s, secret.New(base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(tag(raw, key)))
}

const sessionSecretLength = 32

func tag(raw, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return mac.Sum(nil)
}

// ParseSessionCookie returns the hash a session cookie's value is stored
// under, when the value carries the tag key gives its random part; false
// for any other value, which is dropped without a lookup, so spraying
// forged cookies costs the database nothing.
func ParseSessionCookie(value string, key []byte) (SessionHash, bool) {
	rawPart, tagPart, ok := strings.Cut(value, ".")
	raw, rawErr := base64.RawURLEncoding.DecodeString(rawPart)
	got, tagErr := base64.RawURLEncoding.DecodeString(tagPart)
	if !ok || rawErr != nil || tagErr != nil || len(raw) != sessionSecretLength || !hmac.Equal(got, tag(raw, key)) {
		return SessionHash{}, false
	}
	return sha256.Sum256(raw), true
}

// ErrSessionExpired refuses a session cookie that no longer signs anyone in:
// ended by timeout, sign-out or revocation, or the member is no longer
// active.
var ErrSessionExpired = apperrors.Unauthenticated("session_expired", "the session has ended; sign in again")

// Live reports whether the session still signs holder in at now under the
// timeouts in force: it is not revoked, its member is active, its last
// request is within the idle timeout, and neither the expiry stored at
// sign-in nor its opening plus the current absolute timeout has passed. The
// current timeouts are applied to every live session, so lowering one takes
// effect at once.
func (s Session) Live(now time.Time, holder Member, t SessionTimeouts) bool {
	end := s.ExpiresAt
	if byNow := s.CreatedAt.Add(t.Absolute); byNow.Before(end) {
		end = byNow
	}
	return s.RevokedAt == nil && holder.Status == MemberActive &&
		now.Before(s.LastSeenAt.Add(t.Idle)) && now.Before(end)
}

// NeedsTouch reports whether a request at now should write the session's
// last request time: at most once a minute.
func (s Session) NeedsTouch(now time.Time) bool {
	return !now.Before(s.LastSeenAt.Add(touchInterval))
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
