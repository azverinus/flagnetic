package sinks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	// ErrDeadLetterNotFound answers a dead letter the sink does not have.
	ErrDeadLetterNotFound = apperrors.NotFound("not_found", "the sink has no dead letter with this identifier")
	// errRowsExpired refuses to redeliver a dead letter none of whose rows is
	// stored any more.
	errRowsExpired = apperrors.Invalid("rows_expired",
		"none of the dead letter's rows are stored any more; retention has dropped them")
)

// DeadLetterStore keeps a sink's dead letters. Setting a batch aside is the
// delivery store's, since it is one step with moving the stream on.
type DeadLetterStore interface {
	// DeadLetters returns sink's dead letters, the oldest first.
	DeadLetters(ctx context.Context, sink sinks.ID) ([]sinks.DeadLetter, error)
	// DeadLetter returns sink's dead letter with dl, or ErrNotFound.
	DeadLetter(ctx context.Context, sink sinks.ID, dl sinks.DeadLetterID) (sinks.DeadLetter, error)
	// RecordRedelivery records a redelivery of dl: one more attempt, and
	// either the failure that ended it or, when the target acknowledged it,
	// that it is redelivered at now. It returns the dead letter as it is.
	RecordRedelivery(ctx context.Context, sink sinks.ID, dl sinks.DeadLetterID, failure *sinks.FailureAt, now time.Time) (sinks.DeadLetter, error)
	// DiscardDeadLetter deletes sink's dead letter dl; one gone already is left
	// gone.
	DiscardDeadLetter(ctx context.Context, sink sinks.ID, dl sinks.DeadLetterID) error
	// Resume clears sink's stop and each of its streams' run of dead letters
	// and makes the streams due at now at the latest; it reports when the sink
	// had been stopped, nil when it had not and nothing was done.
	Resume(ctx context.Context, sink sinks.ID, now time.Time) (stoppedAt *time.Time, err error)
}

// setAside sets the batch the target rejected for the last time aside as a
// dead letter and moves the stream on, so that the next batch is chosen in
// the same round. It records sink.dead_lettered and, when this was the dead
// letter that stopped the sink, sink.stopped, in the unit of work of the
// move.
func (h *Deliver) setAside(ctx context.Context, ref StreamRef, token LeaseToken, c ClaimedSink, b sinks.Batch, page sinks.Page, f sinks.Failure, floor *time.Time, scope sinks.Scope) (Result, error) {
	now := h.now()
	dl := NewDeadLetter{
		ID: id.New[sinks.DeadLetter](h.d.IDs), Failure: sinks.FailureAt{Failure: f, At: now},
		Rows: len(page.Rows) + page.Skipped, First: page.Rows[0].Received(), Last: page.Rows[len(page.Rows)-1].Received(),
		Floor: floor,
	}
	var res SetAsideResult
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var err error
		if res, err = h.d.Store.SetAside(ctx, ref, token, dl, h.d.StopAfterDeadLetters, now); err != nil || !res.Held {
			return err
		}
		if err = h.recordDeadLettered(ctx, now, c, ref.Kind, dl, res.Attempts); err != nil {
			return err
		}
		if res.Stopped {
			return h.recordStopped(ctx, now, c, ref.Kind, f, res.InARow)
		}
		return nil
	})
	switch {
	case err != nil:
		return Failed, fmt.Errorf("setting the batch aside: %w", err)
	case !res.Held:
		return LeaseLost, nil
	}
	// Counted once the cursor has passed the batch.
	h.d.Observer.Batch(ref.Sink, ref.Kind, ResultDeadLettered)
	if page.Skipped > 0 {
		h.d.Observer.RowsSkipped(SkipEnvironmentDeleted, page.Skipped)
	}
	h.observeLag(ctx, ref, scope, b.To)
	return DeadLettered, nil
}

func sinkRef(s sinks.Sink) map[string]any {
	return map[string]any{"id": s.ID.String(), "name": s.Name, "kind": string(s.Kind)}
}

func (h *Deliver) record(ctx context.Context, now time.Time, c ClaimedSink, action string, after map[string]any) error {
	r := governance.Record{
		Project: c.Sink.Project, Action: action,
		Entity: governance.Entity{Kind: "sink", ID: c.Sink.ID.String(), Key: c.Sink.Name}, After: after,
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, governance.Request{Actor: governance.SinkDeliveryActor}, r)
	if err != nil {
		return err
	}
	return h.d.Audit.Record(ctx, entry)
}

