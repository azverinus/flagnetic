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
