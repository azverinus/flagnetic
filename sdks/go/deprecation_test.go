package flagnetic_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// deprecatingServer answers every SDK operation with a Deprecation header
// and a Sunset, the stream with an error and the events post with 202, and
// counts what it served.
type deprecatingServer struct {
	*httptest.Server
	rulesets, streams, batches atomic.Int64
}

func newDeprecatingServer(t *testing.T, deprecation, sunset string) *deprecatingServer {
	t.Helper()
	m := &deprecatingServer{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deprecation != "" {
			w.Header().Set("Deprecation", deprecation)
		}
		if sunset != "" {
			w.Header().Set("Sunset", sunset)
		}
		switch r.URL.Path {
		case "/sdk/v1/ruleset":
			m.rulesets.Add(1)
			ruleset(7)(w, r)
		case "/sdk/v1/stream":
			// An error answer carries the header too.
			m.streams.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/sdk/v1/events":
			m.batches.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(accepted))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

func TestADeprecationHeaderIsLoggedOncePerOperationAndFailsNothing(t *testing.T) {
	m := newDeprecatingServer(t, "@1767225600", "Wed, 01 Jul 2026 00:00:00 GMT")
	var l logs
	c, err := flagnetic.New(serverKey, m.URL, flagnetic.WithLogger(l.logger()),
		flagnetic.WithStream(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if stage := readyNow(t, c); stage != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh: a deprecated operation still serves", stage)
	}
	fc := flagnetic.Context{Units: map[string]string{"user": "u-1"}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 2 {
		c.Bool(ctx, fc, "new_checkout", false)
		if err := c.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}
	waitFor(t, func() (struct{}, bool) { return struct{}{}, m.streams.Load() >= 1 }, "a stream attempt")
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if m.batches.Load() < 2 {
		t.Fatalf("the events endpoint answered %d batches, want at least 2", m.batches.Load())
	}

	got := map[any]logEntry{}
	for _, e := range l.named(flagnetic.EventDeprecationReceived) {
		if _, dup := got[e.Fields["operation"]]; dup {
			t.Errorf("deprecation_received logged twice for %v", e.Fields["operation"])
		}
		got[e.Fields["operation"]] = e
	}
	for _, op := range []string{"ruleset", "stream", "events"} {
		e, ok := got[op]
		if !ok {
			t.Errorf("no deprecation_received for %s; logged %v", op, got)
			continue
		}
		if e.Level != slog.LevelWarn {
			t.Errorf("%s: level %v, want warn", op, e.Level)
		}
		want := map[string]any{"deprecation": "@1767225600", "deprecated_since": "2026-01-01T00:00:00Z", "sunset": "2026-07-01T00:00:00Z"}
		for k, v := range want {
			if e.Fields[k] != v {
				t.Errorf("%s: %s = %v, want %v", op, k, e.Fields[k], v)
			}
		}
	}
	if len(got) != 3 {
		t.Errorf("deprecation_received for %d operations, want 3", len(got))
	}
}

func TestEveryPollOfADeprecatedOperationIsSilentAfterTheFirst(t *testing.T) {
	m := newDeprecatingServer(t, "@1767225600", "")
	var l logs
	c, err := flagnetic.New(serverKey, m.URL, flagnetic.WithLogger(l.logger()),
		flagnetic.WithStream(false), flagnetic.WithSummaries(false),
		flagnetic.WithTimeDivisor(1000))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	waitFor(t, func() (struct{}, bool) { return struct{}{}, m.rulesets.Load() >= 4 }, "four fetches")
	if got := l.named(flagnetic.EventDeprecationReceived); len(got) != 1 {
		t.Errorf("deprecation_received logged %d times over %d fetches, want 1", len(got), m.rulesets.Load())
	}
}

func TestADeprecationWithoutASunsetOrInUnknownFormIsStillLogged(t *testing.T) {
	m := newDeprecatingServer(t, "true", "")
	var l logs
	c, err := flagnetic.New(serverKey, m.URL, flagnetic.WithLogger(l.logger()),
		flagnetic.WithStream(false), flagnetic.WithSummaries(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	if stage := readyNow(t, c); stage != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh", stage)
	}
	got := l.named(flagnetic.EventDeprecationReceived)
	if len(got) != 1 {
		t.Fatalf("deprecation_received logged %d times, want 1", len(got))
	}
	f := got[0].Fields
	if f["operation"] != "ruleset" || f["deprecation"] != "true" {
		t.Errorf("fields = %v", f)
	}
	if _, ok := f["sunset"]; ok {
		t.Errorf("sunset = %v, the answer carried none", f["sunset"])
	}
	if _, ok := f["deprecated_since"]; ok {
		t.Errorf("deprecated_since = %v from a header that names no date", f["deprecated_since"])
	}
}

func TestNoDeprecationHeaderLogsNothing(t *testing.T) {
	m := newDeprecatingServer(t, "", "")
	var l logs
	c, err := flagnetic.New(serverKey, m.URL, flagnetic.WithLogger(l.logger()),
		flagnetic.WithStream(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if stage := readyNow(t, c); stage != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh", stage)
	}
	c.Bool(context.Background(), flagnetic.Context{}, "new_checkout", false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := l.named(flagnetic.EventDeprecationReceived); len(got) != 0 {
		t.Errorf("deprecation_received logged without a header: %v", got)
	}
}
