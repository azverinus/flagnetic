package flagnetic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// The evaluation summary and the ingest client it rides on: the events
// envelope, the retry policy, the window that merges, ages out and
// splits, and the promise that no unit ID, attribute value or flag value
// leaves the process.

// batch is one ingest batch the mock received: its headers and its
// decompressed body.
type batch struct {
	header http.Header
	raw    []byte
	wire   wireBatch
}

// eventIDs are the batch's event IDs in order.
func (b batch) eventIDs() []string {
	out := make([]string, 0, len(b.wire.Events))
	for _, e := range b.wire.Events {
		out = append(out, e.EventID)
	}
	return out
}

// reply is what the mock answers one batch.
type reply struct {
	status     int
	body       string
	retryAfter string
	// networkError drops the connection instead of answering.
	networkError bool
}

const accepted = `{"accepted": 1, "duplicates": 0, "dropped_rows": 0, "rejected": []}`

// ingestMock serves the ruleset once and scripts the answers to every
// ingest batch, keeping each one decompressed.
type ingestMock struct {
	*httptest.Server
	t       *testing.T
	answers []reply
	// ruleset is the document the ruleset endpoint serves; the package's
	// own document unless a test replaces it before construction returns.
	ruleset string

	mu      sync.Mutex
	batches []batch
	arrived chan struct{}
}

func newIngestMock(t *testing.T, answers ...reply) *ingestMock {
	t.Helper()
	if len(answers) == 0 {
		answers = []reply{{status: http.StatusAccepted, body: accepted}}
	}
	m := &ingestMock{t: t, answers: answers, ruleset: document, arrived: make(chan struct{}, 64)}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sdk/v1/ruleset":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"7"`)
			_, _ = w.Write([]byte(m.ruleset))
		case "/sdk/v1/events":
			m.serveEvents(w, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *ingestMock) serveEvents(w http.ResponseWriter, r *http.Request) {
	raw, err := decompress(r)
	if err != nil {
		m.t.Errorf("read the batch: %v", err)
		w.WriteHeader(http.StatusTeapot)
		return
	}
	var wire wireBatch
	if err := json.Unmarshal(raw, &wire); err != nil {
		m.t.Errorf("decode the batch %s: %v", raw, err)
	}
	m.mu.Lock()
	n := len(m.batches)
	m.batches = append(m.batches, batch{header: r.Header.Clone(), raw: raw, wire: wire})
	a := m.answers[min(n, len(m.answers)-1)]
	m.mu.Unlock()
	select {
	case m.arrived <- struct{}{}:
	default:
	}
	if a.networkError {
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
		return
	}
	if a.retryAfter != "" {
		w.Header().Set("Retry-After", a.retryAfter)
	}
	if a.body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(a.status)
	_, _ = io.WriteString(w, a.body)
}

// decompress reads a request body, gunzipping it when the SDK said it
// was gzipped.
// nth waits for the nth batch (1-based) to have arrived and returns it.
func (m *ingestMock) nth(n int) batch {
	m.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		m.mu.Lock()
		got := len(m.batches)
		m.mu.Unlock()
		if got >= n {
			m.mu.Lock()
			defer m.mu.Unlock()
			return m.batches[n-1]
		}
		select {
		case <-m.arrived:
		case <-deadline:
			m.t.Fatalf("batch %d never arrived; %d did", n, got)
		}
	}
}

// count is how many batches have arrived.
func (m *ingestMock) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.batches)
}

// quiet fails when another batch arrives within d.
func (m *ingestMock) quiet(d time.Duration, was int) {
	m.t.Helper()
	time.Sleep(d)
	if got := m.count(); got != was {
		m.t.Errorf("%d batches arrived, want %d", got, was)
	}
}