// recordDeadLettered records sink.dead_lettered, an entry the audit log keeps
// and no webhook event is made of.
func (h *Deliver) recordDeadLettered(ctx context.Context, now time.Time, c ClaimedSink, kind sinks.EventKind, dl NewDeadLetter, attempts int) error {
	return h.record(ctx, now, c, "sink.dead_lettered", map[string]any{
		"sink": sinkRef(c.Sink), "dead_letter": dl.ID.String(), "event_kind": string(kind), "rows": dl.Rows,
		"attempts": attempts, "failure": failureDoc(dl.Failure.Failure, now),
	})
}

// recordStopped records sink.stopped, which the governance record derives the
// event webhook subscriptions are sent from.
func (h *Deliver) recordStopped(ctx context.Context, now time.Time, c ClaimedSink, kind sinks.EventKind, f sinks.Failure, inARow int) error {
	return h.record(ctx, now, c, governance.EventSinkStopped, map[string]any{
		"sink": sinkRef(c.Sink), "event_kind": string(kind), "consecutive_dead_letters": inARow,
		"failure": failureDoc(f, now),
	})
}

// ListDeadLetters answers a sink's dead letters.
type ListDeadLetters struct{ d Deps }

// NewListDeadLetters returns the query.
func NewListDeadLetters(d Deps) *ListDeadLetters { return &ListDeadLetters{d: d} }

// Handle returns sink's dead letters in every status, the oldest first.
func (h *ListDeadLetters) Handle(ctx context.Context, project workspace.ProjectID, sink sinks.ID) ([]sinks.DeadLetter, error) {
	s, err := sinkOf(ctx, h.d.Store, project, sink)
	if err != nil {
		return nil, err
	}
	return h.d.DeadLetters.DeadLetters(ctx, s.ID)
}

// deadLetterOf returns sink's dead letter, or not_found.
func (d Deps) deadLetterOf(ctx context.Context, sink sinks.ID, dl sinks.DeadLetterID) (sinks.DeadLetter, error) {
	v, err := d.DeadLetters.DeadLetter(ctx, sink, dl)
	if errors.Is(err, ErrNotFound) {
		return v, ErrDeadLetterNotFound
	}
	return v, err
}

// DeadLetterCommand names a dead letter of the sink Sink of Project.
type DeadLetterCommand struct {
	Project    workspace.ProjectID
	Sink       sinks.ID
	DeadLetter sinks.DeadLetterID
	Request    governance.Request
}

// deadLetterDoc is a dead letter as the audit log tells it: the batch by its
// size and times, never by its positions or anything the target said.
func deadLetterDoc(dl sinks.DeadLetter) map[string]any {
	return map[string]any{
		"id": dl.ID.String(), "event_kind": string(dl.EventKind), "rows": dl.Rows, "attempts": dl.Attempts,
		"status": string(dl.Status), "first_received_at": dl.FirstReceivedAt.UTC().Format(time.RFC3339Nano),
		"last_received_at": dl.LastReceivedAt.UTC().Format(time.RFC3339Nano),
	}
}

// RedeliverDeadLetter sends a dead letter's rows again.
type RedeliverDeadLetter struct{ d Deps }

// NewRedeliverDeadLetter returns the handler.
func NewRedeliverDeadLetter(d Deps) *RedeliverDeadLetter { return &RedeliverDeadLetter{d: d} }

