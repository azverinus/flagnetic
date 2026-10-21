package access

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// AuthRetentionStore deletes what signing in leaves behind once it can no
// longer matter.
type AuthRetentionStore interface {
	// TryLockAuthRetention takes, until the transaction ends, the lock that
	// lets one replica purge at a time; false when another holds it.
	TryLockAuthRetention(ctx context.Context) (bool, error)
	// PurgeSessions deletes at most limit sessions the cutoffs end.
	PurgeSessions(ctx context.Context, cut access.SessionCutoffs, limit int) (int, error)
	// PurgeEmailFailures deletes at most limit emails' failures that no lock
	// holds and whose last failure is past the cutoff.
	PurgeEmailFailures(ctx context.Context, cut access.FailureCutoffs, limit int) (int, error)
	// PurgeSourceFailures deletes at most limit sources' failures whose
	// window is over.
	PurgeSourceFailures(ctx context.Context, cut access.FailureCutoffs, limit int) (int, error)
}

// AuthRetentionBatch is how many rows one transaction of the purge deletes,
// so a backlog never holds locks or a transaction open for long.
const AuthRetentionBatch = 1000

// AuthPurged counts the rows one purge deleted.
type AuthPurged struct {
	Sessions       int
	EmailFailures  int
	SourceFailures int
}

// PurgeAuthRecords deletes ended sessions, emails' failures that no longer
// count and sources' windows that are over, so their tables do not grow
// forever.
type PurgeAuthRecords struct {
	tx       Transactor
	store    AuthRetentionStore
	clock    clock.Clock
	timeouts access.SessionTimeouts
}

// NewPurgeAuthRecords returns the handler.
func NewPurgeAuthRecords(tx Transactor, store AuthRetentionStore, c clock.Clock) *PurgeAuthRecords {
	return &PurgeAuthRecords{tx: tx, store: store, clock: c, timeouts: access.DefaultSessionTimeouts}
}

// errPurgingElsewhere stops a purge when another replica holds the lock: it
// is doing the same work, and doing it twice would only contend.
var errPurgingElsewhere = errors.New("access: another replica is purging")

// Handle purges in batches, each in a transaction of its own holding the
// purge lock, until a batch finds less than a full batch to delete. When
// another replica holds the lock it leaves the work to it and returns what
// it purged so far.
func (h *PurgeAuthRecords) Handle(ctx context.Context) (AuthPurged, error) {
	now := h.clock.Now()
	sessions, failures := access.EndedSessionCutoffs(now, h.timeouts), access.AttemptCutoffs(now)
	var purged AuthPurged
	for _, step := range []struct {
		count *int
		purge func(ctx context.Context) (int, error)
	}{
		{&purged.Sessions, func(ctx context.Context) (int, error) {
			return h.store.PurgeSessions(ctx, sessions, AuthRetentionBatch)
		}},
		{&purged.EmailFailures, func(ctx context.Context) (int, error) {
			return h.store.PurgeEmailFailures(ctx, failures, AuthRetentionBatch)
		}},
		{&purged.SourceFailures, func(ctx context.Context) (int, error) {
			return h.store.PurgeSourceFailures(ctx, failures, AuthRetentionBatch)
		}},
	} {
		err := h.batches(ctx, step.count, step.purge)
		if errors.Is(err, errPurgingElsewhere) {
			return purged, nil
		}
		if err != nil {
			return purged, err
		}
	}
	return purged, nil
}

// batches runs purge, each time under the lock, until it deletes less than
// a full batch, adding what it deletes to count.
func (h *PurgeAuthRecords) batches(ctx context.Context, count *int, purge func(context.Context) (int, error)) error {
	for {
		var n int
		err := h.tx.Within(ctx, func(ctx context.Context) error {
			held, err := h.store.TryLockAuthRetention(ctx)
			if err != nil {
				return err
			}
			if !held {
				return errPurgingElsewhere
			}
			n, err = purge(ctx)
			return err
		})
		if err != nil {
			return err
		}
		*count += n
		if n < AuthRetentionBatch {
			return nil
		}
	}
}
