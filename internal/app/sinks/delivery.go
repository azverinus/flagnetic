package sinks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// LeaseToken names one claim of a replica on the streams it leased, so that a
// replica whose lease ran out finds out the stream is no longer its own.
type LeaseToken = id.Of[Lease]

// Lease is a replica's hold on the streams of one claim.
type Lease struct{}

// LeaseDuration is how long a claimed stream stays claimed. It is renewed
// immediately before each attempt, so it only has to outlast the longest
// attempt: a replica that dies mid-attempt leaves its stream to the next
// claim once it runs out.
const LeaseDuration = 5 * time.Minute

// claimBatch bounds the streams one claim leases; a round claims again once it
// has attempted them.
const claimBatch = 50

// StreamRef names one stream: the delivery of an event kind of a sink.
type StreamRef struct {
	Sink sinks.ID
	Kind sinks.EventKind
}

// Inflight is the batch a stream is delivering: its upper bound and the
// environments it was filtered to, committed before the first send so that
// every retry, on any replica, re-reads exactly the same rows.
type Inflight struct {
	To           events.Cursor
	Environments []workspace.EnvironmentID
}

// Due is a stream a replica leased, as an attempt needs it.
type Due struct {
	Stream sinks.Stream
	// Inflight is the batch committed before the last send; nil when the
	// stream has none.
	Inflight *Inflight
	// Rejections is how many times the target rejected the batch in flight.
	Rejections int
}

// ClaimedSink is a sink with the leased streams of it that are due, which are
// attempted one after another.
type ClaimedSink struct {
	Sink       sinks.Sink
	ProjectKey string
	Streams    []Due
}

// Claim is what one round of claiming leased.
type Claim struct {
	Token LeaseToken
	Sinks []ClaimedSink
}

// Renewal is what a stream's state was when its lease was renewed.
type Renewal struct {
	// Held is false when the lease ran out and another replica holds the
	// stream, or the stream is gone; nothing else is then known.
	Held bool
	// Enabled and Stopped are the sink's state now.
	Enabled, Stopped bool
}

// DeliveryStore is what the delivery worker reads and writes of streams. Every
// write names the token of the lease and applies only while it holds, and
// reports whether it did: a replica whose lease was taken records nothing.
type DeliveryStore interface {
	// Claim leases to token, until until, the streams due by now of sinks
	// that are enabled or still have a batch in flight and are not stopped,
	// leaving out those of the sinks busy, the longest due first, at most
	// limit streams. A replay a stream has waiting is applied as it is
	// leased: its cursor is the position the replay set.
	Claim(ctx context.Context, token LeaseToken, now, until time.Time, limit int, busy []sinks.ID) ([]ClaimedSink, error)
	// Renew pushes the lease token holds on stream out to until and reports
	// the state of its sink.
	Renew(ctx context.Context, stream StreamRef, token LeaseToken, until time.Time) (Renewal, error)
	// Begin commits the batch to deliver next of stream, as in flight, while
	// its sink is enabled and not stopped; ErrHalted otherwise. held is false
	// when the lease is gone.
	Begin(ctx context.Context, stream StreamRef, token LeaseToken, in Inflight) (held bool, err error)
	// Ack moves stream's cursor to to, clears its batch in flight and its
	// failures, ends its run of dead letters when it delivered rows (passing
	// rows of other projects is no delivery), counts delivered rows and
	// schedules its next attempt for next, releasing the lease. A bounded
	// replay that to reaches the end of is over: the cursor goes to where
	// the replay returns to instead.
	Ack(ctx context.Context, stream StreamRef, token LeaseToken, to events.Cursor, delivered int, next, now time.Time) (held bool, err error)
	// Fail records a failed attempt of stream: one more consecutive failure,
	// the failure, a rejection counted when rejected, the next attempt at
	// next, the lease released. entered is true when this is the sink's first
	// failing stream, which begins an episode.
	Fail(ctx context.Context, stream StreamRef, token LeaseToken, f sinks.Failure, rejected bool, next, now time.Time) (held, entered bool, err error)
	// SetAside sets the batch in flight of stream aside as the dead letter dl,
	// in the same unit of work: the cursor moves to the batch's end, the batch
	// and the stream's failures are cleared, its run of dead letters grows by
	// one and the stream is due at now. When that run reaches stopAfter the
	// sink is stopped, if it is not already. The lease is released.
	SetAside(ctx context.Context, stream StreamRef, token LeaseToken, dl NewDeadLetter, stopAfter int, now time.Time) (SetAsideResult, error)
	// Idle releases stream's lease with nothing to send, to be attempted
	// again at next.
	Idle(ctx context.Context, stream StreamRef, token LeaseToken, next time.Time) (held bool, err error)
	// Release gives up stream's lease without an attempt, leaving everything
	// else as it was.
	Release(ctx context.Context, stream StreamRef, token LeaseToken) (held bool, err error)
}

