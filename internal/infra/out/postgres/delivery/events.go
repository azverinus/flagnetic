package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// eventTables are the raw event tables, each partitioned by the UTC day of
// received_at.
var eventTables = []string{"evaluation_summary_events", "alias_events"}

// dedupTables are the tables recording the event IDs received, by type.
var dedupTables = map[delivery.EventType]string{
	delivery.EventEvaluationSummary: "evaluation_summary_dedup",
	delivery.EventAlias:             "alias_dedup",
}

// DeclaredUnitTypes returns which of keys project declares as unit types.
func (s *Store) DeclaredUnitTypes(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]bool, error) {
	return s.keysIn(ctx, "unit_types", project, keys)
}

// KnownFlags returns which of keys name a flag of project, archived ones
// included: an SDK still evaluating an archived flag is what tells its code
// is not deleted yet.
func (s *Store) KnownFlags(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]bool, error) {
	return s.keysIn(ctx, "flags", project, keys)
}

// keysIn returns which of keys are the key of a row of project in table.
func (s *Store) keysIn(ctx context.Context, table string, project workspace.ProjectID, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT key FROM `+table+` WHERE project_id = $1 AND key = ANY($2)`, project.Bytes(), keys)
	if err != nil {
		return nil, fmt.Errorf("reading keys of %s: %w", table, err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("reading keys of %s: %w", table, err)
	}
	for _, k := range found {
		out[k] = true
	}
	return out, nil
}

// ClaimEvents records the IDs of events of typ of project as received at at
// and returns those not recorded before. The IDs are inserted in the order
// given, which the caller sorts, so concurrent claims of overlapping IDs
// wait on each other rather than deadlock.
func (s *Store) ClaimEvents(ctx context.Context, project workspace.ProjectID, typ delivery.EventType, ids []delivery.EventID,
	at time.Time,
) (map[delivery.EventID]bool, error) {
	table, ok := dedupTables[typ]
	if !ok {
		return nil, fmt.Errorf("claiming events: no events of type %q are stored", typ)
	}
	raw := make([][16]byte, len(ids))
	for i, v := range ids {
		raw[i] = v.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `INSERT INTO `+table+` (project_id, event_id, received_at)
		SELECT $1, e.id, $3 FROM unnest($2::uuid[]) WITH ORDINALITY AS e (id, n) ORDER BY e.n
		ON CONFLICT DO NOTHING RETURNING event_id`, project.Bytes(), raw, at)
	if err != nil {
		return nil, fmt.Errorf("claiming events: %w", err)
	}
	claimed, err := pgx.CollectRows(rows, pgx.RowTo[[16]byte])
	if err != nil {
		return nil, fmt.Errorf("claiming events: %w", err)
	}
	out := make(map[delivery.EventID]bool, len(claimed))
	for _, b := range claimed {
		out[id.FromBytes[delivery.Event](b)] = true
	}
	return out, nil
}

// nullable returns s, or nil for "", so an absent member is stored null.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SaveSummaries stores the rows of evaluation summaries.
func (s *Store) SaveSummaries(ctx context.Context, rows []delivery.SummaryRecord) error {
	if len(rows) == 0 {
		return nil
	}
	if err := s.ensurePartitions(ctx, rows[0].ReceivedAt); err != nil {
		return err
	}
	_, err := s.db(ctx).CopyFrom(ctx, pgx.Identifier{"evaluation_summary_events"}, []string{
		"source", "event_id", "project_id", "environment_id", "sdk_key_id", "flag_key", "variation_key", "evaluation_reason",
		"count", "period_start", "period_end", "sdk_name", "sdk_version", "platform", "app_version", "ruleset_version", "received_at",
	}, pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		var event any
		if !r.EventID.IsZero() {
			event = r.EventID.Bytes()
		}
		return []any{
			string(r.Source), event, r.Key.Project.Bytes(), r.Key.Environment.Bytes(), r.Key.ID.Bytes(), r.Row.Flag,
			nullable(r.Row.Variation), r.Row.Reason, r.Row.Count, r.PeriodStart, r.PeriodEnd, r.SDK.Name, r.SDK.Version,
			nullable(r.SDK.Platform), nullable(r.AppVersion), r.RulesetVersion, r.ReceivedAt,
		}, nil
	}))
	if err != nil {
		return fmt.Errorf("saving evaluation summaries: %w", err)
	}
	return nil
}

// SaveAliases stores aliases.
func (s *Store) SaveAliases(ctx context.Context, aliases []delivery.AliasRecord) error {
	if len(aliases) == 0 {
		return nil
	}
	if err := s.ensurePartitions(ctx, aliases[0].ReceivedAt); err != nil {
		return err
	}
	_, err := s.db(ctx).CopyFrom(ctx, pgx.Identifier{"alias_events"}, []string{
		"event_id", "project_id", "environment_id", "sdk_key_id", "from_unit_type", "from_unit_id", "to_unit_type", "to_unit_id",
		"sdk_name", "sdk_version", "platform", "occurred_at", "received_at",
	}, pgx.CopyFromSlice(len(aliases), func(i int) ([]any, error) {
		a := aliases[i]
		return []any{
			a.EventID.Bytes(), a.Key.Project.Bytes(), a.Key.Environment.Bytes(), a.Key.ID.Bytes(), a.Alias.FromUnitType,
			a.Alias.FromUnitID, a.Alias.ToUnitType, a.Alias.ToUnitID, a.SDK.Name, a.SDK.Version, nullable(a.SDK.Platform),
			a.Alias.OccurredAt, a.ReceivedAt,
		}, nil
	}))
	if err != nil {
		return fmt.Errorf("saving aliases: %w", err)
	}
	return nil
}

// ensurePartitions makes sure the partitions of the day of at exist before
// rows received then are written: the partition worker creates them ahead of
// time, and this covers a day it has not reached yet, such as the first
// minutes of a new instance. Each day is checked once per process.
func (s *Store) ensurePartitions(ctx context.Context, at time.Time) error {
	day := at.UTC().Format(time.DateOnly)
	if _, ok := s.partitions.Load(day); ok {
		return nil
	}
	if err := s.CreateEventPartitions(ctx, at, 0); err != nil {
		return err
	}
	return nil
}

// CreateEventPartitions creates the partitions of every raw event table for
// the UTC day of from and the ahead days after it, those missing. It runs
// on its own connection, outside any unit of work the context carries, so
// that creating a partition never waits on the caller's own locks; replicas
// creating at once take turns under an advisory lock.
func (s *Store) CreateEventPartitions(ctx context.Context, from time.Time, ahead int) error {
	start := from.UTC().Truncate(24 * time.Hour)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("creating event partitions: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('event_partitions', 0))`); err != nil {
		return fmt.Errorf("creating event partitions: %w", err)
	}
	var days []string
	for i := 0; i <= ahead; i++ {
		day := start.AddDate(0, 0, i)
		days = append(days, day.Format(time.DateOnly))
		for _, table := range eventTables {
			partition := pgx.Identifier{table + "_p" + day.Format("20060102")}.Sanitize()
			_, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+partition+` PARTITION OF `+pgx.Identifier{table}.Sanitize()+
				` FOR VALUES FROM ('`+day.Format(time.RFC3339)+`') TO ('`+day.AddDate(0, 0, 1).Format(time.RFC3339)+`')`)
			if err != nil {
				return fmt.Errorf("creating the partition %s: %w", partition, err)
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("creating event partitions: %w", err)
	}
	for _, d := range days {
		s.partitions.Store(d, true)
	}
	return nil
}

