package access

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

// Background runs work that outlives the request that caused it, such as a
// mail or the part of a request that must not be felt by whoever asked, and
// is awaited when the replica stops, so what the requests caused leaves
// with it.
type Background struct {
	logger *slog.Logger
	wg     sync.WaitGroup
	// mu orders each job's place in wg before Wait begins: once waiting is
	// set, a job still runs but is not awaited, since adding to an empty
	// wait group while it is being waited on is not allowed.
	mu      sync.Mutex
	waiting bool
}

// NewBackground returns the runner, which logs a job's panic to logger.
func NewBackground(logger *slog.Logger) *Background { return &Background{logger: logger} }

// Go runs fn in a goroutine of its own, bounded by bound, with the values of
// ctx but not its cancellation: the request that started it ends first.
func (b *Background) Go(ctx context.Context, name string, bound time.Duration, fn func(ctx context.Context)) {
	done := b.track()
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), bound)
	safego.Go(bg, name, b.logger, func(ctx context.Context) {
		defer done()
		defer cancel()
		fn(ctx)
	})
}

// track counts a job Wait awaits and returns what ends it, or nothing to
// end once Wait has begun.
func (b *Background) track() (done func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiting {
		return func() {}
	}
	b.wg.Add(1)
	return b.wg.Done
}

// Wait returns once every job started so far has ended, or ctx ends. A
// replica stopping calls it after its requests are done.
func (b *Background) Wait(ctx context.Context) {
	b.mu.Lock()
	b.waiting = true
	b.mu.Unlock()
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