// NewDeadLetter is the batch in flight of a stream, rejected for the last
// time, as it is set aside.
type NewDeadLetter struct {
	ID sinks.DeadLetterID
	// Failure is the rejection that set the batch aside.
	Failure sinks.FailureAt
	// Rows is how many rows the batch held, and First and Last when the first
	// and the last of the rows sent were received.
	Rows        int
	First, Last time.Time
	// Floor is the floor the batch was read with, which a redelivery reads
	// its rows with again; nil when there was none.
	Floor *time.Time
}

// SetAsideResult is what setting a batch aside found and did.
type SetAsideResult struct {
	// Held is false when the lease is gone, or the stream has no batch in
	// flight, and nothing was done.
	Held bool
	// Attempts is how many times the batch was rejected.
	Attempts int
	// InARow is how many dead letters in a row the stream has now.
	InARow int
	// Stopped is true when this stopped the sink; false when it was already
	// stopped or the run is short of the limit.
	Stopped bool
}

// Rows reads the rows the streams export.
type Rows interface {
	// Scope returns the scope of a stream of project's sinks naming the
	// environments with keys, every environment when none; ok is false when
	// the sink names some and none exists any more.
	Scope(ctx context.Context, project workspace.ProjectID, keys []string) (scope sinks.Scope, ok bool, err error)
	// NextBatch returns the next batch of at most limit rows past from, not
	// reaching past until when it is not nil.
	NextBatch(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, from events.Cursor, limit int, until *events.Cursor) (sinks.Batch, error)
	ReadBatch(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, b sinks.Batch) (sinks.Page, error)
	OldestUnread(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, from events.Cursor) (at time.Time, ok bool, err error)
	// Tail returns the position of the last row of kind below the snapshot
	// horizon, where a stream that is to export what happens from now begins.
	Tail(ctx context.Context, kind sinks.EventKind) (events.Cursor, error)
	// Before returns the position just before the first row of kind in scope
	// received at or after from; ok is false when there is none.
	Before(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, from time.Time) (at events.Cursor, ok bool, err error)
	// Last returns the position of the last row of kind in scope received at
	// or before to, below the horizon; ok is false when there is none.
	Last(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, to time.Time) (at events.Cursor, ok bool, err error)
}

// Call is what a target is called with.
type Call struct {
	Sink sinks.Sink
	// ProjectKey is the key of the sink's project.
	ProjectKey string
	// Secrets are the secrets that sign or authenticate the call, opened: the
	// sink's, then the one a rotation replaced while its overlap lasts.
	Secrets []string
}

// Delivery is one batch to send.
type Delivery struct {
	// ID is the delivery's identity to the target, the same on every resend.
	ID   string
	Kind sinks.EventKind
	Rows []sinks.Row
	// Sample says the rows are a sample, not events.
	Sample bool
	// Test says there are no rows: the call only shows the target answers.
	Test bool
	Now  time.Time
}

// Probed is what checking a target found.
type Probed struct {
	// Failure is why the target cannot be delivered to; nil when it can.
	Failure *sinks.Failure
	// Warnings are what is wrong with it that does not stop delivery.
	Warnings []string
}

// Target is the end of one kind of sink: the system a batch is delivered to.
type Target interface {
	// Probe shows whether the target answers and can take a batch, with a
	// call made at now. A target that needs preparing, such as tables to
	// create, is prepared when prepare is set.
	Probe(ctx context.Context, c Call, now time.Time, prepare bool) Probed
	// Deliver sends d and returns why it failed; nil when the target
	// acknowledged it.
	Deliver(ctx context.Context, c Call, d Delivery) *sinks.Failure
}

