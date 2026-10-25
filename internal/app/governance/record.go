package governance

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
)

// AuditAppender appends an entry to the audit log in the unit of work ctx
// carries.
type AuditAppender interface {
	Append(ctx context.Context, e governance.Entry) error
}

// Record is how every context records its audit entries: each context
// declares an audit port of its own and this service satisfies all of them.
// Recording runs in the writing transaction, so an entry is kept exactly when
// the change it records is. Being the one place every audited write passes,
// it is where what an entry sets in motion beyond the log belongs, without
// any other context knowing of it.
type Record struct {
	log AuditAppender
}

// NewRecord returns the service appending to log.
func NewRecord(log AuditAppender) *Record { return &Record{log: log} }

// Record appends e to the audit log in the unit of work ctx carries. An
// entry recorded while a change request is applied carries its identifier,
// whichever context wrote the change, so an auditor traces the change to
// its approval.
func (r *Record) Record(ctx context.Context, e governance.Entry) error {
	if applying, ok := ctx.Value(applyingKey{}).(governance.ChangeRequestID); ok {
		e.ChangeRequest = applying
	}
	return r.log.Append(ctx, e)
}

type applyingKey struct{}

// applying returns ctx carrying that the change request with changeRequest
// is being applied in it.
func applying(ctx context.Context, changeRequest governance.ChangeRequestID) context.Context {
	return context.WithValue(ctx, applyingKey{}, changeRequest)
}
