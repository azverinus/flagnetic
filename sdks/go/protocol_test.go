package flagnetic_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// protocolDir holds the protocol fixtures: the documents of /sdk/v1 as a
// real server serves them, the degradation cases and the scenario list.
const protocolDir = "../../conformance/protocol"

// decompress reads a request body, gunzipping it when the request says
// it is gzipped, as every events batch is.
func decompress(r *http.Request) ([]byte, error) {
	if !strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		return io.ReadAll(r.Body)
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

// protocolScenarios are the server-mode scenarios of readScenarios this
// SDK runs on the fetch loop's own clock. The change stream's four run in
// TestProtocolStreamScenarios, where two loops wait at once.
var protocolScenarios = map[string]bool{
	"server: the fallback chain keeps the held document through every failure":                true,
	"server: a stored last good document serves before the first request":                     true,
	"server: a revoked key keeps the last good and retries at a capped backoff":               true,
	"server: If-None-Match revalidates and 304 keeps the document fresh":                      true,
	"server: local overrides are refused in a protected environment":                          true,
	"server: local overrides are refused without the option":                                  true,
	"server: a local override serves local_override, emits no exposure and is counted":        true,
	"server: offline mode makes no request":                                                   true,
	"server: events are retried on 5xx and 429 with the same IDs and never after a rejection": true,
	"server: the evaluation summary counts every evaluation, fallback included":               true,
	"server: a schema_version outside the supported range is a failure logged once":           true,
	"server: an answer carries the experiment block":                                          true,
	"server: an explicit exposure is sent with its envelope":                                  true,
	"server: an evaluation alone records no exposure":                                         true,
	"server: automatic exposure follows the flag's setting":                                   true,
	"server: repeated exposures of one assignment are sent once":                              true,
}

// quietFor is how long a no_request step waits. The scenarios ask for up
// to a minute of silence, which no test may spend: the SDK's own timers
// are set so far out by the scenario's options that a request in this
// window would be one the SDK was never meant to send at all.
const quietFor = 200 * time.Millisecond

// answer is what a getter of Type returns for Flag with Default.
type answer struct {
	Flag      string          `json:"flag"`
	Type      string          `json:"type"`
	Default   json.RawMessage `json:"default"`
	Value     json.RawMessage `json:"value"`
	Variation string          `json:"variation"`
	Reason    string          `json:"reason"`
	// Experiment is the experiment block the answer carries; an answer
	// without one carries none.
	Experiment json.RawMessage `json:"experiment"`
}

// protocolResponse is a scripted answer of the mock server.
type protocolResponse struct {
	Status       int               `json:"status"`
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body"`
	NetworkError bool              `json:"network_error"`
	// Stream keeps the response open as the change stream, which later
	// steps write to and close.
	Stream bool `json:"stream"`
}

type window struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// protocolRequest is the request a step expects.
type protocolRequest struct {
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Headers       map[string]string `json:"headers"`
	AbsentHeaders []string          `json:"absent_headers"`
	DelayMS       *window           `json:"delay_ms"`
	Events        *protocolEvents   `json:"events"`
}

// protocolEvents is what an ingest batch must carry: its event types in
// order, how its event IDs compare with another step's batch, the rows of
// its evaluation summary and the strings that may appear nowhere in it.
type protocolEvents struct {
	Types                 []string        `json:"types"`
	SameIDsAsStep         *int            `json:"same_ids_as_step"`
	ExcludesIDsOfStep     *int            `json:"excludes_ids_of_step"`
	SummaryCounts         []protocolCount `json:"summary_counts"`
	SummaryRulesetVersion *int64          `json:"summary_ruleset_version"`
	AbsentStrings         []string        `json:"absent_strings"`
	// Exposures are the fields each exposure event of the batch must carry,
	// in order.
	Exposures []map[string]any `json:"exposures"`
}

// protocolCount is one row of an evaluation summary a batch must carry.
type protocolCount struct {
	Flag      string  `json:"flag"`
	Variation *string `json:"variation"`
	Reason    string  `json:"reason"`
	Count     int64   `json:"count"`
}

// protocolCall is one call of the SDK's API a step makes.
type protocolCall struct {
	Op      string          `json:"op"`
	Flag    string          `json:"flag"`
	Type    string          `json:"type"`
	Default json.RawMessage `json:"default"`
	Value   json.RawMessage `json:"value"`
	Times   int             `json:"times"`
}

// aliasCall is the alias every scenario makes: the anonymous unit of its
// context linked to the user unit it signs in as (scenarios.json).
var aliasCall = [4]string{"anonymous", "d-8f1c", "user", "u-1001"}

// protocolExpect is what must hold once a step's action is done.
type protocolExpect struct {
	Stage      flagnetic.Stage `json:"stage"`
	StaleSince string          `json:"stale_since"`
	AnswersOf  string          `json:"answers_of"`
	Logs       map[string]int  `json:"logs"`
	Result     *answer         `json:"result"`
	Refused    *bool           `json:"refused"`
	// Recorded is the offline recorder's events by type.
	Recorded map[string]int `json:"recorded"`
}

type protocolStep struct {
	Note    string           `json:"note"`
	Request *protocolRequest `json:"request"`
	Respond protocolResponse `json:"respond"`
	// StreamSend writes bytes to the open change stream; StreamClose ends
	// it from the server's side.
	StreamSend  string `json:"stream_send"`
	StreamClose bool   `json:"stream_close"`
	// AdvanceMS lets time pass; NoRequest lets it pass while the SDK sends
	// nothing to a path.
	AdvanceMS int64 `json:"advance_ms"`
	NoRequest *struct {
		Path  string `json:"path"`
		ForMS int64  `json:"for_ms"`
	} `json:"no_request"`
	// Call calls the SDK's API.
	Call   *protocolCall   `json:"call"`
	Expect *protocolExpect `json:"expect"`
}

type scenarioFile struct {
	Context   flagnetic.Context   `json:"context"`
	Answers   map[string][]answer `json:"answers"`
	Scenarios []struct {
		Name  string `json:"name"`
		Mode  string `json:"mode"`
		Given struct {
			Stored  string `json:"stored"`
			Offline string `json:"offline"`
			Options struct {
				PollIntervalMS    int64 `json:"poll_interval_ms"`
				SummaryIntervalMS int64 `json:"summary_interval_ms"`
				FlushIntervalMS   int64 `json:"flush_interval_ms"`
				SendSummaries     bool  `json:"send_summaries"`
				SendExposures     bool  `json:"send_exposures"`
				AllowOverrides    bool  `json:"allow_overrides"`
			} `json:"options"`
		} `json:"given"`
		Steps []protocolStep `json:"steps"`
	} `json:"scenarios"`
}

// readScenarios is scenarios.json, which the server produces, with the
// scenarios of versioning-scenarios.json appended: the same step language
// over the same documents, for what only the SDKs decide (versioning and
// compatibility); and those of experiment-scenarios.json, with the answers
// of the experiment documents they name.
func readScenarios(t *testing.T) scenarioFile {
	t.Helper()
	var file, versioning, experiments scenarioFile
	if err := json.Unmarshal(readProtocol(t, "scenarios.json"), &file); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readProtocol(t, "versioning-scenarios.json"), &versioning); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readProtocol(t, "experiment-scenarios.json"), &experiments); err != nil {
		t.Fatal(err)
	}
	file.Scenarios = append(file.Scenarios, versioning.Scenarios...)
	file.Scenarios = append(file.Scenarios, experiments.Scenarios...)
	for doc, answers := range experiments.Answers {
		if _, ok := file.Answers[doc]; ok {
			t.Fatalf("experiment-scenarios.json redefines the answers of %s", doc)
		}
		file.Answers[doc] = answers
	}
	return file
}