// Handle reads the rows of the dead letter that are still stored and sends
// them to the target now, under a delivery ID of this attempt, outside the
// stream: its cursor, failures and health are not touched. The target's
// acknowledgement makes the dead letter redelivered; its failure leaves it as
// it was, with one more attempt and the failure. Either is recorded as
// sink.dead_letter_redelivered. When no row is stored any more it is refused
// rows_expired and nothing is sent.
func (h *RedeliverDeadLetter) Handle(ctx context.Context, cmd DeadLetterCommand) (sinks.DeadLetter, error) {
	s, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return sinks.DeadLetter{}, err
	}
	dl, err := h.d.deadLetterOf(ctx, s.ID, cmd.DeadLetter)
	if err != nil {
		return dl, err
	}
	if dl.Status == sinks.DeadLetterExpired {
		return dl, errRowsExpired
	}
	page, err := h.d.Rows.ReadBatch(ctx, dl.EventKind, sinks.Scope{Project: s.Project, Environments: dl.Environments, Floor: dl.Floor},
		sinks.Batch{From: dl.From, To: dl.To, Rows: dl.Rows})
	if err != nil {
		return dl, fmt.Errorf("reading the dead letter's rows: %w", err)
	}
	if len(page.Rows) == 0 {
		return dl, errRowsExpired
	}
	target, ok := h.d.Targets.For(s.Kind)
	if !ok {
		return dl, errNoTarget
	}
	projectKey, err := h.d.Store.ProjectKey(ctx, s.Project)
	if err != nil {
		return dl, err
	}
	secrets, err := openSecrets(h.d.Opener, s, h.d.now())
	if err != nil {
		return dl, err
	}
	failure := target.Deliver(ctx, Call{Sink: s, ProjectKey: projectKey, Secrets: secrets}, Delivery{
		ID: sinks.RedeliveryID(dl.ID, dl.Attempts), Kind: dl.EventKind, Rows: page.Rows, Now: h.d.now(),
	})
	now := h.d.now()
	var failed *sinks.FailureAt
	result := ResultRedelivered
	if failure != nil {
		failed, result = &sinks.FailureAt{Failure: *failure, At: now}, ResultRedeliveryFailed
	}
	var out sinks.DeadLetter
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var txErr error
		if out, txErr = h.d.DeadLetters.RecordRedelivery(ctx, s.ID, dl.ID, failed, now); txErr != nil {
			if errors.Is(txErr, ErrNotFound) {
				return ErrDeadLetterNotFound
			}
			return txErr
		}
		after := deadLetterDoc(out)
		after["acknowledged"] = failure == nil
		return h.d.recordDoc(ctx, now, cmd.Request, "sink.dead_letter_redelivered", s, after)
	})
	if err != nil {
		return out, err
	}
	h.d.Observer.Batch(s.ID, dl.EventKind, result)
	return out, nil
}

// DiscardDeadLetter gives a dead letter up.
type DiscardDeadLetter struct{ d Deps }

// NewDiscardDeadLetter returns the handler.
func NewDiscardDeadLetter(d Deps) *DiscardDeadLetter { return &DiscardDeadLetter{d: d} }

// Handle deletes the dead letter, whatever its status, and records
// sink.dead_letter_discarded.
func (h *DiscardDeadLetter) Handle(ctx context.Context, cmd DeadLetterCommand) error {
	now := h.d.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		s, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
		if err != nil {
			return err
		}
		dl, err := h.d.deadLetterOf(ctx, s.ID, cmd.DeadLetter)
		if err != nil {
			return err
		}
		if err = h.d.DeadLetters.DiscardDeadLetter(ctx, s.ID, dl.ID); err != nil {
			return err
		}
		return h.d.recordDoc(ctx, now, cmd.Request, "sink.dead_letter_discarded", s, deadLetterDoc(dl))
	})
}

// ResumeCommand asks for the stopped sink Sink of Project to start again.
type ResumeCommand struct {
	Project workspace.ProjectID
	Sink    sinks.ID
	Request governance.Request
}

// ResumeSink starts a stopped sink again.
type ResumeSink struct{ d Deps }

// NewResumeSink returns the handler.
func NewResumeSink(d Deps) *ResumeSink { return &ResumeSink{d: d} }

// Handle lifts the stop of the sink: each stream keeps its place and its batch
// in flight, its run of dead letters is cleared and it is due now. Dead
// letters are not redelivered. It records sink.resumed. A sink that is not
// stopped is answered as it is.
func (h *ResumeSink) Handle(ctx context.Context, cmd ResumeCommand) (sinks.Sink, error) {
	now := h.d.now()
	var out sinks.Sink
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := h.d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		s, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
		if err != nil {
			return err
		}
		stopped, err := h.d.DeadLetters.Resume(ctx, s.ID, now)
		if err != nil {
			return err
		}
		if stopped != nil {
			var entry governance.Entry
			entry, err = governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, cmd.Request, governance.Record{
				Project: s.Project, Action: "sink.resumed", Entity: governance.Entity{Kind: "sink", ID: s.ID.String(), Key: s.Name},
				Before: map[string]any{"stopped_at": stopped.UTC().Format(time.RFC3339Nano)}, After: map[string]any{"stopped_at": nil},
			})
			if err != nil {
				return err
			}
			if err = h.d.Audit.Record(ctx, entry); err != nil {
				return err
			}
		}
		out, err = h.d.read(ctx, cmd.Project, cmd.Sink)
		return err
	})
	return out, err
}
