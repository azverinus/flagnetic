package events

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ClaimPurgeBatch is the most event IDs purged in one statement, so that a
// purge far behind never holds locks long enough to stall ingest.
const ClaimPurgeBatch = 10000

// claimWindowMargin is how much longer than the raw retention an event ID is
// kept at least: a retry arriving in the last hours of the window still finds
// its ID, whatever the time of day the purge runs.
const claimWindowMargin = 24 * time.Hour

// claimMinimumRetention is how long an event ID is kept whatever the raw
// retention. The window check reads the time in event_id plus the batch's
// skew, so a verbatim replay of an old envelope (whose sent_at is as old as
// its IDs) always lands inside the window; only the recorded ID refuses it, so
// the IDs outlive the raw events, and a window shorter than a year does not
// shorten them.
const claimMinimumRetention = 365 * 24 * time.Hour

// claimRetention is how long the event IDs received are kept when raw events
// are kept for retention: a year, or the window plus a day when that is
// longer.
func claimRetention(retention time.Duration) time.Duration {
	return max(claimMinimumRetention, retention+claimWindowMargin)
}

// RetentionStore is what retention reads, drops and purges.
type RetentionStore interface {
	CursorStore
	// CreateEventPartitions creates the partitions of every raw event
	// table for the UTC day of from and the ahead days after it, those
	// missing, outside any unit of work ctx carries.
	CreateEventPartitions(ctx context.Context, from time.Time, ahead int) error
	// LockRetention holds the retention of typ's raw table for the unit of
	// work, waiting for the replica holding it, so one replica at a time
	// drops its partitions.
	LockRetention(ctx context.Context, typ events.EventType) error
	// Partitions returns the UTC days of the partitions of typ's raw table.
	Partitions(ctx context.Context, typ events.EventType) ([]time.Time, error)
	// LockPartitions takes typ's raw table and the partitions of days
	// exclusively until the unit of work ends, giving up after a short
	// timeout rather than stalling ingest behind a transaction still
	// writing them.
	LockPartitions(ctx context.Context, typ events.EventType, days []time.Time) error
	// Lost returns what the partitions of days of typ's raw table hold past
	// from.
	Lost(ctx context.Context, typ events.EventType, days []time.Time, from events.Cursor) (events.Lost, error)
	// FirstStoredAfter returns the first row of typ's raw table past from
	// outside the partitions of days, nil when there is none.
	FirstStoredAfter(ctx context.Context, typ events.EventType, days []time.Time, from events.Cursor) (*events.Cursor, error)
	// Horizon returns the ID of the oldest transaction still running: every
	// row of a smaller one has committed or never will.
	Horizon(ctx context.Context) (uint64, error)
	SaveGap(ctx context.Context, gap events.Gap) error
	// DropPartitions drops the partitions of days of typ's raw table.
	DropPartitions(ctx context.Context, typ events.EventType, days []time.Time) error
	// PurgeClaims deletes at most limit event IDs of typ received before
	// before and returns how many it deleted.
	PurgeClaims(ctx context.Context, typ events.EventType, before time.Time, limit int) (int, error)
}

// RetentionReport is what a round of retention did.
type RetentionReport struct {
	// Dropped is how many partitions of each raw table were dropped.
	Dropped map[events.EventType]int
	// Gaps are the gaps the drops recorded.
	Gaps []events.Gap
}

// RunRetention keeps the raw event tables bounded: it creates their
// partitions ahead of time, drops the partitions past retention whole, and
// purges the event IDs a year (or the window and a day, if longer) old.
type RunRetention struct {
	tx        Transactor
	store     RetentionStore
	clock     clock.Clock
	ids       *id.Source
	retention time.Duration
	ahead     int
}

// NewRunRetention returns the handler, keeping raw events for retention and
// creating partitions ahead days after today.
func NewRunRetention(tx Transactor, store RetentionStore, c clock.Clock, ids *id.Source, retention time.Duration, ahead int,
) *RunRetention {
	return &RunRetention{tx: tx, store: store, clock: c, ids: ids, retention: retention, ahead: ahead}
}

