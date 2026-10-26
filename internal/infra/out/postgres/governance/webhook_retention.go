package governance

import (
	"context"
	"fmt"
	"time"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
)

var _ appgovernance.WebhookRetentionStore = (*Deliveries)(nil)

// TryLockWebhookRetention takes the retention lock until the transaction
// ends, without waiting; false when another replica holds it. The key names
// the schema too, so instances sharing a database in separate schemas prune
// independently.
func (s *Deliveries) TryLockWebhookRetention(ctx context.Context) (bool, error) {
	var held bool
	err := s.db(ctx).QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtext('flagnetic.governance.webhook_retention@' || current_schema()))`).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("taking the webhook retention lock: %w", err)
	}
	return held, nil
}

// PruneDeliveries deletes at most limit deliveries no longer pending made
// before cutoff.
func (s *Deliveries) PruneDeliveries(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		DELETE FROM webhook_deliveries WHERE id IN (
		    SELECT id FROM webhook_deliveries WHERE status <> 'pending' AND created_at < $1 LIMIT $2)`,
		cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("pruning webhook deliveries: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ForgetExpiredSecrets clears every previous secret whose overlap ended by
// now. It is no edit of the subscription: its version stays.
func (s *Deliveries) ForgetExpiredSecrets(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE webhook_subscriptions SET previous_secret_sealed = NULL, previous_secret_expires_at = NULL
		WHERE previous_secret_expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("forgetting expired webhook secrets: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
