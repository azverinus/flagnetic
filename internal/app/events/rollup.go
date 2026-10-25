package events

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// RollupBatchSize is the most raw rows a rollup applies in one unit of work,
// so that a stream far behind catches up in transactions short enough not to
// hold the horizon back themselves.
const RollupBatchSize = 20000

// CursorStore keeps where each rollup stream has read to.
type CursorStore interface {
	// LockStream holds stream for the unit of work, waiting for the
	// replica holding it, so one replica at a time moves it.
	LockStream(ctx context.Context, stream events.Stream) error
	// Cursor returns where stream has read to, the zero cursor before it
	// first moved.
	Cursor(ctx context.Context, stream events.Stream) (events.Cursor, error)
	// MoveCursor moves stream's cursor to to, as of at.
	MoveCursor(ctx context.Context, stream events.Stream, to events.Cursor, at time.Time) error
	// OldestUnread returns when the first row past stream's cursor was
	// received, whether or not the horizon still holds it back; ok is
	// false when every committed row was read.
	OldestUnread(ctx context.Context, stream events.Stream) (at time.Time, ok bool, err error)
}

// RollupTargets are what a batch of raw rows names and the rollup's
// aggregates reference.
type RollupTargets struct {
	Projects     []workspace.ProjectID
	Environments []workspace.EnvironmentID
	Flags        []flags.FlagID
	Experiments  []experiments.ExperimentID
}

// LastEvaluatedStore keeps when SDKs last evaluated each flag.
type LastEvaluatedStore interface {
	// MoveLastEvaluated moves each flag's last evaluation in each
	// environment to the time given, unless it holds a later one.
	MoveLastEvaluated(ctx context.Context, last map[events.FlagEnvironment]time.Time) error
}

// ExposureRollupStore is what the exposure rollup reads and writes.
type ExposureRollupStore interface {
	CursorStore
	// ExposuresAfter returns the next exposures past from, at most limit,
	// of transactions that had all ended, and the cursor of the last;
	// from when there is none.
	ExposuresAfter(ctx context.Context, from events.Cursor, limit int) ([]events.ExposureRow, events.Cursor, error)
	targetLocker
	// FirstExposures returns the first exposures stored of units.
	FirstExposures(ctx context.Context, units []events.UnitKey) (map[events.UnitKey]events.FirstExposure, error)
	SaveFirstExposures(ctx context.Context, records map[events.UnitKey]events.FirstExposure) error
	AddExposureCounts(ctx context.Context, counts map[events.ExposureDay]events.ExposureDayCounts) error
	AddEventCounts(ctx context.Context, counts map[events.EventDay]int64) error
	LastEvaluatedStore
}

// RollUpExposures rolls the stored exposures up into first exposures, daily
// exposure counts and daily event counts.
type RollUpExposures struct {
	tx    Transactor
	store ExposureRollupStore
	clock clock.Clock
}

// NewRollUpExposures returns the handler.
func NewRollUpExposures(tx Transactor, store ExposureRollupStore, c clock.Clock) *RollUpExposures {
	return &RollUpExposures{tx: tx, store: store, clock: c}
}

// Handle applies every exposure committed below the horizon that the stream
// has not read yet, in batches, each in one unit of work with the move of
// the cursor past it: a batch is applied once, never half. It returns how
// many rows it read.
func (h *RollUpExposures) Handle(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := h.batch(ctx)
		total += n
		if err != nil || n < RollupBatchSize {
			return total, err
		}
	}
}

func (h *RollUpExposures) batch(ctx context.Context) (int, error) {
	n := 0
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		if err := h.store.LockStream(ctx, events.StreamExposures); err != nil {
			return err
		}
		from, err := h.store.Cursor(ctx, events.StreamExposures)
		if err != nil {
			return err
		}
		rows, to, err := h.store.ExposuresAfter(ctx, from, RollupBatchSize)
		if err != nil || len(rows) == 0 {
			return err
		}
		n = len(rows)
		rows, received, err := h.live(ctx, rows)
		if err != nil {
			return err
		}
		units := make([]events.UnitKey, 0, len(rows))
		seen := map[events.UnitKey]bool{}
		for _, r := range rows {
			if !r.Override && !seen[r.Unit] {
				seen[r.Unit] = true
				units = append(units, r.Unit)
			}
		}
		stored, err := h.store.FirstExposures(ctx, units)
		if err != nil {
			return err
		}
		out := events.RollUpExposures(stored, rows)
		// The event volume counts what the project received, whether or not
		// the experiment or environment the exposure names is gone since.
		out.Events = events.CountExposureEvents(received)
		if err = h.store.SaveFirstExposures(ctx, out.Records); err != nil {
			return err
		}
		if err = h.store.AddExposureCounts(ctx, out.Days); err != nil {
			return err
		}
		if err = h.store.AddEventCounts(ctx, out.Events); err != nil {
			return err
		}
		if err = h.store.MoveLastEvaluated(ctx, events.LastExposed(rows)); err != nil {
			return err
		}
		return h.store.MoveCursor(ctx, events.StreamExposures, to, h.clock.Now())
	})
	return n, err
}

