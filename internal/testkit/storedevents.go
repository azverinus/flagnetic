package testkit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// What follows reads the raw event tables as a test needs to see what an
// ingest stored; nothing in the server reads them this way, so the store
// does not carry these reads.

// StoredEvaluationSummaries returns the stored rows of the evaluation summaries of
// environment, in the order they were committed: what reports read.
func StoredEvaluationSummaries(ctx context.Context, pool *pgxpool.Pool, environment workspace.EnvironmentID) ([]delivery.SummaryRecord, error) {
	rows, err := pool.Query(ctx, `SELECT source, event_id, project_id, environment_id, sdk_key_id, flag_key,
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

// StoredAliases returns the stored aliases of environment, in the order they were
// committed.
func StoredAliases(ctx context.Context, pool *pgxpool.Pool, environment workspace.EnvironmentID) ([]events.AliasRecord, error) {
	rows, err := pool.Query(ctx, `SELECT event_id, sdk_key_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id,
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

// StoredAliasLinks returns the alias links of environment, in the order of their
// pairs.
func StoredAliasLinks(ctx context.Context, pool *pgxpool.Pool, environment workspace.EnvironmentID) ([]events.AliasLink, error) {
	rows, err := pool.Query(ctx, `SELECT event_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id, occurred_at,
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

// StoredExposures returns the stored exposures of environment, in the order they
// were committed: what reports read.
func StoredExposures(ctx context.Context, pool *pgxpool.Pool, environment workspace.EnvironmentID) ([]events.ExposureRecord, error) {
	rows, err := pool.Query(ctx, `SELECT event_id, sdk_key_id, experiment_id, experiment_key, flag_key, variation_key,
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
