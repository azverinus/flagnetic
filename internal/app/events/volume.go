package events

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// VolumeReader reads what the rollups counted of the events received and
// the gaps retention left.
type VolumeReader interface {
	// EventCounts returns the events project received on each UTC day from
	// first to last, both included, by type.
	EventCounts(ctx context.Context, project workspace.ProjectID, first, last time.Time) (map[events.EventDay]int64, error)
	// Gaps returns the gaps whose rows were received from from to to, in
	// the order they were received.
	Gaps(ctx context.Context, from, to time.Time) ([]events.Gap, error)
}

// VolumeQuery names a project's event volume over a range of UTC days, nil
// leaving an end to its default.
type VolumeQuery struct {
	Project  workspace.ProjectID
	From, To *time.Time
}

// GetEventVolume reads the events a project received per day and the gaps
// retention left over the same days.
type GetEventVolume struct {
	snapshots Snapshotter
	store     VolumeReader
	clock     clock.Clock
}

// NewGetEventVolume returns the handler.
func NewGetEventVolume(snapshots Snapshotter, store VolumeReader, c clock.Clock) *GetEventVolume {
	return &GetEventVolume{snapshots: snapshots, store: store, clock: c}
}

// Handle returns the volume q names; a range of days that cannot be read is
// an *events.RangeError. The gaps are the instance's: a stream reads every
// project's rows, so what a drop took is not told apart by project.
func (h *GetEventVolume) Handle(ctx context.Context, q VolumeQuery) (events.EventVolume, error) {
	first, last, rangeErr := events.DaysRead(q.From, q.To, h.clock.Now())
	if rangeErr != nil {
		return events.EventVolume{}, rangeErr
	}
	var out events.EventVolume
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		counts, err := h.store.EventCounts(ctx, q.Project, first, last)
		if err != nil {
			return err
		}
		out.Days = events.VolumeDays(counts)
		// The last instant of the last day PostgreSQL can tell apart from
		// the next day's first.
		out.Gaps, err = h.store.Gaps(ctx, first, last.AddDate(0, 0, 1).Add(-time.Microsecond))
		return err
	})
	return out, err
}