// Targets returns the target of each kind of sink.
type Targets interface {
	For(kind sinks.Kind) (Target, bool)
}

// Opener opens a value sealed under the instance's secret key.
type Opener interface {
	Open(purpose secret.Purpose, sealed string) ([]byte, error)
}

// The results of a batch, as the metric counts them.
const (
	ResultAck       = "ack"
	ResultTransient = "transient"
	ResultRejected  = "rejected"
	// ResultDeadLettered counts the batch a rejection set aside, beside the
	// rejection itself.
	ResultDeadLettered = "dead_lettered"
	// ResultRedelivered and ResultRedeliveryFailed count a dead letter sent
	// again, acknowledged or not.
	ResultRedelivered      = "redelivered"
	ResultRedeliveryFailed = "redelivery_failed"
)

// SkipEnvironmentDeleted is the reason a row is skipped when its environment
// was deleted: the key it would be exported under is gone with it.
const SkipEnvironmentDeleted = "environment_deleted"

// Observer is told what the delivery makes visible beside the streams: what
// became of each batch, how far behind each stream is, and rows passed over.
type Observer interface {
	Batch(sink sinks.ID, kind sinks.EventKind, result string)
	Lag(sink sinks.ID, kind sinks.EventKind, lag time.Duration)
	RowsSkipped(reason string, n int)
}

// DeliverDeps are what the delivery worker runs on.
type DeliverDeps struct {
	Tx       Transactor
	Store    DeliveryStore
	Rows     Rows
	Targets  Targets
	Opener   Opener
	Audit    AuditLog
	Clock    clock.Clock
	IDs      *id.Source
	Observer Observer
	// Random returns a number in [0, 1), the spread of the backoff.
	Random func() float64
	// RejectionAttempts is how many times the target must reject a batch for
	// it to be set aside as a dead letter, and StopAfterDeadLetters how many
	// dead letters in a row on one stream stop the sink.
	RejectionAttempts, StopAfterDeadLetters int
}

// ErrHalted answers a batch that may not begin because its sink was disabled
// or stopped since the stream was claimed.
var ErrHalted = errors.New("the sink was disabled or stopped")

// Result is what became of one attempt on a stream.
type Result string

// The results of an attempt.
const (
	// Acked is a batch the target acknowledged.
	Acked Result = "acked"
	// Failed is a batch the target did not take, or a call that got no answer.
	Failed Result = "failed"
	// Idled is a stream with nothing to send.
	Idled Result = "idled"
	// Released is a stream not attempted because its sink was stopped or
	// disabled after the claim.
	Released Result = "released"
	// DeadLettered is a batch the target rejected for the last time and the
	// stream set aside.
	DeadLettered Result = "dead_lettered"
	// LeaseLost is an attempt whose lease another replica took, which
	// records nothing: the target may see the batch twice, under one delivery
	// ID.
	LeaseLost Result = "lease_lost"
)

// Deliver attempts the streams that are due.
type Deliver struct{ d DeliverDeps }

// NewDeliver returns the use case.
func NewDeliver(d DeliverDeps) *Deliver { return &Deliver{d: d} }

func (h *Deliver) now() time.Time { return h.d.Clock.Now().UTC().Truncate(time.Microsecond) }

// Claim leases the streams due now, leaving out the sinks busy, which the
// caller is still attempting.
func (h *Deliver) Claim(ctx context.Context, busy []sinks.ID) (Claim, error) {
	token := id.New[Lease](h.d.IDs)
	now := h.now()
	var claimed []ClaimedSink
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var err error
		claimed, err = h.d.Store.Claim(ctx, token, now, now.Add(LeaseDuration), claimBatch, busy)
		return err
	})
	return Claim{Token: token, Sinks: claimed}, err
}

