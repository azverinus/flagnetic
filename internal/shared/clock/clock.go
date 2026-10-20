// Package clock is the one time source of the server. Every instant the server
// reasons about (expiry, leases, schedules, created_at) is read here and passed
// on, to the domain as an argument and to SQL as a query parameter; SQL never
// reads the database's clock. A test that moves the fake clock therefore moves
// everything at once.
package clock

import (
	"sync"
	"time"
)

// Clock tells the current time.
type Clock interface {
	Now() time.Time
}

// System is the wall clock.
type System struct{}

// Now returns the wall clock's time.
func (System) Now() time.Time { return time.Now() }

// Timers is implemented by a clock that can be waited on, such as Fake, so
// that moving the clock fires what waits on it.
type Timers interface {
	// After sends the clock's time on the returned channel once d has passed
	// on the clock.
	After(d time.Duration) <-chan time.Time
}

// After waits d on c: on c's own timers when it has them, otherwise on the
// wall clock.
func After(c Clock, d time.Duration) <-chan time.Time {
	if t, ok := c.(Timers); ok {
		return t.After(d)
	}
	return time.After(d)
}

// Fake is a clock that stands still until it is moved, for tests. Its timers
// fire when the clock is moved past them, so time-dependent behaviour is tested
// without sleeping.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	c  chan time.Time
}

// NewFake returns a fake clock reading now.
func NewFake(now time.Time) *Fake {
	return &Fake{now: now}
}

// Now returns the clock's time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the clock forward by d, firing every timer that falls due.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moveTo(f.now.Add(d))
}

// Set moves the clock to t, forward or back, firing every timer that falls due.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moveTo(t)
}

func (f *Fake) moveTo(t time.Time) {
	f.now = t
	pending := f.waiters[:0]
	for _, w := range f.waiters {
		if w.at.After(f.now) {
			pending = append(pending, w)
			continue
		}
		w.c <- f.now
	}
	f.waiters = pending
}

// After returns a channel that receives the clock's time once the clock has
// been moved d past its current time.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: f.now.Add(d), c: ch})
	return ch
}
