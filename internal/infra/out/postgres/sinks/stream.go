package sinks

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
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

// NextBatch returns the next batch of at most limit rows of the stream of
// kind in scope past from, of transactions below the snapshot horizon. The
// rows of other projects and environments are not in it; a batch short of
// limit, empty or not, ends past them at the horizon, so the stream's cursor
// passes them.
func (s *Store) NextBatch(ctx context.Context, kind sinks.EventKind, scope pgevents.Scope, from events.Cursor, limit int) (pgevents.Batch, error) {
	table, err := rawTable(kind)
	if err != nil {
		return pgevents.Batch{}, err
	}
	return pgevents.Bound(ctx, s.db(ctx), table, scope, from, limit)
}

// OldestUnread returns when the first row of the stream of kind in scope
// past from was received, held back by the horizon or not; ok is false when
// the stream has read every row its filter admits. The age of that row is
// the stream's lag, and shows how far a long transaction anywhere in the
// database holds it back.
func (s *Store) OldestUnread(ctx context.Context, kind sinks.EventKind, scope pgevents.Scope, from events.Cursor) (at time.Time, ok bool, err error) {
	table, err := rawTable(kind)
	if err != nil {
		return time.Time{}, false, err
	}
	return pgevents.OldestAfter(ctx, s.db(ctx), table, scope, from)
}

// Page is the rows of one batch as they are exported.
type Page struct {
	// Rows are the rows of the batch, in the order they were committed.
	Rows []sinks.Row
	// Skipped is how many rows of the batch were passed over because their
	// environment no longer exists: the key they would be exported under is
	// gone with it.
	Skipped int
}

// ReadBatch reads the rows of batch b of the stream of kind in scope, with
// the keys of their project and environment, in the order they were
// committed. Every read of one batch finds the same rows, whichever replica
// reads it and however often, since a batch holds only rows of transactions
// that had ended when it was chosen.
func (s *Store) ReadBatch(ctx context.Context, kind sinks.EventKind, scope pgevents.Scope, b pgevents.Batch) (Page, error) {
	where, args := b.Where("r")
	scoped, scopeArgs := scope.Where("r", len(args)+1)
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
		return Page{}, fmt.Errorf("no rows of the event kind %q", kind)
	}
}

func (s *Store) readExposures(ctx context.Context, where string, args []any) (Page, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT r.event_id::text, p.key, env.key, r.experiment_key, r.flag_key, r.variation_key, r.unit_type, r.unit_id,
			r.evaluation_reason, r.override, coalesce(r.platform, ''), coalesce(r.app_version, ''), r.sdk_name,
			r.sdk_version, r.ruleset_version, r.occurred_at, r.received_at
		FROM exposure_events r
		JOIN projects p ON p.id = r.project_id
		LEFT JOIN environments env ON env.id = r.environment_id
		WHERE `+where+` ORDER BY r.xid, r.seq`, args...)
	if err != nil {
		return Page{}, fmt.Errorf("reading exposures: %w", err)
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

func (s *Store) readAliases(ctx context.Context, where string, args []any) (Page, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT r.event_id::text, p.key, env.key, r.from_unit_type, r.from_unit_id, r.to_unit_type, r.to_unit_id,
			coalesce(r.platform, ''), r.sdk_name, r.sdk_version, r.occurred_at, r.received_at
		FROM alias_events r
		JOIN projects p ON p.id = r.project_id
		LEFT JOIN environments env ON env.id = r.environment_id
		WHERE `+where+` ORDER BY r.xid, r.seq`, args...)
	if err != nil {
		return Page{}, fmt.Errorf("reading aliases: %w", err)
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

func (s *Store) readSummaries(ctx context.Context, where string, args []any) (Page, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT r.source, r.event_id::text, r.xid::text, r.seq, p.key, env.key, r.flag_key, coalesce(r.variation_key, ''),
			r.evaluation_reason, r.count, r.period_start, r.period_end, r.sdk_name, r.sdk_version,
			coalesce(r.platform, ''), coalesce(r.app_version, ''), r.ruleset_version, r.received_at
		FROM evaluation_summary_events r
		JOIN projects p ON p.id = r.project_id
		LEFT JOIN environments env ON env.id = r.environment_id
		WHERE `+where+` ORDER BY r.xid, r.seq`, args...)
	if err != nil {
		return Page{}, fmt.Errorf("reading evaluation summaries: %w", err)
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
func collect(rows pgx.Rows, read func(pgx.Rows) (sinks.Row, bool, error)) (Page, error) {
	defer rows.Close()
	var page Page
	for rows.Next() {
		row, keep, err := read(rows)
		if err != nil {
			return Page{}, fmt.Errorf("reading a row to export: %w", err)
		}
		if !keep {
			page.Skipped++
			continue
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("reading rows to export: %w", err)
	}
	return page, nil
}
