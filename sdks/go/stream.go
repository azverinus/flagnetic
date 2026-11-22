package flagnetic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// Defaults for the ruleset stream.
const (
	// defaultDownPollInterval is the safety poll while the ruleset stream
	// is down or disabled; while it is connected the poll interval serves.
	defaultDownPollInterval = 30 * time.Second
	// heartbeatTimeout is how long the stream may stay silent. The server
	// sends a comment every 15 seconds, so three missed ones reconnect.
	heartbeatTimeout = 45 * time.Second
	// streamRetryCap bounds the reconnect backoff after an error.
	streamRetryCap = 30 * time.Second
	// closeJitterCap bounds the wait after a server-side close of a stable
	// stream, which is routine: the platform closes every stream after
	// about an hour.
	closeJitterCap = 2 * time.Second
	// rulesetEvent is the only event the stream carries.
	rulesetEvent = "ruleset"
)

// catchUpDelays are the waits before re-fetching when the ruleset endpoint
// answered below the version an event announced, a replica still behind.
// After the last one the safety poll takes over.
var catchUpDelays = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// Causes of a stream_dropped event.
const (
	causeServerClosed = "server_closed"
	// causeClosedEarly is a stream the server closed before its first
	// heartbeat, as a draining replica or a proxy that ends an event
	// stream early does.
	causeClosedEarly = "closed_early"
	causeSilence     = "silence"
	causeMalformed   = "malformed"
)

// streamOutcome is how one connection to the ruleset stream ended.
type streamOutcome int

const (
	// streamErrored: the connection never came up, broke, or was closed
	// before it was stable; reconnect with exponential backoff.
	streamErrored streamOutcome = iota
	// streamClosed: the server ended a stable stream, one that had
	// delivered a heartbeat, as it does about once an hour; reconnect after
	// a short jitter.
	streamClosed
	// streamStopped: the client is closing.
	streamStopped
	// streamRefused: the key was refused; the stream stays down, the fetch
	// loop carries on alone, and a fetch that succeeds after the refusal
	// opens the stream again.
	streamRefused
)

// streamLoop keeps the ruleset stream open, reconnecting until ctx is done.
// A refused key keeps it closed while the fetch loop, which keeps serving and
// retrying at the unauthorized cap, is refused too.
func (c *Client) streamLoop(ctx context.Context) {
	failures := 0
	for {
		outcome, stable := c.streamOnce(ctx)
		c.firstAttemptDone()
		if outcome == streamStopped || ctx.Err() != nil {
			return
		}
		if outcome == streamRefused {
			if !c.awaitReopen(ctx) {
				return
			}
			continue
		}
		// A stream is stable once it has delivered a heartbeat: one that
		// ends before that, however it was answered, is a failed
		// connection, so a draining replica or a proxy that closes every
		// stream at once is backed off from instead of reconnected to
		// every other second.
		if stable {
			failures = 0
		}
		var wait time.Duration
		if outcome == streamClosed {
			// The server's own retry hint, never longer than the
			// protocol's window, so a deploy's streams come back spread out.
			ceiling := c.wait.closeJitterCap
			if c.retryHint > 0 && c.retryHint < ceiling {
				ceiling = c.retryHint
			}
			wait = c.jitter(ceiling)
		} else {
			failures++
			wait = c.jitter(backoffCeiling(failures, c.wait.retryBase, c.wait.streamRetryCap))
		}
		if !c.sleep(ctx, wait) {
			return
		}
	}
}

// streamOnce opens the ruleset stream and reads it until it ends, reporting
// how it ended and whether it was stable, that is, delivered a heartbeat.
func (c *Client) streamOnce(ctx context.Context) (outcome streamOutcome, stable bool) {
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.streamURL, nil)
	if err != nil {
		c.dropped(causeNetwork, err, 0)
		return streamErrored, false
	}
	req.Header.Set("Authorization", "Bearer "+c.sdkKey)
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	// The held version is where the stream resumes: the server sends the
	// current version at once when it is above, nothing when it is not.
	lastEventID := c.heldVersion()
	if lastEventID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(lastEventID, 10))
	}
	// The stream's client has no Timeout, so that an open stream may last
	// an hour; the wait for the response to start is bounded like any other
	// request, or a connection that is accepted and never answered would
	// hold this attempt, and every reconnect after it, for good. The bound
	// is stopped once the headers arrive: from then on the heartbeat
	// timeout watches the stream.
	var headersLate atomic.Bool
	headerTimer := time.AfterFunc(c.wait.requestTimeout, func() {
		headersLate.Store(true)
		cancel()
	})
	resp, err := c.streamHTTP.Do(req)
	// Stop reports false when the timer has fired, which may be just after
	// the headers arrived: the request is cancelled either way.
	timedOut := !headerTimer.Stop() && headersLate.Load()
	// The first fetch waits for this attempt, so that the stream is open
	// before it and no change falls between the two.
	c.firstAttemptDone()
	if timedOut {
		if err == nil {
			_ = resp.Body.Close()
		}
		err = fmt.Errorf("stream: no response within %v: %w", c.wait.requestTimeout, context.DeadlineExceeded)
	}
	if err != nil {
		if ctx.Err() != nil {
			return streamStopped, false
		}
		c.dropped(transportCause(err), err, 0)
		return streamErrored, false
	}
	defer func() { _ = resp.Body.Close() }()
	c.noteDeprecation(operationStream, resp.Header)

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		c.keyRevoked(resp.StatusCode)
		c.dropped(causeUnauthorized, fmt.Errorf("stream: %s", resp.Status), resp.StatusCode)
		return streamRefused, false
	default:
		c.dropped(causeHTTPStatus, fmt.Errorf("stream: %s", resp.Status), resp.StatusCode)
		return streamErrored, false
	}

	c.streamConnected.Store(true)
	defer func() {
		c.streamConnected.Store(false)
		// The fetch loop may be asleep on the long poll interval chosen
		// while the stream was up: tell it the safety poll applies now.
		select {
		case c.streamDown <- struct{}{}:
		default:
		}
	}()
	c.log(slog.LevelInfo, EventStreamConnected, map[string]any{"last_event_id": lastEventID})
	if !c.firstConnect {
		// Versions may have been missed while the stream was down.
		c.signal()
	}
	c.firstConnect = false
	return c.readStream(rctx, cancel, resp.Body)
}

