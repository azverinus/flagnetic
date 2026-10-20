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
	"time"
)

// Defaults for the change stream.
const (
	// defaultDownPollInterval is the safety poll while the change stream
	// is down or disabled; while it is connected the poll interval serves.
	defaultDownPollInterval = 30 * time.Second
	// heartbeatTimeout is how long the stream may stay silent. The server
	// sends a comment every 15 seconds, so three missed ones reconnect.
	heartbeatTimeout = 45 * time.Second
	// streamRetryCap bounds the reconnect backoff after an error.
	streamRetryCap = 30 * time.Second
	// closeJitterCap bounds the wait after a server-side close, which is
	// routine: the platform closes every stream after about an hour.
	closeJitterCap = 2 * time.Second
	// stableFor is how long a connection must hold for the reconnect
	// backoff to start from the base again.
	stableFor = 60 * time.Second
	// rulesetEvent is the only event the stream carries.
	rulesetEvent = "ruleset"
)

// catchUpDelays are the waits before re-fetching when the ruleset endpoint
// answered below the version an event announced, a replica still behind.
// After the last one the safety poll takes over.
var catchUpDelays = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// Causes of an sse_dropped event.
const (
	causeServerClosed = "server_closed"
	causeSilence      = "silence"
	causeMalformed    = "malformed"
)

// streamOutcome is how one connection to the change stream ended.
type streamOutcome int

const (
	// streamErrored: the connection never came up or broke; reconnect with
	// exponential backoff.
	streamErrored streamOutcome = iota
	// streamClosed: the server ended a healthy stream, as it does about
	// once an hour; reconnect after a short jitter.
	streamClosed
	// streamStopped: the key was refused or the client is closing; the
	// stream stays down and the fetch loop carries on alone.
	streamStopped
)

// streamLoop keeps the change stream open, reconnecting until ctx is done.
// A refused key stops it for good; the fetch loop keeps serving and keeps
// retrying at the unauthorized cap.
func (c *Client) streamLoop(ctx context.Context) {
	failures := 0
	for {
		started := time.Now()
		outcome := c.streamOnce(ctx)
		c.firstAttemptDone()
		if outcome == streamStopped || ctx.Err() != nil {
			return
		}
		if time.Since(started) >= c.wait.stableFor {
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

// streamOnce opens the change stream and reads it until it ends.
func (c *Client) streamOnce(ctx context.Context) streamOutcome {
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.streamURL, nil)
	if err != nil {
		c.dropped(causeNetwork, err, 0)
		return streamErrored
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
	resp, err := c.streamHTTP.Do(req)
	// The first fetch waits for this attempt, so that the stream is open
	// before it and no change falls between the two.
	c.firstAttemptDone()
	if err != nil {
		if ctx.Err() != nil {
			return streamStopped
		}
		c.dropped(transportCause(err), err, 0)
		return streamErrored
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		c.keyRevoked(resp.StatusCode)
		c.dropped(causeUnauthorized, fmt.Errorf("stream: %s", resp.Status), resp.StatusCode)
		return streamStopped
	default:
		c.dropped(causeHTTPStatus, fmt.Errorf("stream: %s", resp.Status), resp.StatusCode)
		return streamErrored
	}

	c.streamConnected.Store(true)
	defer c.streamConnected.Store(false)
	c.log(slog.LevelInfo, EventSSEConnected, map[string]any{"last_event_id": lastEventID})
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
// closing.
func (c *Client) readStream(ctx context.Context, cancel context.CancelFunc, body io.Reader) streamOutcome {
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
	for {
		frame, err := r.next()
		c.retryHint = r.retry
		if err != nil {
			select {
			case <-silent:
				c.dropped(causeSilence, fmt.Errorf("no frame for %v", c.wait.heartbeat), 0)
				return streamErrored
			default:
			}
			if ctx.Err() != nil {
				return streamStopped
			}
			if errors.Is(err, io.EOF) {
				c.dropped(causeServerClosed, err, 0)
				return streamClosed
			}
			c.dropped(transportCause(err), err, 0)
			return streamErrored
		}
		select {
		case beat <- struct{}{}:
		default:
		}
		if frame.Comment || frame.Event != rulesetEvent {
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
			return streamErrored
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

// dropped logs sse_dropped. reconnect is false only where the stream stays
// down, which is a refused key.
func (c *Client) dropped(cause string, err error, status int) {
	fields := map[string]any{"cause": cause, "error": err.Error(), "reconnect": cause != causeUnauthorized}
	if status != 0 {
		fields["status"] = status
	}
	level := slog.LevelWarn
	if cause == causeServerClosed {
		level = slog.LevelInfo
	}
	c.log(level, EventSSEDropped, fields)
}

// heldVersion is the version of the held ruleset, zero at stage built_in.
func (c *Client) heldVersion() int64 {
	if held := c.ruleset.Load(); held != nil {
		return held.rs.Version
	}
	return 0
}

// keyRevoked logs key_revoked once, however many of the fetch loop and the
// change stream see the refusal, until a fetch succeeds again.
func (c *Client) keyRevoked(status int) {
	if c.revoked.CompareAndSwap(false, true) {
		c.log(slog.LevelError, EventKeyRevoked, map[string]any{"status": status})
	}
}
