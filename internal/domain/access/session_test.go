package access_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var sessionKey = bytes.Repeat([]byte{7}, 32)

func openSession(t *testing.T, ua string) (access.Session, string) {
	t.Helper()
	s, v := access.NewSession(id.New[access.Session](newIDs()), access.NewSessionInput{
		Member: id.New[access.Member](newIDs()), Method: access.SignInLocal, ClientIP: netip.MustParseAddr("192.0.2.1"),
		UserAgent: ua, Timeouts: access.DefaultSessionTimeouts, Now: now,
	}, rand.Reader, sessionKey)
	return s, v.Reveal()
}

func TestASessionCookieIsARandomPartAndItsTag(t *testing.T) {
	s, value := openSession(t, "test")
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}$`).MatchString(value) {
		t.Fatalf("cookie value = %q, want two unpadded base64url parts of 32 bytes", value)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.Split(value, ".")[0])
	if s.Hash != sha256.Sum256(raw) {
		t.Error("the session is not stored under the SHA-256 of the cookie's random part")
	}
	if h, ok := access.ParseSessionCookie(value, sessionKey); !ok || h != s.Hash {
		t.Errorf("ParseSessionCookie = %x, %v; want the session's hash", h, ok)
	}
	if !s.ExpiresAt.Equal(now.Add(30*24*time.Hour)) || !s.LastSeenAt.Equal(now) || !s.CreatedAt.Equal(now) {
		t.Errorf("session times = %+v, want opened now and expiring in 30 days", s)
	}
}

func TestAForgedSessionCookieIsRefusedByItsTag(t *testing.T) {
	_, value := openSession(t, "test")
	_, other := openSession(t, "test")
	rawPart, tagPart, _ := strings.Cut(value, ".")
	flipped := []byte(tagPart)
	flipped[0] ^= 1
	for name, forged := range map[string]string{
		"tag altered":   rawPart + "." + string(flipped),
		"tag missing":   rawPart,
		"tag of other":  rawPart + "." + strings.Split(other, ".")[1],
		"random absent": "." + tagPart,
		"not base64":    "forged.cookie",
		"empty":         "",
	} {
		if _, ok := access.ParseSessionCookie(forged, sessionKey); ok {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, ok := access.ParseSessionCookie(value, bytes.Repeat([]byte{8}, 32)); ok {
		t.Error("a cookie signed under another key was accepted")
	}
}

func TestASessionKeepsAtMost256BytesOfUserAgentOnACharacterBoundary(t *testing.T) {
	s, _ := openSession(t, strings.Repeat("a", 255)+"жж")
	if s.UserAgent != strings.Repeat("a", 255) {
		t.Errorf("user agent = %d bytes, want the 255 before the split character", len(s.UserAgent))
	}
	s, _ = openSession(t, strings.Repeat("b", 300))
	if s.UserAgent != strings.Repeat("b", 256) {
		t.Errorf("user agent = %d bytes, want 256", len(s.UserAgent))
	}
}

func TestASessionEndsIdleAfterADayOrInAllAfterThirtyDays(t *testing.T) {
	s, _ := openSession(t, "test")
	active := access.Member{Status: access.MemberActive}
	timeouts := access.DefaultSessionTimeouts
	if !s.Live(now.Add(23*time.Hour), active, timeouts) || s.Live(now.Add(24*time.Hour), active, timeouts) {
		t.Error("idle: want live at 23 hours and ended at 24")
	}
	for at := now; at.Before(now.Add(30 * 24 * time.Hour)); at = at.Add(20 * time.Hour) {
		if !s.Live(at, active, timeouts) {
			t.Fatalf("ended at %v while in use", at.Sub(now))
		}
		s.LastSeenAt = at
	}
	if s.Live(now.Add(30*24*time.Hour), active, timeouts) {
		t.Error("live 30 days after opening, want ended")
	}
}

func TestALowerAbsoluteTimeoutEndsLiveSessionsAtOnce(t *testing.T) {
	s, _ := openSession(t, "test")
	s.LastSeenAt = now.Add(2 * time.Hour)
	active := access.Member{Status: access.MemberActive}
	if s.Live(now.Add(2*time.Hour), active, access.SessionTimeouts{Idle: 24 * time.Hour, Absolute: time.Hour}) {
		t.Error("live past a lowered absolute timeout")
	}
	if !s.Live(now.Add(2*time.Hour), active, access.SessionTimeouts{Idle: 24 * time.Hour, Absolute: 90 * 24 * time.Hour}) {
		t.Error("ended under a raised absolute timeout before the stored expiry")
	}
	if s.Live(now.Add(31*24*time.Hour), active, access.SessionTimeouts{Idle: 90 * 24 * time.Hour, Absolute: 90 * 24 * time.Hour}) {
		t.Error("live past the expiry stored at sign-in, want a raised timeout never to lengthen a session")
	}
}

func TestASessionEndsWhenRevokedOrItsMemberIsNotActive(t *testing.T) {
	s, _ := openSession(t, "test")
	for _, status := range []access.MemberStatus{access.MemberDeactivated, access.MemberInvited} {
		if s.Live(now, access.Member{Status: status}, access.DefaultSessionTimeouts) {
			t.Errorf("live for a member %s", status)
		}
	}
	revoked := now
	s.RevokedAt = &revoked
	if s.Live(now, access.Member{Status: access.MemberActive}, access.DefaultSessionTimeouts) {
		t.Error("live once revoked")
	}
}

func TestASessionsLastRequestIsWrittenAtMostOnceAMinute(t *testing.T) {
	s, _ := openSession(t, "test")
	if s.NeedsTouch(now.Add(59*time.Second)) || !s.NeedsTouch(now.Add(time.Minute)) {
		t.Error("want no write within a minute and one after")
	}
}
