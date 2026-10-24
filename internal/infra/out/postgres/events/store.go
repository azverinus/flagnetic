// Package events is the PostgreSQL repository of the events context: the raw
// events SDKs post and the tables that keep them apart from retries.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Store is the repository of raw events.
type Store struct {
	pool *pgxpool.Pool
	// partitions are the days whose event partitions this process knows
	// exist, as "2006-01-02", so that it creates a missing one at most once.
	partitions sync.Map
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// eventTables are the raw event tables, each partitioned by the UTC day of
// received_at.
var eventTables = []string{"evaluation_summary_events", "alias_events", "exposure_events"}

// dedupTables are the tables recording the event IDs received, by type.
var dedupTables = map[events.EventType]string{
	events.EventEvaluationSummary: "evaluation_summary_dedup",
	events.EventAlias:             "alias_dedup",
	events.EventExposure:          "exposure_dedup",
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
func (s *Store) ClaimEvents(ctx context.Context, project workspace.ProjectID, typ events.EventType, ids []delivery.EventID,
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
	if err := s.EnsureEventPartitions(ctx, rows[0].ReceivedAt); err != nil {
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
func (s *Store) SaveAliases(ctx context.Context, aliases []events.AliasRecord) error {
	if len(aliases) == 0 {
		return nil
	}
	if err := s.EnsureEventPartitions(ctx, aliases[0].ReceivedAt); err != nil {
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

// EnsureEventPartitions makes sure the partitions of the day of at exist
// before rows received then are written: the partition worker creates them
// ahead of time, and this covers a day it has not reached yet, such as the
// first minutes of a new instance. Each day is checked once per process.
func (s *Store) EnsureEventPartitions(ctx context.Context, at time.Time) error {
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
	// Attaching a partition locks its parent table against every reader and
	// writer, and while it waits for a long reader to finish, every insert
	// queues behind it. Giving up after a few seconds bounds that stall: the
	// worker tries again at its next round, days ahead of need, and a batch
	// that needed the day now fails rather than hangs, and is retried.
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
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
func (s *Store) Aliases(ctx context.Context, environment workspace.EnvironmentID) ([]events.AliasRecord, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT event_id, sdk_key_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id,
			sdk_name, sdk_version, coalesce(platform, ''), occurred_at, received_at
		FROM alias_events WHERE environment_id = $1 ORDER BY xid, seq`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading aliases: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.AliasRecord, error) {
		var (
			a            events.AliasRecord
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

// LinkAliases links the pair of each alias in its environment, keeping per
// pair the alias that occurred earliest. Pairs are written in their order,
// so batches linking the same pairs at once wait on each other rather than
// deadlock; a pair named twice in aliases is written once, since one
// statement cannot update a row twice.
func (s *Store) LinkAliases(ctx context.Context, aliases []events.AliasRecord) error {
	if len(aliases) == 0 {
		return nil
	}
	n := len(aliases)
	var (
		environments, eventIDs             = make([][16]byte, n), make([][16]byte, n)
		fromTypes, fromIDs, toTypes, toIDs = make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		occurredAt, receivedAt             = make([]time.Time, n), make([]time.Time, n)
	)
	for i, a := range aliases {
		environments[i], eventIDs[i] = a.Key.Environment.Bytes(), a.EventID.Bytes()
		fromTypes[i], fromIDs[i], toTypes[i], toIDs[i] = a.Alias.FromUnitType, a.Alias.FromUnitID, a.Alias.ToUnitType, a.Alias.ToUnitID
		occurredAt[i], receivedAt[i] = a.Alias.OccurredAt, a.ReceivedAt
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO alias_links (environment_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id, event_id,
			occurred_at, received_at)
		SELECT DISTINCT ON (t.environment_id, t.from_unit_type, t.from_unit_id, t.to_unit_type, t.to_unit_id)
			t.environment_id, t.from_unit_type, t.from_unit_id, t.to_unit_type, t.to_unit_id, t.event_id, t.occurred_at,
			t.received_at
		FROM unnest($1::uuid[], $2::text[], $3::text[], $4::text[], $5::text[], $6::uuid[], $7::timestamptz[],
			$8::timestamptz[]) AS t (environment_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id, event_id,
			occurred_at, received_at)
		ORDER BY t.environment_id, t.from_unit_type, t.from_unit_id, t.to_unit_type, t.to_unit_id, t.occurred_at, t.event_id
		ON CONFLICT (environment_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id) DO UPDATE SET
			event_id = excluded.event_id, occurred_at = excluded.occurred_at, received_at = excluded.received_at
		WHERE excluded.occurred_at < alias_links.occurred_at`,
		environments, fromTypes, fromIDs, toTypes, toIDs, eventIDs, occurredAt, receivedAt)
	if err != nil {
		return fmt.Errorf("linking aliases: %w", err)
	}
	return nil
}

// AliasLinks returns the alias links of environment, in the order of their
// pairs.
func (s *Store) AliasLinks(ctx context.Context, environment workspace.EnvironmentID) ([]events.AliasLink, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT event_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id, occurred_at,
			received_at
		FROM alias_links WHERE environment_id = $1
		ORDER BY from_unit_type, from_unit_id, to_unit_type, to_unit_id`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading alias links: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.AliasLink, error) {
		var (
			l     events.AliasLink
			event [16]byte
		)
		scanErr := row.Scan(&event, &l.Alias.FromUnitType, &l.Alias.FromUnitID, &l.Alias.ToUnitType, &l.Alias.ToUnitID,
			&l.Alias.OccurredAt, &l.ReceivedAt)
		l.EventID = id.FromBytes[delivery.Event](event)
		return l, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading alias links: %w", err)
	}
	return out, nil
}

// Experiments returns the experiments of project keyed by keys, of any status,
// by key, each locked against deletion until the unit of work ends: the count
// of a refusal references its experiment, and an experiment deleted between
// the lookup and the count would fail the whole batch.
func (s *Store) Experiments(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]events.ExperimentFacts, error) {
	out := map[string]events.ExperimentFacts{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT e.id, e.key, f.key, e.unit_type, e.variations
		FROM experiments e JOIN flags f ON f.id = e.flag_id
		WHERE e.project_id = $1 AND e.key = ANY($2)
		ORDER BY e.id
		FOR KEY SHARE OF e`, project.Bytes(), keys)
	if err != nil {
		return nil, fmt.Errorf("reading experiments for exposures: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			f          events.ExperimentFacts
			experiment [16]byte
			key        string
			variations []byte
		)
		if err = rows.Scan(&experiment, &key, &f.FlagKey, &f.UnitType, &variations); err != nil {
			return nil, fmt.Errorf("reading experiments for exposures: %w", err)
		}
		var weights []struct {
			Variation string `json:"variation"`
		}
		if err = json.Unmarshal(variations, &weights); err != nil {
			return nil, fmt.Errorf("decoding the weights of experiment %q: %w", key, err)
		}
		for _, w := range weights {
			f.Variations = append(f.Variations, w.Variation)
		}
		f.ID = id.FromBytes[experiments.Experiment](experiment)
		out[key] = f
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading experiments for exposures: %w", err)
	}
	return out, nil
}

// SaveExposures stores exposures.
func (s *Store) SaveExposures(ctx context.Context, exposures []events.ExposureRecord) error {
	if len(exposures) == 0 {
		return nil
	}
	if err := s.EnsureEventPartitions(ctx, exposures[0].ReceivedAt); err != nil {
		return err
	}
	_, err := s.db(ctx).CopyFrom(ctx, pgx.Identifier{"exposure_events"}, []string{
		"event_id", "project_id", "environment_id", "sdk_key_id", "experiment_id", "experiment_key", "flag_key", "variation_key",
		"unit_type", "unit_id", "evaluation_reason", "override", "ruleset_version", "sdk_name", "sdk_version", "platform",
		"app_version", "occurred_at", "received_at",
	}, pgx.CopyFromSlice(len(exposures), func(i int) ([]any, error) {
		x := exposures[i]
		e := x.Exposure
		return []any{
			x.EventID.Bytes(), x.Key.Project.Bytes(), x.Key.Environment.Bytes(), x.Key.ID.Bytes(), x.Experiment.Bytes(),
			e.ExperimentKey, e.FlagKey, e.VariationKey, e.UnitType, e.UnitID, e.Reason, e.Override, e.RulesetVersion,
			x.SDK.Name, x.SDK.Version, nullable(e.Platform), nullable(e.AppVersion), e.OccurredAt, x.ReceivedAt,
		}, nil
	}))
	if err != nil {
		return fmt.Errorf("saving exposures: %w", err)
	}
	return nil
}

// CountRejections adds counts to those of exposures refused in environment, as
// of at. The counts are given in the order of experiment and reason, which
// the caller sorts, so concurrent batches counting the same rows wait on each
// other rather than deadlock.
func (s *Store) CountRejections(ctx context.Context, environment workspace.EnvironmentID, counts []events.RejectionCount,
	at time.Time,
) error {
	experimentIDs := make([][16]byte, len(counts))
	reasons := make([]string, len(counts))
	n := make([]int64, len(counts))
	for i, c := range counts {
		experimentIDs[i], reasons[i], n[i] = c.Experiment.Bytes(), string(c.Reason), c.Count
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO experiment_rejection_counts (experiment_id, environment_id, reason, count, last_rejected_at)
		SELECT t.experiment_id, $1, t.reason, t.count, $2
		FROM unnest($3::uuid[], $4::text[], $5::bigint[]) WITH ORDINALITY AS t (experiment_id, reason, count, n)
		ORDER BY t.n
		ON CONFLICT (experiment_id, environment_id, reason) DO UPDATE SET
			count = experiment_rejection_counts.count + excluded.count,
			last_rejected_at = excluded.last_rejected_at`,
		environment.Bytes(), at, experimentIDs, reasons, n)
	if err != nil {
		return fmt.Errorf("counting refused exposures: %w", err)
	}
	return nil
}

// RejectionCountsOf returns how many exposures of experiment were refused in
// environment for each reason.
func (s *Store) RejectionCountsOf(ctx context.Context, experiment experiments.ExperimentID,
	environment workspace.EnvironmentID,
) (map[events.RejectionCode]int64, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT reason, count FROM experiment_rejection_counts
		WHERE experiment_id = $1 AND environment_id = $2`, experiment.Bytes(), environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading refused exposures: %w", err)
	}
	defer rows.Close()
	out := map[events.RejectionCode]int64{}
	for rows.Next() {
		var (
			reason string
			count  int64
		)
		if err = rows.Scan(&reason, &count); err != nil {
			return nil, fmt.Errorf("reading refused exposures: %w", err)
		}
		out[events.RejectionCode(reason)] = count
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading refused exposures: %w", err)
	}
	return out, nil
}

// Exposures returns the stored exposures of environment, in the order they
// were committed: what reports read.
func (s *Store) Exposures(ctx context.Context, environment workspace.EnvironmentID) ([]events.ExposureRecord, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT event_id, sdk_key_id, experiment_id, experiment_key, flag_key, variation_key,
			unit_type, unit_id, evaluation_reason, override, ruleset_version, sdk_name, sdk_version, coalesce(platform, ''),
			coalesce(app_version, ''), occurred_at, received_at
		FROM exposure_events WHERE environment_id = $1 ORDER BY xid, seq`, environment.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading exposures: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.ExposureRecord, error) {
		var (
			x                        events.ExposureRecord
			event, keyID, experiment [16]byte
		)
		e := &x.Exposure
		scanErr := row.Scan(&event, &keyID, &experiment, &e.ExperimentKey, &e.FlagKey, &e.VariationKey, &e.UnitType, &e.UnitID,
			&e.Reason, &e.Override, &e.RulesetVersion, &x.SDK.Name, &x.SDK.Version, &e.Platform, &e.AppVersion, &e.OccurredAt,
			&x.ReceivedAt)
		x.EventID = id.FromBytes[delivery.Event](event)
		x.Key.ID, x.Key.Environment = id.FromBytes[delivery.SDKKey](keyID), environment
		x.Experiment = id.FromBytes[experiments.Experiment](experiment)
		e.OccurredAt, x.ReceivedAt = e.OccurredAt.UTC(), x.ReceivedAt.UTC()
		return x, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading exposures: %w", err)
	}
	return out, nil
}
