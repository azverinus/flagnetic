package governance

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// AuditAppender appends an entry to the audit log in the unit of work ctx
// carries.
type AuditAppender interface {
	Append(ctx context.Context, e governance.Entry) error
}

// WaitingChangeRequests reads and supersedes the change requests an audit
// entry may move.
type WaitingChangeRequests interface {
	// Waiting returns project's open and approved change requests naming
	// what moved names.
	Waiting(ctx context.Context, project workspace.ProjectID, moved governance.Moved) ([]governance.ChangeRequest, error)
	// UpdateChangeRequest stores cr in place of the change request with
	// its identifier.
	UpdateChangeRequest(ctx context.Context, cr governance.ChangeRequest) error
}

// Outbox holds the catalogue events waiting to be fanned out to
// subscriptions.
type Outbox interface {
	// WantedEventTypes returns the event types the enabled subscriptions of
	// project name, `*` among them; zero project for the instance's.
	WantedEventTypes(ctx context.Context, project workspace.ProjectID) ([]string, error)
	// ScopeKeys returns the keys of project and of environment, "" for a
	// zero identifier or one that no longer exists.
	ScopeKeys(ctx context.Context, project workspace.ProjectID, environment workspace.EnvironmentID) (string, string, error)
	// Enqueue writes events to the outbox in the unit of work ctx carries.
	Enqueue(ctx context.Context, events []governance.QueuedEvent) error
}

// Record is how every context records its audit entries: each context
// declares an audit port of its own and this service satisfies all of them.
// Recording runs in the writing transaction, so an entry is kept exactly when
// the change it records is. Being the one place every audited write passes,
// it is where what an entry sets in motion beyond the log belongs, without
// any other context knowing of it: it supersedes the change requests the
// entry's write moved, and writes the catalogue events each entry causes to
// the outbox in the same transaction, so a change that rolls back notifies
// nobody.
type Record struct {
	log            AuditAppender
	changeRequests WaitingChangeRequests
	outbox         Outbox
	ids            *id.Source
}

// RecordOption configures a Record.
type RecordOption func(*Record)

// WithOutbox writes the catalogue events each entry causes to outbox,
// identified from ids. Without it, recording writes no event.
func WithOutbox(outbox Outbox, ids *id.Source) RecordOption {
	return func(r *Record) { r.outbox, r.ids = outbox, ids }
}

// NewRecord returns the service appending to log and superseding the change
// requests of changeRequests, its entries identified from ids.
func NewRecord(log AuditAppender, changeRequests WaitingChangeRequests, ids *id.Source, opts ...RecordOption) *Record {
	r := &Record{log: log, changeRequests: changeRequests, ids: ids}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Record appends e to the audit log in the unit of work ctx carries, with
// the events it causes, then supersedes every open or approved change
// request e's write moved. An entry recorded while a change request is
// applied carries its identifier, whichever context wrote the change, so an
// auditor traces the change to its approval, and that change request is not
// superseded by it.
func (r *Record) Record(ctx context.Context, e governance.Entry) error {
	if applying, ok := ctx.Value(applyingKey{}).(governance.ChangeRequestID); ok {
		e.ChangeRequest = applying
	}
	if err := r.keep(ctx, e); err != nil {
		return err
	}
	return r.supersede(ctx, e)
}

// keep appends e, recorded or set in motion by another entry, to the log,
// and the events it causes to the outbox.
func (r *Record) keep(ctx context.Context, e governance.Entry) error {
	if err := r.log.Append(ctx, e); err != nil {
		return err
	}
	return r.emit(ctx, e)
}

// supersede supersedes the change requests e moves, each recorded as
// change_request.superseded by e's actor in e's request at e's instant,
// naming e. Those entries carry no change request being applied: they
// record what the application ended, not what it wrote. A deletion records
// its entry before the rows go, so the change requests it ends are found
// and superseded before the cascade removes them.
func (r *Record) supersede(ctx context.Context, e governance.Entry) error {
	moved, ok := governance.MovedBy(e)
	if !ok {
		return nil
	}
	candidates, err := r.changeRequests.Waiting(ctx, e.Project, moved)
	if err != nil {
		return err
	}
	for _, s := range governance.Supersedes(e, candidates) {
		if err = r.changeRequests.UpdateChangeRequest(ctx, s.After); err != nil {
			return err
		}
		entry := governance.Entry{
			ID: id.New[governance.Entry](r.ids), OccurredAt: e.OccurredAt, Actor: e.Actor, RequestID: e.RequestID,
			Record: changeRequestRecord("change_request.superseded", &s.Before, s.After),
		}
		if err = r.keep(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

// emit writes the events e causes that an enabled subscription of e's scope
// wants. The subscriptions are read in the writing transaction, and nothing
// more is read or written when none wants anything, which is the common
// case: an instance without subscriptions pays one indexed read per entry.
// Which subscription receives an event, by its environments and its
// creation, is the fan-out's to decide.
func (r *Record) emit(ctx context.Context, e governance.Entry) error {
	if r.outbox == nil {
		return nil
	}
	wanted, err := r.outbox.WantedEventTypes(ctx, e.Project)
	if err != nil || len(wanted) == 0 {
		return err
	}
	var derived []governance.Derived
	for _, d := range governance.DeriveEvents(e) {
		if governance.WantsEventType(wanted, d.Type) {
			derived = append(derived, d)
		}
	}
	if len(derived) == 0 {
		return nil
	}
	// An environment's deletion is recorded before its row goes, so its key
	// is found as any other environment's is.
	projectKey, environmentKey, err := r.outbox.ScopeKeys(ctx, e.Project, e.Environment)
	if err != nil {
		return err
	}
	queued := make([]governance.QueuedEvent, len(derived))
	for i, d := range derived {
		event := governance.EntryEvent(id.New[governance.Event](r.ids), e, d, projectKey, environmentKey)
		if queued[i], err = event.Queue(); err != nil {
			return err
		}
	}
	return r.outbox.Enqueue(ctx, queued)
}

type applyingKey struct{}

// applying returns ctx carrying that the change request with changeRequest
// is being applied in it.
func applying(ctx context.Context, changeRequest governance.ChangeRequestID) context.Context {
	return context.WithValue(ctx, applyingKey{}, changeRequest)
}
