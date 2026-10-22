package delivery_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
)

// bumpedAt is a clock reading of the present era: 2026-01-01T09:00:00Z.
var bumpedAt = time.Date(2026, time.January, 1, 9, 0, 0, 0, time.UTC)

const nowMillis = int64(1_767_258_000_000)

func TestTheNextRulesetVersionIsTheClockInMillisecondsWhenThePreviousIsBehindIt(t *testing.T) {
	for name, previous := range map[string]int64{
		"a new environment":              1,
		"a version counted one by one":   4_812,
		"a version a millisecond behind": nowMillis - 1,
	} {
		if got := delivery.NextRulesetVersion(previous, bumpedAt); got != nowMillis {
			t.Errorf("%s: next of %d = %d, want the clock's %d", name, previous, got, nowMillis)
		}
	}
}

func TestTheNextRulesetVersionIsThePreviousPlusOneWhenTheClockIsNotAhead(t *testing.T) {
	for name, previous := range map[string]int64{
		"a bump earlier in the same millisecond": nowMillis,
		"a clock set back by a minute":           nowMillis + 60_000,
		"a version from a clock far ahead":       nowMillis + 365*24*3_600_000,
	} {
		if got := delivery.NextRulesetVersion(previous, bumpedAt); got != previous+1 {
			t.Errorf("%s: next of %d = %d, want %d", name, previous, got, previous+1)
		}
	}
}

// Whatever the clock does between bumps (stands still, moves back, jumps
// ahead), every version is above the one before it, so an SDK never meets a
// version it already holds with other content.
func TestRulesetVersionsOnlyIncreaseWhateverTheClockDoes(t *testing.T) {
	steps := []time.Duration{
		0, 0, 0, time.Millisecond, -time.Hour, 0, 2 * time.Hour, -time.Millisecond, time.Microsecond, -24 * time.Hour,
		time.Second, 0, -time.Second, 30 * 24 * time.Hour, -30 * 24 * time.Hour,
	}
	at, version := bumpedAt, int64(1)
	for i, step := range steps {
		at = at.Add(step)
		next := delivery.NextRulesetVersion(version, at)
		if next <= version {
			t.Fatalf("step %d (clock %s): next of %d = %d, want above it", i, at, version, next)
		}
		if next < at.UnixMilli() {
			t.Fatalf("step %d: next = %d, want at least the clock's %d", i, next, at.UnixMilli())
		}
		version = next
	}
}

func TestManyBumpsInOneMillisecondGiveConsecutiveVersions(t *testing.T) {
	version := delivery.NextRulesetVersion(1, bumpedAt)
	for i := 1; i <= 1_000; i++ {
		next := delivery.NextRulesetVersion(version, bumpedAt)
		if next != version+1 {
			t.Fatalf("bump %d in one millisecond: next of %d = %d, want %d", i, version, next, version+1)
		}
		version = next
	}
}

// Versions travel as JSON numbers, which SDKs in every language read exactly
// only below 2^53.
func TestRulesetVersionsStayBelowTwoToTheFiftyThird(t *testing.T) {
	const limit = int64(1) << 53
	far := time.Date(2200, time.January, 1, 0, 0, 0, 0, time.UTC)
	version := delivery.NextRulesetVersion(1, far)
	for range 1_000_000 {
		version = delivery.NextRulesetVersion(version, far)
	}
	if version >= limit {
		t.Errorf("a million bumps in one millisecond of 2200 reach %d, want below 2^53 (%d)", version, limit)
	}
}
