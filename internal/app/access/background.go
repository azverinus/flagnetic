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
	// mu guards running and idle. A count of its own rather than a wait
	// group: a job may start another (a password reset request queues its
	// mail) after Wait has begun, and that one must be awaited too, or the
	// reset is issued while its mail dies with the replica.
	mu      sync.Mutex
	running int
	// idle is closed when running falls to zero; nil while nothing runs.
	idle chan struct{}
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

// track counts a job Wait awaits and returns what ends it.
func (b *Background) track() (done func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running == 0 {
		b.idle = make(chan struct{})
	}
	b.running++
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.running--
		if b.running == 0 {
			close(b.idle)
			b.idle = nil
		}
	}
}

// Wait returns once every job started so far, and every job those start
// while they run, has ended, or ctx ends. A replica stopping calls it after
// its requests are done.
func (b *Background) Wait(ctx context.Context) {
	b.mu.Lock()
	idle := b.idle
	b.mu.Unlock()
	if idle == nil {
		return
	}
	select {
	case <-idle:
	case <-ctx.Done():
	}
}