func readProtocol(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(protocolDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("protocol fixtures: %v", err)
	}
	return b
}

// write answers as the fixture says, on a connection that is never reused,
// so a dropped one cannot be retried by the transport into the next step.
func (r protocolResponse) write(t *testing.T, w http.ResponseWriter) {
	if r.NetworkError {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	for k, v := range r.Headers {
		w.Header().Set(k, v)
	}
	if !r.Stream {
		w.Header().Set("Connection", "close")
	}
	w.WriteHeader(r.Status)
	if r.Body != "" {
		_, _ = w.Write(readProtocol(t, r.Body))
	}
}

// defaultOf is the built-in default an answer's getter is called with, in
// the Go type its flag type maps to.
func defaultOf(a answer) any {
	switch a.Type {
	case "boolean":
		var v bool
		_ = json.Unmarshal(a.Default, &v)
		return v
	case "string":
		var v string
		_ = json.Unmarshal(a.Default, &v)
		return v
	case "number":
		var v float64
		_ = json.Unmarshal(a.Default, &v)
		return v
	default:
		return a.Default
	}
}

// answerMismatch calls the getter of a through Detail and says how what it
// served differs from what a expects, or "" when it does not.
func answerMismatch(c *flagnetic.Client, ctx flagnetic.Context, a answer) string {
	d := c.Detail(context.Background(), ctx, a.Flag, defaultOf(a))
	if d.Variation != a.Variation || string(d.Reason) != a.Reason {
		return fmt.Sprintf("served %q/%s, want %q/%s", d.Variation, d.Reason, a.Variation, a.Reason)
	}
	if diff := jsonDiff("value", d.Value, a.Value); diff != "" {
		return diff
	}
	block := a.Experiment
	if len(block) == 0 {
		block = json.RawMessage("null")
	}
	return jsonDiff("experiment", d.Experiment, block)
}

// jsonDiff says how got, encoded, differs from the JSON want as values, or
// "" when it does not.
func jsonDiff(field string, got any, want json.RawMessage) string {
	served, err := json.Marshal(got)
	if err != nil {
		return fmt.Sprintf("%s %v does not encode: %v", field, got, err)
	}
	var g, w any
	if err = json.Unmarshal(served, &g); err != nil {
		return fmt.Sprintf("%s %s does not decode: %v", field, served, err)
	}
	if err = json.Unmarshal(want, &w); err != nil {
		return fmt.Sprintf("the fixture's %s %s does not decode: %v", field, want, err)
	}
	if !reflect.DeepEqual(g, w) {
		return fmt.Sprintf("%s = %s, want %s", field, served, want)
	}
	return ""
}

// checkAnswers calls every getter of want through Detail and reports every
// answer that differs.
func checkAnswers(t *testing.T, c *flagnetic.Client, ctx flagnetic.Context, want []answer) {
	t.Helper()
	for _, a := range want {
		if diff := answerMismatch(c, ctx, a); diff != "" {
			t.Errorf("%s: %s", a.Flag, diff)
		}
	}
}

// answersMatch reports whether every getter already serves what want says,
// without failing the test: the poll that waits for a fetch to be applied.
func answersMatch(c *flagnetic.Client, ctx flagnetic.Context, want []answer) bool {
	for _, a := range want {
		if answerMismatch(c, ctx, a) != "" {
			return false
		}
	}
	return true
}

// makeCall performs one step's call on the SDK's API and returns whether a
// set_override was refused.
func makeCall(t *testing.T, c *flagnetic.Client, ctx flagnetic.Context, i int, step protocolStep) (refused bool) {
	t.Helper()
	call := step.Call
	for range max(call.Times, 1) {
		switch call.Op {
		case "evaluate":
			a := answer{Flag: call.Flag, Type: call.Type, Default: call.Default}
			c.Detail(context.Background(), ctx, call.Flag, defaultOf(a))
		case "set_override":
			var value any
			if err := json.Unmarshal(call.Value, &value); err != nil {
				t.Fatalf("step %d %s: decode the override %s: %v", i, step.Note, call.Value, err)
			}
			refused = c.LocalOverrides().Set(call.Flag, value) != nil
		case "exposure":
			c.Exposure(context.Background(), ctx, call.Flag)
		case "alias":
			c.Alias(context.Background(), aliasCall[0], aliasCall[1], aliasCall[2], aliasCall[3])
		case "flush":
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := c.Flush(fctx); err != nil {
				t.Errorf("step %d %s: Flush: %v", i, step.Note, err)
			}
			cancel()
		default:
			t.Fatalf("step %d %s: this SDK does not run the call %q", i, step.Note, call.Op)
		}
	}
	return refused
}

// waitHandled waits until the mock server has answered the request of step
// i, so that its assertions have run before the next step's.
func waitHandled(t *testing.T, mu *sync.Mutex, next *int, i int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		mu.Lock()
		handled := *next > i
		mu.Unlock()
		if handled {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("step %d: the request never arrived", i)
}

// quiet lets quietFor pass and fails the test when a request to path
// arrived meanwhile; an empty path means any request at all.
func quiet(t *testing.T, mu *sync.Mutex, paths *[]string, path string, i int, note string) {
	t.Helper()
	mu.Lock()
	before := len(*paths)
	mu.Unlock()
	time.Sleep(quietFor)
	mu.Lock()
	defer mu.Unlock()
	for _, got := range (*paths)[before:] {
		if path == "" || got == path {
			t.Errorf("step %d %s: %s was requested, want no request for %v", i, note, got, quietFor)
		}
	}
}

// checkBatch asserts what an ingest batch carries: its event types in
// order, the rows and ruleset version of its evaluation summary, and the
// strings that may appear nowhere in the body.
func checkBatch(t *testing.T, note string, r *http.Request, want protocolEvents) []string {
	t.Helper()
	raw, err := decompress(r)
	if err != nil {
		t.Errorf("%s: read the batch: %v", note, err)
		return nil
	}
	var wire wireBatch
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Errorf("%s: decode the batch %s: %v", note, raw, err)
		return nil
	}
	types := make([]string, 0, len(wire.Events))
	ids := make([]string, 0, len(wire.Events))
	for _, e := range wire.Events {
		types = append(types, e.Type)
		ids = append(ids, e.EventID)
	}
	if !slices.Equal(types, want.Types) {
		t.Errorf("%s: the batch carries %v, want %v", note, types, want.Types)
	}
	for _, absent := range want.AbsentStrings {
		if strings.Contains(string(raw), absent) {
			t.Errorf("%s: the batch carries %q:\n%s", note, absent, raw)
		}
	}
	if want.Exposures != nil {
		checkExposures(t, note, raw, want.Exposures)
	}
	if want.SummaryCounts == nil {
		return ids
	}
	var summary *wireEvent
	for i := range wire.Events {
		if wire.Events[i].Type == "evaluation_summary" {
			summary = &wire.Events[i]
			break
		}
	}
	if summary == nil {
		t.Errorf("%s: the batch carries no evaluation summary", note)
		return ids
	}
	wantRows := make([]row, 0, len(want.SummaryCounts))
	for _, c := range want.SummaryCounts {
		variation := "«none»"
		if c.Variation != nil {
			variation = *c.Variation
		}
		wantRows = append(wantRows, row{c.Flag, variation, c.Reason, c.Count})
	}
	assertRows(t, rowsOf(*summary), wantRows)
	if v := want.SummaryRulesetVersion; v != nil && summary.RulesetVersion != *v {
		t.Errorf("%s: ruleset_version = %d, want %d", note, summary.RulesetVersion, *v)
	}
	return ids
}

// checkExposures asserts that the exposure events of a batch, in order,
// carry every field the fixture names with the value it gives.
func checkExposures(t *testing.T, note string, raw []byte, want []map[string]any) {
	t.Helper()
	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Errorf("%s: decode the batch %s: %v", note, raw, err)
		return
	}
	var exposures []map[string]any
	for _, e := range body.Events {
		if e["type"] == "exposure" {
			exposures = append(exposures, e)
		}
	}
	if len(exposures) != len(want) {
		t.Errorf("%s: the batch carries %d exposures, want %d:\n%s", note, len(exposures), len(want), raw)
		return
	}
	for i, fields := range want {
		for k, v := range fields {
			if got := exposures[i][k]; !reflect.DeepEqual(got, v) {
				t.Errorf("%s: exposure %d: %s = %v, want %v", note, i, k, got, v)
			}
		}
	}
}

