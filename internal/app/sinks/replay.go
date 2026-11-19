package sinks

import (
	"context"
	"errors"
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
//
// The events are searched for before the project is locked: the search scans
// the raw tables for as far back as From goes and reads only committed rows,
// so it needs no lock, and holding the project's lock for it would hold up
// every write of the project, the kill switch included. The lock is taken to
// plan against the streams' positions as they are then; a sink edited between
// the search and the lock is searched for again.
func (h *Replay) Handle(ctx context.Context, cmd ReplayCommand) (sinks.Sink, error) {
	now := h.d.now()
	if err := sinks.CheckReplay(cmd.From, cmd.To, now, h.d.Retention); err != nil {
		return sinks.Sink{}, err
	}
	for attempt := 1; ; attempt++ {
		seen, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
		if err != nil {
			return sinks.Sink{}, err
		}
		found, err := h.search(ctx, seen, cmd)
		if err != nil {
			return sinks.Sink{}, err
		}
		err = h.d.Tx.Within(ctx, func(ctx context.Context) error { return h.replay(ctx, cmd, now, seen.Version, found) })
		if errors.Is(err, errSinkChanged) && attempt < maxSearches {
			continue
		}
		if err != nil {
			return sinks.Sink{}, err
		}
		return h.d.read(ctx, cmd.Project, cmd.Sink)
	}
}

// maxSearches is how many times a request that keeps meeting a sink edited
// under it searches before it gives up.
const maxSearches = 3

// found is what a replay's search found, per event kind of the sink.
type found struct {
	kinds []sinks.EventKind
	named bool
	reads map[sinks.EventKind]foundRead
}

// foundRead is where the events of one kind to replay start and stop.
type foundRead struct {
	start, stop           events.Cursor
	startFound, stopFound bool
}

// search finds, for each stream the replay concerns, the position of the
// first event received at or after From and of the last one at or before To.
func (h *Replay) search(ctx context.Context, s sinks.Sink, cmd ReplayCommand) (found, error) {
	kinds, err := sinks.SelectKinds(cmd.Kinds, s.StreamKinds())
	if err != nil {
		return found{}, err
	}
	scope, named, err := h.d.Rows.Scope(ctx, s.Project, s.Environments)
	if err != nil {
		return found{}, err
	}
	out := found{kinds: kinds, named: named, reads: map[sinks.EventKind]foundRead{}}
	for _, k := range kinds {
		// A sink that was never enabled has no stream to send again, and one
		// naming only environments that are gone has nothing to read.
		if !named || !hasStream(s, k) {
			continue
		}
		// The horizon is read first: the first row received at or after From
		// is found among the rows committed, so a transaction still open with
		// a lower transaction ID would commit behind it. The replay never
		// starts past the horizon, which every such transaction is at or
		// above.
		tail, tailErr := h.d.Rows.Tail(ctx, k)
		if tailErr != nil {
			return found{}, tailErr
		}
		var r foundRead
		if r.start, r.startFound, err = h.d.Rows.Before(ctx, k, scope, cmd.From); err != nil {
			return found{}, err
		}
		if r.startFound && beyond(r.start, tail) {
			r.start = tail
		}
		if cmd.To != nil {
			if r.stop, r.stopFound, err = h.d.Rows.Last(ctx, k, scope, *cmd.To); err != nil {
				return found{}, err
			}
		}
		out.reads[k] = r
	}
	return out, nil
}

func (h *Replay) replay(ctx context.Context, cmd ReplayCommand, now time.Time, searched int64, f found) error {
	if err := h.d.lockProject(ctx, cmd.Project); err != nil {
		return err
	}
	s, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return err
	}
	// What was searched for was the sink as it then was: an edit of its event
	// kinds or environments since changes what there is to replay.
	if s.Version != searched {
		return errSinkChanged
	}
	moved := []string{}
	for _, k := range f.kinds {
		read, ok := f.reads[k]
		if !ok {
			continue
		}
		plan, ok, planErr := h.plan(ctx, s, k, read, cmd)
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
		"event_kinds": kindNames(f.kinds), "replayed": moved,
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
func (h *Replay) plan(ctx context.Context, s sinks.Sink, k sinks.EventKind, read foundRead, cmd ReplayCommand) (sinks.ReplayPlan, bool, error) {
	stream, err := h.d.Store.LockStream(ctx, s.ID, k)
	if err != nil {
		return sinks.ReplayPlan{}, false, err
	}
	plan, ok := sinks.PlanReplay(sinks.ReplayAsk{
		Live: stream.Live(), Bounded: cmd.To != nil, From: cmd.From, Floor: stream.Floor,
		Start: read.start, StartFound: read.startFound, Stop: read.stop, StopFound: read.stopFound,
	})
	return plan, ok, nil
}

// beyond reports whether a is past b.
func beyond(a, b events.Cursor) bool {
	return a.XID > b.XID || (a.XID == b.XID && a.Seq > b.Seq)
}
