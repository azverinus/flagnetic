package flagnetic_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// event is one ruleset event of the change stream, as the endpoint writes
// it: the version and nothing else.
func event(version int64) string {
	return fmt.Sprintf("event: ruleset\nid: %d\ndata: {\"version\":%d}\n\n", version, version)
}

// preamble is what the endpoint writes first: the reconnection time it
// asks for and the version it holds.
func preamble(version int64) string {
	return "retry: 3000\n\n" + event(version)
}

// killSwitch answers 200 with document at version v, greeting switched
// off: what an operator's kill switch makes of it.
func killSwitch(v int64) response {
	doc := strings.Replace(documentAt(v),
		`"key": "greeting", "type": "string", "automatic_exposure": false, "on": true,`,
		`"key": "greeting", "type": "string", "automatic_exposure": false, "on": false,`, 1)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, v))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	}
}

// streamConn is one open change stream: the test writes frames to it and
// ends it from the server's side.
type streamConn struct {
	frames chan string
	done   chan struct{}
	once   sync.Once
}

// send writes frames to the open stream and returns once they are on the
// wire.
func (s *streamConn) send(t *testing.T, frames string) {
	t.Helper()
	select {
	case s.frames <- frames:
	case <-s.done:
		t.Error("the stream ended before the frames were written")
	case <-time.After(10 * time.Second):
		t.Fatal("the SDK never read the change stream")
	}
}

// close ends the stream from the server's side, as the platform does about
// once an hour.
func (s *streamConn) close() { s.once.Do(func() { close(s.done) }) }

// streamServer serves both endpoints an online client uses: the ruleset
// from a script of responses, the last one repeating, and the change
// stream from a script of statuses, the last one repeating too, so a test
// decides which connection comes up and which is refused. A 200 hands the
// open stream to the test.
type streamServer struct {
	*httptest.Server
	mu sync.Mutex
	// streamStatuses answers the change stream requests in order, the last
	// one repeating; empty means every connection comes up.
	streamStatuses []int
	responses      []response
	rulesets       []http.Header
	streams        []http.Header
	conns          chan *streamConn
}

func newStreamServer(t *testing.T, responses ...response) *streamServer {
	t.Helper()
	m := &streamServer{responses: responses, conns: make(chan *streamConn, 8)}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sdk/v1/ruleset":
			m.mu.Lock()
			m.rulesets = append(m.rulesets, r.Header.Clone())
			next := m.responses[min(len(m.rulesets), len(m.responses))-1]
			m.mu.Unlock()
			next(w, r)
		case "/sdk/v1/stream":
			m.mu.Lock()
			m.streams = append(m.streams, r.Header.Clone())
			status := http.StatusOK
			if n := len(m.streamStatuses); n > 0 {
				status = m.streamStatuses[min(len(m.streams), n)-1]
			}
			m.mu.Unlock()
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			m.serveStream(w, r)
		default:
			t.Errorf("request %s %s, want an SDK endpoint", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *streamServer) serveStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	holdOpen(w, r, m.conns)
}

// holdOpen keeps a response open as the change stream, hands the
// connection to the test through conns, and writes whatever the test sends
// until it closes the stream or the SDK hangs up. The status line and the
// headers are already written.
func holdOpen(w http.ResponseWriter, r *http.Request, conns chan *streamConn) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
	conn := &streamConn{frames: make(chan string), done: make(chan struct{})}
	select {
	case conns <- conn:
	default:
	}
	for {
		select {
		case frames := <-conn.frames:
			_, _ = w.Write([]byte(frames))
			flusher.Flush()
		case <-conn.done:
			return
		case <-r.Context().Done():
			conn.close()
			return
		}
	}
}

// connect waits for the next change stream the SDK opens.
func (m *streamServer) connect(t *testing.T) *streamConn {
	t.Helper()
	select {
	case c := <-m.conns:
		return c
	case <-time.After(10 * time.Second):
		t.Fatal("the SDK never opened the change stream")
		return nil
	}
}

// streamRequest waits for the ith change stream request and returns its
// headers.
func (m *streamServer) streamRequest(t *testing.T, i int) http.Header {
	t.Helper()
	return waitFor(t, func() (http.Header, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if i < len(m.streams) {
			return m.streams[i], true
		}
		return nil, false
	}, fmt.Sprintf("stream request %d", i))
}

