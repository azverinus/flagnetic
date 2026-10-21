package access_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestFiveConsecutiveFailuresLockForAMinuteDoublingUpToAnHour(t *testing.T) {
	want := map[int]time.Duration{
		1: 0, 4: 0, 5: time.Minute, 6: 2 * time.Minute, 7: 4 * time.Minute, 8: 8 * time.Minute,
		9: 16 * time.Minute, 10: 32 * time.Minute, 11: time.Hour, 12: time.Hour, 1000: time.Hour,
	}
	for failures, window := range want {
		if got := access.LockoutWindow(failures); got != window {
			t.Errorf("LockoutWindow(%d) = %v, want %v", failures, got, window)
		}
	}
	if got := access.LockoutWindows(); !slices.Equal(got, []time.Duration{
		time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour,
	}) {
		t.Errorf("LockoutWindows() = %v", got)
	}
}

func TestIPv6SourcesAreCountedByTheir64AndIPv4ByTheAddress(t *testing.T) {
	for addr, want := range map[string]string{
		"203.0.113.5":               "203.0.113.5/32",
		"::ffff:203.0.113.5":        "203.0.113.5/32",
		"2001:db8:1:2:3:4:5:6":      "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff::1": "2001:db8:1:2::/64",
		"fe80::1%eth0":              "fe80::/64",
	} {
		if got := access.SourceOf(netip.MustParseAddr(addr)); got.String() != want {
			t.Errorf("SourceOf(%s) = %s, want %s", addr, got, want)
		}
	}
	if got := access.SourceOf(netip.Addr{}); got.String() != "0.0.0.0/32" {
		t.Errorf("SourceOf(no address) = %s, want 0.0.0.0/32", got)
	}
}

func TestAnAttemptIsKeyedByItsEmailWhateverItsCaseAndSpaces(t *testing.T) {
	if got := access.AttemptKey("  Ada@Example.COM "); got != "ada@example.com" {
		t.Errorf("AttemptKey = %q", got)
	}
}

func TestAReservationRefusesAThrottledSourceFirstThenALockedEmail(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	refusal := func(r access.Reservation) (string, time.Duration) {
		e, ok := apperrors.From(r.Refusal(now))
		if !ok {
			return "", 0
		}
		return e.Code, e.RetryAfter
	}
	if code, _ := refusal(access.Reservation{Failures: 5, SourceFailures: 100, WindowStart: now}); code != "" {
		t.Errorf("the hundredth failure of a source and the fifth of an email: refused %s, want verified", code)
	}
	code, wait := refusal(access.Reservation{
		LockedUntil: now.Add(time.Minute), SourceFailures: 101, WindowStart: now.Add(-5 * time.Minute),
	})
	if code != "rate_limited" || wait != 10*time.Minute {
		t.Errorf("throttled and locked: %s after %v, want rate_limited for the rest of the window", code, wait)
	}
	if code, wait := refusal(access.Reservation{LockedUntil: now.Add(90 * time.Second), SourceFailures: 3, WindowStart: now}); code != "locked" || wait != 90*time.Second {
		t.Errorf("locked: %s after %v, want locked for 90s", code, wait)
	}
	if code, _ := refusal(access.Reservation{LockedUntil: now, Failures: 6, SourceFailures: 3, WindowStart: now}); code != "" {
		t.Errorf("a lock ending now: refused %s, want verified", code)
	}
}

func TestOnlyAnAttemptReservedAtTheThresholdOrPastItLocks(t *testing.T) {
	now := time.Now()
	for r, locks := range map[access.Reservation]bool{
		{Failures: 4}:                   false,
		{Failures: 5}:                   true,
		{Failures: 9}:                   true,
		{Failures: 0, LockedUntil: now}: false,
	} {
		if r.Locks() != locks {
			t.Errorf("%+v: Locks = %v, want %v", r, r.Locks(), locks)
		}
	}
}
