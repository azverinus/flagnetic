package sdk_test

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// retention is the server's default raw event retention.
const retention = 30 * 24 * time.Hour

const gapRows = `flagnetic_event_gap_rows_total{stream="evaluation_summaries"}`

// startRetention returns a summary fixture once every rollup has read what
// the package's earlier tests left, so that the gaps a drop records are this
// test's rows only.
func startRetention(t *testing.T) *summaryFixture {
	t.Helper()
	f := startSummaries(t)
	f.srv.TickRollup(t, worker.ExposureRollupName)
	f.srv.TickRollup(t, worker.SummaryRollupName)
	f.srv.TickRollup(t, worker.AliasRollupName)
	return f
}

func (f *summaryFixture) retain(t *testing.T) {
	t.Helper()
	if err := f.srv.Tick(t, worker.EventRetentionName); err != nil {
		t.Fatalf("retention: %v", err)
	}
}

func (f *summaryFixture) storedSummaries(t *testing.T) int {
	t.Helper()
	rows, err := pgevents.NewStore(testkit.Pool(t)).EvaluationSummaries(t.Context(), f.p.environmentID(t, "production"))
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func (f *summaryFixture) storedAliases(t *testing.T) int {
	t.Helper()
	rows, err := pgevents.NewStore(testkit.Pool(t)).Aliases(t.Context(), f.p.environmentID(t, "production"))
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// gaps returns every gap recorded so far.
func gaps(t *testing.T) []events.Gap {
	t.Helper()
	all, err := pgevents.NewStore(testkit.Pool(t)).Gaps(t.Context(), time.Time{}, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// gapsSince returns the gaps recorded since before was read.
func gapsSince(t *testing.T, before []events.Gap) []events.Gap {
	t.Helper()
	return slices.DeleteFunc(gaps(t), func(g events.Gap) bool {
		return slices.ContainsFunc(before, func(b events.Gap) bool { return b.ID == g.ID })
	})
}

func TestRetentionDropsPartitionsPastRetentionAndKeepsTheAggregates(t *testing.T) {
	f := startRetention(t)
	end := f.clock.Now().Add(-time.Minute)
	f.post(t, f.summary(end, counted("checkout", "true", "rule", 10)), aliasEvent(eventID(f.clock.Now(), 900)))
	f.rollUp(t)
	evaluated := rows(f.evaluations(t, "checkout", &end, &end))
	before := gaps(t)

	// Still inside the window: nothing of the day goes.
	f.clock.Advance(retention - time.Hour)
	f.retain(t)
	if s, a := f.storedSummaries(t), f.storedAliases(t); s != 1 || a != 1 {
		t.Fatalf("inside retention: %d summary rows and %d aliases stored, want 1 and 1", s, a)
	}

	// The whole day past retention: its partitions are dropped, and what was
	// rolled up from them stays.
	f.clock.Advance(26 * time.Hour)
	f.retain(t)
	if s, a := f.storedSummaries(t), f.storedAliases(t); s != 0 || a != 0 {
		t.Errorf("past retention: %d summary rows and %d aliases stored, want both dropped", s, a)
	}
	if got := rows(f.evaluations(t, "checkout", &end, &end)); !slices.Equal(got, evaluated) {
		t.Errorf("evaluations after the drop = %v, want unchanged %v", got, evaluated)
	}
	if got := gapsSince(t, before); len(got) != 0 {
		t.Errorf("gaps = %+v, want none: the rollup had read every row dropped", got)
	}
}

func TestADropOfRowsARollupHadNotReadRecordsAGapAndMovesItsCursorPast(t *testing.T) {
	f := startRetention(t)
	day1 := f.clock.Now()
	f.post(t, f.summary(day1.Add(-time.Minute), counted("checkout", "true", "rule", 10), counted("banner", "true", "rule", 1)))
	f.clock.Advance(time.Hour)
	f.post(t, f.summary(f.clock.Now().Add(-time.Minute), counted("checkout", "false", "fallthrough", 4)))
	// Two days later, a row of a partition retention keeps, which the rollup
	// has not read either.
	f.clock.Advance(48 * time.Hour)
	kept := f.clock.Now().Add(-time.Minute)
	f.post(t, f.summary(kept, counted("checkout", "true", "rule", 7)))
	before := gaps(t)

	f.clock.Advance(retention - 24*time.Hour)
	f.retain(t)

	got := gapsSince(t, before)
	if len(got) != 1 {
		t.Fatalf("gaps = %+v, want one", got)
	}
	g := got[0]
	if g.Stream != events.StreamEvaluationSummaries || g.EventType != events.EventEvaluationSummary || g.Rows != 3 ||
		!g.PeriodStart.Equal(day1) || !g.PeriodEnd.Equal(day1.Add(time.Hour)) || !g.DroppedAt.Equal(f.clock.Now()) {
		t.Errorf("gap = %+v, want the 3 summary rows received from %s to an hour later, dropped now", g, day1)
	}
	if n := testkit.Metric(t, f.srv.Scrape(t), gapRows); n != 3 {
		t.Errorf("%s = %v, want 3", gapRows, n)
	}
	warnings := f.srv.Logs.Find("raw events dropped before a stream read them")
	if len(warnings) != 1 || warnings[0].Attrs["stream"] != "evaluation_summaries" || warnings[0].Attrs["rows"] != int64(3) {
		t.Errorf("warnings = %+v, want one naming the stream and the 3 rows", warnings)
	}

	// The rollup goes on from the first row still stored.
	today := f.clock.Now().Add(-time.Minute)
	f.post(t, f.summary(today, counted("checkout", "true", "rule", 2)))
	f.rollUp(t)
	from := day1.Add(-24 * time.Hour)
	want := []string{kept.Format(time.DateOnly) + " sdk true rule 7", today.Format(time.DateOnly) + " sdk true rule 2"}
	if got := rows(f.evaluations(t, "checkout", &from, &today)); !slices.Equal(got, want) {
		t.Errorf("evaluations = %v, want only %v", got, want)
	}
	if got := gapsSince(t, before); len(got) != 1 {
		t.Errorf("gaps after the rollup = %+v, want still the one", got)
	}
}

func TestRetentionWaitsForATransactionStillWritingAPartition(t *testing.T) {
	f := startRetention(t)
	f.post(t, aliasEvent(eventID(f.clock.Now(), 901)))
	written, release := f.srv.HoldNextIngest(t)
	done := make(chan sdk.EventBatchResult, 1)
	body := f.envelope(t, f.summary(f.clock.Now().Add(-time.Minute), counted("checkout", "true", "rule", 1)))
	go func() { done <- batchResult(t, f.srv, f.server, body) }()
	<-written

	// The drop gives up on the table the open transaction writes rather than
	// take the partition from under it.
	f.clock.Advance(retention + 24*time.Hour)
	if err := f.srv.Tick(t, worker.EventRetentionName); err == nil {
		t.Error("retention dropped a partition a transaction still writes")
	}
	// The tables the transaction does not write still drop in the same round.
	if n := f.storedAliases(t); n != 0 {
		t.Errorf("%d aliases stored after the round, want their partition dropped", n)
	}
	release()
	if res := <-done; res.Accepted != 1 {
		t.Fatalf("held batch = %+v", res)
	}
	if n := f.storedSummaries(t); n != 1 {
		t.Fatalf("%d summary rows once the transaction committed, want 1", n)
	}

	f.retain(t)
	if n := f.storedSummaries(t); n != 0 {
		t.Errorf("%d summary rows after the next round, want the partition dropped", n)
	}
}

func TestARetryIsADuplicateInsideTheWindowThenOutOfWindow(t *testing.T) {
	f := startRetention(t)
	ev := f.summary(f.clock.Now().Add(-time.Minute), counted("checkout", "true", "rule", 1))
	f.post(t, ev)
	retry := func(name string, wantDuplicate bool) {
		t.Helper()
		f.retain(t)
		res := batchResult(t, f.srv, f.server, f.envelope(t, ev))
		switch {
		case wantDuplicate && (res.Duplicates != 1 || res.Accepted != 0):
			t.Errorf("%s = %+v, want a duplicate", name, res)
		case !wantDuplicate && (len(res.Rejected) != 1 || res.Rejected[0].Code != string(events.RejectOutOfWindow)):
			t.Errorf("%s = %+v, want out_of_window", name, res)
		}
	}
	f.clock.Advance(24 * time.Hour)
	retry("across a day boundary", true)
	f.clock.Advance(retention - 24*time.Hour - 2*time.Minute)
	retry("on the last day of the window", true)
	if n := f.storedSummaries(t); n != 1 {
		t.Fatalf("%d summary rows after two retries, want 1", n)
	}
	f.clock.Advance(24 * time.Hour)
	retry("a day past the window", false)
}

func TestEventIDsOutliveTheWindowAndArePurgedAYearOn(t *testing.T) {
	f := startRetention(t)
	at := f.clock.Now()
	ev := f.summary(at.Add(-time.Minute), counted("checkout", "true", "rule", 1))
	envelope := f.envelope(t, ev)
	if res := batchResult(t, f.srv, f.server, envelope); res.Accepted != 1 {
		t.Fatalf("batch = %+v, want accepted", res)
	}
	eventID := id.MustParse[delivery.Event](ev["event_id"].(string))
	claimed := func() bool {
		t.Helper()
		// Claiming the ID again tells whether it is still recorded; the claim
		// itself is purged with it by the next round.
		got, err := pgevents.NewStore(testkit.Pool(t)).ClaimEvents(t.Context(), f.p.projectID(t), events.EventEvaluationSummary,
			[]delivery.EventID{eventID}, at)
		if err != nil {
			t.Fatal(err)
		}
		return !got[eventID]
	}

	f.clock.Advance(retention + 2*24*time.Hour)
	f.retain(t)
	if !claimed() {
		t.Error("the event ID was purged with the raw events, a window and a day on")
	}
	// A verbatim replay of the envelope lands on the window's edge once the
	// skew is applied, so only the event ID still recorded refuses it.
	if res := batchResult(t, f.srv, f.server, envelope); res.Duplicates != 1 || res.Accepted != 0 {
		t.Errorf("a replay past the window = %+v, want a duplicate", res)
	}
	f.clock.Advance(365*24*time.Hour - retention - 2*24*time.Hour - time.Hour)
	f.retain(t)
	if !claimed() {
		t.Error("the event ID was purged before a year")
	}
	f.clock.Advance(2 * time.Hour)
	f.retain(t)
	if claimed() {
		t.Error("the event ID was kept past a year")
	}
}

func TestAnEnvelopeSixtyDaysBehindIsKeptAndItsReplayIsADuplicateAfterTheWindow(t *testing.T) {
	f := startRetention(t)
	slow := f.clock.Now().Add(-60 * 24 * time.Hour)
	ev := f.summary(slow.Add(-time.Minute), counted("checkout", "true", "rule", 1))
	envelope, err := json.Marshal(map[string]any{
		"schema_version": 1, "sent_at": slow.Format(time.RFC3339Nano),
		"sdk": map[string]any{"name": "flagnetic-go", "version": "1.4.0", "platform": "linux"}, "events": []any{ev},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := batchResult(t, f.srv, f.server, envelope); res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("an envelope 60 days behind = %+v, want accepted", res)
	}

	f.clock.Advance(retention + 2*24*time.Hour)
	f.retain(t)
	if res := batchResult(t, f.srv, f.server, envelope); res.Duplicates != 1 || res.Accepted != 0 || len(res.Rejected) != 0 {
		t.Errorf("the same envelope after the raw window = %+v, want a duplicate", res)
	}
}

func TestTwoReplicasDropOnceAndRecordOneGap(t *testing.T) {
	f := startRetention(t)
	other := testkit.StartServer(t, testkit.WithClock(f.clock))
	f.post(t, f.summary(f.clock.Now().Add(-time.Minute), counted("checkout", "true", "rule", 1), counted("banner", "true", "rule", 1)))
	before := gaps(t)

	f.clock.Advance(retention + 24*time.Hour)
	var wg sync.WaitGroup
	for _, srv := range []*testkit.Server{f.srv, other} {
		wg.Go(func() {
			if err := srv.Tick(t, worker.EventRetentionName); err != nil {
				t.Errorf("retention: %v", err)
			}
		})
	}
	wg.Wait()

	if got := gapsSince(t, before); len(got) != 1 || got[0].Rows != 2 {
		t.Errorf("gaps = %+v, want one of 2 rows", got)
	}
	if n := testkit.Metric(t, f.srv.Scrape(t), gapRows) + testkit.Metric(t, other.Scrape(t), gapRows); n != 2 {
		t.Errorf("%s across replicas = %v, want 2", gapRows, n)
	}
	if n := f.storedSummaries(t); n != 0 {
		t.Errorf("%d summary rows, want the partition dropped", n)
	}
}