// rulesetRequests waits until n ruleset requests have arrived and returns
// their headers.
func (m *streamServer) rulesetRequests(t *testing.T, n int) []http.Header {
	t.Helper()
	return waitFor(t, func() ([]http.Header, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(m.rulesets) >= n {
			return append([]http.Header(nil), m.rulesets...), true
		}
		return nil, false
	}, fmt.Sprintf("%d ruleset requests", n))
}

func (m *streamServer) rulesetCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rulesets)
}

// waitFor polls until ready reports what it waited for, or fails after ten
// seconds naming it.
func waitFor[T any](t *testing.T, ready func() (T, bool), what string) T {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if v, ok := ready(); ok {
			return v
		}
		if time.Now().After(deadline) {
			var zero T
			t.Fatalf("%s never arrived", what)
			return zero
		}
		time.Sleep(time.Millisecond)
	}
}

// newStreaming builds an online client with the change stream on and a
// safety poll far enough away that only the stream causes a fetch.
func newStreaming(t *testing.T, baseURL string, opts ...flagnetic.Option) *flagnetic.Client {
	t.Helper()
	opts = append([]flagnetic.Option{flagnetic.WithChangeStream(true), flagnetic.WithSummaries(false),
		flagnetic.WithPollInterval(time.Hour)}, opts...)
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

func TestChangeStreamOpensBeforeTheFirstFetch(t *testing.T) {
	srv := newStreamServer(t, ruleset(7))
	c := newStreaming(t, srv.URL)
	if got := readyNow(t, c); got != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh", got)
	}
	// The stream is open before the first fetch, so a change between the
	// two is announced rather than missed.
	first := srv.streamRequest(t, 0)
	if got, ok := first["Last-Event-Id"]; ok {
		t.Errorf("the first stream request sent Last-Event-ID %q, with nothing held", got)
	}
	if got := first.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream", got)
	}
	if got := first.Get("Authorization"); got != "Bearer "+serverKey {
		t.Errorf("Authorization = %q", got)
	}
	if got := first.Get("User-Agent"); !strings.HasPrefix(got, "flagnetic-go/") {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestAStreamThatNeverAnswersDoesNotHoldTheFirstFetch(t *testing.T) {
	// A black hole between the SDK and the instance: the stream request is
	// neither answered nor refused. The first fetch waits for the stream,
	// but never for longer than one request would take.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sdk/v1/stream" {
			<-r.Context().Done()
			return
		}
		ruleset(7)(w, r)
	}))
	// The stream handler only returns once the SDK hangs up, so the client
	// must be closed before the server: cleanups run in reverse.
	t.Cleanup(srv.Close)
	c := newStreaming(t, srv.URL, flagnetic.WithRequestTimeout(200*time.Millisecond))

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if stage := c.Ready(ctx); stage != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh: the ruleset was never fetched", stage)
	}
	if waited := time.Since(started); waited > 2*time.Second {
		t.Errorf("the first fetch waited %v for the stream, want about the request timeout", waited)
	}
}

func TestEventAboveTheHeldVersionReachesTheChangeCallbackWithinTwoSeconds(t *testing.T) {
	srv := newStreamServer(t, ruleset(7), killSwitch(8))
	c := newStreaming(t, srv.URL)
	readyNow(t, c)
	changed := make(chan []string, 4)
	c.OnChange(func(keys []string) { changed <- keys })
	conn := srv.connect(t)
	conn.send(t, preamble(7))

	started := time.Now()
	conn.send(t, event(8))
	var keys []string
	select {
	case keys = <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("the change callback did not fire within 2 s of the event")
	}
	// Printed unconditionally (not t.Logf, which -v alone would hide) so CI
	// can put the kill-switch propagation number in the job summary, next
	// to the two-second target the SDK promises.
	fmt.Printf("kill_switch_propagation_ms=%d\n", time.Since(started).Milliseconds())
	if want := []string{"greeting"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("the change callback fired with %v, want exactly %v", keys, want)
	}
	if got := c.Detail(context.Background(), linux, "greeting", "none"); got.Value != "hello" || got.Reason != flagnetic.ReasonOff {
		t.Errorf("after the kill switch detail = %+v, want the off variation", got)
	}
	if got := srv.rulesetRequests(t, 2)[1].Get("If-None-Match"); got != `"7"` {
		t.Errorf("the event's fetch revalidated with %q, want the held version", got)
	}
}

