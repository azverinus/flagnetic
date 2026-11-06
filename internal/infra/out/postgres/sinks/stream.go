package sinks

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The stream reader of a sink is the events adapter's: a batch is chosen by
// the same bound below the snapshot horizon and re-read by the same range
// condition, narrowed to the sink's project and environments.

// rawTable is the raw table the stream of kind reads.
func rawTable(kind sinks.EventKind) (string, error) {
	table, ok := pgevents.RawTable(events.EventType(kind))
	if !ok {
		return "", fmt.Errorf("no raw event table for the event kind %q", kind)
	}
	return table, nil
}

// pgScope is scope as the events adapter's reader takes it.
func pgScope(scope sinks.Scope) pgevents.Scope {
	return pgevents.Scope{Project: scope.Project, Environments: scope.Environments}
}

// Scope returns the scope of a stream of project's sink naming the
// environments with keys: their IDs, or every environment when none. A key
// whose environment was deleted names nothing, and a sink that names some and
// none exists reads nothing, which is ok false: an empty list of environments
// would read the whole project.
func (s *Store) Scope(ctx context.Context, project workspace.ProjectID, keys []string) (sinks.Scope, bool, error) {
	scope := sinks.Scope{Project: project}
	if len(keys) == 0 {
		return scope, true, nil
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT id FROM environments WHERE project_id = $1 AND key = ANY($2)`, project.Bytes(), keys)
	if err != nil {
		return scope, false, fmt.Errorf("resolving environment keys: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[[16]byte])
	if err != nil {
		return scope, false, fmt.Errorf("resolving environment keys: %w", err)
	}
	for _, b := range ids {
		scope.Environments = append(scope.Environments, id.FromBytes[workspace.Environment](b))
	}
	return scope, len(ids) > 0, nil
}

// NextBatch returns the next batch of at most limit rows of the stream of
// kind in scope past from, of transactions below the snapshot horizon. The
// rows of other projects and environments are not in it; a batch short of
// limit, empty or not, ends past them at the horizon, so the stream's cursor
// passes them.
func (s *Store) NextBatch(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, from events.Cursor, limit int, until *events.Cursor) (sinks.Batch, error) {
	table, err := rawTable(kind)
	if err != nil {
		return sinks.Batch{}, err
	}
	b, err := pgevents.BoundUntil(ctx, s.db(ctx), table, pgScope(scope), from, until, limit)
	return sinks.Batch{From: b.From, To: b.To, Rows: b.Rows}, err
}

// OldestUnread returns when the first row of the stream of kind in scope
// past from was received, held back by the horizon or not; ok is false when
// the stream has read every row its filter admits. The age of that row is
// the stream's lag, and shows how far a long transaction anywhere in the
// database holds it back.
func (s *Store) OldestUnread(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, from events.Cursor) (at time.Time, ok bool, err error) {
	table, err := rawTable(kind)
	if err != nil {
		return time.Time{}, false, err
	}
	return pgevents.OldestAfter(ctx, s.db(ctx), table, pgScope(scope), from)
}

// Tail returns the position of the last row of kind below the snapshot
// horizon.
func (s *Store) Tail(ctx context.Context, kind sinks.EventKind) (events.Cursor, error) {
	table, err := rawTable(kind)
	if err != nil {
		return events.Cursor{}, err
	}
	return pgevents.Tail(ctx, s.db(ctx), table)
}

// Before returns the position just before the first row of kind in scope
// received at or after from; ok is false when there is none.
func (s *Store) Before(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, from time.Time) (events.Cursor, bool, error) {
	table, err := rawTable(kind)
	if err != nil {
		return events.Cursor{}, false, err
	}
	return pgevents.Before(ctx, s.db(ctx), table, pgScope(scope), from)
}

// Last returns the position of the last row of kind in scope received at or
// before to, below the snapshot horizon; ok is false when there is none.
func (s *Store) Last(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, to time.Time) (events.Cursor, bool, error) {
	table, err := rawTable(kind)
	if err != nil {
		return events.Cursor{}, false, err
	}
	return pgevents.Last(ctx, s.db(ctx), table, pgScope(scope), to)
}

// ReadBatch reads the rows of batch b of the stream of kind in scope, with
// the keys of their project and environment, in the order they were
// committed. Every read of one batch finds the same rows, whichever replica
// reads it and however often, since a batch holds only rows of transactions
// that had ended when it was chosen.
func (s *Store) ReadBatch(ctx context.Context, kind sinks.EventKind, scope sinks.Scope, b sinks.Batch) (sinks.Page, error) {
	where, args := pgevents.Batch{From: b.From, To: b.To, Rows: b.Rows}.Where("r")
	scoped, scopeArgs := pgScope(scope).Where("r", len(args)+1)
	if scoped != "" {
		where += " AND " + scoped
	}
	args = append(args, scopeArgs...)
	switch kind {
	case sinks.EventExposure:
		return s.readExposures(ctx, where, args)
	case sinks.EventAlias:
		return s.readAliases(ctx, where, args)
	case sinks.EventEvaluationSummary:
		return s.readSummaries(ctx, where, args)
	default:
		return sinks.Page{}, fmt.Errorf("no rows of the event kind %q", kind)
	}
}

func (s *Store) readExposures(ctx context.Context, where string, args []any) (sinks.Page, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT r.event_id::text, p.key, env.key, r.experiment_key, r.flag_key, r.variation_key, r.unit_type, r.unit_id,
			r.evaluation_reason, r.override, coalesce(r.platform, ''), coalesce(r.app_version, ''), r.sdk_name,
			r.sdk_version, r.ruleset_version, r.occurred_at, r.received_at
		FROM exposure_events r
		JOIN projects p ON p.id = r.project_id
		LEFT JOIN environments env ON env.id = r.environment_id
		WHERE `+where+` ORDER BY r.xid, r.seq`, args...)
	if err != nil {
		return sinks.Page{}, fmt.Errorf("reading exposures: %w", err)
	}
	return collect(rows, func(row pgx.Rows) (sinks.Row, bool, error) {
		var (
			x                      sinks.ExposureRow
			environment            *string
			occurredAt, receivedAt time.Time
		)
		if err := row.Scan(&x.EventID, &x.ProjectKey, &environment, &x.ExperimentKey, &x.FlagKey, &x.VariationKey,
			&x.UnitType, &x.UnitID, &x.EvaluationReason, &x.Override, &x.Platform, &x.AppVersion, &x.SDKName,
			&x.SDKVersion, &x.RulesetVersion, &occurredAt, &receivedAt); err != nil {
			return nil, false, err
		}
		if environment == nil {
			return nil, false, nil
		}
		x.EnvironmentKey, x.OccurredAt, x.ReceivedAt = *environment, occurredAt.UTC(), receivedAt.UTC()
		return x, true, nil
	})
}