// summaryClient builds an online client whose ruleset fetch has been
// applied and whose summary interval only the returned tick channel
// drives, so a test decides when a window closes.
func summaryClient(t *testing.T, m *ingestMock, opts ...flagnetic.Option) (*flagnetic.Client, chan time.Time) {
	t.Helper()
	ticks := make(chan time.Time, 4)
	clk := newSleeper()
	opts = append([]flagnetic.Option{clk.option(), flagnetic.WithSummaries(true), flagnetic.WithSummaryTick(ticks)}, opts...)
	c := newOnline(t, m.URL, opts...)
	clk.next(t) // the fetch has been applied and the loop waits
	if stage := c.Stage(); stage != flagnetic.StageFresh {
		t.Fatalf("stage = %q, want fresh", stage)
	}
	return c, ticks
}

// shortInterval makes every backoff outlast the summary interval, so a
// failed send gives up at once and the window merges into the next.
func shortInterval() []flagnetic.Option {
	return []flagnetic.Option{
		flagnetic.WithSummaryInterval(10 * time.Millisecond),
		flagnetic.WithJitter(func(ceiling time.Duration) time.Duration { return ceiling }),
	}
}

// secretive is a context whose unit ID and attribute value appear nowhere
// else, so finding either in a summary body means the SDK leaked it.
var secretive = flagnetic.Context{
	Units: map[string]string{"user": "u-secret-unit"},
	Attributes: map[string]any{
		"os":               "linux",
		"favourite_colour": "secret-attribute-value",
	},
}

// flushAnyway closes the window and reports it, tolerating the error of a
// report the platform refused: the window merges into the next either way.
func flushAnyway(t *testing.T, c *flagnetic.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.Flush(ctx)
}

func flush(t *testing.T, c *flagnetic.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func TestSummaryCountsEveryEvaluationByFlagVariationAndReason(t *testing.T) {
	m := newIngestMock(t)
	c, _ := summaryClient(t, m, flagnetic.WithLocalOverrides())
	if err := c.LocalOverrides().Set("greeting", "Local greeting"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	for range 3 {
		c.Bool(context.Background(), linux, "new_checkout", false)
	}
	for range 2 {
		c.String(context.Background(), linux, "missing_flag", "«none»") // absent: fallback
	}
	c.Bool(context.Background(), linux, "theme", false)           // a type mismatch: fallback
	c.String(context.Background(), linux, "greeting", "Built-in") // the local override
	c.Number(context.Background(), linux, "page_size", 0)         // the flag is off

	flush(t, c)
	b := m.nth(1)
	if len(b.wire.Events) != 1 {
		t.Fatalf("the batch carries %d events, want 1", len(b.wire.Events))
	}
	got := rowsOf(b.wire.Events[0])
	want := []row{
		{"greeting", "«none»", "local_override", 1},
		{"missing_flag", "«none»", "fallback", 2},
		{"new_checkout", "on", "rule", 3},
		{"page_size", "small", "off", 1},
		{"theme", "«none»", "fallback", 1},
	}
	assertRows(t, got, want)
}

func TestSummaryEnvelopeIsTheProtocolsAndCarriesNoUnitOrValue(t *testing.T) {
	m := newIngestMock(t)
	c, _ := summaryClient(t, m, flagnetic.WithLocalOverrides())
	if err := c.LocalOverrides().Set("greeting", "Local greeting"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	before := time.Now()
	c.Bool(context.Background(), secretive, "new_checkout", false)
	c.String(context.Background(), secretive, "greeting", "Built-in")
	flush(t, c)
	b := m.nth(1)

	if got := b.header.Get("Authorization"); got != "Bearer "+serverKey {
		t.Errorf("Authorization = %q", got)
	}
	for header, want := range map[string]string{
		"Content-Type":             "application/json",
		"Content-Encoding":         "gzip",
		"Flagnetic-Schema-Version": "1",
		"User-Agent":               fmt.Sprintf("flagnetic-go/%s (%s)", flagnetic.Version, runtime.GOOS),
	} {
		if got := b.header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if b.wire.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", b.wire.SchemaVersion)
	}
	if b.wire.SDK.Name != "flagnetic-go" || b.wire.SDK.Version != flagnetic.Version || b.wire.SDK.Platform != runtime.GOOS {
		t.Errorf("sdk = %+v", b.wire.SDK)
	}
	if b.wire.SentAt.Before(before) {
		t.Errorf("sent_at = %v, want at or after %v", b.wire.SentAt, before)
	}
	e := b.wire.Events[0]
	if e.Type != "evaluation_summary" {
		t.Errorf("type = %q", e.Type)
	}
	assertUUIDv7(t, e.EventID)
	if !e.IntervalStart.Before(e.IntervalEnd) && !e.IntervalStart.Equal(e.IntervalEnd) {
		t.Errorf("interval %v–%v", e.IntervalStart, e.IntervalEnd)
	}
	if e.IntervalStart.Before(before.Add(-time.Minute)) || e.IntervalEnd.After(time.Now().Add(time.Second)) {
		t.Errorf("interval %v–%v is outside the test's own run", e.IntervalStart, e.IntervalEnd)
	}
	if e.RulesetVersion != 7 {
		t.Errorf("ruleset_version = %d, want 7, the version held at the interval's end", e.RulesetVersion)
	}
	if e.AppVersion != nil {
		// The Go SDK has no option naming the application's version yet,
		// and the field is nullable, so it must be absent rather than
		// guessed at.
		t.Errorf("app_version = %q, want none", *e.AppVersion)
	}

	// Nothing that identifies a unit, names an attribute value or repeats
	// a served or substituted value may be in the body.
	for _, secret := range []string{"u-secret-unit", "secret-attribute-value", "Local greeting", "hello"} {
		if strings.Contains(string(b.raw), secret) {
			t.Errorf("the summary body carries %q:\n%s", secret, b.raw)
		}
	}
}

func assertUUIDv7(t *testing.T, id string) {
	t.Helper()
	if len(id) != 36 {
		t.Fatalf("event_id %q is not 36 characters", id)
	}
	for i, c := range id {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				t.Fatalf("event_id %q has no dash at %d", id, i)
			}
		case 14:
			if c != '7' {
				t.Errorf("event_id %q is version %c, want 7", id, c)
			}
		case 19:
			if c != '8' && c != '9' && c != 'a' && c != 'b' {
				t.Errorf("event_id %q has variant %c, want the RFC 4122 one", id, c)
			}
		default:
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Errorf("event_id %q has %q at %d, want a lowercase hex digit", id, c, i)
			}
		}
	}
}

