package sdk_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// protocolTime is the time the protocol documents' events are sent at.
var protocolTime = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

// postEvents posts body to srv's events with key value, and the extra
// headers given as name, value pairs.
func postEvents(t *testing.T, srv *testkit.Server, value string, body []byte, headers ...string) answer {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+sdk.BaseURL+"/events", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+value)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var got bytes.Buffer
	_, _ = got.ReadFrom(res.Body)
	return answer{status: res.StatusCode, header: res.Header, body: got.Bytes()}
}

// batchResult posts body and decodes the 202 answered.
func batchResult(t *testing.T, srv *testkit.Server, value string, body []byte) sdk.EventBatchResult {
	t.Helper()
	a := postEvents(t, srv, value, body)
	if a.status != http.StatusAccepted {
		t.Fatalf("events = %d: %s", a.status, a.body)
	}
	var res sdk.EventBatchResult
	if err := json.Unmarshal(a.body, &res); err != nil {
		t.Fatalf("not a batch result: %v: %s", err, a.body)
	}
	return res
}

// batch wraps events in an envelope.
func batch(events ...any) []byte {
	if events == nil {
		events = []any{}
	}
	b, err := json.Marshal(map[string]any{
		"schema_version": 1, "sent_at": protocolTime.Format(time.RFC3339),
		"sdk": map[string]any{"name": "flagnetic-dart", "version": "1.0.0", "platform": "linux"}, "events": events,
	})
	if err != nil {
		panic(err)
	}
	return b
}

// eventID returns a UUIDv7 minted at t, numbered n.
func eventID(t time.Time, n int) string {
	ms := fmt.Sprintf("%012x", t.UnixMilli())
	return fmt.Sprintf("%s-%s-7000-8000-%012x", ms[:8], ms[8:], n)
}

func aliasEvent(id string) map[string]any {
	return map[string]any{
		"type": "alias", "event_id": id, "occurred_at": protocolTime.Format(time.RFC3339),
		"from_unit_type": "anonymous", "from_unit_id": "d-8f1c", "to_unit_type": "user", "to_unit_id": "u-1001",
	}
}

func summaryEvent(id string, counts ...map[string]any) map[string]any {
	return map[string]any{
		"type": "evaluation_summary", "event_id": id, "interval_start": protocolTime.Add(-time.Minute).Format(time.RFC3339),
		"interval_end": protocolTime.Format(time.RFC3339), "ruleset_version": 1, "app_version": "1.18.0", "counts": counts,
	}
}

// protocolExchanges reads the exchanges the protocol documents pin, by name.
func protocolExchanges(t *testing.T) map[string]exchange {
	t.Helper()
	var all []exchange
	readJSON(t, filepath.Join(protocolDir, "server", "exchanges.json"), &all)
	out := map[string]exchange{}
	for _, e := range all {
		out[e.Name] = e
	}
	return out
}

// eventsServer starts a server whose clock reads the protocol documents'
// time, with a project whose flags are those the documents' summaries name,
// and returns them with a client key of production.
func eventsServer(t *testing.T) (*testkit.Server, project, string) {
	t.Helper()
	srv := testkit.StartServer(t, testkit.WithClock(clock.NewFake(protocolTime)))
	p := newProject(t, srv)
	p.flag(t, "checkout")
	p.flag(t, "banner_text")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindClient)
	return srv, p, value
}

func TestTheProtocolsEventExchangesAreAnsweredAsTheDocumentsSay(t *testing.T) {
	srv, _, value := eventsServer(t)
	exchanges := protocolExchanges(t)
	for _, name := range []string{"events", "events with one rejection", "exposure of an unknown experiment"} {
		ex := exchanges[name]
		a := postEvents(t, srv, value, ex.Request.Body)
		if a.status != ex.Response.Status {
			t.Fatalf("%s: status = %d, want %d: %s", name, a.status, ex.Response.Status, a.body)
		}
		for h, v := range ex.Response.Headers {
			// The documents count down one limit across all four event
			// exchanges; this test posts three of them.
			if h == "RateLimit-Remaining" {
				continue
			}
			if got := a.header.Get(h); got != v {
				t.Errorf("%s: %s = %q, want %q", name, h, got, v)
			}
		}
		var got, want any
		readJSON(t, filepath.Join(protocolDir, ex.Response.Body), &want)
		if err := json.Unmarshal(a.body, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: body = %s, want %v", name, a.body, want)
		}
	}
}