// AttemptSink attempts the streams of c one after another, so that a stop one
// of them brings about holds back the others, and returns the result of each,
// with every error met. A stream that fails to attempt does not stop the rest:
// its lease runs out and it is attempted again.
func (h *Deliver) AttemptSink(ctx context.Context, token LeaseToken, c ClaimedSink) (map[sinks.EventKind]Result, error) {
	results := make(map[sinks.EventKind]Result, len(c.Streams))
	var errs []error
	for _, due := range c.Streams {
		if ctx.Err() != nil {
			break
		}
		res, err := h.attempt(ctx, token, c, due)
		results[due.Stream.EventKind] = res
		if err != nil {
			errs = append(errs, fmt.Errorf("event kind %s: %w", due.Stream.EventKind, err))
		}
	}
	return results, errors.Join(errs...)
}

// attempt does one stream's turn: it chooses and commits its next batch, or
// takes the one already in flight, reads its rows, renews the lease right
// before the call it covers, sends, and records the outcome.
func (h *Deliver) attempt(ctx context.Context, token LeaseToken, c ClaimedSink, due Due) (Result, error) {
	ref := StreamRef{Sink: c.Sink.ID, Kind: due.Stream.EventKind}
	scope, named, err := h.d.Rows.Scope(ctx, c.Sink.Project, c.Sink.Environments)
	if err != nil {
		return Failed, fmt.Errorf("resolving the sink's environments: %w", err)
	}
	batch, in := sinks.Batch{From: due.Stream.Cursor}, due.Inflight
	// The rejections of the batch before this attempt: those counted while it
	// was in flight, none for a batch chosen now.
	rejections := 0
	if in != nil {
		rejections = due.Rejections
		// The batch in flight keeps the environments it was filtered to, so
		// it finishes even once they are gone: its rows are then passed.
		batch.To = in.To
		scope.Environments = in.Environments
	}
	// A batch in flight is read with the floor it was chosen with: only a
	// replay changes the stream's floor or whether it reads with it, and a
	// replay drops the batch in flight.
	read, live := due.Stream.ReadScope(scope), due.Stream.LiveScope(scope)
	if in == nil {
		if !named {
			// Every environment the sink names is gone: it exports nothing,
			// and must not read the project's other environments instead.
			return h.idle(ctx, ref, token, c.Sink)
		}
		if batch, err = h.d.Rows.NextBatch(ctx, ref.Kind, read, due.Stream.Cursor, c.Sink.BatchSize, due.Stream.Stop); err != nil {
			return Failed, fmt.Errorf("choosing a batch: %w", err)
		}
		if batch.To == due.Stream.Cursor && due.Stream.Stop != nil {
			// No row is left up to the end of a bounded replay, perhaps
			// because retention dropped the last ones: it is over.
			return h.ack(ctx, ref, token, c, due, sinks.Batch{From: batch.From, To: *due.Stream.Stop}, 0, 0, live)
		}
		if batch.To == due.Stream.Cursor {
			// Nothing to send, perhaps because a transaction still open holds
			// the horizon back: the stream's lag is told all the same, since
			// that is when it grows.
			res, idleErr := h.idle(ctx, ref, token, c.Sink)
			if res == Idled {
				h.observeLag(ctx, ref, live, due.Stream.Live())
			}
			return res, idleErr
		}
		if batch.Rows == 0 {
			// The rows past the cursor are other projects' or other
			// environments', or were received before the floor: the stream
			// passes them.
			return h.ack(ctx, ref, token, c, due, batch, 0, 0, live)
		}
		in = &Inflight{To: batch.To, Environments: scope.Environments}
		held, beginErr := h.d.Store.Begin(ctx, ref, token, *in)
		switch {
		case errors.Is(beginErr, ErrHalted):
			return h.release(ctx, ref, token)
		case beginErr != nil:
			return Failed, fmt.Errorf("committing the batch: %w", beginErr)
		case !held:
			return LeaseLost, nil
		}
	}
	page, err := h.d.Rows.ReadBatch(ctx, ref.Kind, read, batch)
	if err != nil {
		return Failed, fmt.Errorf("reading the batch: %w", err)
	}
	// The rows of a deleted environment count towards the batch being full,
	// which it is by the rows its range holds, but are never sent.
	batch.Rows = len(page.Rows) + page.Skipped
	if len(page.Rows) == 0 {
		return h.ack(ctx, ref, token, c, due, batch, 0, page.Skipped, live)
	}
	secrets, err := h.secrets(c.Sink, h.now())
	if err != nil {
		return Failed, err
	}
	target, ok := h.d.Targets.For(c.Sink.Kind)
	if !ok {
		return Failed, fmt.Errorf("no target for sinks of kind %s", c.Sink.Kind)
	}

	// The lease is renewed right before the call it covers: a round claims
	// several streams and attempts them in turn, so the lease taken at the
	// claim could run out on the last of them before it is sent. A sink
	// stopped since the claim starts nothing; a disabled one finishes the
	// batch it already has in flight, which was committed while it was
	// enabled.
	renewal, err := h.d.Store.Renew(ctx, ref, token, h.now().Add(LeaseDuration))
	switch {
	case err != nil:
		return Failed, fmt.Errorf("renewing the lease: %w", err)
	case !renewal.Held:
		return LeaseLost, nil
	case renewal.Stopped:
		return h.release(ctx, ref, token)
	}
	now := h.now()
	failure := target.Deliver(ctx, Call{Sink: c.Sink, ProjectKey: c.ProjectKey, Secrets: secrets}, Delivery{
		ID:   sinks.DeliveryID(c.Sink.ID, ref.Kind, batch.From, batch.To, sinks.RowIDs(page.Rows)),
		Kind: ref.Kind, Rows: page.Rows, Now: now,
	})
	if failure == nil {
		h.d.Observer.Batch(ref.Sink, ref.Kind, ResultAck)
		return h.ack(ctx, ref, token, c, due, batch, len(page.Rows), page.Skipped, live)
	}
	rejected := failure.Class == sinks.ClassRejected
	if rejected {
		h.d.Observer.Batch(ref.Sink, ref.Kind, ResultRejected)
	} else {
		h.d.Observer.Batch(ref.Sink, ref.Kind, ResultTransient)
	}
	if rejected && sinks.SetsAside(rejections, h.d.RejectionAttempts) {
		return h.setAside(ctx, ref, token, c, batch, page, *failure, read.Floor, live)
	}
	return h.fail(ctx, ref, token, c, due, *failure, rejected, live)
}

