package flagnetic_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// serverKey is a server SDK key in the format the server issues.
const serverKey = "fn_srv_AbCdEfGh0123456789AbCdEfGh0123456789"

// documentAt is document carrying ruleset version v.
func documentAt(v int64) string {
	return strings.Replace(document, `"version": 7,`, fmt.Sprintf(`"version": %d,`, v), 1)
}

// response answers one request of the mock server.
type response func(w http.ResponseWriter, r *http.Request)

// ruleset answers 200 with document at version v, as the endpoint does.
func ruleset(v int64) response {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, v))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(documentAt(v)))
	}
}

// body answers 200 with a raw body.
func body(b string) response {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(b))
	}
}

// gzipped answers 200 with document at version v, gzip-encoded.
func gzipped(v int64) response {
	return func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write([]byte(documentAt(v)))
		_ = zw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, v))
		_, _ = w.Write(buf.Bytes())
	}
}

func status(code int) response {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	}
}

// truncated promises a body and closes the connection partway: a network
// error after the status line, so the transport cannot retry it.
func truncated() response {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte(`{"schema_version": 1,`))
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}
}

// held answers with then once release is closed, or never when the client
// gives up first.
func held(release <-chan struct{}, then response) response {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
			then(w, r)
		case <-r.Context().Done():
		}
	}
}

// mockServer serves the ruleset endpoint from a script of responses, the
// last one repeating, and records the request headers.
type mockServer struct {
	*httptest.Server
	mu        sync.Mutex
	responses []response
	requests  []http.Header
}

