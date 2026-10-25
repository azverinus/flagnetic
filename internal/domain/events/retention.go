package events

import (
	"math"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

// RawEventTypes are the event types stored raw, each in a table of its own
// partitioned by the UTC day the rows were received.
var RawEventTypes = []EventType{EventEvaluationSummary, EventAlias, EventExposure}

// StreamsReading returns the rollup streams reading the raw rows of typ.
// Aliases are linked as they arrive, so no stream reads them.
func StreamsReading(typ EventType) []Stream {
	switch typ {
	case EventExposure:
		return []Stream{StreamExposures}
	case EventEvaluationSummary:
		return []Stream{StreamEvaluationSummaries}
	default:
		return nil
	}
}

// PartitionDue reports whether the partition of the UTC day starting at day
// is past retention at now: every row it can hold was received more than
// retention ago. A day still partly inside the window is kept whole.
func PartitionDue(day, now time.Time, retention time.Duration) bool {
	return !day.AddDate(0, 0, 1).After(now.Add(-retention))
}

// Lost is what a drop of partitions takes from a stream: the rows past its
// cursor that it had not read.
type Lost struct {
	Rows        int64
	First, Last Cursor
	// PeriodStart and PeriodEnd are when the first and the last of the rows
	// lost were received.
	PeriodStart, PeriodEnd time.Time
}

// GapID identifies a gap.
type GapID = id.Of[Gap]

// Gap is the record of rows a stream lost because retention dropped their
// partition while its cursor still pointed before them.
type Gap struct {
	ID        GapID
	Stream    Stream
	EventType EventType
	// From is where the stream had read to, To the last row lost.
	From, To Cursor
	// Moved is where the drop moved the stream's cursor.
	Moved                  Cursor
	PeriodStart, PeriodEnd time.Time
	Rows                   int64
	DroppedAt              time.Time
}

// justBefore returns the cursor just before c: every row before c is at or
// before it, and c is past it.
func justBefore(c Cursor) Cursor { return Cursor{XID: c.XID, Seq: c.Seq - 1} }

func cursorLess(a, b Cursor) bool { return a.XID < b.XID || (a.XID == b.XID && a.Seq < b.Seq) }

// GapOf returns the gap gapID a drop leaves in stream, read up to from, taking lost
// from it, with next the first row still stored past from (nil for none) and
// horizon the oldest transaction still running; ok is false when the drop
// takes nothing the stream had not read. The stream's cursor moves to just
// before next, or past the last row lost when nothing is stored after it, so
// the stream goes on with what is left. It never moves past the horizon: a
// transaction running since before the rows lost may still commit rows the
// stream has to read. Nor does it move back.
func GapOf(gapID GapID, stream Stream, typ EventType, from Cursor, lost Lost, next *Cursor, horizon uint64, at time.Time) (Gap, bool) {
	if lost.Rows == 0 {
		return Gap{}, false
	}
	moved := lost.Last
	if next != nil {
		moved = justBefore(*next)
	}
	if moved.XID >= horizon {
		moved = Cursor{XID: horizon - 1, Seq: math.MaxInt64}
	}
	if cursorLess(moved, from) {
		moved = from
	}
	return Gap{
		ID: gapID, Stream: stream, EventType: typ, From: from, To: lost.Last, Moved: moved,
		PeriodStart: lost.PeriodStart, PeriodEnd: lost.PeriodEnd, Rows: lost.Rows, DroppedAt: at,
	}, true
}