func TestAMixedBatchIsJudgedPerEvent(t *testing.T) {
	srv, _, value := eventsServer(t)
	exchanges := protocolExchanges(t)
	batchResult(t, srv, value, exchanges["events"].Request.Body)
	// The mixed exchange of the protocol documents, without the exposures of
	// an experiment that exists, which no experiment can be before
	// experiments do: every other rejection is this server's answer today.
	var body map[string]any
	if err := json.Unmarshal(exchanges["events with a duplicate and rejections"].Request.Body, &body); err != nil {
		t.Fatal(err)
	}
	body["events"] = body["events"].([]any)[:9]
	sent, _ := json.Marshal(body)
	got := batchResult(t, srv, value, sent)

	var want sdk.EventBatchResult
	readJSON(t, filepath.Join(protocolDir, "server", "events-mixed.json"), &want)
	want.Rejected = want.Rejected[:6]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mixed batch =\n%+v\nwant\n%+v", got, want)
	}
}

func TestAcceptedSummariesAndAliasesAreStoredOnce(t *testing.T) {
	srv, p, value := eventsServer(t)
	body := batch(aliasEvent(eventID(protocolTime, 1)), summaryEvent(eventID(protocolTime, 2),
		map[string]any{"flag": "checkout", "variation": "true", "reason": "targeting_match", "count": 3},
		map[string]any{"flag": "banner_text", "variation": nil, "reason": "fallback", "count": 2},
		map[string]any{"flag": "retired_flag", "variation": "on", "reason": "fallthrough", "count": 1},
	))
	first := batchResult(t, srv, value, body)
	if first.Accepted != 2 || first.Duplicates != 0 || first.DroppedRows != 1 || len(first.Rejected) != 0 {
		t.Errorf("first post = %+v, want 2 accepted and 1 row dropped", first)
	}
	again := batchResult(t, srv, value, body)
	if again.Accepted != 0 || again.Duplicates != 2 || again.DroppedRows != 0 {
		t.Errorf("the same batch again = %+v, want 2 duplicates", again)
	}

	store := pgdelivery.NewStore(testkit.Pool(t))
	production := p.environmentID(t, "production")
	summaries, err := store.EvaluationSummaries(t.Context(), production)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summary rows = %+v, want checkout and banner_text once each", summaries)
	}
	for _, r := range summaries {
		if r.Source != "sdk" || r.SDK.Name != "flagnetic-dart" || r.SDK.Platform != "linux" || r.AppVersion != "1.18.0" ||
			r.RulesetVersion != 1 || !r.ReceivedAt.Equal(protocolTime) || r.EventID.String() != eventID(protocolTime, 2) {
			t.Errorf("summary row = %+v", r)
		}
	}
	if r := summaries[0]; r.Row.Flag != "checkout" || r.Row.Variation != "true" || r.Row.Reason != "targeting_match" || r.Row.Count != 3 {
		t.Errorf("first row = %+v", r.Row)
	}
	if r := summaries[1]; r.Row.Flag != "banner_text" || r.Row.Variation != "" || r.Row.Reason != "fallback" || r.Row.Count != 2 {
		t.Errorf("second row = %+v", r.Row)
	}
	aliases, err := store.Aliases(t.Context(), production)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || aliases[0].Alias.FromUnitID != "d-8f1c" || aliases[0].Alias.ToUnitType != "user" {
		t.Errorf("aliases = %+v, want the one posted", aliases)
	}
}

func TestAnEventIDIsDeduplicatedPerProject(t *testing.T) {
	srv, _, value := eventsServer(t)
	other := newProject(t, srv)
	_, otherValue := other.sdkKey(t, "production", management.SdkKeyKindServer)
	body := batch(aliasEvent(eventID(protocolTime, 1)))
	if res := batchResult(t, srv, value, body); res.Accepted != 1 {
		t.Errorf("first project = %+v", res)
	}
	if res := batchResult(t, srv, otherValue, body); res.Accepted != 1 || res.Duplicates != 0 {
		t.Errorf("the same ID in another project = %+v, want accepted", res)
	}
	if res := batchResult(t, srv, value, batch(aliasEvent(eventID(protocolTime, 2)), aliasEvent(eventID(protocolTime, 2)))); res.Accepted != 1 || res.Duplicates != 1 {
		t.Errorf("an ID twice in one batch = %+v, want 1 accepted and 1 duplicate", res)
	}
}

func TestARetryOnALaterDayIsStillADuplicate(t *testing.T) {
	c := clock.NewFake(protocolTime)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	body := batch(aliasEvent(eventID(protocolTime, 1)))
	batchResult(t, srv, value, body)
	c.Advance(3 * 24 * time.Hour)
	if res := batchResult(t, srv, value, body); res.Accepted != 0 || res.Duplicates != 1 {
		t.Errorf("a retry 3 days later = %+v, want a duplicate", res)
	}
}

