package sinks

import (
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
)

// GapID identifies a gap. A sink's gaps are kept beside the rollup streams'
// in one table, so the identifier is the events context's.
type GapID = events.GapID

// Gap is the rows of one event kind that retention dropped before the sink
// delivered them. The sink's stream went on after them, with the rows still
// stored; a member dismisses the gap once seen, which stops it counting and
// keeps it listed.
type Gap struct {
	ID        GapID
	Sink      ID
	EventKind EventKind
	// Rows is how many rows were lost that the sink's project and
	// environments admit.
	Rows int64
	// PeriodStart and PeriodEnd are when the first and the last of them were
	// received.
	PeriodStart, PeriodEnd time.Time
	DroppedAt              time.Time
	// DismissedAt is when a member dismissed the gap, nil while it counts.
	DismissedAt *time.Time
}

// GapStream names the stream of kind in the table sink gaps share with the
// rollups' and in the metric counting the rows lost: one per event kind, not
// per sink, so that the series do not grow with the sinks.
func GapStream(kind EventKind) events.Stream { return events.Stream("sink_" + string(kind)) }

// GapStreams are the names GapStream gives, one per event kind.
func GapStreams() []events.Stream {
	out := make([]events.Stream, len(EventKinds))
	for i, k := range EventKinds {
		out[i] = GapStream(k)
	}
	return out
}
