package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Deliveries is the repository the delivery worker leases deliveries from
// and records their attempts in.
type Deliveries struct {
	pool *pgxpool.Pool
	// projects takes the project row lock the same way change requests do.
	projects *ChangeRequests
}

// NewDeliveries returns the repository on pool.
func NewDeliveries(pool *pgxpool.Pool) *Deliveries {
	return &Deliveries{pool: pool, projects: NewChangeRequests(pool)}
}

var _ appgovernance.DeliveryStore = (*Deliveries)(nil)

func (s *Deliveries) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// ClaimDeliveries leases to token, until until, up to limit deliveries due
// by now of enabled subscriptions, at most governance.MaxDeliveriesInFlight
// of one subscription counting the leases of every replica still running.
//
// The subscriptions with something due are locked first, skipping those
// another replica is claiming for: FOR NO KEY UPDATE conflicts with itself
// and with the counters a recorded attempt moves, not with a delivery
// inserted for the subscription, so the request path never waits on it.
// The leases are then counted and taken in a new statement, so in a
// snapshot taken after the locks: it sees every lease another replica
// committed before them.
func (s *Deliveries) ClaimDeliveries(ctx context.Context, token appgovernance.LeaseToken, now, until time.Time,
	limit int,
) ([]governance.DeliveryID, error) {
	rows, err := s.db(ctx).Query(ctx, `
		WITH due AS (
		    SELECT subscription_id, min(next_attempt_at) AS first FROM webhook_deliveries
		    WHERE status = 'pending' AND next_attempt_at <= $1 AND (lease_until IS NULL OR lease_until <= $1)
		    GROUP BY subscription_id
		)
		SELECT s.id FROM webhook_subscriptions s JOIN due ON due.subscription_id = s.id
		WHERE s.enabled
		ORDER BY due.first, s.id LIMIT $2
		FOR NO KEY UPDATE OF s SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("locking webhook subscriptions with deliveries due: %w", err)
	}
	subscriptions, err := pgx.CollectRows(rows, pgx.RowTo[[16]byte])
	if err != nil {
		return nil, fmt.Errorf("locking webhook subscriptions with deliveries due: %w", err)
	}
	if len(subscriptions) == 0 {
		return nil, nil
	}
	rows, err = s.db(ctx).Query(ctx, `
		WITH in_flight AS (
		    SELECT subscription_id, count(*) AS n FROM webhook_deliveries
		    WHERE subscription_id = ANY($1) AND status = 'pending' AND lease_until > $2
		    GROUP BY subscription_id
		), due AS (
		    SELECT id, subscription_id, next_attempt_at,
		           row_number() OVER (PARTITION BY subscription_id ORDER BY next_attempt_at, id) AS n
		    FROM webhook_deliveries
		    WHERE subscription_id = ANY($1) AND status = 'pending' AND next_attempt_at <= $2
		      AND (lease_until IS NULL OR lease_until <= $2)
		), picked AS (
		    SELECT due.id FROM due LEFT JOIN in_flight f USING (subscription_id)
		    WHERE due.n <= $3 - coalesce(f.n, 0)
		    ORDER BY due.next_attempt_at, due.id LIMIT $4
		)
		UPDATE webhook_deliveries d SET lease_until = $5, lease_token = $6
		FROM picked WHERE d.id = picked.id
		RETURNING d.id`,
		subscriptions, now, governance.MaxDeliveriesInFlight, limit, until, token.Bytes())
	if err != nil {
		return nil, fmt.Errorf("leasing webhook deliveries: %w", err)
	}
	leased, err := pgx.CollectRows(rows, pgx.RowTo[[16]byte])
	if err != nil {
		return nil, fmt.Errorf("leasing webhook deliveries: %w", err)
	}
	out := make([]governance.DeliveryID, len(leased))
	for i, d := range leased {
		out[i] = id.FromBytes[governance.Delivery](d)
	}
	return out, nil
}

// RenewLease pushes token's lease on delivery to until and returns the
// delivery with its subscription's target and secrets, the previous one
// while its overlap lasts at now; false when token no longer holds a
// pending delivery of an enabled subscription. A lease that ran out is not
// renewed even when no other replica took it yet: it no longer counts
// towards the subscription's deliveries in flight, so renewing it could put
// one more in flight than the claim allows.
func (s *Deliveries) RenewLease(ctx context.Context, delivery governance.DeliveryID, token appgovernance.LeaseToken,
	now, until time.Time,
) (appgovernance.Leased, bool, error) {
	var (
		l                 appgovernance.Leased
		did, wid, eventID [16]byte
		attempts          int
		sealed            string
		previous          *string
	)
	err := s.db(ctx).QueryRow(ctx, `
		UPDATE webhook_deliveries d SET lease_until = $4
		FROM webhook_subscriptions s
		WHERE d.id = $1 AND d.lease_token = $2 AND d.lease_until > $3 AND d.status = 'pending' AND s.id = d.subscription_id
		  AND s.enabled
		RETURNING d.id, d.subscription_id, d.event_id, d.event_type, d.body, jsonb_array_length(d.attempts), s.url,
		          s.secret_sealed, CASE WHEN s.previous_secret_expires_at > $3 THEN s.previous_secret_sealed END`,
		delivery.Bytes(), token.Bytes(), now, until).
		Scan(&did, &wid, &eventID, &l.Delivery.Type, &l.Delivery.Body, &attempts, &l.URL, &sealed, &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, false, nil
	}
	if err != nil {
		return l, false, fmt.Errorf("renewing the lease on a webhook delivery: %w", err)
	}
	l.Delivery.ID, l.Delivery.Webhook, l.Delivery.Event = id.FromBytes[governance.Delivery](did),
		id.FromBytes[governance.Webhook](wid), id.FromBytes[governance.Event](eventID)
	l.Delivery.Status, l.Delivery.Attempts = governance.DeliveryPending, make([]governance.DeliveryAttempt, attempts)
	l.SealedSecrets = []string{sealed}
	if previous != nil {
		l.SealedSecrets = append(l.SealedSecrets, *previous)
	}
	return l, true, nil
}

// LockWebhook locks the subscription's row for the unit of work ctx
// carries; a subscription deleted meanwhile locks nothing.
func (s *Deliveries) LockWebhook(ctx context.Context, webhook governance.WebhookID) error {
	if _, err := s.db(ctx).Exec(ctx, `SELECT 1 FROM webhook_subscriptions WHERE id = $1 FOR NO KEY UPDATE`,
		webhook.Bytes()); err != nil {
		return fmt.Errorf("locking a webhook subscription: %w", err)
	}
	return nil
}

// RecordAttempt appends a to delivery, sets its status and next attempt and
// releases its lease, only while token holds it.
func (s *Deliveries) RecordAttempt(ctx context.Context, delivery governance.DeliveryID, token appgovernance.LeaseToken,
	a governance.DeliveryAttempt, status governance.DeliveryStatus, next *time.Time, now time.Time,
) (bool, error) {
	encoded, err := json.Marshal(attemptValues([]governance.DeliveryAttempt{a}))
	if err != nil {
		return false, fmt.Errorf("encoding a delivery attempt: %w", err)
	}
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = $3, attempts = attempts || $4::jsonb, next_attempt_at = $5, lease_until = NULL, lease_token = NULL,
		    updated_at = $6
		WHERE id = $1 AND lease_token = $2`,
		delivery.Bytes(), token.Bytes(), string(status), encoded, next, now)
	if err != nil {
		return false, fmt.Errorf("recording a delivery attempt: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CountAttempt moves the subscription's counters by an attempt made at at.
func (s *Deliveries) CountAttempt(ctx context.Context, webhook governance.WebhookID, succeeded bool, at time.Time) error {
	query := `UPDATE webhook_subscriptions SET consecutive_failures = consecutive_failures + 1 WHERE id = $1`
	args := []any{webhook.Bytes()}
	if succeeded {
		query = `UPDATE webhook_subscriptions SET last_success_at = $2, consecutive_failures = 0 WHERE id = $1`
		args = append(args, at)
	}
	if _, err := s.db(ctx).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("counting a delivery attempt: %w", err)
	}
	return nil
}

// LockProject locks project's row for the unit of work, or answers
// appgovernance.ErrNotFound when there is none.
func (s *Deliveries) LockProject(ctx context.Context, project workspace.ProjectID) error {
	return s.projects.LockProject(ctx, project)
}

// SubscriptionOf returns the subscription with webhook, of whichever
// project, or appgovernance.ErrNotFound.
func (s *Deliveries) SubscriptionOf(ctx context.Context, webhook governance.WebhookID) (governance.Webhook, error) {
	w, err := scanWebhook(s.db(ctx).QueryRow(ctx, `SELECT `+webhookColumns+webhookFrom+` WHERE w.id = $1`, webhook.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return w, appgovernance.ErrNotFound
	}
	if err != nil {
		return w, fmt.Errorf("reading a webhook subscription: %w", err)
	}
	return w, nil
}

// DisableWebhook stores w's disabling by the platform.
func (s *Deliveries) DisableWebhook(ctx context.Context, w governance.Webhook) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE webhook_subscriptions SET enabled = $2, disabled_reason = $3, updated_at = $4, version = $5 WHERE id = $1`,
		w.ID.Bytes(), w.Enabled, reasonOf(w), w.UpdatedAt, w.Version)
	if err != nil {
		return fmt.Errorf("disabling a webhook subscription: %w", err)
	}
	return nil
}

// DropPending drops every pending delivery of webhook, leased or not.
func (s *Deliveries) DropPending(ctx context.Context, webhook governance.WebhookID, now time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'dropped', next_attempt_at = NULL, lease_until = NULL, lease_token = NULL, updated_at = $2
		WHERE subscription_id = $1 AND status = 'pending'`, webhook.Bytes(), now)
	if err != nil {
		return fmt.Errorf("dropping a webhook subscription's pending deliveries: %w", err)
	}
	return nil
}