func TestNothingEvaluatedNothingSent(t *testing.T) {
	m := newIngestMock(t)
	c, ticks := summaryClient(t, m)
	ticks <- time.Now()
	flush(t, c)
	m.quiet(200*time.Millisecond, 0)
}

func TestSummariesRetryTheWholeBatchWithTheSameEventIDs(t *testing.T) {
	var l logs
	m := newIngestMock(t,
		reply{status: http.StatusInternalServerError},
		reply{networkError: true},
		reply{status: http.StatusTooManyRequests, retryAfter: "0", body: `{"title": "Too many requests"}`},
		reply{status: http.StatusAccepted, body: accepted},
	)
	retries := newSleeper()
	c, _ := summaryClient(t, m, flagnetic.WithLogger(l.logger()), retries.summaryOption())
	c.Bool(context.Background(), linux, "new_checkout", false)

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done <- c.Flush(ctx)
	}()
	first := m.nth(1).eventIDs()
	for n := 2; n <= 4; n++ {
		retries.next(t).wake() // the backoff between attempts
		if got := m.nth(n).eventIDs(); !sameIDs(got, first) {
			t.Errorf("attempt %d carried %v, want %v: a retry keeps its event IDs", n, got, first)
		}
	}
	if err := <-done; err != nil {
		t.Errorf("Flush: %v", err)
	}
	m.quiet(200*time.Millisecond, 4)
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestA429IsRetriedAfterTheDelayItAsksFor(t *testing.T) {
	m := newIngestMock(t,
		reply{status: http.StatusTooManyRequests, retryAfter: "2", body: `{"title": "Too many requests"}`},
		reply{status: http.StatusAccepted, body: accepted},
	)
	retries := newSleeper()
	c, _ := summaryClient(t, m,
		flagnetic.WithSummaryInterval(time.Hour),
		retries.summaryOption())
	c.Bool(context.Background(), linux, "new_checkout", false)

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done <- c.Flush(ctx)
	}()
	m.nth(1)
	nap := retries.next(t)
	if nap.d != 2*time.Second {
		t.Errorf("the reporter waited %v, want the two seconds Retry-After asked for", nap.d)
	}
	nap.wake()
	m.nth(2)
	if err := <-done; err != nil {
		t.Errorf("Flush: %v", err)
	}
}