func TestConcurrentRetriesOnTwoReplicasStoreEachEventOnce(t *testing.T) {
	c := clock.NewFake(protocolTime)
	a := testkit.StartServer(t, testkit.WithClock(c))
	b := testkit.StartServer(t, testkit.WithClock(c), testkit.WithPool(testkit.Pool(t)))
	p := newProject(t, a)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	var events []any
	for i := range 50 {
		events = append(events, aliasEvent(eventID(protocolTime, i+1)))
	}
	body := batch(events...)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for _, srv := range []*testkit.Server{a, b, a, b} {
		wg.Go(func() {
			res := batchResult(t, srv, value, body)
			mu.Lock()
			accepted += res.Accepted
			mu.Unlock()
		})
	}
	wg.Wait()
	if accepted != 50 {
		t.Errorf("accepted across replicas = %d, want each of 50 events once", accepted)
	}
	aliases, err := pgdelivery.NewStore(testkit.Pool(t)).Aliases(t.Context(), p.environmentID(t, "production"))
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 50 {
		t.Errorf("aliases stored = %d, want 50", len(aliases))
	}
}

func TestTheEnvelopeIsRefusedAsAWhole(t *testing.T) {
	srv, _, value := eventsServer(t)
	for name, c := range map[string]struct {
		body    string
		status  int
		code    string
		pointer string
	}{
		"not JSON":              {`{"events":`, 400, "invalid_request", ""},
		"no events":             {`{"schema_version":1,"sdk":{"name":"flagnetic-go","version":"1.0.0"},"sent_at":"2026-09-19T10:00:00Z"}`, 400, "invalid_request", "/events"},
		"schema version 2":      {`{"schema_version":2,"sdk":{"name":"flagnetic-go","version":"1.0.0"},"sent_at":"2026-09-19T10:00:00Z","events":[]}`, 400, "invalid_request", "/schema_version"},
		"no SDK name":           {`{"schema_version":1,"sdk":{"version":"1.0.0"},"sent_at":"2026-09-19T10:00:00Z","events":[]}`, 400, "invalid_request", "/sdk/name"},
		"SDK platform not text": {`{"schema_version":1,"sdk":{"name":"flagnetic-go","version":"1.0.0","platform":3},"sent_at":"2026-09-19T10:00:00Z","events":[]}`, 400, "invalid_request", "/sdk/platform"},
		"SDK name with NUL":     {`{"schema_version":1,"sdk":{"name":"flag\u0000","version":"1.0.0"},"sent_at":"2026-09-19T10:00:00Z","events":[]}`, 400, "invalid_request", "/sdk/name"},
		"sent_at not a time":    {`{"schema_version":1,"sdk":{"name":"flagnetic-go","version":"1.0.0"},"sent_at":"yesterday","events":[]}`, 400, "invalid_request", "/sent_at"},
		"events not a list":     {`{"schema_version":1,"sdk":{"name":"flagnetic-go","version":"1.0.0"},"sent_at":"2026-09-19T10:00:00Z","events":{}}`, 400, "invalid_request", "/events"},
	} {
		a := postEvents(t, srv, value, []byte(c.body))
		refused(t, name, a.status, a.body, c.status, c.code)
		if c.pointer == "" {
			continue
		}
		p := problemOf(t, a.body)
		if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != c.pointer {
			t.Errorf("%s: errors = %s, want one at %s", name, a.body, c.pointer)
		}
	}
	if res := batchResult(t, srv, value, batch()); res.Accepted != 0 || len(res.Rejected) != 0 {
		t.Errorf("an empty batch = %+v, want nothing stored", res)
	}
	refused(t, "no key", postEvents(t, srv, "", batch()).status, postEvents(t, srv, "", batch()).body, 401, "sdk_key_missing")
}

func TestBatchLimits(t *testing.T) {
	srv, _, value := eventsServer(t)
	events := make([]any, 501)
	for i := range events {
		events[i] = aliasEvent(eventID(protocolTime, i+1))
	}
	a := postEvents(t, srv, value, batch(events...))
	refused(t, "501 events", a.status, a.body, 413, "request_too_large")
	if res := batchResult(t, srv, value, batch(events[:500]...)); res.Accepted != 500 {
		t.Errorf("500 events = %+v, want every one accepted", res)
	}

	big := batch(map[string]any{"type": "alias", "padding": strings.Repeat("x", 1<<20)})
	a = postEvents(t, srv, value, big)
	refused(t, "over 1 MiB", a.status, a.body, 413, "request_too_large")
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(big)
	_ = zw.Close()
	a = postEvents(t, srv, value, zipped.Bytes(), "Content-Encoding", "gzip")
	refused(t, "over 1 MiB after decompression", a.status, a.body, 413, "request_too_large")

	zipped.Reset()
	zw = gzip.NewWriter(&zipped)
	_, _ = zw.Write(batch(aliasEvent(eventID(protocolTime, 900))))
	_ = zw.Close()
	if a = postEvents(t, srv, value, zipped.Bytes(), "Content-Encoding", "gzip"); a.status != http.StatusAccepted {
		t.Errorf("a gzip batch = %d: %s", a.status, a.body)
	}
}

