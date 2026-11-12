package sinks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ErrGapNotFound answers a gap the sink does not have.
var ErrGapNotFound = apperrors.NotFound("not_found", "the sink has no gap with this identifier")

// GapStream is a stream a drop of rows concerns, with the sink it belongs to:
// its identity, project, name, kind and environments.
type GapStream struct {
	Sink   sinks.Sink
	Stream sinks.Stream
}

// GapStore keeps the gaps retention leaves in the sinks' streams.
type GapStore interface {
	// LockStreams holds every stream of kind until the unit of work ends,
	// the streams of disabled and stopped sinks included.
	LockStreams(ctx context.Context, kind sinks.EventKind) error
	// GapStreams returns every stream of kind with its sink.
	GapStreams(ctx context.Context, kind sinks.EventKind) ([]GapStream, error)
	// RecordGap saves gap as the gap of sink's stream of kind and moves the
	// stream's live position, the position a bounded replay returns to when
	// replaying is set and the cursor when not, to gap.Moved, dropping its
	// batch in flight and its lease.
	RecordGap(ctx context.Context, sink sinks.ID, kind sinks.EventKind, replaying bool, gap events.Gap) error
	// ExpireDeadLetters expires the pending dead letters of kind none of
	// whose rows is stored at or after before.
	ExpireDeadLetters(ctx context.Context, kind sinks.EventKind, before time.Time) error
	// Gaps returns sink's gaps, the oldest first.
	Gaps(ctx context.Context, sink sinks.ID) ([]sinks.Gap, error)
	// Dismiss marks sink's gap dismissed at now, keeping the first time of
	// one dismissed already; first says whether this call dismissed it.
	// ErrNotFound when the sink has no such gap.
	Dismiss(ctx context.Context, sink sinks.ID, gap sinks.GapID, now time.Time) (gap2 sinks.Gap, first bool, err error)
}

// GapRows reads the rows a drop takes from a stream.
type GapRows interface {
	// Scope resolves the environments a sink names, as Rows does.
	Scope(ctx context.Context, project workspace.ProjectID, keys []string) (scope sinks.Scope, ok bool, err error)
	// Lost returns what the partitions of days of kind's rows hold past from
	// in scope.
	Lost(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, days []time.Time, from events.Cursor) (events.Lost, error)
	// FirstStored returns the first row of kind in scope past from outside
	// the partitions of days, nil when there is none.
	FirstStored(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, days []time.Time, from events.Cursor) (*events.Cursor, error)
}

// RetentionDeps are what the sinks' side of a partition drop runs on.
type RetentionDeps struct {
	Store GapStore
	Rows  GapRows
	Audit AuditLog
	IDs   *id.Source
}

// Retention is the sinks' side of retention dropping a partition: the events
// context calls it inside the drop's unit of work, so that the rows the drop
// takes can still be read and the gap is exact.
type Retention struct{ d RetentionDeps }

// NewRetention returns the sinks' side of retention.
func NewRetention(d RetentionDeps) *Retention { return &Retention{d: d} }

// LockStreams holds the streams of every sink reading typ until the drop's
// unit of work ends, the streams of disabled and stopped sinks included: a
// delivery cannot move a cursor under the drop, nor read a partition as it
// goes.
func (r *Retention) LockStreams(ctx context.Context, typ events.EventType) error {
	return r.d.Store.LockStreams(ctx, sinks.EventKind(typ))
}

// RecordGaps records, for every stream of typ, the rows the drop of days takes
// that the stream had not delivered: a gap, the stream's live position moved
// to just before the first row still stored (never past the horizon), its
// batch in flight and lease dropped, and sink.data_lost. Measured from the
// position a bounded replay returns to while one runs, since the replay's own
// cursor is behind rows already delivered. The pending dead letters with no
// row left are expired. It returns the gaps recorded.
func (r *Retention) RecordGaps(ctx context.Context, typ events.EventType, days []time.Time, horizon uint64, now time.Time) ([]events.Gap, error) {
	kind := sinks.EventKind(typ)
	streams, err := r.d.Store.GapStreams(ctx, kind)
	if err != nil {
		return nil, err
	}
	var recorded []events.Gap
	for _, gs := range streams {
		gap, ok, gapErr := r.gap(ctx, gs, kind, typ, days, horizon, now)
		if gapErr != nil {
			return nil, gapErr
		}
		if ok {
			recorded = append(recorded, gap)
		}
	}
	end := slices.MaxFunc(days, time.Time.Compare).AddDate(0, 0, 1)
	if err = r.d.Store.ExpireDeadLetters(ctx, kind, end); err != nil {
		return nil, err
	}
	return recorded, nil
}