// readStream reads frames until the stream ends or falls silent for the
// heartbeat timeout, acting on every ruleset event. cancel is the
// request's own: cancelling it is what unblocks a read of the body, so the
// heartbeat watchdog needs it, while ctx says whether the client is
// closing. It reports whether a heartbeat arrived, with the outcome.
func (c *Client) readStream(ctx context.Context, cancel context.CancelFunc, body io.Reader) (streamOutcome, bool) {
	beat := make(chan struct{}, 1)
	silent := make(chan struct{})
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		t := time.NewTimer(c.wait.heartbeat)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				close(silent)
				cancel()
				return
			case <-beat:
				t.Stop()
				t.Reset(c.wait.heartbeat)
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() {
		cancel()
		<-watching
	}()

	r := newSSEReader(body)
	heartbeat := false
	for {
		frame, err := r.next()
		c.retryHint = r.retry
		if err != nil {
			select {
			case <-silent:
				c.dropped(causeSilence, fmt.Errorf("no frame for %v", c.wait.heartbeat), 0)
				return streamErrored, heartbeat
			default:
			}
			if ctx.Err() != nil {
				return streamStopped, heartbeat
			}
			if errors.Is(err, io.EOF) {
				if !heartbeat {
					c.dropped(causeClosedEarly, err, 0)
					return streamErrored, false
				}
				c.dropped(causeServerClosed, err, 0)
				return streamClosed, true
			}
			c.dropped(transportCause(err), err, 0)
			return streamErrored, heartbeat
		}
		select {
		case beat <- struct{}{}:
		default:
		}
		if frame.Comment {
			heartbeat = true
			continue
		}
		if frame.Event != rulesetEvent {
			continue
		}
		var announced struct {
			Version int64 `json:"version"`
		}
		if err := json.Unmarshal([]byte(frame.Data), &announced); err != nil {
			// A ruleset event the SDK cannot read means the stream is not
			// what it claims to be: drop it, and the fetch that follows the
			// reconnect finds whatever was announced.
			c.dropped(causeMalformed, err, 0)
			return streamErrored, heartbeat
		}
		if announced.Version > c.heldVersion() {
			c.announce(announced.Version)
		}
	}
}

// announce records the version an event carries and asks the fetch loop
// for a ruleset, which it revalidates with If-None-Match.
func (c *Client) announce(version int64) {
	for {
		held := c.target.Load()
		if version <= held || c.target.CompareAndSwap(held, version) {
			break
		}
	}
	c.signal()
}

// awaitReopen waits, after the stream was refused, for a fetch that
// succeeds after the refusal: the key the stream was refused for answers the
// fetch, a restored key or a refusal that was a proxy's, so the stream is
// tried again. A refused stream is not retried on its own, since every
// attempt would be refused like the fetch. It reports false when ctx is done.
func (c *Client) awaitReopen(ctx context.Context) bool {
	// The fetch that follows a refusal at startup shows nothing the refusal
	// did not, since both went out with the same key at once: the stream is
	// tried again by the one after it.
	after := max(c.fetchSuccesses.Load(), 1)
	for c.fetchSuccesses.Load() <= after {
		select {
		case <-c.fetchSuccess:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// signal wakes the fetch loop, coalescing with a wake already pending.
func (c *Client) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// firstAttemptDone releases the first fetch, which waits for the stream to
// be open or to have failed.
func (c *Client) firstAttemptDone() {
	c.streamReadyOnce.Do(func() { close(c.streamReady) })
}

// dropped logs stream_dropped. reconnect is false only where the stream stays
// down until a fetch succeeds, which is a refused key.
func (c *Client) dropped(cause string, err error, status int) {
	fields := map[string]any{"cause": cause, "error": err.Error(), "reconnect": cause != causeUnauthorized}
	if status != 0 {
		fields["status"] = status
	}
	level := slog.LevelWarn
	if cause == causeServerClosed {
		level = slog.LevelInfo
	}
	c.log(level, EventStreamDropped, fields)
}

// heldVersion is the version of the held ruleset, zero at stage built_in.
func (c *Client) heldVersion() int64 {
	if held := c.ruleset.Load(); held != nil {
		return held.rs.Version
	}
	return 0
}

// keyRevoked logs key_revoked once, however many of the fetch loop and the
// ruleset stream see the refusal, until a fetch succeeds again.
func (c *Client) keyRevoked(status int) {
	if c.revoked.CompareAndSwap(false, true) {
		c.log(slog.LevelError, EventKeyRevoked, map[string]any{"status": status})
	}
}
