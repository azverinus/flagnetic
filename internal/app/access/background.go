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
	// limit is the most jobs running at once; a job started past it is
	// dropped, so a client pipelining requests that each start one cannot
	// queue goroutines, each holding a pool connection, at wire speed.
	limit int
	// dropped counts a job refused for want of room, by its name.
	dropped func(name string)
	// mu guards running and idle. A count of its own rather than a wait
	// group: a job may start another (a password reset request queues its
	// mail) after Wait has begun, and that one must be awaited too, or the
	// reset is issued while its mail dies with the replica.
	mu      sync.Mutex
	running int
	// idle is closed when running falls to zero; nil while nothing runs.
	idle chan struct{}
}

// DefaultBackgroundLimit is how many jobs run at once unless the runner is
// given another limit.
const DefaultBackgroundLimit = 256

// BackgroundOption adjusts a runner.
type BackgroundOption func(*Background)

// WithBackgroundLimit sets the most jobs running at once; n below one leaves
// the default.
func WithBackgroundLimit(n int) BackgroundOption {
	return func(b *Background) {
		if n > 0 {
			b.limit = n
		}
	}
}

// WithDroppedCounter calls count with the name of every job dropped for want
// of room.
func WithDroppedCounter(count func(name string)) BackgroundOption {
	return func(b *Background) { b.dropped = count }
}

// NewBackground returns the runner, which logs a job's panic to logger.
func NewBackground(logger *slog.Logger, opts ...BackgroundOption) *Background {
	b := &Background{logger: logger, limit: DefaultBackgroundLimit}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Go runs fn in a goroutine of its own, bounded by bound, with the values of
// ctx but not its cancellation: the request that started it ends first. A
// job started while the limit of jobs is running is dropped, logged and
// counted, and Go reports false: shedding the work is what keeps a flood
// from exhausting the database connections the jobs hold.
func (b *Background) Go(ctx context.Context, name string, bound time.Duration, fn func(ctx context.Context)) bool {
	done, ok := b.track(inJob(ctx))
	if !ok {
		b.logger.WarnContext(ctx, "background job dropped: too many running", "job", name, "limit", b.limit)
		if b.dropped != nil {
			b.dropped(name)
		}
		return false
	}
	bg, cancel := context.WithTimeout(context.WithValue(context.WithoutCancel(ctx), jobKey{}, true), bound)
	safego.Go(bg, name, b.logger, func(ctx context.Context) {
		defer done()
		defer cancel()
		fn(ctx)
	})
	return true
}

// jobKey marks the context of a running job.
type jobKey struct{}

// inJob reports whether ctx belongs to a running job.
func inJob(ctx context.Context) bool { ok, _ := ctx.Value(jobKey{}).(bool); return ok }

// track counts a job Wait awaits and returns what ends it, or false when the
// limit of jobs is already running. A job started by a running job (a password
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