func TestEventAtOrBelowTheHeldVersionIsIgnored(t *testing.T) {
	srv := newStreamServer(t, ruleset(7), killSwitch(8))
	c := newStreaming(t, srv.URL)
	readyNow(t, c)
	conn := srv.connect(t)
	// The version held, then one below it, then one above: only the last
	// may cause a fetch, and it proves the first two were read.
	conn.send(t, preamble(7))
	conn.send(t, event(6))
	conn.send(t, event(8))

	srv.rulesetRequests(t, 2)
	waitFor(t, func() (bool, bool) {
		return true, c.Detail(context.Background(), linux, "greeting", "none").RulesetVersion == 8
	}, "the fetch the event above the held version caused")
	if got := srv.rulesetCount(); got != 2 {
		t.Errorf("%d ruleset requests, want the first fetch and the one event above the held version", got)
	}
}

func TestDroppedStreamReconnectsWithTheHeldVersionAndFetches(t *testing.T) {
	srv := newStreamServer(t, ruleset(7), killSwitch(8))
	var l logs
	c := newStreaming(t, srv.URL, flagnetic.WithLogger(l.logger()))
	readyNow(t, c)
	conn := srv.connect(t)
	conn.send(t, preamble(7))
	// The version is bumped while the stream is down, so only the fetch
	// that follows the reconnect finds it.
	conn.close()

	if got := srv.streamRequest(t, 1).Get("Last-Event-ID"); got != "7" {
		t.Errorf("the reconnect sent Last-Event-ID %q, want the held version", got)
	}
	waitFor(t, func() (bool, bool) {
		return true, c.Detail(context.Background(), linux, "greeting", "none").RulesetVersion == 8
	}, "the fetch that follows the reconnect")
	if got := c.Detail(context.Background(), linux, "greeting", "none"); got.Reason != flagnetic.ReasonOff {
		t.Errorf("after the reconnect's fetch detail = %+v, want the off variation", got)
	}
	dropped := l.named(flagnetic.EventSSEDropped)
	if len(dropped) == 0 || dropped[0].Fields["cause"] != "server_closed" {
		t.Errorf("sse_dropped = %+v, want one for the server-side close", dropped)
	}
	if got := len(l.named(flagnetic.EventSSEConnected)); got < 2 {
		t.Errorf("sse_connected logged %d times, want the connect and the reconnect", got)
	}
}