func TestTheReportingIntervalClosesAWindowOnItsOwn(t *testing.T) {
	m := newIngestMock(t)
	clk := newSleeper()
	// No tick channel: the reporter's own interval closes the window, and
	// nothing calls Flush.
	c := newOnline(t, m.URL, clk.option(), flagnetic.WithSummaries(true),
		flagnetic.WithSummaryInterval(20*time.Millisecond))
	clk.next(t)
	c.Bool(context.Background(), linux, "new_checkout", false)
	assertRows(t, rowsOf(m.nth(1).wire.Events[0]), []row{{"new_checkout", "on", "rule", 1}})
}

func TestAFailedWindowMergesIntoTheNext(t *testing.T) {
	m := newIngestMock(t,
		reply{status: http.StatusInternalServerError},
		reply{status: http.StatusAccepted, body: accepted},
	)
	// The first backoff, a full second without jitter, outlasts the
	// interval, so the window's one attempt is its only one.
	c, _ := summaryClient(t, m, shortInterval()...)

	for range 2 {
		c.Bool(context.Background(), linux, "new_checkout", false)
	}
	started := time.Now()
	flushAnyway(t, c)
	failed := m.nth(1)
	assertRows(t, rowsOf(failed.wire.Events[0]), []row{{"new_checkout", "on", "rule", 2}})

	c.Bool(context.Background(), linux, "new_checkout", false)
	c.String(context.Background(), linux, "greeting", "Built-in")
	flush(t, c)
	merged := m.nth(2)
	e := merged.wire.Events[0]
	assertRows(t, rowsOf(e), []row{
		{"new_checkout", "on", "rule", 3},
		{"greeting", "hi", "fallthrough", 1},
	})
	if e.IntervalStart.After(started) {
		t.Errorf("the merged interval starts at %v, want the failed window's start, at or before %v", e.IntervalStart, started)
	}
	if e.EventID == failed.wire.Events[0].EventID {
		t.Error("the merged window reuses the failed window's event ID; it carries other counts and is another report")
	}
	m.quiet(200*time.Millisecond, 2)
}

func TestAWindowOlderThanOneHourIsDroppedWithoutThisIntervalsCounts(t *testing.T) {
	var l logs
	m := newIngestMock(t,
		reply{status: http.StatusInternalServerError},
		reply{status: http.StatusAccepted, body: accepted},
	)
	now := &testClock{at: time.Now()}
	c, _ := summaryClient(t, m, append(shortInterval(),
		flagnetic.WithLogger(l.logger()),
		flagnetic.WithSummaryNow(now.now))...)

	c.Bool(context.Background(), linux, "new_checkout", false)
	flushAnyway(t, c)
	assertRows(t, rowsOf(m.nth(1).wire.Events[0]), []row{{"new_checkout", "on", "rule", 1}})

	now.advance(61 * time.Minute)
	c.String(context.Background(), linux, "greeting", "Built-in")
	flush(t, c)
	// The kept window began over an hour ago and goes; what this interval
	// counted is reported rather than going down with it.
	assertRows(t, rowsOf(m.nth(2).wire.Events[0]), []row{{"greeting", "hi", "fallthrough", 1}})
	if got := len(l.named(flagnetic.EventQueueDropped)); got != 1 {
		t.Errorf("queue_dropped logged %d times, want 1", got)
	}
	m.quiet(200*time.Millisecond, 2)
}

