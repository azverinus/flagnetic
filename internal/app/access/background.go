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
	// limit is the most pieces of work running at once; one started past it is
	// dropped, so a client pipelining requests that each start one cannot
	// queue goroutines, each holding a pool connection, at wire speed.
	limit int
	// dropped counts work refused for want of room, by its name.
	dropped func(name string)
	// mu guards running and idle. A count of its own rather than a wait
	// group: one piece of work may start another (a password reset request queues its
	// mail) after Wait has begun, and that one must be awaited too, or the
	// reset is issued while its mail dies with the replica.
	mu      sync.Mutex
	running int
	// idle is closed when running falls to zero; nil while nothing runs.
	idle chan struct{}
}

// DefaultBackgroundLimit is how many pieces of work run at once unless the runner is
// given another limit.
const DefaultBackgroundLimit = 256

// BackgroundOption adjusts a runner.
type BackgroundOption func(*Background)

// WithBackgroundLimit sets the most pieces of work running at once; n below one leaves
// the default.
func WithBackgroundLimit(n int) BackgroundOption {
	return func(b *Background) {
		if n > 0 {
			b.limit = n
		}
	}
}

// WithDroppedCounter calls count with the name of every piece of work dropped for want
// of room.
func WithDroppedCounter(count func(name string)) BackgroundOption {
	return func(b *Background) { b.dropped = count }
}

// NewBackground returns the runner, which logs the panic of work to logger.
func NewBackground(logger *slog.Logger, opts ...BackgroundOption) *Background {
	b := &Background{logger: logger, limit: DefaultBackgroundLimit}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Go runs fn in a goroutine of its own, bounded by bound, with the values of
// ctx but not its cancellation: the request that started it ends first. A
// piece of work started while the limit is running is dropped, logged and
// counted, and Go reports false: shedding the work is what keeps a flood
// from exhausting the database connections the work holds.
func (b *Background) Go(ctx context.Context, name string, bound time.Duration, fn func(ctx context.Context)) bool {
	done, ok := b.track(inWork(ctx))
	if !ok {
		b.logger.WarnContext(ctx, "background work dropped: too many running", "work", name, "limit", b.limit)
		if b.dropped != nil {
			b.dropped(name)
		}
		return false
	}
	bg, cancel := context.WithTimeout(context.WithValue(context.WithoutCancel(ctx), workKey{}, true), bound)
	safego.Go(bg, name, b.logger, func(ctx context.Context) {
		defer done()
		defer cancel()
		fn(ctx)
	})
	return true
}

// workKey marks the context of running work.
type workKey struct{}

// inWork reports whether ctx belongs to running work.
func inWork(ctx context.Context) bool { ok, _ := ctx.Value(workKey{}).(bool); return ok }

// track counts work Wait awaits and returns what ends it, or false when the
// limit is already running. Work started by running work (a password
// reset request queues its mail after committing the reset) is exempt: the
// limit sheds incoming work, and dropping the follow-up of work already
// accepted would leave a reset issued whose mail never leaves.
func (b *Background) track(nested bool) (done func(), ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !nested && b.running >= b.limit {
		return nil, false
	}
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
	}, true
}

// Wait returns once all work started so far, and all work that starts
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