func TestRefusedKeyClosesTheChangeStreamAndKeepsServing(t *testing.T) {
	var l logs
	// The key was revoked while the service was down: the store holds the
	// last good ruleset and every request, the stream included, is refused.
	path := filepath.Join(t.TempDir(), "ruleset.json")
	if err := os.WriteFile(path, []byte(documentAt(7)), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := newStreaming(t, srv.URL, flagnetic.WithStore(path), flagnetic.WithLogger(l.logger()), flagnetic.WithTimeDivisor(100))
	if got := readyNow(t, c); got != flagnetic.StageLastGood {
		t.Fatalf("Ready = %q, want last_good", got)
	}
	waitFor(t, func() (bool, bool) {
		return true, len(l.named(flagnetic.EventFetchFailed)) >= 3
	}, "the fetch loop retrying at the unauthorized cap")

	if got := c.Detail(context.Background(), linux, "greeting", "none"); got.Value != "hi" || got.Stage != flagnetic.StageLastGood {
		t.Errorf("while the key is refused detail = %+v, want the last good served", got)
	}
	revoked := l.named(flagnetic.EventKeyRevoked)
	if len(revoked) != 1 {
		t.Fatalf("key_revoked logged %d times by the stream and the fetch loop together, want 1", len(revoked))
	}
	if revoked[0].Level != slog.LevelError {
		t.Errorf("key_revoked at %v, want error", revoked[0].Level)
	}
	dropped := l.named(flagnetic.EventSSEDropped)
	if len(dropped) != 1 || dropped[0].Fields["reconnect"] != false {
		t.Errorf("sse_dropped = %+v, want exactly one that does not reconnect", dropped)
	}
	// The stream stays closed: nothing reopens it while the fetch loop
	// goes on retrying.
	if got := len(l.named(flagnetic.EventSSEConnected)); got != 0 {
		t.Errorf("sse_connected logged %d times after a refusal, want 0", got)
	}
}

func TestPollIntervalFollowsTheChangeStream(t *testing.T) {
	srv := newStreamServer(t, ruleset(7), status(http.StatusNotModified))
	srv.streamStatuses = []int{http.StatusOK, http.StatusServiceUnavailable} // once dropped, the stream stays down
	clk := newSleeper()
	newStreaming(t, srv.URL, clk.option(), flagnetic.WithPollInterval(2*time.Minute))
	conn := srv.connect(t)
	conn.send(t, preamble(7))

	connected := clk.next(t)
	if connected.d != 2*time.Minute {
		t.Errorf("with the stream connected the loop sleeps %v, want the poll interval", connected.d)
	}
	conn.close()
	connected.wake()

	// Both loops wait on the same clock now: the fetch loop's safety poll
	// is the one asking for 30 s while the stream is down.
	for range 20 {
		n := clk.next(t)
		n.wake()
		if n.d == 30*time.Second {
			return
		}
	}
	t.Error("with the stream down the loop never asked for the 30 s safety poll")
}

func TestReconnectBackoffGrowsAndResetsAfterAStableConnection(t *testing.T) {
	// Every duration is divided by a hundred, so a connection outlasts the
	// minute of stability in well under a second; every backoff waits its
	// full ceiling, so the loop's waits are the ceilings themselves. The
	// stream's ceilings here are 80 ms at most, while the fetch loop's
	// safety poll is 300 ms and its poll interval an hour, so the two
	// loops' waits are told apart by their length.
	const divisor = 100
	srv := newStreamServer(t, ruleset(7), status(http.StatusNotModified))
	srv.streamStatuses = []int{
		http.StatusServiceUnavailable, http.StatusServiceUnavailable,
		http.StatusOK,
		http.StatusServiceUnavailable,
	}
	clk := newSleeper()
	newStreaming(t, srv.URL, clk.option(), ceiling(), flagnetic.WithTimeDivisor(divisor))

	// nextReconnect returns the stream loop's next wait, letting the fetch
	// loop's own waits pass.
	nextReconnect := func() nap {
		t.Helper()
		for {
			n := clk.next(t)
			if n.d < 100*time.Millisecond {
				return n
			}
			n.wake()
		}
	}
	var backoffs []time.Duration
	take := func() {
		t.Helper()
		n := nextReconnect()
		backoffs = append(backoffs, n.d)
		n.wake()
	}

	take() // after the first refusal
	take() // after the second, doubled
	// The third connection comes up. Hold it open past the minute of
	// stability, heartbeat by heartbeat as the platform does, then close
	// it from the server's side.
	conn := srv.connect(t)
	for range 6 {
		time.Sleep(15 * time.Second / divisor)
		conn.send(t, ": hb\n\n")
	}
	conn.close()
	take() // the wait after a server-side close, its own short window
	take() // after the refusal that follows: stable, so back to the base

	want := []time.Duration{
		time.Second / divisor,
		2 * time.Second / divisor,
		2 * time.Second / divisor,
		time.Second / divisor,
	}
	if !reflect.DeepEqual(backoffs, want) {
		t.Errorf("reconnect waits = %v, want %v", backoffs, want)
	}
}

func TestCloseDuringTheReconnectBackoffReturnsPromptly(t *testing.T) {
	// Nothing listens there: every connect fails at once, so both loops
	// spend their time in a backoff an hour long.
	c, err := flagnetic.New(serverKey, "http://127.0.0.1:1",
		flagnetic.WithChangeStream(true), flagnetic.WithSummaries(false),
		flagnetic.WithJitter(func(time.Duration) time.Duration { return time.Hour }))
	if err != nil {
		t.Fatal(err)
	}
	readyNow(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if waited := time.Since(started); waited > time.Second {
		t.Errorf("Close returned after %v, want promptly", waited)
	}
}
