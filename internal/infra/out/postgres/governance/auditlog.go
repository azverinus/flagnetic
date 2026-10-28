// Package governance is the PostgreSQL repository of the governance context:
// the audit log.
package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// AuditLog writes audit entries, each in the unit of work of the change it
// records.
type AuditLog struct {
	pool *pgxpool.Pool
}

// NewAuditLog returns the audit log on pool.
func NewAuditLog(pool *pgxpool.Pool) *AuditLog { return &AuditLog{pool: pool} }

// Append writes e as the handler that made the change built it.
func (l *AuditLog) Append(ctx context.Context, e governance.Entry) error {
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
		    action, entity_kind, entity_id, entity_key, before, after, kill_switch, request_id, change_request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid, $9, $10, $11, $12, $13, $14, $15, NULLIF($16, ''), $17)`,
		e.ID.Bytes(), e.OccurredAt, optional(e.Project), optional(e.Environment),
		string(e.Actor.Kind), e.Actor.ID, e.Actor.Name, e.Actor.MemberID,
		e.Action, e.Entity.Kind, e.Entity.ID, e.Entity.Key, before, after, e.KillSwitch, e.RequestID, optional(e.ChangeRequest))
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
	return l.Entries(ctx, appgovernance.AuditFilter{}, governance.EntryID{}, limit)
}

func scanEntry(row pgx.Row) (governance.Entry, error) {
	var (
		e                    governance.Entry
		entryID              [16]byte
		project, environment *[16]byte
		changeRequest        *[16]byte
		kind                 string
		before, after        []byte
	)
	if err := row.Scan(&entryID, &e.OccurredAt, &project, &environment, &kind, &e.Actor.ID, &e.Actor.Name,
		&e.Actor.MemberID, &e.Action, &e.Entity.Kind, &e.Entity.ID, &e.Entity.Key, &before, &after, &e.KillSwitch,
		&e.RequestID, &changeRequest); err != nil {
		return governance.Entry{}, err
	}
	e.ID, e.Actor.Kind = id.FromBytes[governance.Entry](entryID), governance.ActorKind(kind)
	if project != nil {
		e.Project = id.FromBytes[workspace.Project](*project)
	}
	if environment != nil {
		e.Environment = id.FromBytes[workspace.Environment](*environment)
	}
	if changeRequest != nil {
		e.ChangeRequest = id.FromBytes[governance.ChangeRequest](*changeRequest)
	}
	if before != nil {
		e.Before = json.RawMessage(before)
	}
	if after != nil {
		e.After = json.RawMessage(after)
	}
	return e, nil
}

const entryColumns = `id, occurred_at, project_id, environment_id, actor_kind, actor_id, actor_name,
	coalesce(actor_member_id::text, ''), action, entity_kind, entity_id, entity_key,
	before, after, kill_switch, coalesce(request_id, ''), change_request_id`

// auditWhere returns the WHERE clause (or "") selecting the entries matching
// f and older than after unless it is zero, and the arguments it binds.
func auditWhere(f appgovernance.AuditFilter, after governance.EntryID) (string, []any) {
	var (
		conds []string
		args  []any
	)
	where := func(cond string, arg any) {
		args = append(args, arg)
		conds = append(conds, strings.ReplaceAll(cond, "$?", "$"+strconv.Itoa(len(args))))
	}
	if !f.Project.IsZero() {
		where("project_id = $?", f.Project.Bytes())
	}
	if !f.Actor.IsZero() {
		where("actor_member_id = $?", f.Actor.Bytes())
	}
	if f.ActorKind != "" {
		where("actor_kind = $?", string(f.ActorKind))
	}
	if f.Action != "" {
		where("action = $?", f.Action)
	}
	if f.EntityKind != "" {
		where("entity_kind = $?", f.EntityKind)
	}
	if f.EntityID != "" {
		where("entity_id = $?", f.EntityID)
	}
	if !f.Environment.IsZero() {
		where("environment_id = $?", f.Environment.Bytes())
	}
	if !f.From.IsZero() {
		where("occurred_at >= $?", f.From)
	}
	if !f.To.IsZero() {
		where("occurred_at <= $?", f.To)
	}
	if f.KillSwitch != nil {
		// Only true is indexed, by the partial index of the few kill
		// switches; false narrows what another filter or the log's order
		// yields.
		where("kill_switch = $?", *f.KillSwitch)
	}
	if !f.ChangeRequest.IsZero() {
		where("change_request_id = $?", f.ChangeRequest.Bytes())
	}
	if f.Q != "" {
		// The expression is the search index's, so the index answers it.
		where(`(entity_key || E'\n' || actor_name || E'\n' || action) ILIKE '%' || $? || '%' ESCAPE '\'`, escapeLike(f.Q))
	}
	if !after.IsZero() {
		where("id < $?", after.Bytes())
	}
	if len(conds) == 0 {
		return "", args
	}
	return ` WHERE ` + strings.Join(conds, ` AND `), args
}

