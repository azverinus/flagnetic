package load

import (
	"context"
	"sync"
	"time"
)

// DefaultMaxInFlight bounds the requests a run holds open at once: a server
// that has stopped answering must not grow the generator without limit. A
// request over the bound is not sent and counts as an error.
const DefaultMaxInFlight = 20000

// Options say how Run sends.
type Options struct {
	// Scenario names the Result.
	Scenario string
	// Rate is the arrival rate in requests per second.
	Rate float64
	// Duration is the length of the schedule.
	Duration time.Duration
	// MaxInFlight is DefaultMaxInFlight when zero.
	MaxInFlight int

	clock clock
}

// clock is time as Run reads it, replaceable by a test.
type clock struct {
	now   func() time.Time
	until func(ctx context.Context, t time.Time)
}

func realClock() clock {
	return clock{now: time.Now, until: func(ctx context.Context, t time.Time) {
		d := time.Until(t)
		if d <= 0 {
			return
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}}
}

// Run sends fn at the schedule of o, each call on its own goroutine, and
// reports the latency of every call measured from the time it was
// scheduled, not from the time it was sent: a generator that falls behind,
// as much as a server that answers late, shows up as latency. fn gets the
// number of the request, from 0. Run returns when every call has returned.
func Run(ctx context.Context, o Options, fn func(ctx context.Context, i int) error) Result {
	if o.clock.now == nil {
		o.clock = realClock()
	}
	if o.MaxInFlight <= 0 {
		o.MaxInFlight = DefaultMaxInFlight
	}
	offsets := Offsets(o.Rate, o.Duration)
	var (
		mu         sync.Mutex
		latencies  = make([]time.Duration, 0, len(offsets))
		errs       int
		firstError string
		last       time.Time
		wg         sync.WaitGroup
		inFlight   = make(chan struct{}, o.MaxInFlight)
	)
	record := func(scheduled time.Time, err error) {
		done := o.clock.now()
		mu.Lock()
		defer mu.Unlock()
		latencies = append(latencies, max(done.Sub(scheduled), 0))
		if done.After(last) {
			last = done
		}
		if err != nil {
			errs++
			if firstError == "" {
				firstError = err.Error()
			}
		}
	}
	start := o.clock.now()
	for i, off := range offsets {
		scheduled := start.Add(off)
		o.clock.until(ctx, scheduled)
		if ctx.Err() != nil {
			break
		}
		select {
		case inFlight <- struct{}{}:
		default:
			record(scheduled, errInFlight)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-inFlight }()
			record(scheduled, fn(ctx, i))
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return Summarize(o.Scenario, o.Rate, latencies, errs, firstError, last.Sub(start))
}

type inFlightError struct{}

func (inFlightError) Error() string {
	return "too many requests in flight; the server stopped answering"
}

var errInFlight error = inFlightError{}
