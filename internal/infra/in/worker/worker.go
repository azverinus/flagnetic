// Package worker runs a replica's background work in rounds. Every round runs
// under one recovering wrapper, so a round that fails or panics is logged,
// counted per worker, and followed by the next: one poison row cannot
// crash-loop the fleet. Every replica runs every worker; what keeps two
// replicas from doing one piece of work twice is the work's own locking in
// PostgreSQL, never the runner.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

// Worker is one kind of background work.
type Worker struct {
	// Name labels the worker's log lines and metrics.
	Name string
	// Interval is the time between the end of a round and the start of the
	// next, before jitter.
	Interval time.Duration
	// Timeout bounds a round; the interval when zero. The round's context is
	// cancelled then, and a round that does not return is left to finish on
	// its own while the replica's stop is bounded separately.
	Timeout time.Duration
	// Round does one round of the work. Its context is cancelled at the
	// timeout and when the runner stops.
	Round func(ctx context.Context) error
}

type registered struct {
	Worker
	// one serialises the worker's rounds, so a round driven by Tick never
	// overlaps one started by the timer.
	one sync.Mutex
}

type state int

const (
	idle state = iota
	running
	stopped
)

// Runner runs registered workers.
type Runner struct {
	log *slog.Logger

	rounds, failures, panics *prometheus.CounterVec
	durations                *prometheus.HistogramVec

	mu      sync.Mutex
	state   state
	workers map[string]*registered
	order   []string
	cancel  context.CancelFunc
	// rounds in progress, from the timer or from Tick, which Stop awaits.
	inFlight sync.WaitGroup
}

// NewRunner returns a runner whose metrics are registered on reg.
func NewRunner(log *slog.Logger, reg prometheus.Registerer) *Runner {
	counter := func(name, help string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "worker", Name: name, Help: help,
		}, []string{"worker"})
	}
	r := &Runner{
		log:      log,
		rounds:   counter("rounds_total", "Rounds a worker ran."),
		failures: counter("failures_total", "Rounds of a worker that failed, panics included."),
		panics:   counter("panics_total", "Panics recovered in a worker's rounds or in goroutines run under a name of their own."),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Subsystem: "worker", Name: "round_duration_seconds",
			Help: "Time a worker's round took.", Buckets: prometheus.ExponentialBuckets(0.005, 4, 9),
		}, []string{"worker"}),
		workers: map[string]*registered{},
	}
	reg.MustRegister(r.rounds, r.failures, r.panics, r.durations)
	return r
}

// Register adds a worker before Start. It is refused without a name, an
// interval and a round, or with the name of another.
func (r *Runner) Register(w Worker) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.state != idle:
		return fmt.Errorf("worker %q registered after the runner started", w.Name)
	case w.Name == "" || w.Interval <= 0 || w.Round == nil:
		return fmt.Errorf("worker %q needs a name, a positive interval and a round", w.Name)
	case w.Timeout < 0:
		return fmt.Errorf("worker %q has a negative timeout", w.Name)
	}
	if _, taken := r.workers[w.Name]; taken {
		return fmt.Errorf("worker %q is registered twice", w.Name)
	}
	if w.Timeout == 0 {
		w.Timeout = w.Interval
	}
	r.workers[w.Name] = &registered{Worker: w}
	r.order = append(r.order, w.Name)
	return nil
}

// PanicHook returns ctx with the hook that counts a panic recovered by
// safego under the name it was run with. The replica hands this context to
// everything that spawns goroutines, so their panics are counted beside the
// workers'.
func (r *Runner) PanicHook(ctx context.Context) context.Context {
	return safego.WithPanicHook(ctx, func(name string) { r.panics.WithLabelValues(name).Inc() })
}

// Start runs every worker's first round at once and then one round every
// interval with jitter, until Stop. ctx is the replica's own context, never a
// request's. A runner starts once; Start after Start or Stop does nothing.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != idle {
		return
	}
	r.state = running
	ctx, r.cancel = context.WithCancel(ctx)
	for _, name := range r.order {
		w := r.workers[name]
		r.inFlight.Go(func() {
			for {
				_ = r.round(ctx, w)
				select {
				case <-ctx.Done():
					return
				case <-time.After(jitter(w.Interval)):
				}
			}
		})
	}
}

// Stop cancels every round and waits at most bound for them to end. It
// reports whether they all did; a round that ignores its cancellation is
// left behind rather than holding the replica's stop forever. A runner
// stopped before it started never starts, and stopping again returns at once.
func (r *Runner) Stop(bound time.Duration) bool {
	r.mu.Lock()
	wasRunning := r.state == running
	r.state = stopped
	if wasRunning {
		r.cancel()
	}
	r.mu.Unlock()
	if !wasRunning {
		return true
	}
	done := make(chan struct{})
	go func() {
		r.inFlight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(bound):
		r.log.Warn("stopped with worker rounds still running", "bound", bound.String())
		return false
	}
}

// Tick runs one round of the worker called name now, through the same
// wrapper as the timer: tests drive workers with it. Rounds can only run
// through the runner, so no registered worker runs outside the wrapper. A
// stopped runner runs no more rounds.
func (r *Runner) Tick(ctx context.Context, name string) error {
	r.mu.Lock()
	w, ok := r.workers[name]
	if r.state == stopped {
		r.mu.Unlock()
		return errors.New("the runner is stopped")
	}
	r.inFlight.Add(1)
	r.mu.Unlock()
	defer r.inFlight.Done()
	if !ok {
		return fmt.Errorf("no worker called %q", name)
	}
	return r.round(ctx, w)
}

// round runs one round under the recovering wrapper and counts it. A round
// fails when it panics, or returns an error other than the cancellation of
// the runner's stop; running past its timeout is one such error.
func (r *Runner) round(ctx context.Context, w *registered) error {
	w.one.Lock()
	defer w.one.Unlock()
	roundCtx, cancel := context.WithTimeout(r.PanicHook(ctx), w.Timeout)
	defer cancel()
	start := time.Now()
	err := safego.Call(roundCtx, w.Name, r.log, w.Round)
	r.durations.WithLabelValues(w.Name).Observe(time.Since(start).Seconds())
	r.rounds.WithLabelValues(w.Name).Inc()

	var p *safego.PanicError
	switch {
	case err == nil:
	case errors.As(err, &p):
		r.failures.WithLabelValues(w.Name).Inc() // logged by safego with the stack
	case ctx.Err() != nil && errors.Is(err, context.Canceled):
		// The runner is stopping; the round did as asked.
	default:
		r.failures.WithLabelValues(w.Name).Inc()
		if errors.Is(err, context.DeadlineExceeded) && errors.Is(roundCtx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("ran past its timeout of %v: %w", w.Timeout, err)
		}
		r.log.ErrorContext(ctx, "worker round failed", "worker", w.Name, "error", err.Error())
	}
	return err
}

// jitter returns interval moved by up to 10 % either way, at random, so that
// replicas started together drift apart instead of hitting PostgreSQL in
// bursts.
func jitter(interval time.Duration) time.Duration {
	return time.Duration(float64(interval) * (0.9 + 0.2*rand.Float64())) //nolint:gosec // spreading load needs no secret randomness
}
