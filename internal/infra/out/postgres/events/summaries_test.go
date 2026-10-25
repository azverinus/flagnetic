package events_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// A summary is one event received however its rows fall into batches: only
// its first row is marked first, even when a batch ends among its rows.
func TestASummaryIsFirstOnceWhereverABatchEnds(t *testing.T) {
	store := pgevents.NewStore(testkit.Pool(t))
	ids := id.NewSource(clock.System{})
	now := time.Now().UTC().Truncate(time.Microsecond)
	key := delivery.SDKKey{ID: id.New[delivery.SDKKey](ids), Project: id.New[workspace.Project](ids),
		Environment: id.New[workspace.Environment](ids)}

	// Everything stored before is read first, so the test starts past it.
	from := drain(t, store, events.Cursor{})
	record := func(event delivery.EventID, source delivery.SummarySource, flag string) delivery.SummaryRecord {
		return delivery.SummaryRecord{
			Source: source, EventID: event, Key: key, SDK: delivery.SDKIdentity{Name: "flagnetic-go", Version: "1.0.0"},
			RulesetVersion: 1, PeriodStart: now.Add(-time.Minute), PeriodEnd: now,
			Row: delivery.SummaryRow{Flag: flag, Variation: "on", Reason: "rule", Count: 1}, ReceivedAt: now,
		}
	}
	one, two := id.New[delivery.Event](ids), id.New[delivery.Event](ids)
	if err := store.SaveSummaries(t.Context(), []delivery.SummaryRecord{
		record(one, delivery.SourceSDK, "a"), record(one, delivery.SourceSDK, "b"), record(one, delivery.SourceSDK, "c"),
		record(two, delivery.SourceSDK, "a"), record(delivery.EventID{}, delivery.SourceRemoteEvaluation, "a"),
	}); err != nil {
		t.Fatal(err)
	}

	var first []bool
	for len(first) < 5 {
		rows, to := summariesAfter(t, store, from, 2)
		for _, r := range rows {
			if r.Project == key.Project {
				first = append(first, r.First)
			}
		}
		from = to
	}
	if want := []bool{true, false, false, true, false}; !slices.Equal(first, want) {
		t.Errorf("first = %v, want %v", first, want)
	}
}

// summariesAfter waits for rows past from to pass the horizon, which other
// tests' transactions may hold back, and returns at most limit of them.
func summariesAfter(t *testing.T, store *pgevents.Store, from events.Cursor, limit int) ([]events.SummaryRow, events.Cursor) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rows, to, err := store.SummariesAfter(t.Context(), from, limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			return rows, to
		}
		if time.Now().After(deadline) {
			t.Fatal("no summary rows passed the horizon within 30 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// drain returns the cursor past every summary row below the horizon.
func drain(t *testing.T, store *pgevents.Store, from events.Cursor) events.Cursor {
	t.Helper()
	for {
		rows, to, err := store.SummariesAfter(t.Context(), from, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			return to
		}
		from = to
	}
}
