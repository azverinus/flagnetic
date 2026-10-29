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
	if got := access.LockoutSchedule(); !slices.Equal(got, []time.Duration{
		0, 0, 0, 0, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour,
	}) {
		t.Errorf("LockoutSchedule() = %v, want the window of each failure from the first to the first locking for an hour", got)
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

func TestAnAttemptIsKeyedByItsEmailWhateverItsCaseAndSpacesAndBySource(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	a := access.NewAttempt("  Ada@Example.COM ", netip.MustParseAddr("2001:db8:1:2::9"), now)
	if a.Email != "ada@example.com" || a.Source.String() != "2001:db8:1:2::/64" || !a.At.Equal(now) {
		t.Errorf("NewAttempt = %+v", a)
	}
}

func TestAReservationRefusesAThrottledSourceFirstThenALockedEmail(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	refusal := func(r access.Reservation) (string, time.Duration) {
		r.At = now
		e, ok := apperrors.From(r.Refusal())
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

func TestAReservationIsJudgedAtTheTimeItWasMade(t *testing.T) {
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	r := access.Reservation{Attempt: access.Attempt{At: at}, LockedUntil: at.Add(time.Second), WindowStart: at}
	if e, ok := apperrors.From(r.Refusal()); !ok || e.Code != "locked" || e.RetryAfter != time.Second {
		t.Errorf("a lock ending a second after the reservation: %v, want locked for a second", r.Refusal())
	}
	if locks, wait := r.Locks(); locks {
		t.Errorf("a reservation that starts no lock: Locks = %v %v", locks, wait)
	}
	r = access.Reservation{Attempt: access.Attempt{At: at}, Failures: 6, LocksUntil: at.Add(2 * time.Minute)}
	if locks, wait := r.Locks(); !locks || wait != 2*time.Minute {
		t.Errorf("a failure locking for two minutes from the reservation: Locks = %v %v", locks, wait)
	}
}

func TestFailuresArePurgedOnlyOnceTheyCanNoLongerCount(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	cut := access.AttemptCutoffs(now)
	if !cut.LastEmailFailureBy.Equal(now.Add(-24*time.Hour)) || !cut.Now.Equal(now) {
		t.Errorf("email cutoff = %+v, want a day before now", cut)
	}
	if !cut.SourceWindowStartBy.Equal(now.Add(-15 * time.Minute)) {
		t.Errorf("source cutoff = %v, want the source window before now", cut.SourceWindowStartBy)
	}
}

func TestASourceIsKnownForThirtyDaysAfterTheMemberSignedInFromIt(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	a := access.NewAttempt("ada@example.com", netip.MustParseAddr("203.0.113.5"), now)
	if got := a.KnownAfter(); !got.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Errorf("KnownAfter = %v, want thirty days before the attempt", got)
	}
	if got := access.AttemptCutoffs(now).KnownSourceBy; !got.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Errorf("KnownSourceBy = %v, want thirty days before now: a source no longer known is forgotten", got)
	}
	for addr, want := range map[string]string{"203.0.113.5": "203.0.113.5/32", "2001:db8:1:2::9": "2001:db8:1:2::/64"} {
		if got, ok := access.KnownSourceOf(netip.MustParseAddr(addr)); !ok || got.String() != want {
			t.Errorf("KnownSourceOf(%s) = %s %v, want %s, as its failures are counted", addr, got, ok, want)
		}
	}
	if got, ok := access.KnownSourceOf(netip.Addr{}); ok {
		t.Errorf("KnownSourceOf(no address) = %s, want none: every attempt without an address shares that source", got)
	}
}
