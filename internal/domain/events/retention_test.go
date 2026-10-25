package events_test

import (
	"math"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
)

func TestTheStreamsReadingEachRawTable(t *testing.T) {
	cases := map[events.EventType][]events.Stream{
		events.EventExposure:          {events.StreamExposures},
		events.EventEvaluationSummary: {events.StreamEvaluationSummaries},
		events.EventAlias:             {events.StreamAliases},
	}
	for typ, want := range cases {
		got := events.StreamsReading(typ)
		if len(got) != len(want) || (len(want) == 1 && got[0] != want[0]) {
			t.Errorf("streams reading %s = %v, want %v", typ, got, want)
		}
	}
}

func TestAPartitionIsDueOnceItsWholeDayIsPastRetention(t *testing.T) {
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		now  time.Time
		want bool
	}{
		{day.Add(48 * time.Hour), false},
		{day.Add(72*time.Hour - time.Nanosecond), false},
		{day.Add(72 * time.Hour), true},
	}
	for _, c := range cases {
		if got := events.PartitionDue(day, c.now, 48*time.Hour); got != c.want {
			t.Errorf("day %s at %s due = %v, want %v", day.Format(time.DateOnly), c.now, got, c.want)
		}
	}
}

func TestADropTakingNothingUnreadRecordsNoGap(t *testing.T) {
	_, ok := events.GapOf(events.GapID{}, events.StreamExposures, events.EventExposure, events.Cursor{XID: 10, Seq: 5}, events.Lost{},
		nil, 100, time.Now())
	if ok {
		t.Error("a gap recorded for a drop taking no unread row")
	}
}

func TestADropTakingUnreadRowsMovesTheCursorJustBeforeTheFirstRowStored(t *testing.T) {
	at := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	start := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	from := events.Cursor{XID: 10, Seq: 5}
	lost := events.Lost{
		Rows: 3, First: events.Cursor{XID: 11, Seq: 6}, Last: events.Cursor{XID: 12, Seq: 9},
		PeriodStart: start, PeriodEnd: start.Add(time.Hour),
	}
	next := events.Cursor{XID: 13, Seq: 10}
	gap, ok := events.GapOf(events.GapID{}, events.StreamExposures, events.EventExposure, from, lost, &next, 100, at)
	if !ok {
		t.Fatal("no gap for a drop taking unread rows")
	}
	want := events.Gap{
		Stream: events.StreamExposures, EventType: events.EventExposure, From: from, To: lost.Last,
		Moved: events.Cursor{XID: 13, Seq: 9}, PeriodStart: start, PeriodEnd: start.Add(time.Hour), Rows: 3, DroppedAt: at,
	}
	if gap != want {
		t.Errorf("gap = %+v, want %+v", gap, want)
	}
}

func TestWithNothingStoredAfterTheLostRowsTheCursorMovesPastTheLast(t *testing.T) {
	lost := events.Lost{Rows: 2, First: events.Cursor{XID: 11, Seq: 6}, Last: events.Cursor{XID: 12, Seq: 9}}
	gap, _ := events.GapOf(events.GapID{}, events.StreamExposures, events.EventExposure, events.Cursor{XID: 10, Seq: 5}, lost, nil, 100, time.Now())
	if gap.Moved != lost.Last {
		t.Errorf("cursor moved to %+v, want past the last lost row %+v", gap.Moved, lost.Last)
	}
}

// A transaction older than the lost rows may still be open and write rows a
// stream has not read: the cursor never moves past the horizon, so those
// rows are read once it commits.
func TestTheCursorNeverMovesPastTheHorizon(t *testing.T) {
	lost := events.Lost{Rows: 2, First: events.Cursor{XID: 11, Seq: 6}, Last: events.Cursor{XID: 20, Seq: 9}}
	gap, _ := events.GapOf(events.GapID{}, events.StreamExposures, events.EventExposure, events.Cursor{XID: 10, Seq: 5}, lost, nil, 15, time.Now())
	if want := (events.Cursor{XID: 14, Seq: math.MaxInt64}); gap.Moved != want {
		t.Errorf("cursor moved to %+v, want %+v, below the horizon", gap.Moved, want)
	}
}

func TestTheCursorNeverMovesBack(t *testing.T) {
	from := events.Cursor{XID: 14, Seq: 5}
	lost := events.Lost{Rows: 1, First: events.Cursor{XID: 16, Seq: 6}, Last: events.Cursor{XID: 16, Seq: 6}}
	gap, _ := events.GapOf(events.GapID{}, events.StreamExposures, events.EventExposure, from, lost, nil, 14, time.Now())
	if gap.Moved != from {
		t.Errorf("cursor moved to %+v, want it left at %+v", gap.Moved, from)
	}
}
