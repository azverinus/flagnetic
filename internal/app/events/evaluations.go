package events

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// EvaluationReader reads what the summary rollup made of a flag's
// evaluations.
type EvaluationReader interface {
	// FlagOf returns project's flag keyed key, archived or not, or
	// ErrNotFound.
	FlagOf(ctx context.Context, project workspace.ProjectID, key string) (flags.FlagID, error)
	// EnvironmentOf returns project's environment keyed key, or
	// ErrNotFound.
	EnvironmentOf(ctx context.Context, project workspace.ProjectID, key string) (workspace.EnvironmentID, error)
	// EvaluationCounts returns the flag's evaluations in environment per
	// day from first to last, both included, by day, source, variation and
	// reason.
	EvaluationCounts(ctx context.Context, flag flags.FlagID, environment workspace.EnvironmentID,
		first, last time.Time) ([]events.FlagEvaluationDay, error)
}

var errFlagNotFound = apperrors.NotFound("not_found", "the project has no flag with this key")

// EvaluationQuery names a flag's evaluations in one environment over a range
// of UTC days, nil leaving an end to its default.
type EvaluationQuery struct {
	Project     workspace.ProjectID
	Flag        string
	Environment string
	From, To    *time.Time
}

// GetFlagEvaluations reads a flag's evaluations per day in one environment.
type GetFlagEvaluations struct {
	snapshots Snapshotter
	store     EvaluationReader
	clock     clock.Clock
}

// NewGetFlagEvaluations returns the handler.
func NewGetFlagEvaluations(snapshots Snapshotter, store EvaluationReader, c clock.Clock) *GetFlagEvaluations {
	return &GetFlagEvaluations{snapshots: snapshots, store: store, clock: c}
}

// Handle returns the evaluations q names, or not_found for a flag or an
// environment the project does not have; a range of days that cannot be
// read is an *events.RangeError.
func (h *GetFlagEvaluations) Handle(ctx context.Context, q EvaluationQuery) ([]events.FlagEvaluationDay, error) {
	first, last, rangeErr := events.DaysRead(q.From, q.To, h.clock.Now())
	if rangeErr != nil {
		return nil, rangeErr
	}
	var out []events.FlagEvaluationDay
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		flag, err := h.store.FlagOf(ctx, q.Project, q.Flag)
		if errors.Is(err, ErrNotFound) {
			err = errFlagNotFound
		}
		if err != nil {
			return err
		}
		environment, err := h.store.EnvironmentOf(ctx, q.Project, q.Environment)
		if errors.Is(err, ErrNotFound) {
			err = errEnvironmentNotFound
		}
		if err != nil {
			return err
		}
		out, err = h.store.EvaluationCounts(ctx, flag, environment, first, last)
		return err
	})
	return out, err
}
