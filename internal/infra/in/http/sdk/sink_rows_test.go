package sdk_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	pgsinks "github.com/azverinus/flagnetic/internal/infra/out/postgres/sinks"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// sinkRows is what a sink's streams read of a project's events: the events
// ingested through /sdk/v1, read through the sinks adapter.
type sinkRows struct {
	*rollupFixture
	store *pgsinks.Store
}

func startSinkRows(t *testing.T) sinkRows {
	t.Helper()
	return sinkRows{rollupFixture: startRollup(t), store: pgsinks.NewStore(testkit.Pool(t))}
}

// scope is the whole project, or only the environments given.
func (f sinkRows) scope(t *testing.T, environments ...string) sinks.Scope {
	t.Helper()
	s := sinks.Scope{Project: f.p.projectID(t)}
	for _, e := range environments {
		s.Environments = append(s.Environments, f.p.environmentID(t, e))
	}
	return s
}

// readAll reads the stream of kind in scope from its start in batches of at
// most size rows, once the transactions running have ended, until none is
// left, and answers the batches' sizes, the rows and how many were skipped.
func (f sinkRows) readAll(t *testing.T, kind sinks.EventKind, scope sinks.Scope, size int) (batches []int, rows []sinks.Row, skipped int) {
	t.Helper()
	return readStream(t, f.srv, f.store, kind, scope, events.Cursor{}, size)
}

func readStream(t *testing.T, srv *testkit.Server, store *pgsinks.Store, kind sinks.EventKind, scope sinks.Scope, from events.Cursor, size int) (batches []int, rows []sinks.Row, skipped int) {
	t.Helper()
	srv.AwaitHorizon(t)
	for {
		b, err := store.NextBatch(t.Context(), kind, scope, from, size, nil)
		if err != nil {
			t.Fatal(err)
		}
		if b.Rows == 0 {
			return batches, rows, skipped
		}
		page, err := store.ReadBatch(t.Context(), kind, scope, b)
		if err != nil {
			t.Fatal(err)
		}
		batches, rows, skipped, from = append(batches, b.Rows), append(rows, page.Rows...), skipped+page.Skipped, b.To
	}
}

// postTo sends events with a server key of environment.
func (f sinkRows) postTo(t *testing.T, environment string, evs ...any) {
	t.Helper()
	_, key := f.p.sdkKey(t, environment, management.SdkKeyKindServer)
	if res := batchResult(t, f.srv, key, f.envelope(t, evs...)); res.Accepted != len(evs) {
		t.Fatalf("batch to %s = %+v, want all %d accepted", environment, res, len(evs))
	}
}

func unitsOf(rows []sinks.Row) []string {
	var units []string
	for _, r := range rows {
		units = append(units, r.(sinks.ExposureRow).UnitID)
	}
	return units
}

func TestASinkReadsAnExposureAsItsExportSchemaSaysAndEndsABatchAtItsSize(t *testing.T) {
	f := startSinkRows(t)
	now := f.clock.Now()
	f.post(t, f.exposure("u-1", "true", now), f.exposure("u-2", "false", now), f.override("u-3", "true", now),
		f.exposure("u-4", "true", now), f.exposure("u-5", "false", now))

	batches, rows, skipped := f.readAll(t, sinks.EventExposure, f.scope(t), 2)
	if !slices.Equal(batches, []int{2, 2, 1}) || skipped != 0 {
		t.Errorf("batches = %v with %d skipped, want 2, 2 and 1", batches, skipped)
	}
	if got := unitsOf(rows); !slices.Equal(got, []string{"u-1", "u-2", "u-3", "u-4", "u-5"}) {
		t.Fatalf("rows = %v, want the five in the order they were committed", got)
	}
	want := sinks.ExposureRow{
		EventID: rows[0].ID(), ProjectKey: f.p.key, EnvironmentKey: "production", ExperimentKey: "checkout_test",
		FlagKey: "checkout", VariationKey: "true", UnitType: "user", UnitID: "u-1", EvaluationReason: "experiment",
		Platform: "android", AppVersion: "1.18.0", SDKName: "flagnetic-dart", SDKVersion: "1.0.0", RulesetVersion: 1,
		OccurredAt: now, ReceivedAt: now, Seq: rows[0].Sequence(),
	}
	if rows[0] != want {
		t.Errorf("first row = %+v, want %+v", rows[0], want)
	}
	// The sequence number is the raw row's, which names the range of a file:
	// positive, and rising with the order the rows were committed in.
	for i, r := range rows {
		if r.Sequence() <= 0 || i > 0 && r.Sequence() <= rows[i-1].Sequence() {
			t.Errorf("row %d has sequence %d after %d", i, r.Sequence(), rows[max(i-1, 0)].Sequence())
		}
	}
	if o := rows[2].(sinks.ExposureRow); !o.Override || o.EvaluationReason != "override" {
		t.Errorf("third row = %+v, want the override marked", o)
	}
}