// Entries returns at most limit entries matching f, older than after unless
// it is zero, newest first.
func (l *AuditLog) Entries(ctx context.Context, f appgovernance.AuditFilter, after governance.EntryID, limit int) ([]governance.Entry, error) {
	where, args := auditWhere(f, after)
	args = append(args, limit)
	rows, err := pgxtx.Querier(ctx, l.pool).Query(ctx,
		`SELECT `+entryColumns+` FROM audit_entries`+where+` ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	defer rows.Close()
	out := []governance.Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountEntries returns how many entries match f, or limit when more do, so
// the count never reads more than limit entries.
func (l *AuditLog) CountEntries(ctx context.Context, f appgovernance.AuditFilter, limit int) (int, error) {
	where, args := auditWhere(f, governance.EntryID{})
	args = append(args, limit)
	var n int
	err := pgxtx.Querier(ctx, l.pool).QueryRow(ctx,
		`SELECT count(*) FROM (SELECT 1 FROM audit_entries`+where+` LIMIT $`+strconv.Itoa(len(args))+`) AS matching`, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting audit entries: %w", err)
	}
	return n, nil
}

// EachEntry calls fn with every entry matching f, newest first, as the
// database sends it: no page is held in memory.
func (l *AuditLog) EachEntry(ctx context.Context, f appgovernance.AuditFilter, fn func(governance.Entry) error) error {
	where, args := auditWhere(f, governance.EntryID{})
	rows, err := pgxtx.Querier(ctx, l.pool).Query(ctx, `SELECT `+entryColumns+` FROM audit_entries`+where+` ORDER BY id DESC`, args...)
	if err != nil {
		return fmt.Errorf("reading audit entries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// escapeLike makes every character of s match itself in a LIKE pattern
// escaped by a backslash, so a search for 50% finds 50% and nothing else.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Entry returns the entry of project with entryID, or
// appgovernance.ErrNotFound.
func (l *AuditLog) Entry(ctx context.Context, project workspace.ProjectID, entryID governance.EntryID) (governance.Entry, error) {
	e, err := scanEntry(pgxtx.Querier(ctx, l.pool).QueryRow(ctx,
		`SELECT `+entryColumns+` FROM audit_entries WHERE project_id = $1 AND id = $2`, project.Bytes(), entryID.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return governance.Entry{}, appgovernance.ErrNotFound
	}
	return e, err
}

// RecordedVersion returns the values the newest entry of project recording
// entity in environment holds for the entity at version, its before or its
// after, whichever carries that version, and whether an entry does. Every
// version of an entity written whole into its entries is found so, also
// the first, which the before of the second write holds.
func (l *AuditLog) RecordedVersion(ctx context.Context, project workspace.ProjectID, entity governance.Entity,
	environment workspace.EnvironmentID, version int64,
) ([]byte, bool, error) {
	var values []byte
	err := pgxtx.Querier(ctx, l.pool).QueryRow(ctx, `
		SELECT CASE WHEN after -> 'version' = to_jsonb($5::bigint) THEN after ELSE before END
		FROM audit_entries
		WHERE project_id = $1 AND entity_kind = $2 AND entity_id = $3 AND environment_id = $4
		  AND (after -> 'version' = to_jsonb($5::bigint) OR before -> 'version' = to_jsonb($5::bigint))
		ORDER BY id DESC LIMIT 1`,
		project.Bytes(), entity.Kind, entity.ID, environment.Bytes(), version).Scan(&values)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading a recorded version: %w", err)
	}
	return values, true, nil
}