// testClock is the SDK's clock in a test's hand.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func TestMoreThanAThousandRowsSplitIntoSeveralEvents(t *testing.T) {
	m := newIngestMock(t)
	c, _ := summaryClient(t, m)
	// Every absent flag is its own row: one reason, no variation.
	const rows = 1500
	for i := range rows {
		c.String(context.Background(), linux, fmt.Sprintf("absent_%04d", i), "«none»")
	}
	flush(t, c)
	b := m.nth(1)
	if len(b.wire.Events) != 2 {
		t.Fatalf("%d rows became %d events, want 2 of at most 1000 rows", rows, len(b.wire.Events))
	}
	total, ids := 0, map[string]bool{}
	first := b.wire.Events[0]
	for _, e := range b.wire.Events {
		if len(e.Counts) > 1000 {
			t.Errorf("an event carries %d rows, want at most 1000", len(e.Counts))
		}
		assertUUIDv7(t, e.EventID)
		if ids[e.EventID] {
			t.Errorf("event ID %s is used twice", e.EventID)
		}
		ids[e.EventID] = true
		if !e.IntervalStart.Equal(first.IntervalStart) || !e.IntervalEnd.Equal(first.IntervalEnd) || e.RulesetVersion != first.RulesetVersion {
			t.Errorf("event %s reports another interval or ruleset version than its window's", e.EventID)
		}
		total += len(e.Counts)
	}
	if total != rows {
		t.Errorf("the events carry %d rows together, want %d", total, rows)
	}
}

func TestBatchesStayWithinTheEndpointsLimits(t *testing.T) {
	m := newIngestMock(t)
	c, _ := summaryClient(t, m)
	// Far more rows than one batch may carry: the endpoint takes 500
	// events and a megabyte of decompressed envelope, and an event holds
	// at most 1000 rows.
	const rows = 30000
	for i := range rows {
		c.String(context.Background(), linux, fmt.Sprintf("absent_flag_with_a_realistic_key_%05d", i), "«none»")
	}
	flush(t, c)
	if m.count() < 2 {
		t.Fatalf("%d rows went in one batch: the limits were never exercised", rows)
	}
	total, ids := 0, map[string]bool{}
	for n := 1; n <= m.count(); n++ {
		b := m.nth(n)
		if len(b.raw) > 1<<20 {
			t.Errorf("batch %d is %d bytes decompressed, want at most 1 MiB", n, len(b.raw))
		}
		if len(b.wire.Events) > 500 {
			t.Errorf("batch %d carries %d events, want at most 500", n, len(b.wire.Events))
		}
		for _, e := range b.wire.Events {
			if len(e.Counts) > 1000 {
				t.Errorf("an event carries %d rows, want at most 1000", len(e.Counts))
			}
			if ids[e.EventID] {
				t.Errorf("event ID %s is used twice", e.EventID)
			}
			ids[e.EventID] = true
			total += len(e.Counts)
		}
	}
	if total != rows {
		t.Errorf("the batches carry %d rows together, want %d", total, rows)
	}
}

func TestRejectedRowsAreNeverRetried(t *testing.T) {
	m := newIngestMock(t, reply{status: http.StatusAccepted, body: `{
		"accepted": 0, "duplicates": 1, "dropped_rows": 2,
		"rejected": [{"index": 0, "event_id": "01a0b91b-c500-7000-8000-000000000008",
		              "code": "out_of_window", "detail": "the event_id time is older than the raw retention"}]}`})
	var l logs
	c, ticks := summaryClient(t, m, flagnetic.WithLogger(l.logger()))
	c.Bool(context.Background(), linux, "new_checkout", false)
	flush(t, c)
	m.nth(1)

	// The counts of the 202 are read and logged, so a rejection or a row
	// the platform does not know is visible.
	dropped := l.named(flagnetic.EventQueueDropped)
	if len(dropped) != 1 {
		t.Fatalf("queue_dropped logged %d times, want 1", len(dropped))
	}
	fields := dropped[0].Fields
	if fields["duplicates"] != 1 || fields["dropped_rows"] != 2 {
		t.Errorf("queue_dropped fields = %v, want the 202 answer's counts", fields)
	}
	if codes, ok := fields["rejected"].([]string); !ok || len(codes) != 1 || codes[0] != "out_of_window" {
		t.Errorf("queue_dropped rejected = %v, want [out_of_window]", fields["rejected"])
	}

	// A rejection is final: the rows go, and the next window reports only
	// what was evaluated after it.
	c.String(context.Background(), linux, "greeting", "Built-in")
	ticks <- time.Now()
	assertRows(t, rowsOf(m.nth(2).wire.Events[0]), []row{{"greeting", "hi", "fallthrough", 1}})
	m.quiet(200*time.Millisecond, 2)
}

