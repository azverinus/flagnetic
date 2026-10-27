package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Webhooks is the repository of webhook subscriptions.
type Webhooks struct {
	pool *pgxpool.Pool
	// projects takes the project row lock the same way change requests do.
	projects *ChangeRequests
}

// NewWebhooks returns the repository on pool.
func NewWebhooks(pool *pgxpool.Pool) *Webhooks {
	return &Webhooks{pool: pool, projects: NewChangeRequests(pool)}
}

func (s *Webhooks) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// LockProject locks project's row for the unit of work, or answers
// appgovernance.ErrNotFound when there is none.
func (s *Webhooks) LockProject(ctx context.Context, project workspace.ProjectID) error {
	return s.projects.LockProject(ctx, project)
}

// instanceWebhooksLock is the advisory lock the instance's subscriptions are
// written under, keyed by schema so instances sharing a database never wait
// on one another.
const instanceWebhooksLock = `SELECT pg_advisory_xact_lock(hashtext('flagnetic.governance.instance_webhooks@' || current_schema()))`

// LockInstanceWebhooks takes, until the transaction ends, the lock the
// instance's subscriptions are written under.
func (s *Webhooks) LockInstanceWebhooks(ctx context.Context) error {
	return lockInstanceWebhooks(ctx, s.db(ctx))
}

func lockInstanceWebhooks(ctx context.Context, db pgxtx.DB) error {
	if _, err := db.Exec(ctx, instanceWebhooksLock); err != nil {
		return fmt.Errorf("locking the instance's webhook subscriptions: %w", err)
	}
	return nil
}

// ofScope selects the subscriptions of the project $1, or the instance's
// when $1 is null, their project read from column.
func ofScope(column string) string {
	return `(` + column + ` = $1 OR ($1::uuid IS NULL AND ` + column + ` IS NULL))`
}

// EnvironmentKeys returns the keys of project's environments.
func (s *Webhooks) EnvironmentKeys(ctx context.Context, project workspace.ProjectID) ([]string, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT key FROM environments WHERE project_id = $1`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading environment keys: %w", err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("reading environment keys: %w", err)
	}
	return keys, nil
}

// webhookColumns are a subscription's with its creator's name as it is now.
const webhookColumns = `w.id, w.project_id, w.name, w.url, w.events, w.environments, w.enabled, w.disabled_reason,
	w.secret_sealed, w.previous_secret_sealed, w.previous_secret_expires_at, w.created_by_member_id,
	w.created_by_token_id, coalesce(m.display_name, t.name, ''), w.last_success_at, w.consecutive_failures,
	w.enabled_at, w.created_at, w.updated_at, w.version`

const webhookFrom = ` FROM webhook_subscriptions w
	LEFT JOIN members m ON m.id = w.created_by_member_id
	LEFT JOIN api_tokens t ON t.id = w.created_by_token_id`

func scanWebhook(row pgx.Row) (governance.Webhook, error) {
	var (
		w                        governance.Webhook
		wk                       [16]byte
		pk, member, token        *[16]byte
		reason, previous         *string
		creatorName              string
		previousExpires, success *time.Time
	)
	if err := row.Scan(&wk, &pk, &w.Name, &w.URL, &w.Events, &w.Environments, &w.Enabled, &reason, &w.SealedSecret,
		&previous, &previousExpires, &member, &token, &creatorName, &success, &w.ConsecutiveFailures, &w.EnabledAt,
		&w.CreatedAt, &w.UpdatedAt, &w.Version); err != nil {
		return w, err
	}
	w.ID = id.FromBytes[governance.Webhook](wk)
	if pk != nil {
		w.Project = id.FromBytes[workspace.Project](*pk)
	}
	if reason != nil {
		w.DisabledReason = governance.DisabledReason(*reason)
	}
	if previous != nil {
		w.PreviousSealedSecret = *previous
	}
	switch {
	case member != nil:
		m := id.FromBytes[access.Member](*member)
		w.CreatedBy = &governance.Author{Member: &m, Name: creatorName}
	case token != nil:
		t := id.FromBytes[access.APIToken](*token)
		w.CreatedBy = &governance.Author{Token: &t, Name: creatorName}
	}
	w.PreviousSecretExpiresAt, w.LastSuccessAt = utc(previousExpires), utc(success)
	w.EnabledAt, w.CreatedAt, w.UpdatedAt = w.EnabledAt.UTC(), w.CreatedAt.UTC(), w.UpdatedAt.UTC()
	return w, nil
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// Webhooks returns project's subscriptions, the oldest first; the
// instance's for a zero project.
func (s *Webhooks) Webhooks(ctx context.Context, project workspace.ProjectID) ([]governance.Webhook, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+webhookColumns+webhookFrom+` WHERE `+ofScope("w.project_id")+` ORDER BY w.id`,
		optional(project))
	if err != nil {
		return nil, fmt.Errorf("listing webhook subscriptions: %w", err)
	}
	defer rows.Close()
	out := []governance.Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a webhook subscription: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Webhook returns project's subscription with webhook, the instance's for a
// zero project, or appgovernance.ErrNotFound.
func (s *Webhooks) Webhook(ctx context.Context, project workspace.ProjectID, webhook governance.WebhookID,
) (governance.Webhook, error) {
	w, err := scanWebhook(s.db(ctx).QueryRow(ctx, `SELECT `+webhookColumns+webhookFrom+`
		WHERE `+ofScope("w.project_id")+` AND w.id = $2`, optional(project), webhook.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return w, appgovernance.ErrNotFound
	}
	if err != nil {
		return w, fmt.Errorf("reading a webhook subscription: %w", err)
	}
	return w, nil
}

// CreateWebhook stores w.
func (s *Webhooks) CreateWebhook(ctx context.Context, w governance.Webhook) error {
	var project, member, token any
	if !w.Project.IsZero() {
		project = w.Project.Bytes()
	}
	if w.CreatedBy != nil && w.CreatedBy.Member != nil {
		member = w.CreatedBy.Member.Bytes()
	}
	if w.CreatedBy != nil && w.CreatedBy.Token != nil {
		token = w.CreatedBy.Token.Bytes()
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO webhook_subscriptions (id, project_id, name, url, events, environments, enabled, disabled_reason,
		    secret_sealed, created_by_member_id, created_by_token_id, consecutive_failures, enabled_at, created_at,
		    updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		w.ID.Bytes(), project, w.Name, w.URL, w.Events, w.Environments, w.Enabled, reasonOf(w), w.SealedSecret, member,
		token, w.ConsecutiveFailures, w.EnabledAt, w.CreatedAt, w.UpdatedAt, w.Version)
	if err != nil {
		return fmt.Errorf("creating a webhook subscription: %w", err)
	}
	return nil
}

func reasonOf(w governance.Webhook) any {
	if w.DisabledReason == "" {
		return nil
	}
	return string(w.DisabledReason)
}

// UpdateWebhook stores what an edit changes of w, which its caller read
// under the project's lock. Its secrets, creator and
// delivery counters are left to the operations that change them.
func (s *Webhooks) UpdateWebhook(ctx context.Context, w governance.Webhook) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE webhook_subscriptions
		SET name = $3, url = $4, events = $5, environments = $6, enabled = $7, disabled_reason = $8, enabled_at = $9,
		    updated_at = $10, version = $11
		WHERE `+ofScope("project_id")+` AND id = $2`,
		optional(w.Project), w.ID.Bytes(), w.Name, w.URL, w.Events, w.Environments, w.Enabled, reasonOf(w), w.EnabledAt,
		w.UpdatedAt, w.Version)
	if err != nil {
		return fmt.Errorf("updating a webhook subscription: %w", err)
	}
	return nil
}

