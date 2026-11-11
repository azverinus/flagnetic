package sinks

import (
	"context"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

var (
	// errNoTarget refuses to work with a sink whose kind has no target yet.
	errNoTarget = apperrors.Invalid("sink_not_ready", "no target can deliver to a sink of this kind yet")
	// errOutOfRetention refuses to start a sink further back than events are
	// kept.
	errOutOfRetention = apperrors.Invalid("out_of_retention",
		"start_from is earlier than the oldest event still stored")
)

// notReady refuses to enable a sink whose target failed its check, telling
// why by class, status and code, never by anything the target said.
func notReady(f sinks.Failure) error {
	err := apperrors.Invalid("sink_not_ready", "the target did not pass the check, so the sink was not enabled: "+f.Error()).
		WithExtension("failure", string(f.Class))
	if f.Status != 0 {
		err = err.WithExtension("failure_status", f.Status)
	}
	if f.Code != "" {
		err = err.WithExtension("failure_code", f.Code)
	}
	return err
}

// probe checks s's target with secrets, and prepares it when prepare is set.
func (d Deps) probe(ctx context.Context, s sinks.Sink, secrets []string, prepare bool) (Probed, error) {
	target, ok := d.Targets.For(s.Kind)
	if !ok {
		return Probed{}, errNoTarget
	}
	projectKey, err := d.Store.ProjectKey(ctx, s.Project)
	if err != nil {
		return Probed{}, err
	}
	probed := target.Probe(ctx, Call{Sink: s, ProjectKey: projectKey, Secrets: secrets}, d.now(), prepare)
	if probed.Warnings == nil {
		probed.Warnings = []string{}
	}
	return probed, nil
}

// EnableCommand asks for the sink Sink of Project to start delivering.
type EnableCommand struct {
	Project workspace.ProjectID
	Sink    sinks.ID
	// StartFrom is when the streams this enable creates start reading, a
	// time still within retention; nil to start from now.
	StartFrom *time.Time
	Request   governance.Request
}

// Enable enables a sink.
type Enable struct{ d Deps }

// NewEnable returns the handler.
func NewEnable(d Deps) *Enable { return &Enable{d: d} }

// Handle probes the sink's target, refusing with sink_not_ready and the
// failure when it cannot take a batch, creates the streams the sink lacks at
// the position its start asks for, and records sink.enabled. A sink that is
// already enabled is answered as it is: nothing is probed or recorded. Only the
// streams this creates start at StartFrom; one the sink already has keeps its
// cursor, so enabling a sink again continues where it stopped.
func (h *Enable) Handle(ctx context.Context, cmd EnableCommand) (sinks.Sink, error) {
	seen, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return seen, err
	}
	if seen.Enabled {
		return h.d.judged(ctx, seen)
	}
	secrets, err := openSecrets(h.d.Opener, seen, h.d.now())
	if err != nil {
		return seen, err
	}
	probed, err := h.d.probe(ctx, seen, secrets, true)
	if err != nil {
		return seen, err
	}
	if probed.Failure != nil {
		return seen, notReady(*probed.Failure)
	}
	now := h.d.now()
	var out sinks.Sink
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var txErr error
		out, txErr = h.enable(ctx, cmd, probed.Warnings, now)
		return txErr
	})
	return out, err
}

func (h *Enable) enable(ctx context.Context, cmd EnableCommand, warnings []string, now time.Time) (sinks.Sink, error) {
	if err := h.d.lockProject(ctx, cmd.Project); err != nil {
		return sinks.Sink{}, err
	}
	before, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return before, err
	}
	if before.Enabled {
		return h.d.judged(ctx, before)
	}
	if err = h.d.syncStreams(ctx, before, before.EventKinds, cmd.StartFrom, now); err != nil {
		return before, err
	}
	after := before
	after.Enabled, after.Warnings = true, warnings
	after.UpdatedAt, after.Version = now, before.Version+1
	if err = h.d.Store.Update(ctx, after); err != nil {
		return after, err
	}
	if err = h.d.record(ctx, now, cmd.Request, "sink.enabled", &before, &after); err != nil {
		return after, err
	}
	return h.d.read(ctx, cmd.Project, cmd.Sink)
}

// syncStreams makes the streams of s those of the event kinds it exports
// (kinds, all of them when none): a stream it lacks starts at the position
// from asks for, or at the horizon when it is nil, and one it has no longer
// is dropped. s carries the streams it has.
func (d Deps) syncStreams(ctx context.Context, s sinks.Sink, kinds []sinks.EventKind, from *time.Time, now time.Time) error {
	want := sinks.Input{EventKinds: kinds}.StreamKinds()
	have := map[sinks.EventKind]bool{}
	for _, st := range s.Streams {
		have[st.EventKind] = true
	}
	var (
		scope    sinks.Scope
		named    bool
		resolved bool
	)
	for _, k := range want {
		if have[k] {
			continue
		}
		if from != nil && sinks.OutOfRetention(*from, now, d.Retention) {
			return errOutOfRetention
		}
		if !resolved {
			var err error
			if scope, named, err = d.Rows.Scope(ctx, s.Project, s.Environments); err != nil {
				return err
			}
			resolved = true
		}
		at, err := d.start(ctx, k, scope, named, from)
		if err != nil {
			return err
		}
		if err = d.Store.CreateStream(ctx, s.ID, k, at, floorOf(from), now); err != nil {
			return err
		}
	}
	return d.dropStreams(ctx, s, kinds)
}

// dropStreams drops the streams of s of the event kinds it no longer exports
// (kinds, all of them when none). s carries the streams it has.
func (d Deps) dropStreams(ctx context.Context, s sinks.Sink, kinds []sinks.EventKind) error {
	wanted := map[sinks.EventKind]bool{}
	for _, k := range (sinks.Input{EventKinds: kinds}).StreamKinds() {
		wanted[k] = true
	}
	for _, st := range s.Streams {
		if !wanted[st.EventKind] {
			if err := d.Store.DeleteStream(ctx, s.ID, st.EventKind); err != nil {
				return err
			}
		}
	}
	return nil
}

// floorOf is the floor of a new stream starting at from: from itself, none
// when the stream starts at the horizon. A sink naming no environment left
// gets the floor too, although its stream starts at the horizon: once it is
// edited to name one, the stream reads from that horizon, which an open
// transaction may have held behind rows received before from.
func floorOf(from *time.Time) *time.Time {
	if from == nil {
		return nil
	}
	f := *from
	return &f
}

// start returns where a new stream of kind begins reading: just before the
// first row received at or after from, or at the horizon when it is nil or
// nothing was received since.
//
// The first row received since from is found among the rows committed, so a
// transaction still open with a lower transaction ID would commit behind it
// and never be read. The start is therefore never past the horizon, which is
// read first: every transaction still open when the first row is looked for
// has an ID at or above that horizon, so a stream starting no later reads it.
// The horizon is held back by any transaction open anywhere in the database,
// whose ID may be below rows received before from, so starting there reads
// those rows too: the stream's floor, from, keeps them from being sent.
func (d Deps) start(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, named bool, from *time.Time) (events.Cursor, error) {
	tail, err := d.Rows.Tail(ctx, kind)
	if err != nil || from == nil || !named {
		return tail, err
	}
	at, ok, err := d.Rows.Before(ctx, kind, scope, *from)
	if err != nil {
		return events.Cursor{}, fmt.Errorf("finding where %s starts: %w", kind, err)
	}
	if !ok || tail.XID < at.XID || (tail.XID == at.XID && tail.Seq < at.Seq) {
		return tail, nil
	}
	return at, nil
}
