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

// Outbox is the repository of the catalogue events waiting to be fanned
// out, and of the deliveries the fan-out makes of them.
type Outbox struct{ pool *pgxpool.Pool }

// NewOutbox returns the repository on pool.
func NewOutbox(pool *pgxpool.Pool) *Outbox { return &Outbox{pool: pool} }

var (
	_ appgovernance.Outbox      = (*Outbox)(nil)
	_ appgovernance.FanOutStore = (*Outbox)(nil)
)

func (o *Outbox) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, o.pool) }

// WantedEventTypes returns the event types the enabled subscriptions of
// project name; a zero project reads the instance's.
func (o *Outbox) WantedEventTypes(ctx context.Context, project workspace.ProjectID) ([]string, error) {
	rows, err := o.db(ctx).Query(ctx, `
		SELECT DISTINCT unnest(events) FROM webhook_subscriptions
		WHERE project_id IS NOT DISTINCT FROM $1 AND enabled`, optional(project))
	if err != nil {
		return nil, fmt.Errorf("reading the event types subscriptions want: %w", err)
	}
	types, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("reading the event types subscriptions want: %w", err)
	}
	return types, nil
}

// ScopeKeys returns the keys of project and environment, "" for a zero
// identifier or a row that is gone.
func (o *Outbox) ScopeKeys(ctx context.Context, project workspace.ProjectID, environment workspace.EnvironmentID,
) (string, string, error) {
	var projectKey, environmentKey string
	err := o.db(ctx).QueryRow(ctx, `
		SELECT coalesce((SELECT key FROM projects WHERE id = $1), ''),
		       coalesce((SELECT key FROM environments WHERE id = $2), '')`,
		optional(project), optional(environment)).Scan(&projectKey, &environmentKey)
	if err != nil {
		return "", "", fmt.Errorf("reading the keys of an event's scope: %w", err)
	}
	return projectKey, environmentKey, nil
}

// ApplicationEntry returns the first entry recorded under changeRequest
// that is not about a change request: the change its application wrote.
func (o *Outbox) ApplicationEntry(ctx context.Context, project workspace.ProjectID, changeRequest governance.ChangeRequestID,
) (governance.EntryID, bool, error) {
	var entry [16]byte
	err := o.db(ctx).QueryRow(ctx, `
		SELECT id FROM audit_entries
		WHERE project_id = $1 AND change_request_id = $2 AND entity_kind <> 'change_request'
		ORDER BY id LIMIT 1`, optional(project), changeRequest.Bytes()).Scan(&entry)
	if errors.Is(err, pgx.ErrNoRows) {
		return governance.EntryID{}, false, nil
	}
	if err != nil {
		return governance.EntryID{}, false, fmt.Errorf("finding the entry a change request wrote: %w", err)
	}
	return id.FromBytes[governance.Entry](entry), true, nil
}

// Enqueue writes events to the outbox.
func (o *Outbox) Enqueue(ctx context.Context, events []governance.QueuedEvent) error {
	b := &pgx.Batch{}
	for _, e := range events {
		b.Queue(`INSERT INTO webhook_outbox (id, type, project_id, environment, envelope, created_at)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)`,
			e.ID.Bytes(), e.Type, optional(e.Project), e.EnvironmentKey, json.RawMessage(e.Envelope), e.CreatedAt)
	}
	if err := o.db(ctx).SendBatch(ctx, b).Close(); err != nil {
		return fmt.Errorf("writing events to the outbox: %w", err)
	}
	return nil
}

// ClaimEvents locks up to limit of the oldest events, skipping those another
// transaction holds, and returns them oldest first.
func (o *Outbox) ClaimEvents(ctx context.Context, limit int) ([]governance.QueuedEvent, error) {
	rows, err := o.db(ctx).Query(ctx, `
		SELECT id, type, project_id, coalesce(environment, ''), envelope, created_at FROM webhook_outbox
		ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("claiming outbox events: %w", err)
	}
	defer rows.Close()
	var out []governance.QueuedEvent
	for rows.Next() {
		var (
			e       governance.QueuedEvent
			eventID [16]byte
			project *[16]byte
		)
		if err := rows.Scan(&eventID, &e.Type, &project, &e.EnvironmentKey, &e.Envelope, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("reading an outbox event: %w", err)
		}
		e.ID = id.FromBytes[governance.Event](eventID)
		if project != nil {
			e.Project = id.FromBytes[workspace.Project](*project)
		}
		e.CreatedAt = e.CreatedAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// EnabledWebhooks returns the enabled subscriptions of projects, a zero
// project standing for the instance's.
func (o *Outbox) EnabledWebhooks(ctx context.Context, projects []workspace.ProjectID) ([]governance.Webhook, error) {
	var ids [][16]byte
	instance := false
	for _, p := range projects {
		if p.IsZero() {
			instance = true
			continue
		}
		ids = append(ids, p.Bytes())
	}
	rows, err := o.db(ctx).Query(ctx, `SELECT `+webhookColumns+webhookFrom+`
		WHERE w.enabled AND (w.project_id = ANY($1) OR ($2 AND w.project_id IS NULL))`, ids, instance)
	if err != nil {
		return nil, fmt.Errorf("reading enabled webhook subscriptions: %w", err)
	}
	defer rows.Close()
	var out []governance.Webhook
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a webhook subscription: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// CreateDeliveries stores deliveries.
func (o *Outbox) CreateDeliveries(ctx context.Context, deliveries []governance.Delivery) error {
	b := &pgx.Batch{}
	for _, d := range deliveries {
		attempts, err := json.Marshal(attemptValues(d.Attempts))
		if err != nil {
			return fmt.Errorf("encoding a delivery's attempts: %w", err)
		}
		b.Queue(`INSERT INTO webhook_deliveries (id, subscription_id, event_id, event_type, body, status, attempts,
			    next_attempt_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
			d.ID.Bytes(), d.Webhook.Bytes(), d.Event.Bytes(), d.Type, json.RawMessage(d.Body), string(d.Status), attempts,
			d.NextAttemptAt, d.CreatedAt)
	}
	if err := o.db(ctx).SendBatch(ctx, b).Close(); err != nil {
		return fmt.Errorf("creating deliveries: %w", err)
	}
	return nil
}