// live returns the rows whose project, environment, experiment and flag still
// exist, and the rows whose project still exists, holding them until the
// unit of work ends: the raw rows of a deleted experiment stay until
// retention drops them, and nothing reads their aggregates any more, but the
// project still received them.
func (h *RollUpExposures) live(ctx context.Context, rows []events.ExposureRow) (live, received []events.ExposureRow, err error) {
	var named targetSet
	for _, r := range rows {
		named.add(r.Project, r.Unit.Environment, r.Flag, r.Unit.Experiment)
	}
	found, err := lockTargets(ctx, h.store, named)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		if !found.projects[r.Project] {
			continue
		}
		received = append(received, r)
		if found.environments[r.Unit.Environment] && found.experiments[r.Unit.Experiment] && found.flags[r.Flag] {
			live = append(live, r)
		}
	}
	return live, received, nil
}

// targetSet gathers what a batch of raw rows names, each once.
type targetSet struct {
	RollupTargets
	projects     map[workspace.ProjectID]bool
	environments map[workspace.EnvironmentID]bool
	flags        map[flags.FlagID]bool
	experiments  map[experiments.ExperimentID]bool
}

// add adds what one row names; a zero flag or experiment names none.
func (t *targetSet) add(p workspace.ProjectID, e workspace.EnvironmentID, f flags.FlagID, x experiments.ExperimentID) {
	if t.projects == nil {
		t.projects, t.environments = map[workspace.ProjectID]bool{}, map[workspace.EnvironmentID]bool{}
		t.flags, t.experiments = map[flags.FlagID]bool{}, map[experiments.ExperimentID]bool{}
	}
	if !t.projects[p] {
		t.projects[p] = true
		t.Projects = append(t.Projects, p)
	}
	if !t.environments[e] {
		t.environments[e] = true
		t.Environments = append(t.Environments, e)
	}
	if !f.IsZero() && !t.flags[f] {
		t.flags[f] = true
		t.Flags = append(t.Flags, f)
	}
	if !x.IsZero() && !t.experiments[x] {
		t.experiments[x] = true
		t.Experiments = append(t.Experiments, x)
	}
}

// targetLocker holds what a batch names against deletion.
type targetLocker interface {
	// LockTargets returns those of t that still exist, held against
	// deletion until the unit of work ends.
	LockTargets(ctx context.Context, t RollupTargets) (RollupTargets, error)
}

// lockTargets locks what named holds and returns those found.
func lockTargets(ctx context.Context, store targetLocker, named targetSet) (targetSet, error) {
	found, err := store.LockTargets(ctx, named.RollupTargets)
	if err != nil {
		return targetSet{}, err
	}
	out := targetSet{
		projects: map[workspace.ProjectID]bool{}, environments: map[workspace.EnvironmentID]bool{},
		flags: map[flags.FlagID]bool{}, experiments: map[experiments.ExperimentID]bool{},
	}
	for _, p := range found.Projects {
		out.projects[p] = true
	}
	for _, e := range found.Environments {
		out.environments[e] = true
	}
	for _, f := range found.Flags {
		out.flags[f] = true
	}
	for _, x := range found.Experiments {
		out.experiments[x] = true
	}
	return out, nil
}

// Lag returns how long the oldest exposure the rollup has not read yet has
// been waiting, 0 when it has read every committed one.
func (h *RollUpExposures) Lag(ctx context.Context) (time.Duration, error) {
	at, ok, err := h.store.OldestUnread(ctx, events.StreamExposures)
	if err != nil || !ok {
		return 0, err
	}
	return max(h.clock.Now().Sub(at), 0), nil
}

// ErrNotFound is what a store answers for an entity it does not hold.
var ErrNotFound = errors.New("events: not found")
