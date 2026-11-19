package access_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
)

// Work that starts more once the replica has begun waiting, as a password
// reset request queues its mail, has that one awaited too: otherwise the reset
// would be issued and its mail lost with the replica.
func TestWaitingAwaitsWorkStartedByWorkInFlight(t *testing.T) {
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
		t.Fatal("the work was still running after 5 seconds")
	}
	if !sent.Load() {
		t.Error("Wait returned before the work started by the work in flight ended")
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

// Work past the limit is dropped and counted rather than queued, so a flood of
// requests cannot pile up goroutines; once running work ends there is room again.
func TestWorkPastTheLimitIsDroppedAndCounted(t *testing.T) {
	var dropped []string
	b := appaccess.NewBackground(slog.New(slog.DiscardHandler), appaccess.WithBackgroundLimit(2),
		appaccess.WithDroppedCounter(func(name string) { dropped = append(dropped, name) }))
	release := make(chan struct{})
	var ran atomic.Int32
	hold := func(context.Context) { <-release; ran.Add(1) }
	for range 2 {
		if !b.Go(t.Context(), "held", time.Minute, hold) {
			t.Fatal("work within the limit was dropped")
		}
	}
	if b.Go(t.Context(), "extra", time.Minute, hold) {
		t.Error("work past the limit was started")
	}
	if len(dropped) != 1 || dropped[0] != "extra" {
		t.Errorf("dropped = %v, want [extra]", dropped)
	}
	close(release)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	b.Wait(ctx)
	if got := ran.Load(); got != 2 {
		t.Errorf("work run = %d, want 2", got)
	}
	if !b.Go(t.Context(), "after", time.Minute, func(context.Context) {}) {
		t.Error("work was dropped with nothing running")
	}
	b.Wait(ctx)
}

// Work started by running work is never dropped: the follow-up of work
// already accepted, such as the mail of a reset just issued, must not be lost.
func TestWorkStartedByRunningWorkIgnoresTheLimit(t *testing.T) {
	b := appaccess.NewBackground(slog.New(slog.DiscardHandler), appaccess.WithBackgroundLimit(1))
	var inner atomic.Bool
	started := b.Go(t.Context(), "outer", time.Minute, func(ctx context.Context) {
		if !b.Go(ctx, "inner", time.Minute, func(context.Context) { inner.Store(true) }) {
			t.Error("the follow-up of running work was dropped")
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
