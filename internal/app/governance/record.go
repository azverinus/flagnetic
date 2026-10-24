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

// Record appends e to the audit log in the unit of work ctx carries.
func (r *Record) Record(ctx context.Context, e governance.Entry) error {
	return r.log.Append(ctx, e)
}