// DeleteEvents deletes the events with ids from the outbox.
func (o *Outbox) DeleteEvents(ctx context.Context, ids []governance.EventID) error {
	raw := make([][16]byte, len(ids))
	for i, e := range ids {
		raw[i] = e.Bytes()
	}
	if _, err := o.db(ctx).Exec(ctx, `DELETE FROM webhook_outbox WHERE id = ANY($1)`, raw); err != nil {
		return fmt.Errorf("deleting fanned out events: %w", err)
	}
	return nil
}

// OutboxDepth returns how many events wait in the outbox.
func (o *Outbox) OutboxDepth(ctx context.Context) (int, error) {
	var n int
	if err := o.db(ctx).QueryRow(ctx, `SELECT count(*) FROM webhook_outbox`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting the outbox: %w", err)
	}
	return n, nil
}

// attempt is a delivery attempt as the attempts column stores it.
type attempt struct {
	N               int       `json:"n"`
	At              time.Time `json:"at"`
	StatusCode      *int      `json:"status_code"`
	LatencyMS       int64     `json:"latency_ms"`
	ResponseSnippet *string   `json:"response_snippet"`
	Error           *string   `json:"error"`
}

func attemptValues(attempts []governance.DeliveryAttempt) []attempt {
	out := make([]attempt, len(attempts))
	for i, a := range attempts {
		out[i] = attempt{
			N: a.N, At: a.At, StatusCode: a.StatusCode, LatencyMS: a.LatencyMS, ResponseSnippet: a.ResponseSnippet,
			Error: a.Error,
		}
	}
	return out
}

// deliveryColumns are a delivery's as scanDelivery reads them.
const deliveryColumns = `id, subscription_id, event_id, event_type, body, status, attempts, next_attempt_at, created_at`

func scanDelivery(row pgx.Row) (governance.Delivery, error) {
	var (
		d                     governance.Delivery
		did, webhook, eventID [16]byte
		status                string
		attempts              []attempt
		next                  *time.Time
	)
	if err := row.Scan(&did, &webhook, &eventID, &d.Type, &d.Body, &status, &attempts, &next, &d.CreatedAt); err != nil {
		return d, err
	}
	d.ID, d.Webhook, d.Event = id.FromBytes[governance.Delivery](did), id.FromBytes[governance.Webhook](webhook),
		id.FromBytes[governance.Event](eventID)
	d.Status, d.NextAttemptAt, d.CreatedAt = governance.DeliveryStatus(status), utc(next), d.CreatedAt.UTC()
	d.Attempts = make([]governance.DeliveryAttempt, len(attempts))
	for i, a := range attempts {
		d.Attempts[i] = governance.DeliveryAttempt{
			N: a.N, At: a.At.UTC(), StatusCode: a.StatusCode, LatencyMS: a.LatencyMS, ResponseSnippet: a.ResponseSnippet,
			Error: a.Error,
		}
	}
	return d, nil
}

// Deliveries returns at most limit of webhook's deliveries matching f,
// older than after (unless zero), newest first.
func (s *Webhooks) Deliveries(ctx context.Context, webhook governance.WebhookID, f appgovernance.DeliveryFilter,
	after governance.DeliveryID, limit int,
) ([]governance.Delivery, error) {
	var from, to *time.Time
	if !f.From.IsZero() {
		from = &f.From
	}
	if !f.To.IsZero() {
		to = &f.To
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT `+deliveryColumns+` FROM webhook_deliveries
		WHERE subscription_id = $1
		  AND ($2::uuid IS NULL OR id < $2)
		  AND (NULLIF($3, '') IS NULL OR status = $3)
		  AND (NULLIF($4, '') IS NULL OR event_type = $4)
		  AND ($5::timestamptz IS NULL OR created_at >= $5)
		  AND ($6::timestamptz IS NULL OR created_at <= $6)
		ORDER BY id DESC LIMIT $7`,
		webhook.Bytes(), optional(after), string(f.Status), f.Type, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("listing deliveries: %w", err)
	}
	defer rows.Close()
	out := []governance.Delivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a delivery: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Delivery returns webhook's delivery with delivery, or
// appgovernance.ErrNotFound.
func (s *Webhooks) Delivery(ctx context.Context, webhook governance.WebhookID, delivery governance.DeliveryID,
) (governance.Delivery, error) {
	d, err := scanDelivery(s.db(ctx).QueryRow(ctx, `SELECT `+deliveryColumns+` FROM webhook_deliveries
		WHERE subscription_id = $1 AND id = $2`, webhook.Bytes(), delivery.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return d, appgovernance.ErrNotFound
	}
	if err != nil {
		return d, fmt.Errorf("reading a delivery: %w", err)
	}
	return d, nil
}
