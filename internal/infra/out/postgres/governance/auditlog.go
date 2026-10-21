// Package governance is the PostgreSQL repository of the governance context:
// the audit log.
package governance

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// AuditLog writes audit entries, each in the unit of work of the change it
// records.
type AuditLog struct {
	pool  *pgxpool.Pool
	clock clock.Clock
	ids   *id.Source
}

// NewAuditLog returns the audit log on pool, timing and identifying entries
// with c and ids.
func NewAuditLog(pool *pgxpool.Pool, c clock.Clock, ids *id.Source) *AuditLog {
	return &AuditLog{pool: pool, clock: c, ids: ids}
}

// Record writes the entry of r, naming the actor and request ctx carries.
func (l *AuditLog) Record(ctx context.Context, r governance.Record) error {
	e, err := governance.NewEntry(ctx, id.New[governance.Entry](l.ids), l.clock.Now(), r)
	if err != nil {
		return err
	}
	before, err := encode(e.Before)
	if err != nil {
		return fmt.Errorf("audit entry %s: before: %w", e.Action, err)
	}
	after, err := encode(e.After)
	if err != nil {
		return fmt.Errorf("audit entry %s: after: %w", e.Action, err)
	}
	_, err = pgxtx.Querier(ctx, l.pool).Exec(ctx, `
		INSERT INTO audit_entries (id, occurred_at, project_id, environment_id,
		    actor_kind, actor_id, actor_name, actor_member_id,
		    action, entity_kind, entity_id, entity_key, before, after, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid, $9, $10, $11, $12, $13, $14, NULLIF($15, ''))`,
		e.ID.Bytes(), e.OccurredAt, optional(e.Project), optional(e.Environment),
		string(e.Actor.Kind), e.Actor.ID, e.Actor.Name, e.Actor.MemberID,
		e.Action, e.Entity.Kind, e.Entity.ID, e.Entity.Key, before, after, e.RequestID)
	return err
}

// encode returns v as JSON, or nil for no value.
func encode(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// optional returns an identifier's bytes, or nil for the zero identifier.
func optional[T any](v id.Of[T]) any {
	if v.IsZero() {
		return nil
	}
	return v.Bytes()
}

// Latest returns the newest limit entries of the whole instance, newest
// first.
func (l *AuditLog) Latest(ctx context.Context, limit int) ([]governance.Entry, error) {
	rows, err := pgxtx.Querier(ctx, l.pool).Query(ctx, `
		SELECT id, occurred_at, project_id, environment_id, actor_kind, actor_id, actor_name,
		       coalesce(actor_member_id::text, ''), action, entity_kind, entity_id, entity_key,
		       before, after, coalesce(request_id, '')
		FROM audit_entries
		ORDER BY id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []governance.Entry
	for rows.Next() {
		var (
			e                    governance.Entry
			entryID              [16]byte
			project, environment *[16]byte
			kind                 string
			before, after        []byte
		)
		if err := rows.Scan(&entryID, &e.OccurredAt, &project, &environment, &kind, &e.Actor.ID, &e.Actor.Name,
			&e.Actor.MemberID, &e.Action, &e.Entity.Kind, &e.Entity.ID, &e.Entity.Key, &before, &after, &e.RequestID); err != nil {
			return nil, err
		}
		e.ID, e.Actor.Kind = id.FromBytes[governance.Entry](entryID), governance.ActorKind(kind)
		if project != nil {
			e.Project = id.FromBytes[governance.Project](*project)
		}
		if environment != nil {
			e.Environment = id.FromBytes[governance.Environment](*environment)
		}
		if before != nil {
			e.Before = json.RawMessage(before)
		}
		if after != nil {
			e.After = json.RawMessage(after)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