func (s *Store) readAliases(ctx context.Context, where string, args []any) (sinks.Page, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT r.event_id::text, p.key, env.key, r.from_unit_type, r.from_unit_id, r.to_unit_type, r.to_unit_id,
			coalesce(r.platform, ''), r.sdk_name, r.sdk_version, r.occurred_at, r.received_at
		FROM alias_events r
		JOIN projects p ON p.id = r.project_id
		LEFT JOIN environments env ON env.id = r.environment_id
		WHERE `+where+` ORDER BY r.xid, r.seq`, args...)
	if err != nil {
		return sinks.Page{}, fmt.Errorf("reading aliases: %w", err)
	}
	return collect(rows, func(row pgx.Rows) (sinks.Row, bool, error) {
		var (
			a                      sinks.AliasRow
			environment            *string
			occurredAt, receivedAt time.Time
		)
		if err := row.Scan(&a.EventID, &a.ProjectKey, &environment, &a.FromUnitType, &a.FromUnitID, &a.ToUnitType,
			&a.ToUnitID, &a.Platform, &a.SDKName, &a.SDKVersion, &occurredAt, &receivedAt); err != nil {
			return nil, false, err
		}
		if environment == nil {
			return nil, false, nil
		}
		a.EnvironmentKey, a.OccurredAt, a.ReceivedAt = *environment, occurredAt.UTC(), receivedAt.UTC()
		return a, true, nil
	})
}

func (s *Store) readSummaries(ctx context.Context, where string, args []any) (sinks.Page, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT r.source, r.event_id::text, r.xid::text, r.seq, p.key, env.key, r.flag_key, coalesce(r.variation_key, ''),
			r.evaluation_reason, r.count, r.period_start, r.period_end, r.sdk_name, r.sdk_version,
			coalesce(r.platform, ''), coalesce(r.app_version, ''), r.ruleset_version, r.received_at
		FROM evaluation_summary_events r
		JOIN projects p ON p.id = r.project_id
		LEFT JOIN environments env ON env.id = r.environment_id
		WHERE `+where+` ORDER BY r.xid, r.seq`, args...)
	if err != nil {
		return sinks.Page{}, fmt.Errorf("reading evaluation summaries: %w", err)
	}
	return collect(rows, func(row pgx.Rows) (sinks.Row, bool, error) {
		var (
			m                                sinks.SummaryRow
			event, xid                       *string
			seq                              int64
			environment                      *string
			windowStart, windowEnd, received time.Time
		)
		if err := row.Scan(&m.Source, &event, &xid, &seq, &m.ProjectKey, &environment, &m.FlagKey, &m.VariationKey,
			&m.EvaluationReason, &m.Count, &windowStart, &windowEnd, &m.SDKName, &m.SDKVersion, &m.Platform,
			&m.AppVersion, &m.RulesetVersion, &received); err != nil {
			return nil, false, err
		}
		if environment == nil {
			return nil, false, nil
		}
		// A row the server wrote for the evaluations it answered has no event
		// ID, and takes the one its position gives.
		if event != nil {
			m.ReportID = *event
		} else {
			x, err := strconv.ParseUint(*xid, 10, 64)
			if err != nil {
				return nil, false, fmt.Errorf("reading a transaction ID: %w", err)
			}
			m.ReportID = sinks.ReportID(x, seq)
		}
		m.EnvironmentKey, m.WindowStart, m.WindowEnd, m.ReceivedAt = *environment, windowStart.UTC(), windowEnd.UTC(), received.UTC()
		return m, true, nil
	})
}

// collect reads every row of rows with read, which answers false for one to
// pass over, counted as skipped.
func collect(rows pgx.Rows, read func(pgx.Rows) (sinks.Row, bool, error)) (sinks.Page, error) {
	defer rows.Close()
	var page sinks.Page
	for rows.Next() {
		row, keep, err := read(rows)
		if err != nil {
			return sinks.Page{}, fmt.Errorf("reading a row to export: %w", err)
		}
		if !keep {
			page.Skipped++
			continue
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return sinks.Page{}, fmt.Errorf("reading rows to export: %w", err)
	}
	return page, nil
}