func newMockServer(t *testing.T, responses ...response) *mockServer {
	t.Helper()
	m := &mockServer{responses: responses}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sdk/v1/ruleset" {
			t.Errorf("request %s %s, want GET /sdk/v1/ruleset", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		m.mu.Lock()
		m.requests = append(m.requests, r.Header.Clone())
		next := m.responses[min(len(m.requests), len(m.responses))-1]
		m.mu.Unlock()
		next(w, r)
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *mockServer) request(t *testing.T, i int) http.Header {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if i >= len(m.requests) {
		t.Fatalf("request %d not received; %d so far", i, len(m.requests))
	}
	return m.requests[i]
}

func (m *mockServer) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// sleeper stands in for the clock of the background loop: every wait is
// handed to the test, which decides when it ends.
type sleeper struct {
	naps chan nap
}

// nap is one wait of the background loop.
type nap struct {
	d    time.Duration
	done chan struct{}
}

// wake ends the wait: the loop fetches again.
func (n nap) wake() { close(n.done) }

func newSleeper() *sleeper {
	return &sleeper{naps: make(chan nap)}
}

func (s *sleeper) option() flagnetic.Option {
	return flagnetic.WithSleep(s.wait)
}

// summaryOption hands the reporter's waits to the test instead of the
// fetch loop's.
func (s *sleeper) summaryOption() flagnetic.Option {
	return flagnetic.WithSummarySleep(s.wait)
}

func (s *sleeper) wait(ctx context.Context, d time.Duration) bool {
	n := nap{d: d, done: make(chan struct{})}
	select {
	case s.naps <- n:
	case <-ctx.Done():
		return false
	}
	select {
	case <-n.done:
		return true
	case <-ctx.Done():
		return false
	}
}

// next waits until the loop has handled a fetch result and gone to sleep,
// and returns that sleep. Everything the fetch result causes has happened
// by then.
func (s *sleeper) next(t *testing.T) nap {
	t.Helper()
	select {
	case n := <-s.naps:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("the background loop did not go to sleep")
		return nap{}
	}
}

// ceiling is the identity jitter: every backoff waits its full ceiling.
func ceiling() flagnetic.Option {
	return flagnetic.WithJitter(func(c time.Duration) time.Duration { return c })
}

// newOnline builds an online client with the change stream and the
// evaluation summary off, so that a test of the fetch loop scripts the
// ruleset endpoint alone. The stream's own tests turn the stream back on
// and the summary's turn reporting back on, by passing the option after.
func newOnline(t *testing.T, baseURL string, opts ...flagnetic.Option) *flagnetic.Client {
	t.Helper()
	opts = append([]flagnetic.Option{flagnetic.WithChangeStream(false), flagnetic.WithSummaries(false)}, opts...)
	c, err := flagnetic.New(serverKey, baseURL, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return c
}

func readyNow(t *testing.T, c *flagnetic.Client) flagnetic.Stage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Ready(ctx)
}

// stageLog is the sequence of stage_changed events as "from>to".
func (l *logs) stageLog() []string {
	var out []string
	for _, e := range l.named(flagnetic.EventStageChanged) {
		out = append(out, fmt.Sprintf("%v>%v", e.Fields["from"], e.Fields["to"]))
	}
	return out
}

// lastCause is the cause of the most recent fetch_failed.
func (l *logs) lastCause(t *testing.T) any {
	t.Helper()
	failed := l.named(flagnetic.EventFetchFailed)
	if len(failed) == 0 {
		t.Fatal("no fetch_failed logged")
	}
	return failed[len(failed)-1].Fields["cause"]
}

func TestConstructionFailsOnlyForAClientKey(t *testing.T) {
	if _, err := flagnetic.New("fn_cli_AbCdEfGh0123456789AbCdEfGh0123456789", "http://127.0.0.1:1"); err == nil {
		t.Error("New with a client key succeeded")
	}
	for name, key := range map[string]string{
		"server key":     serverKey,
		"unknown prefix": "not-a-key",
		"empty key":      "",
	} {
		t.Run(name, func(t *testing.T) {
			var l logs
			clk := newSleeper()
			// Nothing listens there: the fetch fails, construction does not.
			c, err := flagnetic.New(key, "http://127.0.0.1:1", flagnetic.WithChangeStream(false), clk.option(), flagnetic.WithLogger(l.logger()))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer func() { _ = c.Close(context.Background()) }()
			if got := c.Stage(); got != flagnetic.StageBuiltIn {
				t.Errorf("stage = %q, want built_in", got)
			}
			clk.next(t)
			if got := l.lastCause(t); got != "network" {
				t.Errorf("fetch_failed cause = %v, want network", got)
			}
		})
	}
}

func TestFetchSendsTheProtocolHeaders(t *testing.T) {
	srv := newMockServer(t, ruleset(7), status(http.StatusNotModified))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option())
	clk.next(t).wake()
	clk.next(t)

	first := srv.request(t, 0)
	if got := first.Get("Authorization"); got != "Bearer "+serverKey {
		t.Errorf("Authorization = %q", got)
	}
	if got := first.Get("User-Agent"); !regexp.MustCompile(`^flagnetic-go/\d+\.\d+\.\d+ \([^)]+\)$`).MatchString(got) {
		t.Errorf("User-Agent = %q, want flagnetic-go/<version> (<platform>)", got)
	}
	if got := first.Get("Flagnetic-Schema-Version"); got != "1" {
		t.Errorf("Flagnetic-Schema-Version = %q", got)
	}
	if got := first.Get("Accept-Encoding"); !strings.Contains(got, "gzip") {
		t.Errorf("Accept-Encoding = %q", got)
	}
	if got, ok := first["If-None-Match"]; ok {
		t.Errorf("first fetch sent If-None-Match %q with nothing held", got)
	}
	if got := srv.request(t, 1).Get("If-None-Match"); got != `"7"` {
		t.Errorf("If-None-Match = %q, want the held version", got)
	}
	if got := c.Stage(); got != flagnetic.StageFresh {
		t.Errorf("stage after 304 = %q", got)
	}
}

