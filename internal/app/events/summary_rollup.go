package events

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// SummaryRollupStore is what the summary rollup reads and writes.
type SummaryRollupStore interface {
	CursorStore
	targetLocker
	LastEvaluatedStore
	// SummariesAfter returns the next summary rows past from, at most
	// limit, of transactions that had all ended, and the cursor of the
	// last; from when there is none.
	SummariesAfter(ctx context.Context, from events.Cursor, limit int) ([]events.SummaryRow, events.Cursor, error)
	// AddEvaluationCounts adds counts to the evaluations per day, each
	// saturating.
	AddEvaluationCounts(ctx context.Context, counts map[events.EvaluationDay]int64) error
	AddEventCounts(ctx context.Context, counts map[events.EventDay]int64) error
}

// RollUpSummaries rolls the stored evaluation summaries up into evaluations
// per day, when each flag was last evaluated, and daily event counts.
type RollUpSummaries struct {
	tx    Transactor
	store SummaryRollupStore
	clock clock.Clock
}

// NewRollUpSummaries returns the handler.
func NewRollUpSummaries(tx Transactor, store SummaryRollupStore, c clock.Clock) *RollUpSummaries {
	return &RollUpSummaries{tx: tx, store: store, clock: c}
}

// Handle applies every summary row committed below the horizon that the
// stream has not read yet, in batches, each in one unit of work with the
// move of the cursor past it. It returns how many rows it read.
func (h *RollUpSummaries) Handle(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := h.batch(ctx)
		total += n
		if err != nil || n < RollupBatchSize {
			return total, err
		}
	}
}

func (h *RollUpSummaries) batch(ctx context.Context) (int, error) {
	n := 0
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		if err := h.store.LockStream(ctx, events.StreamEvaluationSummaries); err != nil {
			return err
		}
		from, err := h.store.Cursor(ctx, events.StreamEvaluationSummaries)
		if err != nil {
			return err
		}
		rows, to, err := h.store.SummariesAfter(ctx, from, RollupBatchSize)
		if err != nil || len(rows) == 0 {
			return err
		}
		n = len(rows)
		if rows, err = h.live(ctx, rows); err != nil {
			return err
		}
		out := events.RollUpSummaries(rows)
		if err = h.store.AddEvaluationCounts(ctx, out.Days); err != nil {
			return err
		}
		if err = h.store.MoveLastEvaluated(ctx, out.LastEvaluated); err != nil {
			return err
		}
		if err = h.store.AddEventCounts(ctx, out.Events); err != nil {
			return err
		}
		return h.store.MoveCursor(ctx, events.StreamEvaluationSummaries, to, h.clock.Now())
	})
	return n, err
}

// live returns the rows whose project still exists, holding it until the
// unit of work ends, with the flag left out of each row whose flag or
// environment is gone since: the project still received the summary, but
// nothing reads the evaluations of what it no longer has.
func (h *RollUpSummaries) live(ctx context.Context, rows []events.SummaryRow) ([]events.SummaryRow, error) {
	var named targetSet
	for _, r := range rows {
		named.add(r.Project, r.Environment, r.Flag, experiments.ExperimentID{})
	}
	found, err := lockTargets(ctx, h.store, named)
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		if !found.projects[r.Project] {
			continue
		}
		if !found.environments[r.Environment] || !found.flags[r.Flag] {
			r.Flag = flags.FlagID{}
		}
		out = append(out, r)
	}
	return out, nil
}

// Lag returns how long the oldest summary row the rollup has not read yet
// has been waiting, 0 when it has read every committed one.
func (h *RollUpSummaries) Lag(ctx context.Context) (time.Duration, error) {
	at, ok, err := h.store.OldestUnread(ctx, events.StreamEvaluationSummaries)
	if err != nil || !ok {
		return 0, err
	}
	return max(h.clock.Now().Sub(at), 0), nil
}