// Handle runs one round. Each raw table is dropped in a unit of work of its
// own, so a table a long transaction still writes leaves the others to go;
// the round goes on past a table that failed and returns every error.
func (h *RunRetention) Handle(ctx context.Context) (RetentionReport, error) {
	now := h.clock.Now()
	report := RetentionReport{Dropped: map[events.EventType]int{}}
	var errs []error
	if err := h.store.CreateEventPartitions(ctx, now, h.ahead); err != nil {
		errs = append(errs, err)
	}
	for _, typ := range events.RawEventTypes {
		dropped, gaps, err := h.drop(ctx, typ, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if dropped > 0 {
			report.Dropped[typ] = dropped
		}
		report.Gaps = append(report.Gaps, gaps...)
	}
	for _, typ := range events.RawEventTypes {
		if err := h.purgeClaims(ctx, typ, now.Add(-claimRetention(h.retention))); err != nil {
			errs = append(errs, err)
		}
	}
	return report, errors.Join(errs...)
}

// drop drops the partitions of typ's raw table past retention, in one unit
// of work with the gaps it leaves: before the drop, the rows it takes can
// still be read, and after it nothing tells a row lost from a row never
// stored. The streams reading the table are held first, in the order every
// rollup takes them, then the table, so no rollup reads a partition as it
// goes and none waits on the drop while holding what the drop waits for.
func (h *RunRetention) drop(ctx context.Context, typ events.EventType, now time.Time) (int, []events.Gap, error) {
	var (
		dropped int
		gaps    []events.Gap
	)
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		dropped, gaps = 0, nil
		if err := h.store.LockRetention(ctx, typ); err != nil {
			return err
		}
		all, err := h.store.Partitions(ctx, typ)
		if err != nil {
			return err
		}
		due := slices.DeleteFunc(all, func(day time.Time) bool { return !events.PartitionDue(day, now, h.retention) })
		if len(due) == 0 {
			return nil
		}
		streams := events.StreamsReading(typ)
		for _, s := range streams {
			if err = h.store.LockStream(ctx, s); err != nil {
				return err
			}
		}
		if err = h.store.LockPartitions(ctx, typ, due); err != nil {
			return err
		}
		if len(streams) > 0 {
			var horizon uint64
			if horizon, err = h.store.Horizon(ctx); err != nil {
				return err
			}
			for _, s := range streams {
				gap, ok, gapErr := h.gap(ctx, s, typ, due, horizon, now)
				if gapErr != nil {
					return gapErr
				}
				if ok {
					gaps = append(gaps, gap)
				}
			}
		}
		if err = h.store.DropPartitions(ctx, typ, due); err != nil {
			return err
		}
		dropped = len(due)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return dropped, gaps, nil
}

// gap moves stream past the rows of days it had not read, recording the gap,
// if there are any.
func (h *RunRetention) gap(ctx context.Context, stream events.Stream, typ events.EventType, days []time.Time, horizon uint64,
	now time.Time,
) (events.Gap, bool, error) {
	from, err := h.store.Cursor(ctx, stream)
	if err != nil {
		return events.Gap{}, false, err
	}
	lost, err := h.store.Lost(ctx, typ, days, from)
	if err != nil || lost.Rows == 0 {
		return events.Gap{}, false, err
	}
	next, err := h.store.FirstStoredAfter(ctx, typ, days, from)
	if err != nil {
		return events.Gap{}, false, err
	}
	gap, ok := events.GapOf(id.New[events.Gap](h.ids), stream, typ, from, lost, next, horizon, now)
	if !ok {
		return events.Gap{}, false, nil
	}
	if gap.Moved != from {
		if err = h.store.MoveCursor(ctx, stream, gap.Moved, now); err != nil {
			return events.Gap{}, false, err
		}
	}
	if err = h.store.SaveGap(ctx, gap); err != nil {
		return events.Gap{}, false, err
	}
	return gap, true, nil
}

// purgeClaims deletes the event IDs of typ received before before, a batch
// at a time until none is left.
func (h *RunRetention) purgeClaims(ctx context.Context, typ events.EventType, before time.Time) error {
	for {
		n, err := h.store.PurgeClaims(ctx, typ, before, ClaimPurgeBatch)
		if err != nil || n < ClaimPurgeBatch {
			return err
		}
	}
}