func TestAcceptedRulesetIsSwappedInFresh(t *testing.T) {
	var l logs
	srv := newMockServer(t, gzipped(7), ruleset(8))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), flagnetic.WithLogger(l.logger()))

	if got := readyNow(t, c); got != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh", got)
	}
	d := c.Detail(context.Background(), linux, "new_checkout", false)
	if d.Value != true || d.Reason != flagnetic.ReasonRule || d.RulesetVersion != 7 || d.Stage != flagnetic.StageFresh || d.ReceivedAt.IsZero() {
		t.Errorf("detail = %+v", d)
	}
	var changed [][]string
	var mu sync.Mutex
	c.OnChange(func(keys []string) { mu.Lock(); changed = append(changed, keys); mu.Unlock() })
	clk.next(t).wake()
	clk.next(t)

	if got := c.Detail(context.Background(), linux, "new_checkout", false).RulesetVersion; got != 8 {
		t.Errorf("ruleset version = %d, want 8", got)
	}
	loaded := l.named(flagnetic.EventRulesetLoaded)
	if len(loaded) != 2 || loaded[0].Fields["source"] != "server" || loaded[1].Fields["ruleset_version"] != int64(8) {
		t.Errorf("ruleset_loaded = %+v", loaded)
	}
	if got, want := l.stageLog(), []string{"built_in>fresh"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stage_changed = %v, want %v", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changed) != 0 {
		t.Errorf("OnChange = %v for a new version with the same flags, want nothing", changed)
	}
}

func TestNewRulesetFiresOnChange(t *testing.T) {
	srv := newMockServer(t, ruleset(7), body(strings.Replace(documentAt(8), `"fallthrough": "hi"`, `"fallthrough": "hello"`, 1)))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option())
	readyNow(t, c)
	var changed [][]string
	var mu sync.Mutex
	c.OnChange(func(keys []string) { mu.Lock(); changed = append(changed, keys); mu.Unlock() })
	clk.next(t).wake()
	clk.next(t)

	mu.Lock()
	defer mu.Unlock()
	if want := [][]string{{"greeting"}}; !reflect.DeepEqual(changed, want) {
		t.Errorf("OnChange = %v, want %v", changed, want)
	}
}

func TestNotModifiedIsFresh(t *testing.T) {
	var l logs
	srv := newMockServer(t, ruleset(7), status(http.StatusInternalServerError), status(http.StatusNotModified))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), flagnetic.WithLogger(l.logger()))
	clk.next(t).wake()
	clk.next(t).wake()
	if got := clk.next(t).d; got != 30*time.Second {
		t.Errorf("after a 304 the loop sleeps %v, want the safety poll with the change stream off", got)
	}
	if got := c.Stage(); got != flagnetic.StageFresh {
		t.Errorf("stage = %q, want fresh", got)
	}
	if got := c.Detail(context.Background(), linux, "new_checkout", false); got.Value != true || got.RulesetVersion != 7 {
		t.Errorf("detail = %+v", got)
	}
	if got, want := l.stageLog(), []string{"built_in>fresh", "fresh>last_good", "last_good>fresh"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stage_changed = %v, want %v", got, want)
	}
}

