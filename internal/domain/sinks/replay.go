package sinks

import (
	"fmt"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

var (
	errInvalidReplayRange   = apperrors.Invalid("invalid_replay_range", "to must not be before from")
	errReplayOutOfRetention = apperrors.Invalid("out_of_retention",
		"from is earlier than the oldest event still stored")
)

// CheckReplay refuses a replay whose end comes before its start, then one that
// starts further back than events are kept: what is older is gone or about to
// be. A replay of a single instant, to equal to from, is allowed.
func CheckReplay(from time.Time, to *time.Time, now time.Time, retention time.Duration) error {
	if to != nil && to.Before(from) {
		return errInvalidReplayRange
	}
	if OutOfRetention(from, now, retention) {
		return errReplayOutOfRetention
	}
	return nil
}

// SelectKinds returns the event kinds an operation on a sink applies to:
// those requested, once each, which the sink must export, or all of the ones
// it exports when none are requested.
func SelectKinds(requested, exported []EventKind) ([]EventKind, error) {
	if len(requested) == 0 {
		return slices.Clone(exported), nil
	}
	var violations []apperrors.Violation
	for i, k := range requested {
		if !slices.Contains(exported, k) {
			violations = append(violations, apperrors.Violation{
				Pointer: fmt.Sprintf("/event_kinds/%d", i), Code: "invalid_event_kind",
				Detail: "the sink does not export this event kind",
			})
		}
	}
	if len(violations) > 0 {
		return nil, apperrors.Invalid("invalid_sink", "the sink is not valid", violations...)
	}
	return distinct(requested), nil
}

// cursorBefore reports whether a is an earlier position than b. Positions
// order by transaction number, a number: as text "10" would come before "9".
func cursorBefore(a, b events.Cursor) bool {
	return a.XID < b.XID || (a.XID == b.XID && a.Seq < b.Seq)
}

// Live is where the stream delivers what happens now: its cursor, or, while a
// bounded replay runs, the position the replay returns to, which the cursor
// has left behind.
func (s Stream) Live() events.Cursor {
	if s.Resume != nil {
		return *s.Resume
	}
	return s.Cursor
}

// ReplayAsk is a replay as resolved against one stream.
type ReplayAsk struct {
	// Live is the stream's live position.
	Live events.Cursor
	// Start is the position just before the first row received at or after
	// the replay's start, and StartFound is false when there is none.
	Start      events.Cursor
	StartFound bool
	// Bounded is set when the replay has an end; Stop is the last row
	// received at or before it, and StopFound is false when there is none.
	Bounded   bool
	Stop      events.Cursor
	StopFound bool
	// From is the time the replay starts at, and Floor the stream's floor.
	From  time.Time
	Floor *time.Time
}

// ReplayPlan is where a replay puts a stream.
type ReplayPlan struct {
	// Cursor is the position the stream reads on from.
	Cursor events.Cursor
	// Stop is the last row a bounded replay sends, and Resume the position
	// the stream returns to after it: its live one. Both are nil for a
	// replay that runs on into the live stream.
	Stop, Resume *events.Cursor
	// Floor is the stream's floor once the replay is set: a replay running on
	// into the live stream lowers it to the replay's start when that is
	// earlier, since every row received since is to be sent again; a bounded
	// one leaves it, since it reads its window without it and the live
	// stream it returns to keeps it.
	Floor *time.Time
}

// PlanReplay decides what a replay does to a stream, and false when it does
// nothing: no row was received at or after its start, its end resolves to no
// row or to one before the first row it would send, or it starts where the
// live stream already is or ahead of it, which the stream delivers anyway and
// must not be skipped to. A replay ending at or past the live position is
// one that runs on into it, since returning to the live position would send
// the rows between again.
func PlanReplay(a ReplayAsk) (ReplayPlan, bool) {
	if !a.StartFound || !cursorBefore(a.Start, a.Live) {
		return ReplayPlan{}, false
	}
	if !a.Bounded {
		return ReplayPlan{Cursor: a.Start, Floor: lowered(a.Floor, a.From)}, true
	}
	if !a.StopFound || !cursorBefore(a.Start, a.Stop) {
		return ReplayPlan{}, false
	}
	if !cursorBefore(a.Stop, a.Live) {
		return ReplayPlan{Cursor: a.Start, Floor: lowered(a.Floor, a.From)}, true
	}
	stop, resume := a.Stop, a.Live
	return ReplayPlan{Cursor: a.Start, Stop: &stop, Resume: &resume, Floor: a.Floor}, true
}

// lowered is floor lowered to from when from is earlier; nil, no floor, stays
// nil.
func lowered(floor *time.Time, from time.Time) *time.Time {
	if floor == nil || !from.Before(*floor) {
		return floor
	}
	return &from
}
