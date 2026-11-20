package load

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOffsets(t *testing.T) {
	t.Run("request i is due at i over the rate", func(t *testing.T) {
		got := Offsets(4, time.Second)
		want := []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond, 750 * time.Millisecond}
		if len(got) != len(want) {
			t.Fatalf("Offsets(4, 1s) has %d requests, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("request %d is due at %v, want %v", i, got[i], want[i])
			}
		}
	})
	t.Run("a run sends rate times duration, rounded down", func(t *testing.T) {
		if got := len(Offsets(150, 30*time.Second)); got != 4500 {
			t.Errorf("150/s for 30s sends %d, want 4500", got)
		}
		if got := len(Offsets(2.5, 3*time.Second)); got != 7 {
			t.Errorf("2.5/s for 3s sends %d, want 7", got)
		}
	})
	t.Run("nothing is sent at no rate or no duration", func(t *testing.T) {
		if Offsets(0, time.Second) != nil || Offsets(-1, time.Second) != nil || Offsets(10, 0) != nil {
			t.Error("a run that is not positive must send nothing")
		}
	})
}

func TestPercentile(t *testing.T) {
	var hundred []time.Duration
	for i := 1; i <= 100; i++ {
		hundred = append(hundred, time.Duration(i)*time.Millisecond)
	}
	for _, c := range []struct {
		name string
		in   []time.Duration
		q    float64
		want time.Duration
	}{
		{"p50 of 100 is the 50th", hundred, 0.50, 50 * time.Millisecond},
		{"p99 of 100 is the 99th", hundred, 0.99, 99 * time.Millisecond},
		{"p99.9 of 100 is the largest", hundred, 0.999, 100 * time.Millisecond},
		{"q of 1 is the largest", hundred, 1, 100 * time.Millisecond},
		{"q of 0 is the smallest", hundred, 0, time.Millisecond},
		{"one sample is every percentile", []time.Duration{7 * time.Millisecond}, 0.99, 7 * time.Millisecond},
		{"no samples is zero", nil, 0.99, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Percentile(c.in, c.q); got != c.want {
				t.Errorf("Percentile(q=%v) = %v, want %v", c.q, got, c.want)
			}
		})
	}
}

func TestSummarizeSortsAndConvertsToMilliseconds(t *testing.T) {
	r := Summarize("evaluate", 10, []time.Duration{30 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}, 1, "boom", 2*time.Second)
	if r.P50 != 20 || r.Max != 30 || r.Sent != 3 || r.Errors != 1 || r.FirstError != "boom" {
		t.Errorf("Summarize = %+v", r)
	}
	if r.Achieved != 1.5 {
		t.Errorf("Achieved = %v, want 1.5", r.Achieved)
	}
	if got := Summarize("x", 1, nil, 0, "", 0); got.Sent != 0 || got.Max != 0 || got.Achieved != 0 {
		t.Errorf("an empty run summarizes to %+v", got)
	}
}

// fakeTime is time as a run reads it, moved by the run's own waits and by
// nothing else: a run on it takes no time, and what it measures is exact.
type fakeTime struct {
	mu       sync.Mutex
	at       time.Time
	nowCalls int
}

func newFakeTime() *fakeTime { return &fakeTime{at: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

// clock is a clock that wakes late after each scheduled time.
func (f *fakeTime) clock(late time.Duration) clock {
	return clock{
		now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.nowCalls++
			return f.at
		},
		until: func(_ context.Context, t time.Time) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if t = t.Add(late); t.After(f.at) {
				f.at = t
			}
		},
	}
}

func (f *fakeTime) reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nowCalls
}

// within fails the test unless cond holds before a deadline no loaded runner
// reaches; a run that never gets there is stuck.
func within(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened", what)
		}
	}
}

func TestRunSendsOnTheScheduleWithoutWaitingForAnswers(t *testing.T) {
	// 20 requests whose answers wait until every one has been sent: an open
	// model has all 20 in flight at once, a closed one with a single worker
	// would never send the second.
	var (
		started atomic.Int32
		release = make(chan struct{})
		done    = make(chan Result, 1)
	)
	go func() {
		done <- Run(context.Background(), Options{Scenario: "slow", Rate: 100, Duration: 200 * time.Millisecond, clock: newFakeTime().clock(0)},
			func(context.Context, int) error { started.Add(1); <-release; return nil })
	}()
	within(t, "all 20 requests in flight at once", func() bool { return started.Load() == 20 })
	close(release)
	r := <-done
	if r.Sent != 20 || r.Errors != 0 {
		t.Errorf("sent %d with %d errors, want 20 and 0", r.Sent, r.Errors)
	}
}

func TestRunMeasuresFromTheScheduledTime(t *testing.T) {
	// A generator that is late for every request (it wakes 40ms after the
	// scheduled time) must show 40ms of latency though the request itself
	// is instant: that is coordinated omission, and the reason to measure
	// from the schedule.
	r := Run(context.Background(), Options{Scenario: "late", Rate: 50, Duration: 100 * time.Millisecond, clock: newFakeTime().clock(40 * time.Millisecond)},
		func(context.Context, int) error { return nil })
	if r.P50 < 40 {
		t.Errorf("p50 = %vms, want at least the 40ms the generator was late", r.P50)
	}
}

func TestRunCountsErrorsAndKeepsTheFirst(t *testing.T) {
	r := Run(context.Background(), Options{Scenario: "err", Rate: 100, Duration: 100 * time.Millisecond, clock: newFakeTime().clock(0)},
		func(_ context.Context, i int) error {
			if i%2 == 1 {
				return errors.New("status 503")
			}
			return nil
		})
	if r.Sent != 10 || r.Errors != 5 || r.FirstError != "status 503" {
		t.Errorf("Run = %+v, want 10 sent, 5 errors, first error 'status 503'", r)
	}
}

func TestRunRefusesRequestsOverTheInFlightBound(t *testing.T) {
	var (
		tm      = newFakeTime()
		release = make(chan struct{})
		done    = make(chan Result, 1)
	)
	go func() {
		done <- Run(context.Background(), Options{Scenario: "stuck", Rate: 1000, Duration: 20 * time.Millisecond, MaxInFlight: 5, clock: tm.clock(0)},
			func(context.Context, int) error { <-release; return nil })
	}()
	// The run reads the time once at its start and once for each request it
	// refuses: past 15 refusals every request has been scheduled, the five
	// that fit still held.
	within(t, "the 15 refusals", func() bool { return tm.reads() >= 16 })
	close(release)
	r := <-done
	if r.Sent != 20 || r.Errors != 15 {
		t.Errorf("Run = %+v, want 20 samples, 15 of them refused over the bound of 5", r)
	}
}

func TestRunStopsSchedulingWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Run(ctx, Options{Scenario: "cancelled", Rate: 100, Duration: time.Second, clock: newFakeTime().clock(0)}, func(context.Context, int) error { return nil })
	if r.Sent > 1 {
		t.Errorf("a cancelled run sent %d requests", r.Sent)
	}
}