func TestFailuresKeepTheHeldRuleset(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fail  response
		cause string
	}{
		{"500", status(http.StatusInternalServerError), "http_status"},
		{"503", status(http.StatusServiceUnavailable), "http_status"},
		{"429", status(http.StatusTooManyRequests), "http_status"},
		{"404", status(http.StatusNotFound), "http_status"},
		{"network error", truncated(), "network"},
		{"corrupt json", body(`{"schema_version": 1, "version": 8, "scope": "ser`), "parse"},
		{"empty body", body(``), "parse"},
		{"empty object", body(`{}`), "parse"},
		{"missing schema", body(`{"version": 8, "scope": "server", "flags": []}`), "parse"},
		{"wrong shape", body(`{"schema_version": 1, "version": 8, "scope": "server", "flags": {"a": 1}}`), "parse"},
		{"array", body(`[]`), "parse"},
		{"lower version", ruleset(6), "version_downgrade"},
		{"client scope", body(strings.Replace(documentAt(8), `"scope": "server"`, `"scope": "client"`, 1)), "wrong_scope"},
		{"unsupported schema", body(strings.Replace(documentAt(8), `"schema_version": 1`, `"schema_version": 2`, 1)), "schema_unsupported"},
		{"schema below the range", body(strings.Replace(documentAt(8), `"schema_version": 1`, `"schema_version": 0`, 1)), "schema_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var l logs
			srv := newMockServer(t, ruleset(7), tc.fail, tc.fail, ruleset(9))
			clk := newSleeper()
			c := newOnline(t, srv.URL, clk.option(), flagnetic.WithLogger(l.logger()))
			clk.next(t).wake()

			clk.next(t).wake()
			if got := l.lastCause(t); got != tc.cause {
				t.Errorf("fetch_failed cause = %v, want %s", got, tc.cause)
			}
			d := c.Detail(context.Background(), linux, "new_checkout", false)
			if d.Value != true || d.RulesetVersion != 7 || d.Stage != flagnetic.StageLastGood || c.Stage() != flagnetic.StageLastGood {
				t.Errorf("after one failure detail = %+v, stage %q", d, c.Stage())
			}

			clk.next(t).wake()
			if got := c.Detail(context.Background(), linux, "greeting", "x"); got.Value != "hi" || got.Stage != flagnetic.StageLastGood {
				t.Errorf("after two failures detail = %+v", got)
			}
			if got := len(l.named(flagnetic.EventFetchFailed)); got != 2 {
				t.Errorf("fetch_failed logged %d times, want 2", got)
			}

			clk.next(t)
			if got := c.Detail(context.Background(), linux, "new_checkout", false); got.RulesetVersion != 9 || got.Stage != flagnetic.StageFresh {
				t.Errorf("after recovery detail = %+v", got)
			}
			if got, want := l.stageLog(), []string{"built_in>fresh", "fresh>last_good", "last_good>fresh"}; !reflect.DeepEqual(got, want) {
				t.Errorf("stage_changed = %v, want %v", got, want)
			}
		})
	}
}

func TestFailureWithNothingHeldStaysBuiltIn(t *testing.T) {
	var l logs
	srv := newMockServer(t, status(http.StatusInternalServerError), body(`{}`), ruleset(7))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), flagnetic.WithLogger(l.logger()))

	if got := readyNow(t, c); got != flagnetic.StageBuiltIn {
		t.Errorf("Ready = %q, want built_in at the first fetch result", got)
	}
	if d := c.Detail(context.Background(), linux, "new_checkout", false); d.Value != false || d.Reason != flagnetic.ReasonFallback || d.Stage != flagnetic.StageBuiltIn {
		t.Errorf("detail = %+v", d)
	}
	clk.next(t).wake()
	clk.next(t).wake()
	if got := c.Stage(); got != flagnetic.StageBuiltIn {
		t.Errorf("stage = %q after two failures", got)
	}
	clk.next(t)
	if got := c.Stage(); got != flagnetic.StageFresh {
		t.Errorf("stage = %q after a 200", got)
	}
	if got, want := l.stageLog(), []string{"built_in>fresh"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stage_changed = %v, want %v", got, want)
	}
}

func TestUnsupportedSchemaIsLoggedOnce(t *testing.T) {
	var l logs
	unsupported := body(strings.Replace(documentAt(8), `"schema_version": 1`, `"schema_version": 2`, 1))
	srv := newMockServer(t, ruleset(7), unsupported)
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), flagnetic.WithLogger(l.logger()))
	clk.next(t).wake()
	clk.next(t).wake()
	clk.next(t).wake()
	clk.next(t)

	logged := l.named(flagnetic.EventSchemaUnsupported)
	if len(logged) != 1 {
		t.Fatalf("schema_unsupported logged %d times, want 1", len(logged))
	}
	if logged[0].Fields["schema_version"] != 2 {
		t.Errorf("schema_unsupported fields = %v", logged[0].Fields)
	}
	if got := len(l.named(flagnetic.EventFetchFailed)); got != 3 {
		t.Errorf("fetch_failed logged %d times, want 3", got)
	}
	if got := c.Detail(context.Background(), linux, "new_checkout", false); got.RulesetVersion != 7 || got.Stage != flagnetic.StageLastGood {
		t.Errorf("detail = %+v", got)
	}
}

