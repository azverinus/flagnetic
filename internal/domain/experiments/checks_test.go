package experiments_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
)

// The threshold is 0.1 % of the first exposures, never below 3 and never
// above 10.
func TestConflictThreshold(t *testing.T) {
	for _, c := range []struct{ firstExposures, want int64 }{
		{0, 3}, {1, 3}, {2999, 3}, {3000, 3}, {3001, 4}, {5000, 5}, {9001, 10}, {10000, 10}, {1_000_000, 10},
	} {
		if got := experiments.ConflictThreshold(c.firstExposures); got != c.want {
			t.Errorf("threshold of %d first exposures = %d, want %d", c.firstExposures, got, c.want)
		}
	}
}

func TestExposureConflictsAtTheThreshold(t *testing.T) {
	below := experiments.CheckExposureConflicts(9, 9, 10000)
	if below.Exceeded() || below.Threshold != 10 {
		t.Errorf("9 conflicts on 10,000 = %+v, want under the threshold of 10", below)
	}
	at := experiments.CheckExposureConflicts(10, 7, 10000)
	want := experiments.ExposureConflicts{Conflicts: 10, ConflictingUnits: 7, FirstExposures: 10000, Threshold: 10}
	if !at.Exceeded() || at != want {
		t.Errorf("10 conflicts on 10,000 = %+v, want %+v at the threshold", at, want)
	}
}

func TestNoExposuresAfterADay(t *testing.T) {
	started := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	exposed := started.Add(30 * time.Hour)
	for _, c := range []struct {
		name  string
		last  *time.Time
		now   time.Time
		holds bool
		since time.Time
	}{
		{"never exposed, running under a day", nil, started.Add(24*time.Hour - time.Second), false, started},
		{"never exposed, running a day", nil, started.Add(24 * time.Hour), true, started},
		{"exposed within the day", &exposed, exposed.Add(24*time.Hour - time.Second), false, exposed},
		{"exposed a day ago", &exposed, exposed.Add(24 * time.Hour), true, exposed},
		// An exposure the server received before the start, from an SDK
		// with a stale ruleset, is not what the start awaits.
		{"exposed only before the start", ptr(started.Add(-time.Hour)), started.Add(25 * time.Hour), true, started},
	} {
		got, holds := experiments.CheckNoExposures(started, c.last, c.now)
		if holds != c.holds || !got.Since.Equal(c.since) || got.LastExposureAt != c.last {
			t.Errorf("%s: %+v, %v, want holding %v since %s", c.name, got, holds, c.holds, c.since)
		}
	}
}

// A running experiment is past its expected end from the UTC day after it.
func TestPastExpectedEnd(t *testing.T) {
	e := running(t)
	end := *e.ExpectedEndAt
	if e.PastExpectedEnd(end.Add(24*time.Hour - time.Nanosecond)) {
		t.Errorf("past its expected end on the day itself, want not yet")
	}
	if !e.PastExpectedEnd(end.Add(24 * time.Hour)) {
		t.Errorf("not past its expected end the day after, want past")
	}
	if got := e.PastExpectedEndDetails(); got.ExpectedEndAt != end {
		t.Errorf("details = %+v, want the expected end %s", got, end)
	}
	stopped, err := e.Stop(experiments.Decision{Outcome: experiments.OutcomeRemoved}, end)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.PastExpectedEnd(end.AddDate(0, 0, 3)) {
		t.Errorf("a stopped experiment past its expected end, want only a running one")
	}
	e.ExpectedEndAt = nil
	if e.PastExpectedEnd(end.AddDate(1, 0, 0)) {
		t.Errorf("an experiment without an expected end past it, want never")
	}
}

func TestAConditionRaisesAndClearsItsAlert(t *testing.T) {
	for _, c := range []struct {
		holds, active bool
		want          experiments.AlertChange
	}{
		{true, false, experiments.AlertRaise},
		{true, true, experiments.AlertKeep},
		{false, true, experiments.AlertClear},
		{false, false, experiments.AlertKeep},
	} {
		if got := experiments.ConditionChange(c.holds, c.active); got != c.want {
			t.Errorf("holds %v, active %v: %v, want %v", c.holds, c.active, got, c.want)
		}
	}
}