func TestOtherClientErrorsDropTheBatchAndLog(t *testing.T) {
	var l logs
	m := newIngestMock(t,
		reply{status: http.StatusBadRequest, body: `{"code": "malformed_body"}`},
		reply{status: http.StatusAccepted, body: accepted},
	)
	c, ticks := summaryClient(t, m, flagnetic.WithLogger(l.logger()))
	c.Bool(context.Background(), linux, "new_checkout", false)
	flush(t, c)
	m.nth(1)
	m.quiet(200*time.Millisecond, 1) // no retry of a 400

	if got := len(l.named(flagnetic.EventQueueDropped)); got != 1 {
		t.Errorf("queue_dropped logged %d times, want 1", got)
	}
	// The dropped window does not merge: it was answered, not lost.
	c.String(context.Background(), linux, "greeting", "Built-in")
	ticks <- time.Now()
	assertRows(t, rowsOf(m.nth(2).wire.Events[0]), []row{{"greeting", "hi", "fallthrough", 1}})
}

func TestARefusedKeyLogsKeyRevokedOnceAndBacksOffToTheUnauthorizedCap(t *testing.T) {
	for name, status := range map[string]int{"401": http.StatusUnauthorized, "403": http.StatusForbidden} {
		t.Run(name, func(t *testing.T) {
			// Ten refusals, then the operator restores the key.
			const refusals = 10
			answers := make([]reply, 0, refusals+1)
			for range refusals {
				answers = append(answers, reply{status: status, body: `{"code": "sdk_key_revoked"}`})
			}
			answers = append(answers, reply{status: http.StatusAccepted, body: accepted})
			var l logs
			m := newIngestMock(t, answers...)
			now := &testClock{at: time.Now()}
			c, _ := summaryClient(t, m,
				flagnetic.WithLogger(l.logger()),
				flagnetic.WithSummaryNow(now.now),
				flagnetic.WithJitter(func(ceiling time.Duration) time.Duration { return ceiling }))

			// A refused key is not retried within the interval: it waits,
			// indefinitely, at a backoff that grows past the five minutes
			// every other failure is capped at, up to the fifteen of the
			// unauthorized cap.
			waits := make([]time.Duration, 0, refusals)
			for n := 1; n <= refusals; n++ {
				c.Bool(context.Background(), linux, "new_checkout", false)
				flushAnyway(t, c)
				m.nth(n)
				wait := lastRetryIn(t, &l, n)
				waits = append(waits, wait)
				// While the backoff runs, a flush posts nothing at all.
				flushAnyway(t, c)
				m.quiet(50*time.Millisecond, n)
				now.advance(wait + time.Second)
			}
			if got := len(l.named(flagnetic.EventKeyRevoked)); got != 1 {
				t.Errorf("key_revoked logged %d times, want 1", got)
			}
			for i, got := range waits {
				want := min(time.Second<<i, 15*time.Minute)
				if got != want {
					t.Errorf("refusal %d waited %v, want %v", i+1, got, want)
				}
			}
			if last := waits[len(waits)-1]; last <= 5*time.Minute || last > 15*time.Minute {
				t.Errorf("the last backoff was %v, want past the 5-minute cap of any other failure and within the 15-minute unauthorized cap", last)
			}

			// The key works again: every count of every refused interval
			// is still there, merged into one report.
			c.Bool(context.Background(), linux, "new_checkout", false)
			flush(t, c)
			assertRows(t, rowsOf(m.nth(refusals + 1).wire.Events[0]),
				[]row{{"new_checkout", "on", "rule", refusals + 1}})
		})
	}
}

