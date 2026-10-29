package access

import (
	"context"
	"sync"
)

// listenedCache is a replica's copy of a value one row of the database
// holds, dropped when a notification announces a change. It trusts its copy
// only while its listener is up: a change announced while the listener is
// down may be missed, so until it is listening again every read goes to
// PostgreSQL.
type listenedCache[T any] struct {
	load func(context.Context) (T, error)

	mu        sync.Mutex
	listening bool
	// generation counts what may have made a read stale: a notification, or
	// the listener going up or down. A read is kept only when no such event
	// happened while it was under way.
	generation uint64
	cached     *T
}

func newListenedCache[T any](load func(context.Context) (T, error)) *listenedCache[T] {
	return &listenedCache[T]{load: load}
}

func (c *listenedCache[T]) get(ctx context.Context) (T, error) {
	c.mu.Lock()
	if c.cached != nil {
		v := *c.cached
		c.mu.Unlock()
		return v, nil
	}
	generation := c.generation
	c.mu.Unlock()
	v, err := c.load(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	c.mu.Lock()
	if c.listening && c.generation == generation {
		c.cached = &v
	}
	c.mu.Unlock()
	return v, nil
}

// Forget drops the copy, as a change made by this replica does once it is
// committed, so the replica's next read sees it without waiting for the
// notification.
func (c *listenedCache[T]) Forget() { c.invalidate(nil) }

// Listening is told when the listener starts or stops listening.
func (c *listenedCache[T]) Listening(up bool) { c.invalidate(&up) }

// Received is told of each change announced.
func (c *listenedCache[T]) Received(string) { c.invalidate(nil) }

func (c *listenedCache[T]) invalidate(listening *bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if listening != nil {
		c.listening = *listening
	}
	c.generation++
	c.cached = nil
}