// DeleteWebhook deletes project's subscription with webhook, the instance's
// for a zero project.
func (s *Webhooks) DeleteWebhook(ctx context.Context, project workspace.ProjectID, webhook governance.WebhookID) error {
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM webhook_subscriptions WHERE `+ofScope("project_id")+` AND id = $2`,
		optional(project), webhook.Bytes())
	if err != nil {
		return fmt.Errorf("deleting a webhook subscription: %w", err)
	}
	return nil
}

// EnableWebhook stores w's enabling, which its caller read under the
// project's lock: enabled, with no reason and no failures, its window
// starting at its enable time.
func (s *Webhooks) EnableWebhook(ctx context.Context, w governance.Webhook) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE webhook_subscriptions
		SET enabled = $3, disabled_reason = $4, consecutive_failures = $5, enabled_at = $6, updated_at = $7, version = $8
		WHERE `+ofScope("project_id")+` AND id = $2`,
		optional(w.Project), w.ID.Bytes(), w.Enabled, reasonOf(w), w.ConsecutiveFailures, w.EnabledAt, w.UpdatedAt, w.Version)
	if err != nil {
		return fmt.Errorf("enabling a webhook subscription: %w", err)
	}
	return nil
}

// RotateWebhookSecret stores w's secrets, which its caller read under the
// project's lock.
func (s *Webhooks) RotateWebhookSecret(ctx context.Context, w governance.Webhook) error {
	var previous any
	if w.PreviousSealedSecret != "" {
		previous = w.PreviousSealedSecret
	}
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE webhook_subscriptions
		SET secret_sealed = $3, previous_secret_sealed = $4, previous_secret_expires_at = $5, updated_at = $6, version = $7
		WHERE `+ofScope("project_id")+` AND id = $2`,
		optional(w.Project), w.ID.Bytes(), w.SealedSecret, previous, w.PreviousSecretExpiresAt, w.UpdatedAt, w.Version)
	if err != nil {
		return fmt.Errorf("rotating a webhook subscription's secret: %w", err)
	}
	return nil
}
