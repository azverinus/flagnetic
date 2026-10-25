package testkit

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Frame is one server-sent event frame: its fields by name, a comment under
// "comment".
type Frame map[string]string

// EventStream is a server-sent event stream a test holds open.
type EventStream struct {
	// Response is the answer to the request that opened the stream; its
	// body is read by the stream unless the status is not 200.
	Response *http.Response
	frames   chan Frame
	cancel   context.CancelFunc
}

// OpenEventStream sends req through c, nil for the default client, and
// returns its answer whatever its status; a 200's frames are read as they
// arrive. The stream is closed when the test ends, or by Close.
func OpenEventStream(t testing.TB, c *http.Client, req *http.Request) *EventStream {
	t.Helper()
	if c == nil {
		c = http.DefaultClient
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	res, err := c.Do(req.WithContext(ctx)) //nolint:gosec // a test opens the stream of the server it started
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &EventStream{Response: res, frames: make(chan Frame, 64), cancel: cancel}
	if res.StatusCode != http.StatusOK {
		close(s.frames)
		return s
	}
	go func() {
		defer close(s.frames)
		defer func() { _ = res.Body.Close() }()
		sc := bufio.NewScanner(res.Body)
		f := Frame{}
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if len(f) > 0 {
					s.frames <- f
				}
				f = Frame{}
				continue
			}
			if c, ok := strings.CutPrefix(line, ":"); ok {
				f["comment"] = strings.TrimSpace(c)
				continue
			}
			name, value, _ := strings.Cut(line, ":")
			f[name] = strings.TrimPrefix(value, " ")
		}
	}()
	return s
}

// Close ends the stream from the client's side.
func (s *EventStream) Close() { s.cancel() }

// Started fails unless s is an open event stream, unbuffered and uncached,
// whose first frame is retry: 3000 alone. A stream's timers are armed by
// the time it is written, so a test may move its clock from then on.
func (s *EventStream) Started(t testing.TB) {
	t.Helper()
	if s.Response.StatusCode != http.StatusOK {
		t.Fatalf("stream = %d, want 200", s.Response.StatusCode)
	}
	h := s.Response.Header
	if !strings.HasPrefix(h.Get("Content-Type"), "text/event-stream") || h.Get("Cache-Control") != "no-cache" || h.Get("X-Accel-Buffering") != "no" {
		t.Errorf("headers = %v, want an event stream, no-cache, unbuffered", h)
	}
	if f := s.Next(t); len(f) != 1 || f["retry"] != "3000" {
		t.Fatalf("first frame = %v, want retry: 3000 alone", f)
	}
}

// Next returns the next frame, failing when none arrives within 5 seconds.
func (s *EventStream) Next(t testing.TB) Frame {
	t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			t.Fatal("the stream ended, want a frame")
		}
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5 seconds")
	}
	return nil
}

// Quiet fails when a frame arrives, or the stream ends, within d.
func (s *EventStream) Quiet(t testing.TB, d time.Duration) {
	t.Helper()
	select {
	case f, ok := <-s.frames:
		if ok {
			t.Errorf("frame %v, want none", f)
		} else {
			t.Error("the stream ended, want it quiet")
		}
	case <-time.After(d):
	}
}

// Ended reports whether the stream ends within d, frames before the end
// skipped.
func (s *EventStream) Ended(d time.Duration) bool {
	deadline := time.After(d)
	for {
		select {
		case _, ok := <-s.frames:
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// IsHeartbeat reports whether f is the heartbeat comment.
func (f Frame) IsHeartbeat() bool { return len(f) == 1 && f["comment"] == "hb" }
