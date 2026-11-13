package sinks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// AtRiskEvery is how seldom a sink is judged for being at risk: it takes a
// query per stream, and a sink falls behind retention over days.
const AtRiskEvery = time.Minute

// AtRiskStore keeps which sinks are at risk.
type AtRiskStore interface {
	// Tracked returns every sink that has a stream or is marked at risk,
	// with its streams, whatever its state: a disabled or stopped sink loses
	// rows to retention too.
	Tracked(ctx context.Context) ([]sinks.Sink, error)
	// EnterAtRisk marks sink at risk since now and reports whether it was
	// not already, so that only one of the replicas judging it at once tells
	// of the episode.
	EnterAtRisk(ctx context.Context, sink sinks.ID, now time.Time) (entered bool, err error)
	// LeaveAtRisk clears the mark.
	LeaveAtRisk(ctx context.Context, sink sinks.ID) error
}

// AtRiskDeps are what judging the sinks runs on.
type AtRiskDeps struct {
	Tx    Transactor
	Store AtRiskStore
	Rows  Rows
	Audit AuditLog
	Clock clock.Clock
	IDs   *id.Source
	// Retention is how long raw events are kept, and LossWarning how long
	// before a row is dropped a sink still undelivered is at risk.
	Retention, LossWarning time.Duration
}

// EvaluateAtRisk judges whether sinks are about to lose events to retention.
type EvaluateAtRisk struct {
	d AtRiskDeps

	mu sync.Mutex
	// judged is when each sink was last judged by this replica, and listed
	// when the sinks were last listed.
	judged map[sinks.ID]time.Time
	listed time.Time
}

// listEvery is how seldom the sinks are listed to find those due: the worker
// runs every second, and no sink is due that often.
const listEvery = AtRiskEvery / 6

// NewEvaluateAtRisk returns the use case.
func NewEvaluateAtRisk(d AtRiskDeps) *EvaluateAtRisk {
	return &EvaluateAtRisk{d: d, judged: map[sinks.ID]time.Time{}}
}

// Handle judges every sink not judged in the last AtRiskEvery: one whose
// oldest undelivered row is within the loss warning of retention's edge enters
// being at risk, once, with the audit entry the event is derived from; one
// that is no longer leaves. A sink that cannot be judged does not hold back the
// others; the errors are returned together.
func (h *EvaluateAtRisk) Handle(ctx context.Context) error {
	now := h.d.Clock.Now().UTC().Truncate(time.Microsecond)
	if !h.listDue(now) {
		return nil
	}
	list, err := h.d.Store.Tracked(ctx)
	if err != nil {
		return fmt.Errorf("listing the sinks to judge: %w", err)
	}
	var errs []error
	for _, s := range list {
		if !h.due(s.ID, now) {
			continue
		}
		if err = h.judge(ctx, now, s); err != nil {
			errs = append(errs, fmt.Errorf("sink %s: %w", s.ID, err))
		}
	}
	h.forgetExcept(list)
	return errors.Join(errs...)
}

// listDue reports whether the sinks are to be listed now, and notes that they
// were.
func (h *EvaluateAtRisk) listDue(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d := now.Sub(h.listed); d >= 0 && d < listEvery {
		return false
	}
	h.listed = now
	return true
}

// due reports whether sink is to be judged now, and notes that it was.
func (h *EvaluateAtRisk) due(sink sinks.ID, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if last, ok := h.judged[sink]; ok && now.Sub(last) >= 0 && now.Sub(last) < AtRiskEvery {
		return false
	}
	h.judged[sink] = now
	return true
}

// forgetExcept drops what is kept of the sinks that are gone.
func (h *EvaluateAtRisk) forgetExcept(list []sinks.Sink) {
	live := make(map[sinks.ID]bool, len(list))
	for _, s := range list {
		live[s.ID] = true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for sink := range h.judged {
		if !live[sink] {
			delete(h.judged, sink)
		}
	}
}

func (h *EvaluateAtRisk) judge(ctx context.Context, now time.Time, s sinks.Sink) error {
	lag, kind, found, err := worstLag(ctx, h.d.Rows, now, s)
	if err != nil {
		return err
	}
	risk := found && sinks.AtRisk(lag, h.d.Retention, h.d.LossWarning)
	switch {
	case risk && s.AtRiskSince == nil:
		return h.d.Tx.Within(ctx, func(ctx context.Context) error {
			entered, err := h.d.Store.EnterAtRisk(ctx, s.ID, now)
			if err != nil || !entered {
				return err
			}
			return h.record(ctx, now, s, kind, lag)
		})
	case !risk && s.AtRiskSince != nil:
		return h.d.Store.LeaveAtRisk(ctx, s.ID)
	}
	return nil
}

// record records sink.at_risk, which the governance record derives the event
// webhook subscriptions are sent from.
func (h *EvaluateAtRisk) record(ctx context.Context, now time.Time, s sinks.Sink, kind sinks.EventKind, lag time.Duration) error {
	r := governance.Record{
		Project: s.Project, Action: governance.EventSinkAtRisk,
		Entity: governance.Entity{Kind: "sink", ID: s.ID.String(), Key: s.Name},
		After: map[string]any{
			"sink":               map[string]any{"id": s.ID.String(), "name": s.Name, "kind": string(s.Kind)},
			"event_kind":         string(kind),
			"lag_seconds":        int64(lag / time.Second),
			"loss_warning_hours": int(h.d.LossWarning / time.Hour),
		},
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, governance.Request{Actor: governance.SinkDeliveryActor}, r)
	if err != nil {
		return err
	}
	return h.d.Audit.Record(ctx, entry)
}

// worstLag returns the age of the oldest row any of s's streams has not
// delivered, and the event kind of that stream; found is false when none has
// an undelivered row, or the sink names environments and none exists.
func worstLag(ctx context.Context, rows Rows, now time.Time, s sinks.Sink) (lag time.Duration, kind sinks.EventKind, found bool, err error) {
	scope, named, err := rows.Scope(ctx, s.Project, s.Environments)
	if err != nil || !named {
		return 0, "", false, err
	}
	for _, st := range s.Streams {
		at, ok, err := rows.OldestUnread(ctx, st.EventKind, st.LiveScope(scope), st.Live())
		if err != nil {
			return 0, "", false, err
		}
		if l := max(now.Sub(at), 0); ok && (!found || l > lag) {
			lag, kind, found = l, st.EventKind, true
		}
	}
	return lag, kind, found, nil
}
