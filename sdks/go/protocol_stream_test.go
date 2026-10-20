package flagnetic_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// streamTimeDivisor is the one factor every duration of a change stream
// scenario is divided by — the SDK's intervals, the scenario's options and
// its windows alike — so that four scenarios covering a minute and a half
// of stream behaviour run in seconds. The fixtures allow it: two loops
// wait at once here, so the fetch loop's own clock cannot drive them.
const streamTimeDivisor = 10

func scaled(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond / streamTimeDivisor
}

// streamScenarios are the change stream scenarios of scenarios.json this
// SDK runs.
var streamScenarios = map[string]bool{
	"server: a stream event above the held version triggers a fetch":                                     true,
	"server: a dropped stream reconnects with Last-Event-ID and fetches":                                 true,
	"server: 45 seconds of silence reconnect the stream":                                                 true,
	"server: a fetch below the event's version is retried after 1, 2 and 4 seconds and never downgrades": true,
}

func TestProtocolStreamScenarios(t *testing.T) {
	file := readScenarios(t)
	ran := 0
	for _, sc := range file.Scenarios {
		if sc.Mode != "server" || !streamScenarios[sc.Name] {
			continue
		}
		ran++
		t.Run(sc.Name, func(t *testing.T) {
			runStreamScenario(t, file, sc.Given.Options.PollIntervalMS, sc.Steps)
		})
	}
	if ran != len(streamScenarios) {
		t.Fatalf("ran %d scenarios, want %d: a listed scenario was renamed or removed", ran, len(streamScenarios))
	}
}

// scenarioServer answers the scenario's steps in order and hands each
// change stream it opens to the scenario.
type scenarioServer struct {
	t     *testing.T
	steps []protocolStep

	// last is the index of the final step with a request; once it has been
	// answered the scenario is over and a retry the SDK makes while the
	// test checks the last expectations is its own business, counted but
	// answered 503. Everything a scenario forbids in the meantime it says
	// with a no_request step.
	last int

	mu       sync.Mutex
	next     int               // the step the next request belongs to
	served   bool              // every request step has been answered
	arrivals map[int]time.Time // when each request step's request arrived
	counts   map[string]int    // requests per path
	conns    chan *streamConn  // the change streams opened so far
}

// lastRequestStep is the index of the final step expecting a request.
func lastRequestStep(steps []protocolStep) int {
	last := -1
	for i, step := range steps {
		if step.Request != nil {
			last = i
		}
	}
	return last
}