// secrets opens the secrets signing a call of s at now: its own, then the one
// a rotation replaced while the overlap lasts.
func (h *Deliver) secrets(s sinks.Sink, now time.Time) ([]string, error) {
	return openSecrets(h.d.Opener, s, now)
}

func openSecrets(o Opener, s sinks.Sink, now time.Time) ([]string, error) {
	purpose := s.Kind.SecretPurpose()
	current, err := o.Open(purpose, s.SealedSecret)
	if err != nil {
		return nil, fmt.Errorf("opening the sink's secret: %w", err)
	}
	out := []string{string(current)}
	if s.PreviousSealedSecret != "" && s.PreviousSecretExpiresAt != nil && now.Before(*s.PreviousSecretExpiresAt) {
		previous, err := o.Open(purpose, s.PreviousSealedSecret)
		if err != nil {
			return nil, fmt.Errorf("opening the sink's previous secret: %w", err)
		}
		out = append(out, string(previous))
	}
	return out, nil
}

// next is when a stream is attempted again after a batch of rows: at once
// when the batch was full, there being more behind it, otherwise after the
// sink's flush interval.
func next(now time.Time, s sinks.Sink, rows int) time.Time {
	if rows >= s.BatchSize {
		return now
	}
	return now.Add(s.FlushInterval)
}

func (h *Deliver) ack(ctx context.Context, ref StreamRef, token LeaseToken, c ClaimedSink, due Due, b sinks.Batch, delivered, skipped int, scope sinks.Scope) (Result, error) {
	now := h.now()
	held, err := h.d.Store.Ack(ctx, ref, token, b.To, delivered, next(now, c.Sink, b.Rows), now)
	switch {
	case err != nil:
		return Failed, fmt.Errorf("recording the delivery: %w", err)
	case !held:
		return LeaseLost, nil
	}
	// Counted once the cursor has passed them, not on every read of a batch
	// that is retried.
	if skipped > 0 {
		h.d.Observer.RowsSkipped(SkipEnvironmentDeleted, skipped)
	}
	// During a bounded replay the lag is the live stream's, which the replay
	// does not move.
	from := b.To
	if due.Stream.Resume != nil {
		from = *due.Stream.Resume
	}
	h.observeLag(ctx, ref, scope, from)
	return Acked, nil
}