func TestRevokedKeyKeepsServingAndRetriesAtTheUnauthorizedCap(t *testing.T) {
	var l logs
	responses := []response{ruleset(7)}
	for i := range 12 {
		responses = append(responses, status([]int{http.StatusUnauthorized, http.StatusForbidden}[i%2]))
	}
	responses = append(responses, ruleset(8), status(http.StatusUnauthorized))
	srv := newMockServer(t, responses...)
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), ceiling(), flagnetic.WithLogger(l.logger()))
	clk.next(t).wake()

	var waits []time.Duration
	for range 12 {
		n := clk.next(t)
		waits = append(waits, n.d)
		n.wake()
	}
	want := []time.Duration{}
	for i := range 12 {
		want = append(want, min(time.Second<<i, 15*time.Minute))
	}
	if !reflect.DeepEqual(waits, want) {
		t.Errorf("unauthorized backoff ceilings = %v, want %v", waits, want)
	}
	revoked := l.named(flagnetic.EventKeyRevoked)
	if len(revoked) != 1 || revoked[0].Level != slog.LevelError {
		t.Fatalf("key_revoked = %+v, want one at error", revoked)
	}
	if revoked[0].Fields["status"] != http.StatusUnauthorized {
		t.Errorf("key_revoked fields = %v", revoked[0].Fields)
	}
	if got := c.Detail(context.Background(), linux, "new_checkout", false); got.Value != true || got.Stage != flagnetic.StageLastGood {
		t.Errorf("while revoked detail = %+v", got)
	}

	// The key is restored: fresh again, and a later revocation is news.
	if got := clk.next(t); got.d != 30*time.Second {
		t.Errorf("after recovery the loop sleeps %v, want the safety poll", got.d)
	} else {
		got.wake()
	}
	if got := clk.next(t).d; got != time.Second {
		t.Errorf("first backoff after recovery = %v, want 1s", got)
	}
	if got := len(l.named(flagnetic.EventKeyRevoked)); got != 2 {
		t.Errorf("key_revoked logged %d times across two revocations, want 2", got)
	}
	if got, want := l.stageLog(), []string{"built_in>fresh", "fresh>last_good", "last_good>fresh", "fresh>last_good"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stage_changed = %v, want %v", got, want)
	}
}

func TestBackoffIsExponentialWithFullJitter(t *testing.T) {
	t.Run("ceilings", func(t *testing.T) {
		srv := newMockServer(t, status(http.StatusInternalServerError))
		clk := newSleeper()
		newOnline(t, srv.URL, clk.option(), ceiling())
		var waits []time.Duration
		for range 12 {
			n := clk.next(t)
			waits = append(waits, n.d)
			n.wake()
		}
		want := []time.Duration{1, 2, 4, 8, 16, 32, 64, 128, 256, 300, 300, 300}
		for i := range want {
			want[i] *= time.Second
		}
		if !reflect.DeepEqual(waits, want) {
			t.Errorf("backoff ceilings = %v, want %v", waits, want)
		}
	})
	t.Run("jitter within the ceiling", func(t *testing.T) {
		srv := newMockServer(t, status(http.StatusServiceUnavailable))
		clk := newSleeper()
		newOnline(t, srv.URL, clk.option())
		distinct := map[time.Duration]bool{}
		for i := range 40 {
			n := clk.next(t)
			limit := min(time.Second<<min(i, 20), 5*time.Minute)
			if n.d < 0 || n.d > limit {
				t.Errorf("attempt %d waits %v, want within [0, %v]", i+1, n.d, limit)
			}
			distinct[n.d] = true
			n.wake()
		}
		if len(distinct) < 10 {
			t.Errorf("only %d distinct waits in 40: not jittered", len(distinct))
		}
	})
	t.Run("a success resets it", func(t *testing.T) {
		srv := newMockServer(t, status(http.StatusInternalServerError), status(http.StatusInternalServerError),
			status(http.StatusInternalServerError), ruleset(7), status(http.StatusBadGateway))
		clk := newSleeper()
		newOnline(t, srv.URL, clk.option(), ceiling())
		var waits []time.Duration
		for range 5 {
			n := clk.next(t)
			waits = append(waits, n.d)
			n.wake()
		}
		want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 30 * time.Second, time.Second}
		if !reflect.DeepEqual(waits, want) {
			t.Errorf("waits = %v, want %v", waits, want)
		}
	})
}

func TestLateRulesetAfterReadyTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := newMockServer(t, held(release, ruleset(7)))
	c := newOnline(t, srv.URL)
	stages := make(chan flagnetic.Stage, 4)
	c.OnStage(func(s flagnetic.Stage) { stages <- s })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := c.Ready(ctx); got != flagnetic.StageBuiltIn {
		t.Errorf("Ready = %q, want built_in at the deadline", got)
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("Ready returned after %v, want at its deadline", waited)
	}
	if d := c.Detail(context.Background(), linux, "new_checkout", false); d.Reason != flagnetic.ReasonFallback || d.Stage != flagnetic.StageBuiltIn {
		t.Errorf("before the ruleset detail = %+v", d)
	}

	close(release)
	select {
	case s := <-stages:
		if s != flagnetic.StageFresh {
			t.Errorf("OnStage(%q), want fresh", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the late ruleset never arrived")
	}
	if got := readyNow(t, c); got != flagnetic.StageFresh {
		t.Errorf("Ready after the late 200 = %q", got)
	}
	if got := c.Bool(context.Background(), linux, "new_checkout", false); !got {
		t.Error("the late ruleset is not served")
	}
}

func TestRequestTimeoutIsAFailure(t *testing.T) {
	var l logs
	never := make(chan struct{})
	srv := newMockServer(t, held(never, ruleset(7)))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), flagnetic.WithRequestTimeout(50*time.Millisecond), flagnetic.WithLogger(l.logger()))
	if got := readyNow(t, c); got != flagnetic.StageBuiltIn {
		t.Errorf("Ready = %q, want built_in", got)
	}
	clk.next(t)
	if got := l.lastCause(t); got != "timeout" {
		t.Errorf("fetch_failed cause = %v, want timeout", got)
	}
}

func TestStageIsReportedFourWays(t *testing.T) {
	var l logs
	release := make(chan struct{})
	srv := newMockServer(t, held(release, ruleset(7)), status(http.StatusInternalServerError), body(`{"schema_version": 1, "version": 8, "scope": "server"}`))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), flagnetic.WithLogger(l.logger()))
	var mu sync.Mutex
	var seen []string
	c.OnStage(func(s flagnetic.Stage) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, string(s))
	})
	if got := c.Stage(); got != flagnetic.StageBuiltIn {
		t.Fatalf("stage before any fetch = %q", got)
	}
	close(release)

	var getter, detail []string
	for i := range 3 {
		n := clk.next(t)
		getter = append(getter, string(c.Stage()))
		detail = append(detail, string(c.Detail(context.Background(), linux, "new_checkout", false).Stage))
		if i < 2 {
			n.wake()
		}
	}
	var logged []string
	for _, e := range l.named(flagnetic.EventStageChanged) {
		logged = append(logged, fmt.Sprint(e.Fields["to"]))
	}
	want := []string{"fresh", "last_good", "fresh"}
	mu.Lock()
	defer mu.Unlock()
	for name, got := range map[string][]string{"Stage": getter, "Detail.Stage": detail, "OnStage": seen, "stage_changed": logged} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	// The empty ruleset is fresh and serves every built-in default.
	if d := c.Detail(context.Background(), linux, "new_checkout", true); d.Value != true || d.Reason != flagnetic.ReasonFallback || d.RulesetVersion != 8 {
		t.Errorf("empty ruleset detail = %+v", d)
	}
}

func TestStorePersistsAndSeedsLastGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ruleset.json")

	srv := newMockServer(t, ruleset(7), ruleset(8))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), flagnetic.WithStore(path))
	clk.next(t)
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("store not written: %v", err)
	}
	if string(stored) != documentAt(7) {
		t.Errorf("store = %s, want the accepted document", stored)
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("store mode = %v: private segments are readable by others", info.Mode().Perm())
	}
	_ = c.Close(context.Background())

	var l logs
	down := newMockServer(t, status(http.StatusInternalServerError))
	clk2 := newSleeper()
	c2, err := flagnetic.New(serverKey, down.URL, flagnetic.WithChangeStream(false), flagnetic.WithSummaries(false),
		clk2.option(), flagnetic.WithStore(path), flagnetic.WithLogger(l.logger()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close(context.Background()) }()
	// At construction, before the first fetch completes.
	if got := c2.Stage(); got != flagnetic.StageLastGood {
		t.Errorf("stage right after New = %q, want last_good from the store", got)
	}
	if got := c2.Detail(context.Background(), linux, "new_checkout", false); got.Value != true || got.RulesetVersion != 7 {
		t.Errorf("detail from the store = %+v", got)
	}
	if got := readyNow(t, c2); got != flagnetic.StageLastGood {
		t.Errorf("Ready = %q, want last_good", got)
	}
	clk2.next(t)
	if got := down.request(t, 0).Get("If-None-Match"); got != `"7"` {
		t.Errorf("If-None-Match = %q, want the stored version", got)
	}
	loaded := l.named(flagnetic.EventRulesetLoaded)
	if len(loaded) != 1 || loaded[0].Fields["source"] != "store" {
		t.Errorf("ruleset_loaded = %+v, want one from the store", loaded)
	}
	if got, want := l.stageLog(), []string{"built_in>last_good"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stage_changed = %v, want %v", got, want)
	}
}

func TestStorePersistsEveryAcceptedRuleset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ruleset.json")
	srv := newMockServer(t, ruleset(7), status(http.StatusNotModified), body(`{}`), ruleset(8))
	clk := newSleeper()
	newOnline(t, srv.URL, clk.option(), flagnetic.WithStore(path))
	for range 3 {
		clk.next(t).wake()
	}
	clk.next(t)
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != documentAt(8) {
		t.Errorf("store = %s, want version 8", stored)
	}
}