// lastRetryIn waits for the nth ingest_failed event and returns the wait
// it announced.
func lastRetryIn(t *testing.T, l *logs, n int) time.Duration {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if entries := l.named(flagnetic.EventIngestFailed); len(entries) >= n {
			raw, ok := entries[n-1].Fields["retry_in"].(string)
			if !ok {
				t.Fatalf("ingest_failed carried retry_in %v, want a duration", entries[n-1].Fields["retry_in"])
			}
			d, err := time.ParseDuration(raw)
			if err != nil {
				t.Fatalf("ingest_failed retry_in %q: %v", raw, err)
			}
			return d
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("ingest_failed was logged fewer than %d times", n)
	return 0
}

func TestReportingIsDisabledByItsOptionAndByOfflineMode(t *testing.T) {
	t.Run("the option", func(t *testing.T) {
		m := newIngestMock(t)
		c, ticks := summaryClient(t, m, flagnetic.WithSummaries(false))
		c.Bool(context.Background(), linux, "new_checkout", false)
		ticks <- time.Now()
		flush(t, c)
		m.quiet(200*time.Millisecond, 0)
	})

	t.Run("offline mode records what it would have sent", func(t *testing.T) {
		c := newOffline(t)
		for range 2 {
			c.Bool(context.Background(), linux, "new_checkout", false)
		}
		flush(t, c)
		got := c.Recorder().EvaluationSummaries()
		if len(got) != 1 {
			t.Fatalf("the recorder holds %d summaries, want 1", len(got))
		}
		s := got[0]
		assertUUIDv7(t, s.EventID)
		if s.RulesetVersion != 7 {
			t.Errorf("ruleset_version = %d, want 7", s.RulesetVersion)
		}
		want := []flagnetic.SummaryCount{{Flag: "new_checkout", Variation: "on", Reason: flagnetic.ReasonRule, Count: 2}}
		if len(s.Counts) != 1 || s.Counts[0] != want[0] {
			t.Errorf("counts = %+v, want %+v", s.Counts, want)
		}
		// The running total the recorder has always exposed stays.
		if rows := c.Recorder().Summary(); len(rows) != 1 || rows[0].Count != 2 {
			t.Errorf("Summary = %+v", rows)
		}
	})
}

func TestCloseFlushesTheCurrentWindow(t *testing.T) {
	m := newIngestMock(t)
	c, _ := summaryClient(t, m)
	c.Bool(context.Background(), linux, "new_checkout", false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertRows(t, rowsOf(m.nth(1).wire.Events[0]), []row{{"new_checkout", "on", "rule", 1}})
}

func TestCloseReturnsWithinItsFlushTimeoutWhileTheBatchHangs(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sdk/v1/ruleset" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(document))
			return
		}
		<-hang
	}))
	// Closing the server waits for its handlers, so the hanging one is
	// released first.
	defer srv.Close()
	defer close(hang)
	clk := newSleeper()
	ticks := make(chan time.Time, 1)
	c, err := flagnetic.New(serverKey, srv.URL,
		flagnetic.WithChangeStream(false), clk.option(), flagnetic.WithSummaryTick(ticks))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	clk.next(t)
	c.Bool(context.Background(), linux, "new_checkout", false)

	// Close gets its flush timeout of five seconds, and
	// returns rather than waiting on a request that never answers.
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("Close took %v, want at most the five-second flush timeout and a little", took)
	}
}

func TestConcurrentEvaluationsAndFlushesCountEveryOne(t *testing.T) {
	m := newIngestMock(t)
	c, ticks := summaryClient(t, m)
	const goroutines, each = 8, 200
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				c.Bool(context.Background(), linux, "new_checkout", false)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 5 {
			ticks <- time.Now()
		}
	}()
	wg.Wait()
	flush(t, c)

	var total int64
	for n := 1; n <= m.count(); n++ {
		for _, e := range m.nth(n).wire.Events {
			for _, r := range e.Counts {
				if r.Flag != "new_checkout" || r.Reason != "rule" {
					t.Errorf("unexpected row %+v", r)
				}
				total += r.Count
			}
		}
	}
	if want := int64(goroutines * each); total != want {
		t.Errorf("the summaries count %d evaluations together, want %d", total, want)
	}
}