func (h *Deliver) idle(ctx context.Context, ref StreamRef, token LeaseToken, s sinks.Sink) (Result, error) {
	held, err := h.d.Store.Idle(ctx, ref, token, h.now().Add(s.FlushInterval))
	switch {
	case err != nil:
		return Failed, fmt.Errorf("releasing the stream: %w", err)
	case !held:
		return LeaseLost, nil
	}
	return Idled, nil
}

func (h *Deliver) release(ctx context.Context, ref StreamRef, token LeaseToken) (Result, error) {
	held, err := h.d.Store.Release(ctx, ref, token)
	switch {
	case err != nil:
		return Failed, fmt.Errorf("releasing the stream: %w", err)
	case !held:
		return LeaseLost, nil
	}
	return Released, nil
}

// fail records a failed attempt and, in the same unit of work, the audit entry
// of the sink's failing when it begins an episode: it ends once every stream
// of the sink has delivered since.
func (h *Deliver) fail(ctx context.Context, ref StreamRef, token LeaseToken, c ClaimedSink, due Due, f sinks.Failure, rejected bool, scope sinks.Scope) (Result, error) {
	now := h.now()
	wait := sinks.Backoff(due.Stream.ConsecutiveFailures+1, h.d.Random)
	held := false
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var entered bool
		var err error
		if held, entered, err = h.d.Store.Fail(ctx, ref, token, f, rejected, now.Add(wait), now); err != nil || !held || !entered {
			return err
		}
		return h.recordFailing(ctx, now, c, ref.Kind, f)
	})
	switch {
	case err != nil:
		return Failed, fmt.Errorf("recording the failure: %w", err)
	case !held:
		return LeaseLost, nil
	}
	h.observeLag(ctx, ref, scope, due.Stream.Live())
	return Failed, nil
}

// recordFailing records sink.failing, which the governance record derives the
// event webhook subscriptions are sent from.
func (h *Deliver) recordFailing(ctx context.Context, now time.Time, c ClaimedSink, kind sinks.EventKind, f sinks.Failure) error {
	r := governance.Record{
		Project: c.Sink.Project, Action: governance.EventSinkFailing,
		Entity: governance.Entity{Kind: "sink", ID: c.Sink.ID.String(), Key: c.Sink.Name},
		After: map[string]any{
			"sink":       map[string]any{"id": c.Sink.ID.String(), "name": c.Sink.Name, "kind": string(c.Sink.Kind)},
			"event_kind": string(kind),
			"failure":    failureDoc(f, now),
		},
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, governance.Request{Actor: governance.SinkDeliveryActor}, r)
	if err != nil {
		return err
	}
	return h.d.Audit.Record(ctx, entry)
}

// failureDoc is a failure as the audit log and the events derived from it
// tell it: class, status and code, null where the target gave none, and when.
func failureDoc(f sinks.Failure, at time.Time) map[string]any {
	doc := map[string]any{"class": string(f.Class), "status": nil, "code": nil, "at": at.UTC().Format(time.RFC3339Nano)}
	if f.Status != 0 {
		doc["status"] = f.Status
	}
	if f.Code != "" {
		doc["code"] = f.Code
	}
	return doc
}

// observeLag tells the observer how far behind a stream at from is. A failure
// to read it is not worth failing an attempt that was recorded.
func (h *Deliver) observeLag(ctx context.Context, ref StreamRef, scope sinks.Scope, from events.Cursor) {
	at, ok, err := h.d.Rows.OldestUnread(ctx, ref.Kind, scope, from)
	if err != nil {
		return
	}
	lag := time.Duration(0)
	if ok {
		lag = max(h.now().Sub(at), 0)
	}
	h.d.Observer.Lag(ref.Sink, ref.Kind, lag)
}
