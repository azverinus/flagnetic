package worker_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
	"github.com/azverinus/flagnetic/internal/testkit"
)

type fixture struct {
	runner *worker.Runner
	reg    *prometheus.Registry
	logs   *testkit.LogSink
}

func newRunner(t *testing.T, workers ...worker.Worker) fixture {
	t.Helper()
	reg := prometheus.NewRegistry()
	logs := testkit.NewLogSink()
	r := worker.NewRunner(logs.Logger(), reg)
	for _, w := range workers {
		if err := r.Register(w); err != nil {
			t.Fatal(err)
		}
	}
	return fixture{r, reg, logs}
}

// count returns a worker metric's value for worker name.
func (f fixture) count(t *testing.T, metric, name string) float64 {
	t.Helper()
	mfs, err := f.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != metric {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "worker" && l.GetValue() == name {
					if m.GetHistogram() != nil {
						return float64(m.GetHistogram().GetSampleCount())
					}
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func (f fixture) expect(t *testing.T, name string, rounds, failures, panics float64) {
	t.Helper()
	got := [4]float64{
		f.count(t, "flagnetic_worker_rounds_total", name),
		f.count(t, "flagnetic_worker_failures_total", name),
		f.count(t, "flagnetic_worker_panics_total", name),
		f.count(t, "flagnetic_worker_round_duration_seconds", name),
	}
	if want := [4]float64{rounds, failures, panics, rounds}; got != want {
		t.Errorf("%s: rounds, failures, panics, durations = %v, want %v", name, got, want)
	}
}

func TestARoundIsCounted(t *testing.T) {
	ran := 0
	f := newRunner(t, worker.Worker{Name: "rollup", Interval: time.Minute, Round: func(context.Context) error { ran++; return nil }})
	if err := f.runner.Tick(t.Context(), "rollup"); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Errorf("the round ran %d times", ran)
	}
	f.expect(t, "rollup", 1, 0, 0)
}

func TestAFailedRoundIsCountedAndLogged(t *testing.T) {
	f := newRunner(t, worker.Worker{Name: "retention", Interval: time.Minute, Round: func(context.Context) error {
		return errors.New("partition locked")
	}})
	if err := f.runner.Tick(t.Context(), "retention"); err == nil {
		t.Error("Tick hid the round's error")
	}
	f.expect(t, "retention", 1, 1, 0)
	logged := f.logs.Find("worker round failed")
	if len(logged) != 1 || logged[0].Attrs["worker"] != "retention" || !strings.Contains(logged[0].Attrs["error"].(string), "partition locked") {
		t.Errorf("logged %v", logged)
	}
}

func TestAPanickingRoundIsCountedAndTheNextRoundRuns(t *testing.T) {
	calls := 0
	f := newRunner(t, worker.Worker{Name: "delivery", Interval: time.Minute, Round: func(context.Context) error {
		calls++
		if calls == 1 {
			panic("poison row")
		}
		return nil
	}})
	var p *safego.PanicError
	if err := f.runner.Tick(t.Context(), "delivery"); !errors.As(err, &p) {
		t.Errorf("Tick = %v, want the recovered panic", err)
	}
	if err := f.runner.Tick(t.Context(), "delivery"); err != nil {
		t.Errorf("the round after a panic = %v", err)
	}
	f.expect(t, "delivery", 2, 1, 1)
	if logged := f.logs.Find("recovered a panic"); len(logged) != 1 || !strings.Contains(logged[0].Attrs["stack"].(string), "worker_test.go") {
		t.Errorf("the panic was not logged with its stack: %v", logged)
	}
}

func TestARoundPastItsTimeoutIsCancelledAndCountedFailed(t *testing.T) {
	f := newRunner(t, worker.Worker{Name: "scheduler", Interval: time.Minute, Timeout: 50 * time.Millisecond, Round: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	start := time.Now()
	if err := f.runner.Tick(t.Context(), "scheduler"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Tick = %v, want the round's deadline", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the round ran %v past a 50ms timeout", took)
	}
	f.expect(t, "scheduler", 1, 1, 0)
}

func TestTickRefusesAnUnknownWorker(t *testing.T) {
	f := newRunner(t)
	if err := f.runner.Tick(t.Context(), "nope"); err == nil {
		t.Error("Tick ran a worker that was never registered")
	}
}

func TestRegisterRefusesAMalformedOrDuplicateWorker(t *testing.T) {
	f := newRunner(t, worker.Worker{Name: "rollup", Interval: time.Minute, Round: func(context.Context) error { return nil }})
	for name, w := range map[string]worker.Worker{
		"duplicate":   {Name: "rollup", Interval: time.Minute, Round: func(context.Context) error { return nil }},
		"no name":     {Interval: time.Minute, Round: func(context.Context) error { return nil }},
		"no interval": {Name: "x", Round: func(context.Context) error { return nil }},
		"no round":    {Name: "y", Interval: time.Minute},
	} {
		if err := f.runner.Register(w); err == nil {
			t.Errorf("%s: registered", name)
		}
	}
}

func TestJitterStaysWithinTenPercent(t *testing.T) {
	const interval = 10 * time.Second
	lowest, highest := interval, time.Duration(0)
	for range 10000 {
		d := worker.Jitter(interval)
		if d < 9*time.Second || d > 11*time.Second {
			t.Fatalf("Jitter(10s) = %v, outside ±10 %%", d)
		}
		lowest, highest = min(lowest, d), max(highest, d)
	}
	if lowest > 9200*time.Millisecond || highest < 10800*time.Millisecond {
		t.Errorf("Jitter(10s) spread over [%v, %v], want it to cover most of [9s, 11s] so replicas drift apart", lowest, highest)
	}
}

func TestStartRunsTheFirstRoundAtOnceAndStopAwaitsIt(t *testing.T) {
	var rounds atomic.Int32
	stopped := make(chan struct{})
	f := newRunner(t, worker.Worker{Name: "rollup", Interval: time.Hour, Round: func(ctx context.Context) error {
		if rounds.Add(1) == 1 {
			<-ctx.Done()
			close(stopped)
		}
		return ctx.Err()
	}})
	f.runner.Start(t.Context())
	deadline := time.Now().Add(5 * time.Second)
	for rounds.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if rounds.Load() != 1 {
		t.Fatal("the first round did not run at once")
	}
	if !f.runner.Stop(5 * time.Second) {
		t.Error("Stop gave up waiting for a round that honours cancellation")
	}
	select {
	case <-stopped:
	default:
		t.Error("Stop returned before the round ended")
	}
	f.expect(t, "rollup", 1, 0, 0)
}

func TestStopIsBoundedWhileARoundIgnoresCancellation(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	entered := make(chan struct{})
	f := newRunner(t, worker.Worker{Name: "stuck", Interval: time.Hour, Timeout: time.Hour, Round: func(context.Context) error {
		close(entered)
		<-release // ignores its context
		return nil
	}})
	f.runner.Start(t.Context())
	<-entered
	start := time.Now()
	if f.runner.Stop(200 * time.Millisecond) {
		t.Error("Stop reported every round ended while one is stuck")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Stop took %v with a bound of 200ms", took)
	}
}

func TestAPanicInAGoroutineOfAWorkerIsCounted(t *testing.T) {
	f := newRunner(t)
	done := make(chan struct{})
	safego.Go(f.runner.PanicHook(t.Context()), "stream", testkit.NewLogSink().Logger(), func(context.Context) {
		defer close(done)
		panic("x")
	})
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for f.count(t, "flagnetic_worker_panics_total", "stream") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := f.count(t, "flagnetic_worker_panics_total", "stream"); got != 1 {
		t.Errorf("flagnetic_worker_panics_total{worker=stream} = %v, want 1", got)
	}
}

func TestStartingOrTickingAStoppedRunnerRunsNothing(t *testing.T) {
	var rounds atomic.Int32
	f := newRunner(t, worker.Worker{Name: "rollup", Interval: time.Hour, Round: func(context.Context) error {
		rounds.Add(1)
		return nil
	}})
	if !f.runner.Stop(time.Second) {
		t.Fatal("stopping a runner never started did not return at once")
	}
	f.runner.Start(t.Context())
	if err := f.runner.Tick(t.Context(), "rollup"); err == nil {
		t.Error("Tick ran a round on a stopped runner")
	}
	time.Sleep(20 * time.Millisecond)
	if n := rounds.Load(); n != 0 {
		t.Errorf("%d rounds ran after Stop", n)
	}
}

func TestARoundThatSucceedsIsNotAFailureWhateverTheClock(t *testing.T) {
	f := newRunner(t, worker.Worker{Name: "rollup", Interval: time.Minute, Timeout: 10 * time.Millisecond, Round: func(ctx context.Context) error {
		<-ctx.Done()
		return nil // finished its work as the deadline came
	}})
	if err := f.runner.Tick(t.Context(), "rollup"); err != nil {
		t.Errorf("Tick = %v", err)
	}
	f.expect(t, "rollup", 1, 0, 0)
}

func TestRoundsOfOneWorkerNeverOverlap(t *testing.T) {
	var inRound, overlapped atomic.Int32
	f := newRunner(t, worker.Worker{Name: "rollup", Interval: time.Minute, Round: func(context.Context) error {
		if inRound.Add(1) > 1 {
			overlapped.Store(1)
		}
		time.Sleep(5 * time.Millisecond)
		inRound.Add(-1)
		return nil
	}})
	done := make(chan struct{})
	for range 4 {
		go func() {
			_ = f.runner.Tick(t.Context(), "rollup")
			done <- struct{}{}
		}()
	}
	for range 4 {
		<-done
	}
	if overlapped.Load() != 0 {
		t.Error("two rounds of one worker ran at once")
	}
}