func TestCorruptStoreStartsBuiltIn(t *testing.T) {
	for name, content := range map[string]string{
		"corrupt json":       `{"schema_version": 1,`,
		"unsupported schema": strings.Replace(documentAt(7), `"schema_version": 1`, `"schema_version": 2`, 1),
		"client scope":       strings.Replace(documentAt(7), `"scope": "server"`, `"scope": "client"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ruleset.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			var l logs
			srv := newMockServer(t, status(http.StatusInternalServerError))
			clk := newSleeper()
			c := newOnline(t, srv.URL, clk.option(), flagnetic.WithStore(path), flagnetic.WithLogger(l.logger()))
			if got := c.Stage(); got != flagnetic.StageBuiltIn {
				t.Errorf("stage = %q, want built_in", got)
			}
			failed := l.named(flagnetic.EventStoreFailed)
			if len(failed) != 1 || failed[0].Fields["path"] != path {
				t.Errorf("store_failed = %+v, want one naming the path", failed)
			}
			clk.next(t)
			if _, ok := srv.request(t, 0)["If-None-Match"]; ok {
				t.Error("a corrupt store sent If-None-Match")
			}
		})
	}
	t.Run("absent file", func(t *testing.T) {
		var l logs
		srv := newMockServer(t, status(http.StatusInternalServerError))
		clk := newSleeper()
		c := newOnline(t, srv.URL, clk.option(), flagnetic.WithStore(filepath.Join(t.TempDir(), "none.json")), flagnetic.WithLogger(l.logger()))
		clk.next(t)
		if got := c.Stage(); got != flagnetic.StageBuiltIn {
			t.Errorf("stage = %q", got)
		}
		if got := l.named(flagnetic.EventStoreFailed); len(got) != 0 {
			t.Errorf("store_failed = %+v for a first start", got)
		}
	})
}

func TestSafetyPollWithoutTheChangeStream(t *testing.T) {
	// With the change stream off the safety poll is the only notice a kill
	// switch gets, so it runs every 30 seconds whatever the poll interval
	// says; the interval itself is asserted with the stream connected, in
	// TestPollIntervalFollowsTheChangeStream.
	for name, opts := range map[string][]flagnetic.Option{
		"default":         nil,
		"configured":      {flagnetic.WithPollInterval(time.Minute)},
		"below the floor": {flagnetic.WithPollInterval(time.Second)},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newMockServer(t, ruleset(7), status(http.StatusNotModified))
			clk := newSleeper()
			newOnline(t, srv.URL, append([]flagnetic.Option{clk.option()}, opts...)...)
			for range 3 {
				n := clk.next(t)
				if n.d != 30*time.Second {
					t.Errorf("poll waits %v, want 30s", n.d)
				}
				n.wake()
			}
			clk.next(t)
			if got := srv.request(t, 3).Get("If-None-Match"); got != `"7"` {
				t.Errorf("poll If-None-Match = %q", got)
			}
		})
	}
}

func TestCloseReturnsPromptly(t *testing.T) {
	t.Run("during backoff", func(t *testing.T) {
		srv := newMockServer(t, status(http.StatusInternalServerError))
		c, err := flagnetic.New(serverKey, srv.URL, flagnetic.WithChangeStream(false), flagnetic.WithJitter(func(time.Duration) time.Duration { return time.Hour }))
		if err != nil {
			t.Fatal(err)
		}
		readyNow(t, c)
		closeWithin(t, c, time.Second)
		before := srv.requestCount()
		time.Sleep(20 * time.Millisecond)
		if got := srv.requestCount(); got != before {
			t.Errorf("%d requests after Close", got-before)
		}
		if got := c.Bool(context.Background(), linux, "new_checkout", true); !got {
			t.Error("a closed client stops serving its defaults")
		}
	})
	t.Run("during a request", func(t *testing.T) {
		srv := newMockServer(t, held(make(chan struct{}), ruleset(7)))
		c, err := flagnetic.New(serverKey, srv.URL, flagnetic.WithChangeStream(false))
		if err != nil {
			t.Fatal(err)
		}
		for srv.requestCount() == 0 {
			time.Sleep(time.Millisecond)
		}
		closeWithin(t, c, time.Second)
		if got := readyNow(t, c); got != flagnetic.StageBuiltIn {
			t.Errorf("Ready after Close = %q", got)
		}
		closeWithin(t, c, time.Second)
	})
}

func closeWithin(t *testing.T, c *flagnetic.Client, limit time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := c.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	if took := time.Since(start); took > limit {
		t.Errorf("Close took %v", took)
	}
}

func TestReloadNeedsOfflineMode(t *testing.T) {
	srv := newMockServer(t, ruleset(7))
	c := newOnline(t, srv.URL, newSleeper().option())
	if err := c.Reload(); err == nil {
		t.Error("Reload on an online client succeeded")
	}
}

func TestConcurrentEvaluationWhileFetching(t *testing.T) {
	var responses []response
	for v := range int64(30) {
		responses = append(responses, ruleset(7+v), status(http.StatusInternalServerError))
	}
	srv := newMockServer(t, responses...)
	c := newOnline(t, srv.URL, flagnetic.WithSleep(func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-time.After(time.Millisecond):
			return true
		case <-ctx.Done():
			return false
		}
	}))
	c.OnStage(func(flagnetic.Stage) {})
	c.OnChange(func([]string) {})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 500 {
				c.Bool(context.Background(), linux, "new_checkout", false)
				c.Detail(context.Background(), linux, "pricing", "old")
				c.Stage()
			}
		})
	}
	wg.Wait()
}