func TestEventsAreJudgedByTheServerClock(t *testing.T) {
	srv, _, value := eventsServer(t)
	old := protocolTime.Add(-31 * 24 * time.Hour)
	stale := aliasEvent(eventID(old, 1))
	stale["occurred_at"] = old.Format(time.RFC3339)
	res := batchResult(t, srv, value, batch(stale, aliasEvent(eventID(protocolTime.Add(-29*24*time.Hour), 2))))
	if res.Accepted != 1 || len(res.Rejected) != 1 || res.Rejected[0].Code != "out_of_window" || res.Rejected[0].Index != 0 {
		t.Errorf("a 31-day-old and a 29-day-old event = %+v, want the first out_of_window", res)
	}
}

func TestIngestCountsEventsByKindAndDropsByReason(t *testing.T) {
	srv, _, value := eventsServer(t)
	body := batch(aliasEvent(eventID(protocolTime, 1)), summaryEvent(eventID(protocolTime, 2),
		map[string]any{"flag": "checkout", "variation": "true", "reason": "rule", "count": 1},
		map[string]any{"flag": "retired_flag", "variation": "on", "reason": "rule", "count": 1},
	), map[string]any{"type": "future_event", "event_id": eventID(protocolTime, 3)})
	batchResult(t, srv, value, body)
	batchResult(t, srv, value, body)
	scrape := srv.Scrape(t)
	for series, want := range map[string]float64{
		`flagnetic_events_ingested_total{kind="alias"}`:               1,
		`flagnetic_events_ingested_total{kind="evaluation_summary"}`:  1,
		`flagnetic_events_dropped_total{reason="duplicate"}`:          2,
		`flagnetic_events_dropped_total{reason="unknown_flag"}`:       1,
		`flagnetic_events_dropped_total{reason="event_type_unknown"}`: 2,
	} {
		if got := testkit.Metric(t, scrape, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
}

func TestThePartitionWorkerKeepsPartitionsAheadOfTheClock(t *testing.T) {
	c := clock.NewFake(protocolTime)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	c.Advance(10 * 24 * time.Hour)
	if err := srv.Tick(t, "event_partitions"); err != nil {
		t.Fatal(err)
	}
	// Received 3 days after the worker's round: its partition was created
	// ahead, and the event is stored.
	c.Advance(3 * 24 * time.Hour)
	if res := batchResult(t, srv, value, batch(aliasEvent(eventID(c.Now(), 1)))); res.Accepted != 1 {
		t.Errorf("an event 3 days after the round = %+v", res)
	}
	aliases, err := pgdelivery.NewStore(testkit.Pool(t)).Aliases(t.Context(), p.environmentID(t, "production"))
	if err != nil || len(aliases) != 1 {
		t.Errorf("aliases = %+v, %v", aliases, err)
	}
}

func TestACopyOfARefusedEventIsRefusedNotADuplicate(t *testing.T) {
	srv, _, value := eventsServer(t)
	undeclared := aliasEvent(eventID(protocolTime, 1))
	undeclared["to_unit_type"] = "device"
	res := batchResult(t, srv, value, batch(undeclared, undeclared))
	if res.Accepted != 0 || res.Duplicates != 0 || len(res.Rejected) != 2 {
		t.Fatalf("an undeclared alias twice = %+v, want both refused", res)
	}
	for _, r := range res.Rejected {
		if r.Code != "undeclared_unit_type" {
			t.Errorf("rejection = %+v, want undeclared_unit_type", r)
		}
	}
}

func TestCreatingAPartitionGivesUpRatherThanStallInserts(t *testing.T) {
	testkit.StartServer(t)
	pool := testkit.Pool(t)
	reader, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(t.Context()) }()
	// A reader that stays open holds the parent table for as long as it runs.
	if _, err = reader.Exec(t.Context(), `LOCK TABLE evaluation_summary_events IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = pgdelivery.NewStore(pool).CreateEventPartitions(t.Context(), protocolTime.AddDate(1, 0, 0), 0)
	if err == nil {
		t.Fatal("a partition was attached while a reader held its parent")
	}
	if waited := time.Since(start); waited > 30*time.Second {
		t.Errorf("gave up after %v, want within seconds", waited)
	}
}