func (r *Retention) gap(ctx context.Context, gs GapStream, kind sinks.EventKind, typ events.EventType, days []time.Time,
	horizon uint64, now time.Time,
) (events.Gap, bool, error) {
	scope, named, err := r.d.Rows.Scope(ctx, gs.Sink.Project, gs.Sink.Environments)
	if err != nil || !named {
		// A sink naming environments that are all gone reads nothing, so it
		// loses nothing.
		return events.Gap{}, false, err
	}
	scope = gs.Stream.LiveScope(scope)
	live := gs.Stream.Live()
	lost, err := r.d.Rows.Lost(ctx, kind, scope, days, live)
	if err != nil || lost.Rows == 0 {
		return events.Gap{}, false, err
	}
	next, err := r.d.Rows.FirstStored(ctx, kind, scope, days, live)
	if err != nil {
		return events.Gap{}, false, err
	}
	gap, ok := events.GapOf(id.New[events.Gap](r.d.IDs), sinks.GapStream(kind), typ, live, lost, next, horizon, now)
	if !ok {
		return events.Gap{}, false, nil
	}
	if err = r.d.Store.RecordGap(ctx, gs.Sink.ID, kind, gs.Stream.Resume != nil, gap); err != nil {
		return events.Gap{}, false, err
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](r.d.IDs), now, governance.Request{Actor: governance.EventRetentionActor},
		governance.Record{
			Project: gs.Sink.Project, Action: governance.EventSinkDataLost,
			Entity: governance.Entity{Kind: "sink", ID: gs.Sink.ID.String(), Key: gs.Sink.Name},
			After: map[string]any{
				"sink": sinkRef(gs.Sink), "event_kind": string(kind),
				"gap": map[string]any{
					"id": gap.ID.String(), "rows": gap.Rows,
					"period_start": gap.PeriodStart.UTC().Format(time.RFC3339Nano),
					"period_end":   gap.PeriodEnd.UTC().Format(time.RFC3339Nano),
					"dropped_at":   gap.DroppedAt.UTC().Format(time.RFC3339Nano),
				},
			},
		})
	if err != nil {
		return events.Gap{}, false, err
	}
	if err = r.d.Audit.Record(ctx, entry); err != nil {
		return events.Gap{}, false, fmt.Errorf("recording sink.data_lost: %w", err)
	}
	return gap, true, nil
}

// ListGaps answers a sink's gaps.
type ListGaps struct{ d Deps }

// NewListGaps returns the query.
func NewListGaps(d Deps) *ListGaps { return &ListGaps{d: d} }

// Handle returns sink's gaps, the oldest first, dismissed ones included.
func (h *ListGaps) Handle(ctx context.Context, project workspace.ProjectID, sink sinks.ID) ([]sinks.Gap, error) {
	s, err := sinkOf(ctx, h.d.Store, project, sink)
	if err != nil {
		return nil, err
	}
	return h.d.Gaps.Gaps(ctx, s.ID)
}

// DismissGapCommand asks for the gap Gap of the sink Sink of Project to be
// dismissed.
type DismissGapCommand struct {
	Project workspace.ProjectID
	Sink    sinks.ID
	Gap     sinks.GapID
	Request governance.Request
}

// DismissGap marks a gap as seen.
type DismissGap struct{ d Deps }

// NewDismissGap returns the handler.
func NewDismissGap(d Deps) *DismissGap { return &DismissGap{d: d} }

// Handle dismisses the gap and records sink.gap_dismissed. A gap dismissed
// already is answered as it is, with its first time, and nothing is
// recorded.
func (h *DismissGap) Handle(ctx context.Context, cmd DismissGapCommand) (sinks.Gap, error) {
	now := h.d.now()
	var out sinks.Gap
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		s, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
		if err != nil {
			return err
		}
		g, first, err := h.d.Gaps.Dismiss(ctx, s.ID, cmd.Gap, now)
		if errors.Is(err, ErrNotFound) {
			return ErrGapNotFound
		}
		if err != nil {
			return err
		}
		out = g
		if !first {
			return nil
		}
		return h.d.recordDoc(ctx, now, cmd.Request, "sink.gap_dismissed", s, map[string]any{
			"gap": g.ID.String(), "event_kind": string(g.EventKind), "rows": g.Rows,
			"period_start": g.PeriodStart.UTC().Format(time.RFC3339Nano), "period_end": g.PeriodEnd.UTC().Format(time.RFC3339Nano),
			"dismissed_at": g.DismissedAt.UTC().Format(time.RFC3339Nano),
		})
	})
	return out, err
}