func TestASinkReadsAnAliasWithAnAbsentPlatformAsAnEmptyString(t *testing.T) {
	f := startSinkRows(t)
	now := f.clock.Now()
	// An SDK that names no platform.
	body, err := json.Marshal(map[string]any{
		"schema_version": 1, "sent_at": now.Format(time.RFC3339Nano),
		"sdk": map[string]any{"name": "flagnetic-dart", "version": "1.0.0"}, "events": []any{aliasEvent(eventID(now, 90))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := batchResult(t, f.srv, f.server, body); res.Accepted != 1 {
		t.Fatalf("batch = %+v, want the alias accepted", res)
	}

	_, rows, _ := f.readAll(t, sinks.EventAlias, f.scope(t), 10)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the alias", rows)
	}
	want := sinks.AliasRow{
		EventID: eventID(now, 90), ProjectKey: f.p.key, EnvironmentKey: "production", FromUnitType: "anonymous",
		FromUnitID: "d-8f1c", ToUnitType: "user", ToUnitID: "u-1001", Platform: "", SDKName: "flagnetic-dart",
		SDKVersion: "1.0.0", OccurredAt: protocolTime, ReceivedAt: now, Seq: rows[0].Sequence(),
	}
	if rows[0] != want || want.Seq <= 0 {
		t.Errorf("alias = %+v, want %+v with a sequence number", rows[0], want)
	}
}

func TestASinkNeverReadsAnotherProjectsRows(t *testing.T) {
	mine, other := startSinkRows(t), startSinkRows(t)
	now := mine.clock.Now()
	mine.post(t, mine.exposure("mine-1", "true", now), mine.exposure("mine-2", "true", now))
	other.post(t, other.exposure("other-1", "true", now))
	mine.post(t, mine.exposure("mine-3", "true", now))

	batches, rows, _ := mine.readAll(t, sinks.EventExposure, mine.scope(t), 2)
	if got := unitsOf(rows); !slices.Equal(got, []string{"mine-1", "mine-2", "mine-3"}) || !slices.Equal(batches, []int{2, 1}) {
		t.Errorf("rows = %v in batches %v, want only this project's three, two to a batch", got, batches)
	}
	_, rows, _ = other.readAll(t, sinks.EventExposure, other.scope(t), 10)
	if got := unitsOf(rows); !slices.Equal(got, []string{"other-1"}) {
		t.Errorf("the other project's rows = %v, want its own", got)
	}
}

func TestASinksCursorPassesOtherProjectsRowsWithNoRowOfItsOwn(t *testing.T) {
	mine, other := startSinkRows(t), startSinkRows(t)
	now := mine.clock.Now()
	mine.post(t, mine.exposure("mine-1", "true", now))
	mine.srv.AwaitHorizon(t)
	first, err := mine.store.NextBatch(t.Context(), sinks.EventExposure, mine.scope(t), events.Cursor{}, 10, nil)
	if err != nil || first.Rows != 1 {
		t.Fatalf("first batch = %+v (%v), want this project's row", first, err)
	}
	other.post(t, other.exposure("other-1", "true", now), other.exposure("other-2", "true", now))
	mine.srv.AwaitHorizon(t)

	b, err := mine.store.NextBatch(t.Context(), sinks.EventExposure, mine.scope(t), first.To, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	passed, err := other.store.NextBatch(t.Context(), sinks.EventExposure, other.scope(t), events.Cursor{}, 10, nil)
	if err != nil || passed.Rows != 2 {
		t.Fatalf("the other project's batch = %+v (%v), want its two rows", passed, err)
	}
	if notBefore := b.To.XID > passed.To.XID || b.To.XID == passed.To.XID && b.To.Seq >= passed.To.Seq; b.Rows != 0 || !notBefore {
		t.Errorf("batch with no row of its own = %+v, want none read and the cursor past the other project's rows at %+v", b, passed.To)
	}
	page, err := mine.store.ReadBatch(t.Context(), sinks.EventExposure, mine.scope(t), b)
	if err != nil || len(page.Rows) != 0 || page.Skipped != 0 {
		t.Errorf("rows of the passing batch = %+v (%v), want none", page, err)
	}
	mine.post(t, mine.exposure("mine-2", "true", now))
	_, rows, _ := readStream(t, mine.srv, mine.store, sinks.EventExposure, mine.scope(t), b.To, 10)
	if got := unitsOf(rows); !slices.Equal(got, []string{"mine-2"}) {
		t.Errorf("rows past the passing batch = %v, want the next of this project", got)
	}
}

func TestASinkLimitedToAnEnvironmentReadsOnlyItsRowsAndLagsOnlyBehindThem(t *testing.T) {
	f := startSinkRows(t)
	start := f.clock.Now()
	f.postTo(t, "production", f.exposure("p-1", "true", start))
	f.clock.Advance(time.Minute)
	f.postTo(t, "staging", f.exposure("s-1", "true", f.clock.Now()))
	f.clock.Advance(time.Minute)
	f.postTo(t, "production", f.exposure("p-2", "true", f.clock.Now()))
	f.srv.AwaitHorizon(t)

	_, rows, _ := f.readAll(t, sinks.EventExposure, f.scope(t, "staging"), 10)
	if got := unitsOf(rows); !slices.Equal(got, []string{"s-1"}) {
		t.Errorf("staging rows = %v, want the one", got)
	}
	batches, rows, _ := f.readAll(t, sinks.EventExposure, f.scope(t), 10)
	if got := unitsOf(rows); !slices.Equal(got, []string{"p-1", "s-1", "p-2"}) || !slices.Equal(batches, []int{3}) {
		t.Errorf("whole project rows = %v in %v, want all three", got, batches)
	}
	// A batch bounded by size counts only the rows its filter admits: the
	// staging row does not take up a place of the production batch.
	b, err := f.store.NextBatch(t.Context(), sinks.EventExposure, f.scope(t, "production"), events.Cursor{}, 2, nil)
	if err != nil || b.Rows != 2 {
		t.Fatalf("production batch = %+v (%v), want its two rows", b, err)
	}
	page, err := f.store.ReadBatch(t.Context(), sinks.EventExposure, f.scope(t, "production"), b)
	if err != nil || !slices.Equal(unitsOf(page.Rows), []string{"p-1", "p-2"}) {
		t.Errorf("production rows = %v (%v), want p-1 and p-2", unitsOf(page.Rows), err)
	}

	// The oldest row not read is the oldest the filter admits.
	at, ok, err := f.store.OldestUnread(t.Context(), sinks.EventExposure, f.scope(t, "staging"), events.Cursor{})
	if err != nil || !ok || !at.Equal(start.Add(time.Minute)) {
		t.Errorf("oldest staging row = %v, %v (%v), want the one received a minute in", at, ok, err)
	}
	at, ok, err = f.store.OldestUnread(t.Context(), sinks.EventExposure, f.scope(t), events.Cursor{})
	if err != nil || !ok || !at.Equal(start) {
		t.Errorf("oldest row = %v, %v (%v), want the first of the project", at, ok, err)
	}
	if _, ok, err = f.store.OldestUnread(t.Context(), sinks.EventExposure, f.scope(t, "staging"), b.To); err != nil || ok {
		t.Errorf("oldest staging row past the production batch = %v (%v), want none: the filter admits nothing more", ok, err)
	}
	other := startSinkRows(t)
	if _, ok, err = f.store.OldestUnread(t.Context(), sinks.EventExposure, other.scope(t), events.Cursor{}); err != nil || ok {
		t.Errorf("oldest row of a project with none = %v (%v), want none", ok, err)
	}
}

func TestARowOfADeletedEnvironmentIsPassedAndCounted(t *testing.T) {
	f := startSinkRows(t)
	now := f.clock.Now()
	f.postTo(t, "staging", f.exposure("s-1", "true", now), f.exposure("s-2", "true", now))
	f.postTo(t, "production", f.exposure("p-1", "true", now))
	staging := f.scope(t, "staging")
	got := do(f.p.admin.GetEnvironmentWithResponse(t.Context(), f.p.key, "staging"))
	etag := got.HTTPResponse.Header.Get("ETag")
	if res := do(f.p.admin.DeleteEnvironmentWithResponse(t.Context(), f.p.key, "staging", &management.DeleteEnvironmentParams{IfMatch: &etag})); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE …/environments/staging = %d: %s", res.StatusCode(), res.Body)
	}

	batches, rows, skipped := f.readAll(t, sinks.EventExposure, sinks.Scope{Project: staging.Project}, 10)
	if got := unitsOf(rows); !slices.Equal(got, []string{"p-1"}) || skipped != 2 || !slices.Equal(batches, []int{3}) {
		t.Errorf("rows = %v, %d skipped in %v, want p-1 and the two of the deleted environment skipped, the batch still passing all three", got, skipped, batches)
	}
	// A sink limited to the deleted environment passes its rows the same way.
	batches, rows, skipped = f.readAll(t, sinks.EventExposure, staging, 10)
	if len(rows) != 0 || skipped != 2 || !slices.Equal(batches, []int{2}) {
		t.Errorf("staging rows = %v, %d skipped in %v, want none exported and the two skipped", rows, skipped, batches)
	}
}

func TestAnIngestTransactionHeldOpenIsNeverSkipped(t *testing.T) {
	f := startSinkRows(t)
	now := f.clock.Now()
	scope := f.scope(t)
	written, release := f.srv.HoldNextIngest(t)
	held := make(chan answer, 1)
	body := f.envelope(t, f.exposure("held", "true", now))
	go func() { held <- postEvents(t, f.srv, f.server, body) }()
	<-written
	// A batch committed after the held one, while it is still open.
	f.post(t, f.exposure("after", "true", now))
	f.srv.AwaitHorizon(t)

	b, err := f.store.NextBatch(t.Context(), sinks.EventExposure, scope, events.Cursor{}, 10, nil)
	if err != nil || b.Rows != 0 {
		t.Fatalf("batch while the transaction is open = %+v (%v), want none: nothing past it is read", b, err)
	}
	// Lag counts the rows the horizon holds back: the held row is not
	// committed yet, but the one committed after it is.
	if _, ok, err := f.store.OldestUnread(t.Context(), sinks.EventExposure, scope, events.Cursor{}); err != nil || !ok {
		t.Errorf("oldest unread while held = %v (%v), want the row committed after the held one", ok, err)
	}

	release()
	if a := <-held; a.status != http.StatusAccepted {
		t.Fatalf("the held batch = %d: %s", a.status, a.body)
	}
	// Read on from where the empty batch ended, as a stream would.
	_, rows, _ := readStream(t, f.srv, f.store, sinks.EventExposure, scope, b.To, 1)
	if got := unitsOf(rows); !slices.Equal(got, []string{"held", "after"}) {
		t.Errorf("rows after the commit = %v, want the held one first and none skipped", got)
	}
}

func TestEverySummaryRowIsExportedWithItsSourceAndRemoteEvaluationsGetStableReportIDs(t *testing.T) {
	f := startSummaries(t)
	store := pgsinks.NewStore(testkit.Pool(t))
	f.post(t, f.summary(f.clock.Now(), counted("checkout", "true", "rule", 4), counted("banner", "", "off", 2)))
	evaluationWith(t, f.srv, f.server, `{"context":{"units":{"user":"u-1"}}}`, "User-Agent", "flagnetic-go/1.4.0 (linux)")
	if err := f.srv.Tick(t, worker.RemoteEvaluationCountsName); err != nil {
		t.Fatal(err)
	}
	scope := sinks.Scope{Project: f.p.projectID(t)}

	_, first, _ := readStream(t, f.srv, store, sinks.EventEvaluationSummary, scope, events.Cursor{}, 2)
	_, again, _ := readStream(t, f.srv, store, sinks.EventEvaluationSummary, scope, events.Cursor{}, 10)
	if len(first) != 4 || len(again) != 4 {
		t.Fatalf("rows = %d then %d, want the two of the SDK's summary and the two flags the server answered", len(first), len(again))
	}
	if !slices.Equal(sinks.RowIDs(first), sinks.RowIDs(again)) {
		t.Errorf("report IDs = %v then %v, want the same however the batches fall", sinks.RowIDs(first), sinks.RowIDs(again))
	}
	var remote []string
	for _, r := range first {
		m := r.(sinks.SummaryRow)
		switch m.Source {
		case "sdk":
			if m.ReportID == "" || strings.Count(m.ReportID, "-") != 4 || m.ProjectKey != f.p.key || m.EnvironmentKey != "production" {
				t.Errorf("sdk row = %+v", m)
			}
			if m.FlagKey == "banner" && (m.VariationKey != "" || m.EvaluationReason != "off" || m.Count != 2) {
				t.Errorf("banner row = %+v, want no variation, off and 2", m)
			}
			if m.FlagKey == "checkout" && (m.VariationKey != "true" || m.EvaluationReason != "rule" || m.Count != 4 || m.SDKName != "flagnetic-go" ||
				m.AppVersion != "" || !m.WindowEnd.Equal(protocolTime) || !m.WindowStart.Equal(protocolTime.Add(-time.Minute))) {
				t.Errorf("checkout row = %+v", m)
			}
		case "remote_evaluation":
			if len(m.ReportID) != 36 || m.ReportID[14] != '5' {
				t.Errorf("remote row report ID = %q, want a version 5 UUID", m.ReportID)
			}
			remote = append(remote, m.ReportID)
		default:
			t.Errorf("row source = %q", m.Source)
		}
	}
	if len(remote) != 2 || remote[0] == remote[1] {
		t.Errorf("remote report IDs = %v, want two distinct ones", remote)
	}
}

func TestAReadThatCannotBeDoneIsAnErrorAndNeverAnEmptyBatch(t *testing.T) {
	f := startSinkRows(t)
	scope := f.scope(t)
	b := sinks.Batch{To: events.Cursor{XID: 1, Seq: 1}}
	if _, err := f.store.NextBatch(t.Context(), "click", scope, events.Cursor{}, 1, nil); err == nil {
		t.Error("a batch of an event kind that has no raw table = nil error, want one")
	}
	if _, _, err := f.store.OldestUnread(t.Context(), "click", scope, events.Cursor{}); err == nil {
		t.Error("the oldest unread row of an event kind that has no raw table = nil error, want one")
	}
	if _, err := f.store.ReadBatch(t.Context(), "click", scope, b); err == nil {
		t.Error("rows of an event kind that has no raw table = nil error, want one")
	}

	// The database cannot be reached: each read says so.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, kind := range sinks.EventKinds {
		if _, err := f.store.NextBatch(ctx, kind, scope, events.Cursor{}, 1, nil); err == nil {
			t.Errorf("a %s batch with the context ended = nil error, want one", kind)
		}
		if _, _, err := f.store.OldestUnread(ctx, kind, scope, events.Cursor{}); err == nil {
			t.Errorf("the oldest unread %s with the context ended = nil error, want one", kind)
		}
		if _, err := f.store.ReadBatch(ctx, kind, scope, b); err == nil {
			t.Errorf("%s rows with the context ended = nil error, want one", kind)
		}
	}
}
