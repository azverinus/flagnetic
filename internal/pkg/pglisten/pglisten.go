// Package pglisten keeps a process listening on a PostgreSQL notification
// channel, and tells it when it is not. Code that caches what PostgreSQL owns
// learns of changes this way; while the listener is down a notification may
// be missed, so such code stops trusting what it holds.
//
// A connection can go deaf without failing: a pooler in between, or a
// half-dead network path, delivers nothing and reports no error. So the
// listener sends itself a notification on a channel of its own every ping
// interval and expects it back within the ping timeout; a miss counts it as
// down, and it reconnects.
package pglisten

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

// Handler is told when listening starts and stops and receives every
// notification's payload in between.
type Handler interface {
	// Listening is called with true once the channel is listened on, and
	// with false whenever that ends.
	Listening(up bool)
	// Received is called with each notification's payload, in order, on the
	// listener's own loop. It must return promptly, handing slow work to
	// another goroutine: while it runs the listener's ping is not read, and a
	// late ping counts the listener as deaf.
	Received(payload string)
}

// Options tune a listener. The zero value of each field is its default.
type Options struct {
	Logger *slog.Logger
	// PingInterval is how often the listener checks itself: 10s.
	PingInterval time.Duration
	// PingTimeout is how long its ping may take to come back: 5s.
	PingTimeout time.Duration
	// MinBackoff and MaxBackoff bound the wait before reconnecting, doubled
	// after each failure: 100ms to 5s.
	MinBackoff, MaxBackoff time.Duration
	// Fault lets a test silence delivery; nil in production.
	Fault *Fault
}

func (o Options) withDefaults() Options {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.PingInterval <= 0 {
		o.PingInterval = 10 * time.Second
	}
	if o.PingTimeout <= 0 {
		o.PingTimeout = 5 * time.Second
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = 100 * time.Millisecond
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 5 * time.Second
	}
	return o
}

// setupTimeout bounds connecting and subscribing, so a database that accepts
// the connection and then answers nothing counts as a failed attempt.
const setupTimeout = 10 * time.Second

// Fault silences a listener's delivery while its connection stays open, the
// way a pooler or a half-dead connection does, so a test can watch the ping
// detect it. Once found deaf, the listener stays down until Restore instead
// of reconnecting into the same silence: a listener that listened again
// while silenced would report itself up and drop the next notification
// before its ping found it deaf, so a test could not tell whether a write
// it made while the listener was down was missed or heard.
type Fault struct {
	mu       sync.Mutex
	silenced bool
	// restored is closed by the Restore that ends the current silence.
	restored chan struct{}
}

// Silence drops every notification from now on, pings included, and keeps
// the listener from listening again until Restore.
func (f *Fault) Silence() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.silenced {
		f.silenced = true
		f.restored = make(chan struct{})
	}
}

// Restore delivers notifications again and lets the listener reconnect.
func (f *Fault) Restore() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.silenced {
		f.silenced = false
		close(f.restored)
	}
}

func (f *Fault) dropped() bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.silenced
}

// awaitRestored returns once the listener is not silenced or ctx ends.
func (f *Fault) awaitRestored(ctx context.Context) {
	if f == nil {
		return
	}
	f.mu.Lock()
	silenced, restored := f.silenced, f.restored
	f.mu.Unlock()
	if !silenced {
		return
	}
	select {
	case <-restored:
	case <-ctx.Done():
	}
}

var errDeaf = errors.New("the listener's own ping did not come back in time")

// Run listens on channel over a connection of its own, made from cfg and
// never taken from a pool, until ctx ends. It reconnects with backoff, reset
// after every connection that got as far as listening. A panic in the handler
// is recovered, logged with its stack, counted through the panic hook of ctx
// (see safego) and treated as a lost connection.
func Run(ctx context.Context, cfg *pgx.ConnConfig, channel string, h Handler, o Options) {
	o = o.withDefaults()
	l := listener{cfg: cfg, channel: channel, h: h, o: o}
	backoff := o.MinBackoff
	for {
		o.Fault.awaitRestored(ctx)
		if ctx.Err() != nil {
			return
		}
		listened, err := l.recovered(ctx)
		if ctx.Err() != nil {
			return
		}
		if listened {
			backoff = o.MinBackoff
		}
		o.Logger.Warn("notification listener down; reconnecting",
			"channel", channel, "error", fmt.Sprint(err), "retry_in", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, o.MaxBackoff)
	}
}

type listener struct {
	cfg     *pgx.ConnConfig
	channel string
	h       Handler
	o       Options
}

// recovered runs one session under safego, so a panic in the handler is
// logged with its stack and counted through the context's panic hook, under
// the same name the listener's goroutine runs with.
func (l listener) recovered(ctx context.Context) (listened bool, err error) {
	err = safego.Call(ctx, "listener "+l.channel, l.o.Logger, func(ctx context.Context) error {
		return l.session(ctx, &listened)
	})
	return listened, err
}

// session listens on one connection until it fails, goes deaf or ctx ends.
// listened is set once the channel is listened on.
func (l listener) session(ctx context.Context, listened *bool) error {
	setupCtx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(setupCtx, l.cfg.Copy())
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	pingChannel := "pglisten_ping_" + strings.ToLower(rand.Text())
	for _, ch := range []string{l.channel, pingChannel} {
		if _, err := conn.Exec(setupCtx, "LISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
			return err
		}
	}
	*listened = true
	defer l.h.Listening(false)
	l.h.Listening(true)

	nextPing := time.Now().Add(l.o.PingInterval)
	var pending string // the ping awaited, if any
	var pongBy time.Time
	for {
		wake := nextPing
		if pending != "" && pongBy.Before(wake) {
			wake = pongBy
		}
		waitCtx, cancel := context.WithDeadline(ctx, wake)
		n, err := conn.WaitForNotification(waitCtx)
		cancel()
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil && !errors.Is(err, context.DeadlineExceeded):
			return err
		case err == nil:
			if l.o.Fault.dropped() {
				continue
			}
			if n.Channel == pingChannel {
				if n.Payload == pending {
					pending = ""
				}
				continue
			}
			l.h.Received(n.Payload)
			continue
		}

		// Woken by the clock: a ping overdue, or one due.
		now := time.Now()
		if pending != "" && !now.Before(pongBy) {
			return errDeaf
		}
		if !now.Before(nextPing) {
			if pending == "" {
				pending = rand.Text()
				pongBy = now.Add(l.o.PingTimeout)
				// Bounded by the ping timeout too: on a half-dead path the
				// send itself may hang.
				sendCtx, cancel := context.WithDeadline(ctx, pongBy)
				_, err := conn.Exec(sendCtx, "SELECT pg_notify($1, $2)", pingChannel, pending)
				cancel()
				if err != nil {
					return fmt.Errorf("send the listener's ping: %w", err)
				}
			}
			nextPing = now.Add(l.o.PingInterval)
		}
	}
}
