package governance

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// WebhookRetentionStore deletes what webhook delivery leaves behind once it
// can no longer matter.
type WebhookRetentionStore interface {
	// TryLockWebhookRetention takes, until the transaction ends, the lock
	// that lets one replica prune at a time; false when another holds it.
	TryLockWebhookRetention(ctx context.Context) (bool, error)
	// PruneDeliveries deletes at most limit deliveries that are no longer
	// pending and were made before cutoff.
	PruneDeliveries(ctx context.Context, cutoff time.Time, limit int) (int, error)
	// ForgetExpiredSecrets clears every previous secret whose overlap ended
	// by now.
	ForgetExpiredSecrets(ctx context.Context, now time.Time) (int, error)
}

// webhookRetentionBatch is how many deliveries one transaction of the
// retention round deletes, so a backlog never holds locks for long.
const webhookRetentionBatch = 1000

// WebhookPruned counts what one retention round removed.
type WebhookPruned struct {
	Deliveries int
	Secrets    int
}

// errPruningElsewhere stops a round when another replica holds the lock: it
// is doing the same work.
var errPruningElsewhere = errors.New("governance: another replica is pruning webhook deliveries")

// RunWebhookRetention keeps the delivery logs to governance.DeliveryRetention
// and drops each previous secret once its overlap is over.
type RunWebhookRetention struct {
	tx    Transactor
	store WebhookRetentionStore
	clock clock.Clock
}

// NewRunWebhookRetention returns the use case.
func NewRunWebhookRetention(tx Transactor, store WebhookRetentionStore, c clock.Clock) *RunWebhookRetention {
	return &RunWebhookRetention{tx: tx, store: store, clock: c}
}

// Handle deletes, a batch per transaction under the retention lock, the
// deliveries no longer pending made more than governance.DeliveryRetention
// ago, then clears the previous secrets whose overlap is over: nothing signs
// under them any more, and keeping one sealed only leaves one more key in a
// database dump. A pending delivery is kept whatever its age: it waits on a
// subscription a member disabled and is sent once it is enabled. When
// another replica holds the lock, it leaves the work to it.
func (h *RunWebhookRetention) Handle(ctx context.Context) (WebhookPruned, error) {
	now := h.clock.Now().UTC()
	var pruned WebhookPruned
	for {
		var n int
		err := h.locked(ctx, func(ctx context.Context) (err error) {
			n, err = h.store.PruneDeliveries(ctx, now.Add(-governance.DeliveryRetention), webhookRetentionBatch)
			return err
		})
		if errors.Is(err, errPruningElsewhere) {
			return pruned, nil
		}
		if err != nil {
			return pruned, err
		}
		pruned.Deliveries += n
		if n < webhookRetentionBatch {
			break
		}
	}
	err := h.locked(ctx, func(ctx context.Context) (err error) {
		pruned.Secrets, err = h.store.ForgetExpiredSecrets(ctx, now)
		return err
	})
	if errors.Is(err, errPruningElsewhere) {
		return pruned, nil
	}
	return pruned, err
}

// locked runs fn in a transaction holding the retention lock.
func (h *RunWebhookRetention) locked(ctx context.Context, fn func(ctx context.Context) error) error {
	return h.tx.Within(ctx, func(ctx context.Context) error {
		held, err := h.store.TryLockWebhookRetention(ctx)
		if err != nil {
			return err
		}
		if !held {
			return errPruningElsewhere
		}
		return fn(ctx)
	})
}