func (s *scenarioServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.counts[r.URL.Path]++
	if s.served {
		s.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	for s.next < len(s.steps) && s.steps[s.next].Request == nil {
		s.next++
	}
	if s.next >= len(s.steps) {
		s.mu.Unlock()
		s.t.Errorf("unexpected request %s %s after the last step", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
		return
	}
	i := s.next
	s.next++
	s.served = i >= s.last
	s.arrivals[i] = time.Now()
	s.mu.Unlock()

	step := s.steps[i]
	req := step.Request
	if r.Method != req.Method || r.URL.Path != req.Path {
		s.t.Errorf("step %d %s: request %s %s, want %s %s", i, step.Note, r.Method, r.URL.Path, req.Method, req.Path)
	}
	for k, v := range req.Headers {
		v = strings.ReplaceAll(v, "<sdk key>", serverKey)
		if got := r.Header.Get(k); got != v {
			s.t.Errorf("step %d %s: %s = %q, want %q", i, step.Note, k, got, v)
		}
	}
	for _, k := range req.AbsentHeaders {
		if _, ok := r.Header[http.CanonicalHeaderKey(k)]; ok {
			s.t.Errorf("step %d %s: %s sent, want absent", i, step.Note, k)
		}
	}
	step.Respond.write(s.t, w)
	if step.Respond.Stream {
		holdOpen(w, r, s.conns)
	}
}

// arrival waits until the request of step i has arrived.
func (s *scenarioServer) arrival(t *testing.T, i int, note string) time.Time {
	t.Helper()
	return waitFor(t, func() (time.Time, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		at, ok := s.arrivals[i]
		return at, ok
	}, "the request of step "+note)
}

func (s *scenarioServer) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[path]
}

func runStreamScenario(t *testing.T, file scenarioFile, pollIntervalMS int64, steps []protocolStep) {
	var l logs
	s := &scenarioServer{t: t, steps: steps, last: lastRequestStep(steps),
		arrivals: map[int]time.Time{}, counts: map[string]int{}, conns: make(chan *streamConn, 8)}
	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	defer srv.Close()

	c, err := flagnetic.New(serverKey, srv.URL,
		flagnetic.WithChangeStream(true),
		// The stream's scenarios script the ruleset endpoint alone.
		flagnetic.WithSummaries(false),
		flagnetic.WithPollInterval(scaled(pollIntervalMS)),
		flagnetic.WithTimeDivisor(streamTimeDivisor),
		// Every backoff is full jitter, so the scenario's windows allow
		// any draw; taking the shortest keeps the run deterministic.
		flagnetic.WithJitter(func(time.Duration) time.Duration { return 0 }),
		flagnetic.WithLogger(l.logger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	var conn *streamConn
	// ended is when the previous step's action finished; every window of
	// the next step is measured from it.
	ended := time.Now()
	for i, step := range steps {
		note := strings.TrimSpace(step.Note)
		if note == "" {
			note = "—"
		}
		switch {
		case step.Request != nil:
			at := s.arrival(t, i, note)
			if w := step.Request.DelayMS; w != nil {
				if d := at.Sub(ended); d < scaled(w.Min) || d > scaled(w.Max) {
					t.Errorf("step %d %s: the request came %v after the previous step, want %v–%v",
						i, note, d, scaled(w.Min), scaled(w.Max))
				}
			}
			ended = at
			if step.Respond.Stream {
				select {
				case conn = <-s.conns:
				case <-time.After(5 * time.Second):
					t.Fatalf("step %d %s: the change stream never opened", i, note)
				}
			}
		case step.StreamSend != "":
			conn.send(t, step.StreamSend)
			ended = time.Now()
		case step.StreamClose:
			conn.close()
			ended = time.Now()
		case step.AdvanceMS > 0:
			sleepUntil(ended.Add(scaled(step.AdvanceMS)))
			ended = time.Now()
		case step.NoRequest != nil:
			before := s.count(step.NoRequest.Path)
			sleepUntil(ended.Add(scaled(step.NoRequest.ForMS)))
			if got := s.count(step.NoRequest.Path); got != before {
				t.Errorf("step %d %s: %d requests to %s within %v, want none",
					i, note, got-before, step.NoRequest.Path, scaled(step.NoRequest.ForMS))
			}
			ended = time.Now()
		}
		checkStreamExpect(t, c, file, &l, i, note, step.Expect)
	}
}

// checkStreamExpect waits for the SDK to have applied the step's result —
// the answers it serves are the one observable that says so — and then
// asserts everything the step expects.
func checkStreamExpect(t *testing.T, c *flagnetic.Client, file scenarioFile, l *logs, i int, note string, e *protocolExpect) {
	t.Helper()
	if e == nil {
		return
	}
	// The SDK applies a response on its own goroutine, so wait for what
	// the step expects to hold; the checks below then report whatever
	// never did.
	want := file.Answers[e.AnswersOf]
	holds := func() bool {
		status := c.Status()
		switch {
		case e.AnswersOf != "" && !answersMatch(c, file.Context, want):
		case e.Stage != "" && status.Stage != e.Stage:
		case e.StaleSince == "set" && status.StaleSince.IsZero():
		case e.StaleSince == "unset" && !status.StaleSince.IsZero():
		default:
			return true
		}
		return false
	}
	for deadline := time.Now().Add(5 * time.Second); !holds() && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if e.AnswersOf != "" {
		checkAnswers(t, c, file.Context, want)
	}
	status := c.Status()
	if e.Stage != "" && status.Stage != e.Stage {
		t.Errorf("step %d %s: stage = %q, want %q", i, note, status.Stage, e.Stage)
	}
	switch e.StaleSince {
	case "set":
		if status.StaleSince.IsZero() {
			t.Errorf("step %d %s: stale_since unset, want set", i, note)
		}
	case "unset":
		if !status.StaleSince.IsZero() {
			t.Errorf("step %d %s: stale_since = %v, want unset", i, note, status.StaleSince)
		}
	}
	for name, n := range e.Logs {
		if got := len(l.named(flagnetic.Event(name))); got != n {
			t.Errorf("step %d %s: %s logged %d times, want %d", i, note, name, got, n)
		}
	}
}

func sleepUntil(deadline time.Time) {
	if d := time.Until(deadline); d > 0 {
		time.Sleep(d)
	}
}