func TestProtocolDegradationCases(t *testing.T) {
	var file struct {
		Context flagnetic.Context `json:"context"`
		Cases   []struct {
			Name     string           `json:"name"`
			Endpoint string           `json:"endpoint"`
			Held     string           `json:"held"`
			Response protocolResponse `json:"response"`
			Outcome  string           `json:"outcome"`
			Answers  []answer         `json:"answers"`
			// Logs are the log events the case must cause, by name.
			Logs map[string]int `json:"logs"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(readProtocol(t, "degradation/cases.json"), &file); err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, tc := range file.Cases {
		if tc.Endpoint != "GET /sdk/v1/ruleset" {
			continue // client mode
		}
		ran++
		t.Run(tc.Name, func(t *testing.T) {
			held := protocolResponse{Status: http.StatusOK, Headers: map[string]string{"Content-Type": "application/json"}, Body: tc.Held}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("If-None-Match") == "" {
					held.write(t, w)
				} else {
					tc.Response.write(t, w)
				}
			}))
			defer srv.Close()
			var l logs
			clk := newSleeper()
			c := newOnline(t, srv.URL, clk.option(), flagnetic.WithLogger(l.logger()))
			clk.next(t).wake()
			clk.next(t)

			want := flagnetic.StageLastGood
			if tc.Outcome == "accept" {
				want = flagnetic.StageFresh
			}
			if got := c.Stage(); got != want {
				t.Errorf("outcome %s: stage = %q, want %q", tc.Outcome, got, want)
			}
			checkAnswers(t, c, file.Context, tc.Answers)
			for event, n := range tc.Logs {
				if got := len(l.named(flagnetic.Event(event))); got != n {
					t.Errorf("%s logged %d times, want %d", event, got, n)
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("no ruleset degradation case ran")
	}
}

// countsAsserted reports whether a scenario asserts the exact rows of an
// evaluation summary. Such a scenario's answers_of checks are skipped:
// calling every getter of the answers is itself evaluation, and the
// fixture's row counts hold only the scenario's own evaluate steps, so
// honouring both at once cannot pass. The four scenarios without a summary
// assert answers_of instead, and TestSummaryCountsEveryEvaluationByFlagVariationAndReason
// covers what a getter contributes.
func countsAsserted(steps []protocolStep) bool {
	for _, step := range steps {
		if step.Request != nil && step.Request.Events != nil && step.Request.Events.SummaryCounts != nil {
			return true
		}
	}
	return false
}

func TestProtocolScenarios(t *testing.T) {
	file := readScenarios(t)
	ran := 0
	for _, sc := range file.Scenarios {
		if sc.Mode != "server" || !protocolScenarios[sc.Name] {
			continue
		}
		ran++
		t.Run(sc.Name, func(t *testing.T) {
			var l logs
			var mu sync.Mutex
			next := 0 // the step the next request belongs to
			// eventIDs are the event IDs of the batch each step received,
			// which the retry policy compares across steps, and paths
			// every request's path, which a no_request step reads.
			eventIDs := map[int][]string{}
			var paths []string
			steps := sc.Steps
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				for next < len(steps) && steps[next].Request == nil {
					next++
				}
				if next >= len(steps) {
					mu.Unlock()
					t.Errorf("unexpected request %s %s after the last step", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusTeapot)
					return
				}
				at := next
				step := steps[next]
				next++
				mu.Unlock()
				req := step.Request
				if r.Method != req.Method || r.URL.Path != req.Path {
					t.Errorf("%s: request %s %s, want %s %s", step.Note, r.Method, r.URL.Path, req.Method, req.Path)
				}
				for k, v := range req.Headers {
					v = strings.ReplaceAll(v, "<sdk key>", serverKey)
					if got := r.Header.Get(k); got != v {
						t.Errorf("%s: %s = %q, want %q", step.Note, k, got, v)
					}
				}
				for _, k := range req.AbsentHeaders {
					if _, ok := r.Header[http.CanonicalHeaderKey(k)]; ok {
						t.Errorf("%s: %s sent, want absent", step.Note, k)
					}
				}
				if req.Events != nil {
					ids := checkBatch(t, step.Note, r, *req.Events)
					mu.Lock()
					eventIDs[at] = ids
					if other := req.Events.SameIDsAsStep; other != nil {
						if len(eventIDs[*other]) == 0 {
							t.Errorf("%s: step %d carried no batch to compare event IDs with", step.Note, *other)
						}
						if !slices.Equal(ids, eventIDs[*other]) {
							t.Errorf("%s: the batch carries %v, want step %d's own %v", step.Note, ids, *other, eventIDs[*other])
						}
					}
					if other := req.Events.ExcludesIDsOfStep; other != nil {
						if len(eventIDs[*other]) == 0 {
							t.Errorf("%s: step %d carried no batch to compare event IDs with", step.Note, *other)
						}
						for _, id := range ids {
							if slices.Contains(eventIDs[*other], id) {
								t.Errorf("%s: %s was already answered in step %d and is sent again", step.Note, id, *other)
							}
						}
					}
					mu.Unlock()
				}
				step.Respond.write(t, w)
			}))
			defer srv.Close()

			clk := newSleeper()
			// backoffs are the waits the queue of exposures and aliases
			// asked for between the attempts at one batch. Like the fetch
			// loop's, they are the SDK's clock, so a delay window is
			// checked against what it asked for rather than against how
			// long the test took.
			backoffs := make(chan time.Duration, 16)
			opts := []flagnetic.Option{clk.option(), flagnetic.WithLogger(l.logger()),
				flagnetic.WithExposureTick(make(chan time.Time)),
				flagnetic.WithExposureSleep(func(_ context.Context, d time.Duration) bool {
					backoffs <- d
					return true
				}),
			}
			if ms := sc.Given.Options.PollIntervalMS; ms > 0 {
				opts = append(opts, flagnetic.WithPollInterval(time.Duration(ms)*time.Millisecond))
			}
			if sc.Given.Options.SendSummaries {
				opts = append(opts, flagnetic.WithSummaries(true))
			}
			if ms := sc.Given.Options.SummaryIntervalMS; ms > 0 {
				opts = append(opts, flagnetic.WithSummaryInterval(time.Duration(ms)*time.Millisecond))
			}
			if ms := sc.Given.Options.FlushIntervalMS; ms > 0 {
				opts = append(opts, flagnetic.WithFlushInterval(time.Duration(ms)*time.Millisecond))
			}
			opts = append(opts, flagnetic.WithExposures(sc.Given.Options.SendExposures))
			if sc.Given.Options.AllowOverrides {
				opts = append(opts, flagnetic.WithLocalOverrides())
			}
			if sc.Given.Offline != "" {
				// Offline mode replaces the key and the base URL: the
				// mock below must record no request at all.
				opts = append(opts, flagnetic.WithOffline(readProtocol(t, sc.Given.Offline)))
			}
			if sc.Given.Stored != "" {
				path := filepath.Join(t.TempDir(), "ruleset.json")
				if err := os.WriteFile(path, readProtocol(t, sc.Given.Stored), 0o600); err != nil {
					t.Fatal(err)
				}
				opts = append(opts, flagnetic.WithStore(path))
			}
			c := newOnline(t, srv.URL, opts...)

			fetched := false // the construction's own fetch has been used
			var pending *nap
			for i, step := range steps {
				refused := false
				if step.Call != nil {
					refused = makeCall(t, c, file.Context, i, step)
				}
				if step.NoRequest != nil {
					quiet(t, &mu, &paths, step.NoRequest.Path, i, step.Note)
				}
				if step.Request != nil && step.Request.Path == "/sdk/v1/ruleset" {
					if fetched {
						if pending == nil {
							n := clk.next(t)
							pending = &n
						}
						if w := step.Request.DelayMS; w != nil {
							// Durations are the SDK's clock: the wait it asked for.
							if ms := pending.d.Milliseconds(); ms < w.Min || ms > w.Max {
								t.Errorf("step %d %s: the loop waited %v, want %d–%d ms", i, step.Note, pending.d, w.Min, w.Max)
							}
						}
						pending.wake()
						pending = nil
					}
					fetched = true
					n := clk.next(t) // the fetch result is applied
					pending = &n
				} else if step.Request != nil {
					waitHandled(t, &mu, &next, i)
					if w := step.Request.DelayMS; w != nil && step.Request.Events != nil {
						// Durations are the SDK's clock: the wait the
						// queue asked for before sending this batch again.
						select {
						case d := <-backoffs:
							if ms := d.Milliseconds(); ms < w.Min || ms > w.Max {
								t.Errorf("step %d %s: the queue waited %v, want %d–%d ms", i, step.Note, d, w.Min, w.Max)
							}
						default:
							t.Errorf("step %d %s: the batch was sent again without a backoff", i, step.Note)
						}
					}
				}
				if step.Expect == nil {
					continue
				}
				e := step.Expect
				status := c.Status()
				if e.Stage != "" && status.Stage != e.Stage {
					t.Errorf("step %d %s: stage = %q, want %q", i, step.Note, status.Stage, e.Stage)
				}
				switch e.StaleSince {
				case "set":
					if status.StaleSince.IsZero() {
						t.Errorf("step %d %s: stale_since unset, want set", i, step.Note)
					}
				case "unset":
					if !status.StaleSince.IsZero() {
						t.Errorf("step %d %s: stale_since = %v, want unset", i, step.Note, status.StaleSince)
					}
				}
				if e.AnswersOf != "" && !countsAsserted(steps) {
					checkAnswers(t, c, file.Context, file.Answers[e.AnswersOf])
				}
				for event, n := range e.Logs {
					if got := len(l.named(flagnetic.Event(event))); got != n {
						t.Errorf("step %d %s: %s logged %d times, want %d", i, step.Note, event, got, n)
					}
				}
				if e.Refused != nil && refused != *e.Refused {
					t.Errorf("step %d %s: the override was refused = %v, want %v", i, step.Note, refused, *e.Refused)
				}
				for kind, n := range e.Recorded {
					got := 0
					switch kind {
					case "exposure":
						got = len(c.Recorder().Exposures())
					case "alias":
						got = len(c.Recorder().Aliases())
					default:
						t.Fatalf("step %d %s: this SDK does not record the event type %q", i, step.Note, kind)
					}
					if got != n {
						t.Errorf("step %d %s: the recorder holds %d %s events, want %d", i, step.Note, got, kind, n)
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for ; next < len(steps); next++ {
				if steps[next].Request != nil {
					t.Errorf("step %d %s: request never arrived", next, steps[next].Note)
				}
			}
		})
	}
	if ran != len(protocolScenarios) {
		t.Fatalf("ran %d scenarios, want %d: a listed scenario was renamed or removed", ran, len(protocolScenarios))
	}
}

// TestEveryServerScenarioIsCovered is the SDK's no-skip-list gate: every
// server-mode scenario of the scenario files readScenarios reads must run
// in exactly one of TestProtocolScenarios (this file) or
// TestProtocolStreamScenarios (protocol_stream_test.go, which needs two
// loops waiting at once). A scenario added to the fixtures and covered by
// neither fails here instead of silently never running, and one listed in
// both instead of running twice.
func TestEveryServerScenarioIsCovered(t *testing.T) {
	file := readScenarios(t)
	for _, sc := range file.Scenarios {
		if sc.Mode != "server" {
			continue
		}
		switch inFile, inStream := protocolScenarios[sc.Name], streamScenarios[sc.Name]; {
		case !inFile && !inStream:
			t.Errorf("scenario %q is not covered by TestProtocolScenarios or TestProtocolStreamScenarios", sc.Name)
		case inFile && inStream:
			t.Errorf("scenario %q is listed for both TestProtocolScenarios and TestProtocolStreamScenarios", sc.Name)
		}
	}
}
