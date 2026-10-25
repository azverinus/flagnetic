package events

import (
	"context"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// AliasRollupStore is what the alias rollup reads and writes.
type AliasRollupStore interface {
	CursorStore
	targetLocker
	// AliasesAfter returns the next aliases past from, at most limit, of
	// transactions that had all ended, and the cursor of the last; from
	// when there is none.
	AliasesAfter(ctx context.Context, from events.Cursor, limit int) ([]events.AliasRow, events.Cursor, error)
	AddEventCounts(ctx context.Context, counts map[events.EventDay]int64) error
}

// RollUpAliases counts the stored aliases into daily event counts. Their
// links are written as they arrive; counting them as they arrive too would
// make every batch of a project's aliases wait on the one row of its day.
type RollUpAliases struct {
	tx    Transactor
	store AliasRollupStore
	clock clock.Clock
}

// NewRollUpAliases returns the handler.
func NewRollUpAliases(tx Transactor, store AliasRollupStore, c clock.Clock) *RollUpAliases {
	return &RollUpAliases{tx: tx, store: store, clock: c}
}

// Handle counts every alias committed below the horizon that the stream has
// not read yet, in batches, each in one unit of work with the move of the
// cursor past it. It returns how many aliases it read.
func (h *RollUpAliases) Handle(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := h.batch(ctx)
		total += n
		if err != nil || n < RollupBatchSize {
			return total, err
		}
	}
}

func (h *RollUpAliases) batch(ctx context.Context) (int, error) {
	n := 0
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		if err := h.store.LockStream(ctx, events.StreamAliases); err != nil {
			return err
		}
		from, err := h.store.Cursor(ctx, events.StreamAliases)
		if err != nil {
			return err
		}
		rows, to, err := h.store.AliasesAfter(ctx, from, RollupBatchSize)
		if err != nil || len(rows) == 0 {
			return err
		}
		n = len(rows)
		if rows, err = h.live(ctx, rows); err != nil {
			return err
		}
		if err = h.store.AddEventCounts(ctx, events.CountAliasEvents(rows)); err != nil {
			return err
		}
		return h.store.MoveCursor(ctx, events.StreamAliases, to, h.clock.Now())
	})
	return n, err
}

// live returns the aliases whose project still exists, holding it until the
// unit of work ends: nothing reads the counts of a project deleted since.
func (h *RollUpAliases) live(ctx context.Context, rows []events.AliasRow) ([]events.AliasRow, error) {
	var named RollupTargets
	for _, r := range rows {
		if !slices.Contains(named.Projects, r.Project) {
			named.Projects = append(named.Projects, r.Project)
		}
	}
	found, err := h.store.LockTargets(ctx, named)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rows, func(r events.AliasRow) bool { return !slices.Contains(found.Projects, r.Project) }), nil
}

// Lag returns how long the oldest alias the rollup has not read yet has been
// waiting, 0 when it has read every committed one.
func (h *RollUpAliases) Lag(ctx context.Context) (time.Duration, error) {
	at, ok, err := h.store.OldestUnread(ctx, events.StreamAliases)
	if err != nil || !ok {
		return 0, err
	}
	return max(h.clock.Now().Sub(at), 0), nil
}
