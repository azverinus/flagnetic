package events

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ConflictSampleSize is how many conflicting units the conflicts of an
// experiment list: enough to see a pattern, few enough to read.
const ConflictSampleSize = 100

// Snapshotter runs a query of several statements on one consistent view.
type Snapshotter interface {
	Snapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

// ExposureReader reads what the exposure rollup made of an experiment's
// exposures.
type ExposureReader interface {
	// ExperimentOf returns the identifier and weights of project's
	// experiment keyed key, of any status, or ErrNotFound.
	ExperimentOf(ctx context.Context, project workspace.ProjectID, key string) (experiments.ExperimentID, []experiments.Weight, error)
	// EnvironmentOf returns project's environment keyed key, or
	// ErrNotFound.
	EnvironmentOf(ctx context.Context, project workspace.ProjectID, key string) (workspace.EnvironmentID, error)
	// ExposureCounts returns the experiment's counts in environment summed
	// over every day, by variation.
	ExposureCounts(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID) (map[string]events.ExposureDayCounts, error)
	// ExposureTimeline returns the experiment's counts in environment per
	// day and variation, from the day of from to the day of to, nil
	// leaving that end open.
	ExposureTimeline(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID,
		from, to *time.Time) ([]events.ExposureTimelineDay, error)
	// ConflictTotals returns how many exposures conflicted with their
	// unit's first exposure, and in how many units.
	ConflictTotals(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID) (total, units int64, err error)
	// Conflicts returns up to limit conflicting units, the most recent
	// conflict first.
	Conflicts(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID, limit int) ([]events.Conflict, error)
	// RejectionCountsOf returns how many exposures of the experiment were
	// refused in environment, by reason.
	RejectionCountsOf(ctx context.Context, experiment experiments.ExperimentID, environment workspace.EnvironmentID) (map[events.RejectionCode]int64, error)
	// CursorMovedAt returns when stream's cursor last moved; ok is false
	// before it first did.
	CursorMovedAt(ctx context.Context, stream events.Stream) (at time.Time, ok bool, err error)
}

var (
	errExperimentNotFound  = apperrors.NotFound("not_found", "the project has no experiment with this key")
	errEnvironmentNotFound = apperrors.NotFound("not_found", "the project has no environment with this key")
)

// ExperimentExposures are what an experiment's exposures in one environment
// came to.
type ExperimentExposures struct {
	Variations []events.VariationExposures
	// ConflictTotal is how many exposures named another variation than
	// their unit's first exposure; ConflictUnits how many units had one.
	ConflictTotal, ConflictUnits int64
	// Rejections count the exposures refused, by reason.
	Rejections map[events.RejectionCode]int64
	// AsOf is when the exposure rollup last moved; nil before it first
	// did.
	AsOf *time.Time
}

// ExposureQuery names an experiment's exposures in one environment.
type ExposureQuery struct {
	Project     workspace.ProjectID
	Experiment  string
	Environment string
}

// GetExperimentExposures reads an experiment's exposures in one environment.
type GetExperimentExposures struct {
	snapshots Snapshotter
	store     ExposureReader
}

// NewGetExperimentExposures returns the handler.
func NewGetExperimentExposures(snapshots Snapshotter, store ExposureReader) *GetExperimentExposures {
	return &GetExperimentExposures{snapshots: snapshots, store: store}
}

// Handle returns the exposures of q, or not_found for an experiment or an
// environment the project does not have. Everything is read in one snapshot,
// so the counts and the conflicts agree.
func (h *GetExperimentExposures) Handle(ctx context.Context, q ExposureQuery) (ExperimentExposures, error) {
	var out ExperimentExposures
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		experiment, weights, environment, err := resolve(ctx, h.store, q)
		if err != nil {
			return err
		}
		counts, err := h.store.ExposureCounts(ctx, environment, experiment)
		if err != nil {
			return err
		}
		out.Variations = events.VariationExposuresOf(weights, counts)
		if out.ConflictTotal, out.ConflictUnits, err = h.store.ConflictTotals(ctx, environment, experiment); err != nil {
			return err
		}
		if out.Rejections, err = h.store.RejectionCountsOf(ctx, experiment, environment); err != nil {
			return err
		}
		at, ok, err := h.store.CursorMovedAt(ctx, events.StreamExposures)
		if ok {
			out.AsOf = &at
		}
		return err
	})
	return out, err
}

// resolve returns the experiment and the environment q names.
func resolve(ctx context.Context, store ExposureReader, q ExposureQuery,
) (experiments.ExperimentID, []experiments.Weight, workspace.EnvironmentID, error) {
	experiment, weights, err := store.ExperimentOf(ctx, q.Project, q.Experiment)
	if errors.Is(err, ErrNotFound) {
		err = errExperimentNotFound
	}
	if err != nil {
		return experiments.ExperimentID{}, nil, workspace.EnvironmentID{}, err
	}
	environment, err := store.EnvironmentOf(ctx, q.Project, q.Environment)
	if errors.Is(err, ErrNotFound) {
		err = errEnvironmentNotFound
	}
	return experiment, weights, environment, err
}

// GetExposureTimeline reads an experiment's exposure counts per day in one
// environment.
type GetExposureTimeline struct {
	snapshots Snapshotter
	store     ExposureReader
}

// NewGetExposureTimeline returns the handler.
func NewGetExposureTimeline(snapshots Snapshotter, store ExposureReader) *GetExposureTimeline {
	return &GetExposureTimeline{snapshots: snapshots, store: store}
}

// Handle returns the counts of q per UTC day and variation, by day then
// variation key, from the day of from to the day of to, both included; nil
// leaves that end open.
func (h *GetExposureTimeline) Handle(ctx context.Context, q ExposureQuery, from, to *time.Time) ([]events.ExposureTimelineDay, error) {
	var out []events.ExposureTimelineDay
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		experiment, _, environment, err := resolve(ctx, h.store, q)
		if err != nil {
			return err
		}
		out, err = h.store.ExposureTimeline(ctx, environment, experiment, from, to)
		return err
	})
	return out, err
}

// GetConflicts reads a sample of an experiment's conflicting units in one
// environment.
type GetConflicts struct {
	snapshots Snapshotter
	store     ExposureReader
}

// NewGetConflicts returns the handler.
func NewGetConflicts(snapshots Snapshotter, store ExposureReader) *GetConflicts {
	return &GetConflicts{snapshots: snapshots, store: store}
}

// Handle returns up to ConflictSampleSize conflicting units of q, the most
// recent conflict first.
func (h *GetConflicts) Handle(ctx context.Context, q ExposureQuery) ([]events.Conflict, error) {
	var out []events.Conflict
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		experiment, _, environment, err := resolve(ctx, h.store, q)
		if err != nil {
			return err
		}
		out, err = h.store.Conflicts(ctx, environment, experiment, ConflictSampleSize)
		return err
	})
	return out, err
}
