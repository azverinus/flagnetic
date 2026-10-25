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

// Record is how every context records its audit entries: each context
// declares an audit port of its own and this service satisfies all of them.
// Recording runs in the writing transaction, so an entry is kept exactly when
// the change it records is. Being the one place every audited write passes,
// it is where what an entry sets in motion beyond the log belongs, without
// any other context knowing of it: it supersedes the change requests the
// entry's write moved.
type Record struct {
	log            AuditAppender
	changeRequests WaitingChangeRequests
	ids            *id.Source
}

// NewRecord returns the service appending to log and superseding the change
// requests of changeRequests, its entries identified from ids.
func NewRecord(log AuditAppender, changeRequests WaitingChangeRequests, ids *id.Source) *Record {
	return &Record{log: log, changeRequests: changeRequests, ids: ids}
}

// Record appends e to the audit log in the unit of work ctx carries, then
// supersedes every open or approved change request e's write moved. An
// entry recorded while a change request is applied carries its identifier,
// whichever context wrote the change, so an auditor traces the change to
// its approval, and that change request is not superseded by it.
func (r *Record) Record(ctx context.Context, e governance.Entry) error {
	if applying, ok := ctx.Value(applyingKey{}).(governance.ChangeRequestID); ok {
		e.ChangeRequest = applying
	}
	if err := r.keep(ctx, e); err != nil {
		return err
	}
	return r.supersede(ctx, e)
}

// keep appends e, recorded or set in motion by another entry, to the log.
func (r *Record) keep(ctx context.Context, e governance.Entry) error { return r.log.Append(ctx, e) }

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
	for _, before := range governance.Supersedes(e, candidates) {
		after, err := before.Supersede(e.ID, e.OccurredAt)
		if err != nil {
			return err
		}
		if err = r.changeRequests.UpdateChangeRequest(ctx, after); err != nil {
			return err
		}
		entry, err := governance.NewEntry(id.New[governance.Entry](r.ids), e.OccurredAt,
			governance.Request{Actor: e.Actor, ID: e.RequestID}, changeRequestRecord("change_request.superseded", &before, after))
		if err != nil {
			return err
		}
		if err = r.keep(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

type applyingKey struct{}

// applying returns ctx carrying that the change request with changeRequest
// is being applied in it.
func applying(ctx context.Context, changeRequest governance.ChangeRequestID) context.Context {
	return context.WithValue(ctx, applyingKey{}, changeRequest)
}
