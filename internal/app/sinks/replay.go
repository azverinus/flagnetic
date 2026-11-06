package sinks

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ReplayCommand asks for the sink Sink of Project to send again the events
// received from From, until To when it is given, of the event kinds Kinds.
type ReplayCommand struct {
	Project workspace.ProjectID
	Sink    sinks.ID
	From    time.Time
	To      *time.Time
	// Kinds are the event kinds to replay; none for every kind the sink
	// exports.
	Kinds   []sinks.EventKind
	Request governance.Request
}

// Replay sends a sink's history again.
type Replay struct{ d Deps }

// NewReplay returns the handler.
func NewReplay(d Deps) *Replay { return &Replay{d: d} }

// Handle sets each stream asked for back to just before the first event
// received at or after From, to stop after the last one received at or before
// To and return to where it was, or to run on into the live stream. The
// delivery worker applies it before the stream's next batch; the batch in
// flight and the lease are dropped now, so that a send already under way
// records nothing over it. A stream with no event in the range is left as it
// is. It records sink.replayed and answers the sink.
func (h *Replay) Handle(ctx context.Context, cmd ReplayCommand) (sinks.Sink, error) {
	now := h.d.now()
	if err := sinks.CheckReplay(cmd.From, cmd.To, now, h.d.Retention); err != nil {
		return sinks.Sink{}, err
	}
	seen, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return seen, err
	}
	kinds, err := sinks.SelectKinds(cmd.Kinds, seen.StreamKinds())
	if err != nil {
		return seen, err
	}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error { return h.replay(ctx, cmd, kinds, now) })
	if err != nil {
		return seen, err
	}
	return h.d.read(ctx, cmd.Project, cmd.Sink)
}

func (h *Replay) replay(ctx context.Context, cmd ReplayCommand, kinds []sinks.EventKind, now time.Time) error {
	if err := h.d.lockProject(ctx, cmd.Project); err != nil {
		return err
	}
	s, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return err
	}
	scope, named, err := h.d.Rows.Scope(ctx, s.Project, s.Environments)
	if err != nil {
		return err
	}
	moved := []string{}
	for _, k := range kinds {
		// A sink that was never enabled has no stream to send again, and one
		// naming only environments that are gone has nothing to read.
		if !named || !hasStream(s, k) {
			continue
		}
		plan, ok, planErr := h.plan(ctx, s, k, scope, cmd)
		if planErr != nil {
			return planErr
		}
		if !ok {
			continue
		}
		if err = h.d.Store.Replay(ctx, s.ID, k, plan, now); err != nil {
			return err
		}
		moved = append(moved, string(k))
	}
	doc := map[string]any{
		"from": cmd.From.UTC().Format(time.RFC3339Nano), "to": nil,
		"event_kinds": kindNames(kinds), "replayed": moved,
	}
	if cmd.To != nil {
		doc["to"] = cmd.To.UTC().Format(time.RFC3339Nano)
	}
	return h.d.recordDoc(ctx, now, cmd.Request, "sink.replayed", s, doc)
}

func hasStream(s sinks.Sink, k sinks.EventKind) bool {
	for _, st := range s.Streams {
		if st.EventKind == k {
			return true
		}
	}
	return false
}

// plan resolves the replay against the stream of kind k, which it locks, so
// that the position it returns to is the one the stream is at and no
// acknowledgement slips in behind it.
func (h *Replay) plan(ctx context.Context, s sinks.Sink, k sinks.EventKind, scope sinks.Scope, cmd ReplayCommand) (sinks.ReplayPlan, bool, error) {
	stream, err := h.d.Store.LockStream(ctx, s.ID, k)
	if err != nil {
		return sinks.ReplayPlan{}, false, err
	}
	// The horizon is read first: the first row received at or after From is
	// found among the rows committed, so a transaction still open with a
	// lower transaction ID would commit behind it. The replay never starts
	// past the horizon, which every such transaction is at or above.
	tail, err := h.d.Rows.Tail(ctx, k)
	if err != nil {
		return sinks.ReplayPlan{}, false, err
	}
	ask := sinks.ReplayAsk{Live: stream.Live(), Bounded: cmd.To != nil}
	if ask.Start, ask.StartFound, err = h.d.Rows.Before(ctx, k, scope, cmd.From); err != nil {
		return sinks.ReplayPlan{}, false, err
	}
	if ask.StartFound && beyond(ask.Start, tail) {
		ask.Start = tail
	}
	if cmd.To != nil {
		if ask.Stop, ask.StopFound, err = h.d.Rows.Last(ctx, k, scope, *cmd.To); err != nil {
			return sinks.ReplayPlan{}, false, err
		}
	}
	plan, ok := sinks.PlanReplay(ask)
	return plan, ok, nil
}

// beyond reports whether a is past b.
func beyond(a, b events.Cursor) bool {
	return a.XID > b.XID || (a.XID == b.XID && a.Seq > b.Seq)
}