// EvaluationSummaries returns the stored rows of the evaluation summaries of
// environment, in the order they were committed: what reports read.
func (s *Store) EvaluationSummaries(ctx context.Context, environment workspace.EnvironmentID) ([]delivery.SummaryRecord, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT source, event_id, project_id, environment_id, sdk_key_id, flag_key,
			coalesce(variation_key, ''), evaluation_reason, count, period_start, period_end, sdk_name, sdk_version,
			coalesce(platform, ''), coalesce(app_version, ''), ruleset_version, received_at
		FROM evaluation_summary_events WHERE environment_id = $1 ORDER BY xid, seq`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading evaluation summaries: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (delivery.SummaryRecord, error) {
		var (
			r                   delivery.SummaryRecord
			source              string
			event               *[16]byte
			project, env, keyID [16]byte
		)
		scanErr := row.Scan(&source, &event, &project, &env, &keyID, &r.Row.Flag, &r.Row.Variation, &r.Row.Reason, &r.Row.Count,
			&r.PeriodStart, &r.PeriodEnd, &r.SDK.Name, &r.SDK.Version, &r.SDK.Platform, &r.AppVersion, &r.RulesetVersion, &r.ReceivedAt)
		r.Source = delivery.SummarySource(source)
		if event != nil {
			r.EventID = id.FromBytes[delivery.Event](*event)
		}
		r.Key.Project, r.Key.Environment = id.FromBytes[workspace.Project](project), id.FromBytes[workspace.Environment](env)
		r.Key.ID = id.FromBytes[delivery.SDKKey](keyID)
		return r, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading evaluation summaries: %w", err)
	}
	return out, nil
}

// Aliases returns the stored aliases of environment, in the order they were
// committed.
func (s *Store) Aliases(ctx context.Context, environment workspace.EnvironmentID) ([]delivery.AliasRecord, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT event_id, sdk_key_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id,
			sdk_name, sdk_version, coalesce(platform, ''), occurred_at, received_at
		FROM alias_events WHERE environment_id = $1 ORDER BY xid, seq`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading aliases: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (delivery.AliasRecord, error) {
		var (
			a            delivery.AliasRecord
			event, keyID [16]byte
		)
		scanErr := row.Scan(&event, &keyID, &a.Alias.FromUnitType, &a.Alias.FromUnitID, &a.Alias.ToUnitType, &a.Alias.ToUnitID,
			&a.SDK.Name, &a.SDK.Version, &a.SDK.Platform, &a.Alias.OccurredAt, &a.ReceivedAt)
		a.EventID = id.FromBytes[delivery.Event](event)
		a.Key.ID, a.Key.Environment = id.FromBytes[delivery.SDKKey](keyID), environment
		return a, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading aliases: %w", err)
	}
	return out, nil
}
