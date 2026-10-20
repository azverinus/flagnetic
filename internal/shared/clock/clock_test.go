package clock_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/clock"
)

var start = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func TestFakeStandsStillUntilMoved(t *testing.T) {
	c := clock.NewFake(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now = %v, want %v", got, start)
	}
	c.Advance(90 * time.Second)
	if got := c.Now(); !got.Equal(start.Add(90 * time.Second)) {
		t.Errorf("after Advance, Now = %v", got)
	}
	earlier := start.Add(-time.Hour)
	c.Set(earlier)
	if got := c.Now(); !got.Equal(earlier) {
		t.Errorf("after Set, Now = %v, want %v", got, earlier)
	}
}

func TestFakeTimerFiresOnlyOnceAdvancedPastIt(t *testing.T) {
	c := clock.NewFake(start)
	fired := c.After(time.Minute)

	c.Advance(59 * time.Second)
	select {
	case <-fired:
		t.Fatal("timer fired before its time")
	default:
	}

	c.Advance(time.Second)
	select {
	case at := <-fired:
		if !at.Equal(start.Add(time.Minute)) {
			t.Errorf("timer sent %v, want the clock's time %v", at, start.Add(time.Minute))
		}
	default:
		t.Fatal("timer did not fire once due")
	}
}

func TestFakeTimerFiresWhenSetPastIt(t *testing.T) {
	c := clock.NewFake(start)
	fired := c.After(time.Hour)
	c.Set(start.Add(2 * time.Hour))
	select {
	case <-fired:
	default:
		t.Fatal("timer did not fire when the clock was set past it")
	}
}

func TestFakeTimerOfNoDurationFiresAtOnce(t *testing.T) {
	c := clock.NewFake(start)
	select {
	case <-c.After(0):
	default:
		t.Fatal("a timer of zero did not fire at once")
	}
}

func TestAfterWaitsOnTheClocksOwnTimers(t *testing.T) {
	c := clock.NewFake(start)
	fired := clock.After(c, time.Hour)
	c.Advance(time.Hour)
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("After did not use the fake clock's timers")
	}
}

func TestAfterWaitsOnTheWallClockOtherwise(t *testing.T) {
	select {
	case <-clock.After(clock.System{}, time.Millisecond):
	case <-time.After(5 * time.Second):
		t.Fatal("After on the system clock never fired")
	}
}

func TestSystemReadsTheWallClock(t *testing.T) {
	before := time.Now()
	got := clock.System{}.Now()
	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("System.Now = %v, not between the wall readings around it", got)
	}
}
