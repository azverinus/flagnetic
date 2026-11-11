package access_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
)

// A job that starts another once the replica has begun waiting, as a password
// reset request queues its mail, has that one awaited too: otherwise the reset
// would be issued and its mail lost with the replica.
func TestWaitingAwaitsAJobStartedByAJobInFlight(t *testing.T) {
	b := appaccess.NewBackground(slog.New(slog.DiscardHandler))
	waiting := make(chan struct{})
	var sent atomic.Bool
	b.Go(t.Context(), "outer", time.Minute, func(ctx context.Context) {
		<-waiting
		b.Go(ctx, "inner", time.Minute, func(context.Context) {
			time.Sleep(100 * time.Millisecond)
			sent.Store(true)
		})
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(waiting)
	}()
	b.Wait(ctx)
	if ctx.Err() != nil {
		t.Fatal("the jobs were still running after 5 seconds")
	}
	if !sent.Load() {
		t.Error("Wait returned before the job started by the job in flight ended")
	}
}

func TestWaitingWithNothingRunningReturnsAtOnce(t *testing.T) {
	b := appaccess.NewBackground(slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	b.Wait(ctx)
	if ctx.Err() != nil {
		t.Error("Wait blocked with nothing running")
	}
}

// Jobs past the limit are dropped and counted rather than queued, so a flood of
// requests cannot pile up goroutines; once a running job ends there is room again.
func TestJobsPastTheLimitAreDroppedAndCounted(t *testing.T) {
	var dropped []string
	b := appaccess.NewBackground(slog.New(slog.DiscardHandler), appaccess.WithBackgroundLimit(2),
		appaccess.WithDroppedCounter(func(name string) { dropped = append(dropped, name) }))
	release := make(chan struct{})
	var ran atomic.Int32
	hold := func(context.Context) { <-release; ran.Add(1) }
	for range 2 {
		if !b.Go(t.Context(), "held", time.Minute, hold) {
			t.Fatal("a job within the limit was dropped")
		}
	}
	if b.Go(t.Context(), "extra", time.Minute, hold) {
		t.Error("a job past the limit was started")
	}
	if len(dropped) != 1 || dropped[0] != "extra" {
		t.Errorf("dropped = %v, want [extra]", dropped)
	}
	close(release)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	b.Wait(ctx)
	if got := ran.Load(); got != 2 {
		t.Errorf("jobs run = %d, want 2", got)
	}
	if !b.Go(t.Context(), "after", time.Minute, func(context.Context) {}) {
		t.Error("a job was dropped with nothing running")
	}
	b.Wait(ctx)
}

// A job started by a running job is never dropped: the follow-up of work
// already accepted, such as the mail of a reset just issued, must not be lost.
func TestAJobStartedByARunningJobIgnoresTheLimit(t *testing.T) {
	b := appaccess.NewBackground(slog.New(slog.DiscardHandler), appaccess.WithBackgroundLimit(1))
	var inner atomic.Bool
	started := b.Go(t.Context(), "outer", time.Minute, func(ctx context.Context) {
		if !b.Go(ctx, "inner", time.Minute, func(context.Context) { inner.Store(true) }) {
			t.Error("the follow-up of a running job was dropped")
		}
	})
	if !started {
		t.Fatal("outer dropped")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	b.Wait(ctx)
	if !inner.Load() {
		t.Error("the follow-up did not run")
	}
}
