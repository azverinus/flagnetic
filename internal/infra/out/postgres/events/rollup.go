package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appevents "github.com/azverinus/flagnetic/internal/app/events"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ExposuresAfter returns the next batch of at most limit exposures past from,
// of transactions below the snapshot horizon, and the cursor of the last;
// from itself when there is none.
func (s *Store) ExposuresAfter(ctx context.Context, from events.Cursor, limit int) ([]events.ExposureRow, events.Cursor, error) {
	b, err := bound(ctx, s.db(ctx), "exposure_events", from, limit)
	if err != nil || b.Rows == 0 {
		return nil, b.To, err
	}
	where, args := b.where()
	rows, err := s.db(ctx).Query(ctx, `SELECT project_id, environment_id, experiment_id, unit_type, unit_id, event_id,
			variation_key, override, occurred_at, received_at
		FROM exposure_events WHERE `+where+` ORDER BY xid, seq`, args...)
	if err != nil {
		return nil, events.Cursor{}, fmt.Errorf("reading exposures to roll up: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.ExposureRow, error) {
		var (
			r                                   events.ExposureRow
			project, environment, experiment, e [16]byte
		)
		scanErr := row.Scan(&project, &environment, &experiment, &r.Unit.UnitType, &r.Unit.UnitID, &e, &r.Variation, &r.Override,
			&r.OccurredAt, &r.ReceivedAt)
		r.Project = id.FromBytes[workspace.Project](project)
		r.Unit.Environment = id.FromBytes[workspace.Environment](environment)
		r.Unit.Experiment = id.FromBytes[experiments.Experiment](experiment)
		r.EventID = id.FromBytes[delivery.Event](e)
		r.OccurredAt, r.ReceivedAt = r.OccurredAt.UTC(), r.ReceivedAt.UTC()
		return r, scanErr
	})
	if err != nil {
		return nil, events.Cursor{}, fmt.Errorf("reading exposures to roll up: %w", err)
	}
	return out, b.To, nil
}

// LockTargets returns which of the projects, environments and experiments
// given still exist, each held against deletion until the unit of work ends,
// so that the rollup's writes referencing them never fail on one deleted
// meanwhile. Raw rows outlive what they name until retention drops them.
func (s *Store) LockTargets(ctx context.Context, t appevents.RollupTargets) (appevents.RollupTargets, error) {
	lock := func(table string, ids [][16]byte) (map[[16]byte]bool, error) {
		rows, err := s.db(ctx).Query(ctx, `SELECT id FROM `+table+` WHERE id = ANY($1) ORDER BY id FOR KEY SHARE`, ids)
		if err != nil {
			return nil, fmt.Errorf("locking the %s rolled up: %w", table, err)
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[[16]byte])
		if err != nil {
			return nil, fmt.Errorf("locking the %s rolled up: %w", table, err)
		}
		out := make(map[[16]byte]bool, len(found))
		for _, b := range found {
			out[b] = true
		}
		return out, nil
	}
	var out appevents.RollupTargets
	// Projects, then environments, then experiments: the order a project's
	// deletion takes them in.
	projects, err := lock("projects", bytesOf(t.Projects))
	if err != nil {
		return out, err
	}
	environments, err := lock("environments", bytesOf(t.Environments))
	if err != nil {
		return out, err
	}
	exps, err := lock("experiments", bytesOf(t.Experiments))
	if err != nil {
		return out, err
	}
	for _, p := range t.Projects {
		if projects[p.Bytes()] {
			out.Projects = append(out.Projects, p)
		}
	}
	for _, e := range t.Environments {
		if environments[e.Bytes()] {
			out.Environments = append(out.Environments, e)
		}
	}
	for _, e := range t.Experiments {
		if exps[e.Bytes()] {
			out.Experiments = append(out.Experiments, e)
		}
	}
	return out, nil
}

func bytesOf[T interface{ Bytes() [16]byte }](ids []T) [][16]byte {
	out := make([][16]byte, len(ids))
	for i, v := range ids {
		out[i] = v.Bytes()
	}
	return out
}

// seenJSON is a first exposure's seen column: per variation, the count and
// the last exposure by occurrence.
type seenJSON map[string]struct {
	Count  int64     `json:"count"`
	LastAt time.Time `json:"last_at"`
	LastID string    `json:"last_id"`
}

// FirstExposures returns the first exposures stored of units, by unit; a unit
// without one is absent.
func (s *Store) FirstExposures(ctx context.Context, units []events.UnitKey) (map[events.UnitKey]events.FirstExposure, error) {
	out := map[events.UnitKey]events.FirstExposure{}
	if len(units) == 0 {
		return out, nil
	}
	environments, exps := make([][16]byte, len(units)), make([][16]byte, len(units))
	types, ids := make([]string, len(units)), make([]string, len(units))
	for i, u := range units {
		environments[i], exps[i], types[i], ids[i] = u.Environment.Bytes(), u.Experiment.Bytes(), u.UnitType, u.UnitID
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT f.environment_id, f.experiment_id, f.unit_type, f.unit_id, f.event_id, f.occurred_at, f.variation_key, f.seen
		FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::text[]) AS u (environment_id, experiment_id, unit_type, unit_id)
		JOIN first_exposures f USING (environment_id, experiment_id, unit_type, unit_id)`, environments, exps, types, ids)
	if err != nil {
		return nil, fmt.Errorf("reading first exposures: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			u                       events.UnitKey
			f                       events.FirstExposure
			environment, experiment [16]byte
			event                   [16]byte
			raw                     []byte
		)
		if err = rows.Scan(&environment, &experiment, &u.UnitType, &u.UnitID, &event, &f.OccurredAt, &f.Variation, &raw); err != nil {
			return nil, fmt.Errorf("reading first exposures: %w", err)
		}
		u.Environment, u.Experiment = id.FromBytes[workspace.Environment](environment), id.FromBytes[experiments.Experiment](experiment)
		f.EventID, f.OccurredAt = id.FromBytes[delivery.Event](event), f.OccurredAt.UTC()
		var seen seenJSON
		if err = json.Unmarshal(raw, &seen); err != nil {
			return nil, fmt.Errorf("decoding the first exposure of unit %q: %w", u.UnitID, err)
		}
		f.Seen = make(map[string]events.Seen, len(seen))
		for v, x := range seen {
			last, parseErr := id.Parse[delivery.Event](x.LastID)
			if parseErr != nil {
				return nil, fmt.Errorf("decoding the first exposure of unit %q: %w", u.UnitID, parseErr)
			}
			f.Seen[v] = events.Seen{Count: x.Count, LastAt: x.LastAt.UTC(), LastID: last}
		}
		out[u] = f
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading first exposures: %w", err)
	}
	return out, nil
}

// SaveFirstExposures stores records, each replacing the unit's own.
func (s *Store) SaveFirstExposures(ctx context.Context, records map[events.UnitKey]events.FirstExposure) error {
	if len(records) == 0 {
		return nil
	}
	n := len(records)
	var (
		environments, exps, eventIDs = make([][16]byte, 0, n), make([][16]byte, 0, n), make([][16]byte, 0, n)
		types, units, variations     = make([]string, 0, n), make([]string, 0, n), make([]string, 0, n)
		occurred                     = make([]time.Time, 0, n)
		seen                         = make([]string, 0, n)
		conflicts                    = make([]int64, 0, n)
		conflictingVariations        = make([]*string, 0, n)
		conflictingAt                = make([]*time.Time, 0, n)
	)
	for u, f := range records {
		raw := seenJSON{}
		for v, x := range f.Seen {
			raw[v] = struct {
				Count  int64     `json:"count"`
				LastAt time.Time `json:"last_at"`
				LastID string    `json:"last_id"`
			}{x.Count, x.LastAt, x.LastID.String()}
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("encoding the first exposure of unit %q: %w", u.UnitID, err)
		}
		environments, exps, types, units = append(environments, u.Environment.Bytes()), append(exps, u.Experiment.Bytes()),
			append(types, u.UnitType), append(units, u.UnitID)
		eventIDs, occurred, variations = append(eventIDs, f.EventID.Bytes()), append(occurred, f.OccurredAt), append(variations, f.Variation)
		seen, conflicts = append(seen, string(b)), append(conflicts, f.Conflicts())
		if v, at, ok := f.Conflicting(); ok {
			conflictingVariations, conflictingAt = append(conflictingVariations, &v), append(conflictingAt, &at)
		} else {
			conflictingVariations, conflictingAt = append(conflictingVariations, nil), append(conflictingAt, nil)
		}
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO first_exposures (environment_id, experiment_id, unit_type, unit_id, event_id, occurred_at, variation_key, seen,
			conflicts, conflicting_variation_key, conflicting_at)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::text[], $5::uuid[], $6::timestamptz[], $7::text[],
			$8::jsonb[], $9::bigint[], $10::text[], $11::timestamptz[])
		ON CONFLICT (environment_id, experiment_id, unit_type, unit_id) DO UPDATE SET
			event_id = excluded.event_id, occurred_at = excluded.occurred_at, variation_key = excluded.variation_key,
			seen = excluded.seen, conflicts = excluded.conflicts,
			conflicting_variation_key = excluded.conflicting_variation_key, conflicting_at = excluded.conflicting_at`,
		environments, exps, types, units, eventIDs, occurred, variations, seen, conflicts, conflictingVariations, conflictingAt)
	if err != nil {
		return fmt.Errorf("saving first exposures: %w", err)
	}
	return nil
}

// AddExposureCounts adds counts to the exposure counts per day.
func (s *Store) AddExposureCounts(ctx context.Context, counts map[events.ExposureDay]events.ExposureDayCounts) error {
	if len(counts) == 0 {
		return nil
	}
	var (
		environments, exps          [][16]byte
		variations                  []string
		days                        []string
		exposures, overridden, firs []int64
	)
	for k, c := range counts {
		environments, exps, variations, days = append(environments, k.Environment.Bytes()), append(exps, k.Experiment.Bytes()),
			append(variations, k.Variation), append(days, k.Day.Format(time.DateOnly))
		exposures, overridden, firs = append(exposures, c.Exposures), append(overridden, c.Overridden), append(firs, c.FirstExposures)
	}
	// The counts of a day are made at zero first, then moved by the changes:
	// a replaced first exposure takes one off a day already counted, and a
	// row proposed with a negative count would fail its check even where it
	// only updates.
	args := []any{environments, exps, variations, days, exposures, overridden, firs}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO exposure_counts_daily (environment_id, experiment_id, variation_key, day, exposures, overridden, first_exposures)
		SELECT environment_id, experiment_id, variation_key, day, 0, 0, 0
		FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::date[]) AS t (environment_id, experiment_id, variation_key, day)
		ORDER BY experiment_id, environment_id, day, variation_key
		ON CONFLICT DO NOTHING`, args[:4]...)
	if err == nil {
		_, err = s.db(ctx).Exec(ctx, `
			UPDATE exposure_counts_daily d SET exposures = d.exposures + t.exposures, overridden = d.overridden + t.overridden,
				first_exposures = d.first_exposures + t.first_exposures
			FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::date[], $5::bigint[], $6::bigint[], $7::bigint[])
				AS t (environment_id, experiment_id, variation_key, day, exposures, overridden, first_exposures)
			WHERE (d.experiment_id, d.environment_id, d.day, d.variation_key) = (t.experiment_id, t.environment_id, t.day, t.variation_key)`,
			args...)
	}
	if err != nil {
		return fmt.Errorf("adding exposure counts: %w", err)
	}
	return nil
}

// AddEventCounts adds counts to the events received per project, day and
// type.
func (s *Store) AddEventCounts(ctx context.Context, counts map[events.EventDay]int64) error {
	if len(counts) == 0 {
		return nil
	}
	var (
		projects [][16]byte
		days     []string
		types    []string
		n        []int64
	)
	for k, c := range counts {
		projects, days, types, n = append(projects, k.Project.Bytes()), append(days, k.Day.Format(time.DateOnly)), append(types, string(k.Type)), append(n, c)
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO event_counts_daily AS d (project_id, day, event_type, count)
		SELECT * FROM unnest($1::uuid[], $2::date[], $3::text[], $4::bigint[])
		ON CONFLICT (project_id, day, event_type) DO UPDATE SET count = d.count + excluded.count`,
		projects, days, types, n)
	if err != nil {
		return fmt.Errorf("adding event counts: %w", err)
	}
	return nil
}

// EventCounts returns the events project received on each UTC day from from
// to to, both included, by type.
func (s *Store) EventCounts(ctx context.Context, project workspace.ProjectID, from, to time.Time) (map[events.EventDay]int64, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT day, event_type, count FROM event_counts_daily
		WHERE project_id = $1 AND day BETWEEN $2::date AND $3::date`,
		project.Bytes(), dateOf(&from), dateOf(&to))
	if err != nil {
		return nil, fmt.Errorf("reading event counts: %w", err)
	}
	defer rows.Close()
	out := map[events.EventDay]int64{}
	for rows.Next() {
		var (
			day   time.Time
			typ   string
			count int64
		)
		if err = rows.Scan(&day, &typ, &count); err != nil {
			return nil, fmt.Errorf("reading event counts: %w", err)
		}
		out[events.EventDay{Project: project, Day: day.UTC(), Type: events.EventType(typ)}] = count
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading event counts: %w", err)
	}
	return out, nil
}

// ExperimentOf returns the identifier and weights of project's experiment
// keyed key, of any status, or appevents.ErrNotFound.
func (s *Store) ExperimentOf(ctx context.Context, project workspace.ProjectID, key string) (experiments.ExperimentID, []experiments.Weight, error) {
	var (
		raw        [16]byte
		variations []byte
	)
	err := s.db(ctx).QueryRow(ctx, `SELECT id, variations FROM experiments WHERE project_id = $1 AND key = $2`,
		project.Bytes(), key).Scan(&raw, &variations)
	if errors.Is(err, pgx.ErrNoRows) {
		return experiments.ExperimentID{}, nil, appevents.ErrNotFound
	}
	if err != nil {
		return experiments.ExperimentID{}, nil, fmt.Errorf("reading experiment %q: %w", key, err)
	}
	var weights []struct {
		Variation string `json:"variation"`
		Weight    int    `json:"weight"`
	}
	if err = json.Unmarshal(variations, &weights); err != nil {
		return experiments.ExperimentID{}, nil, fmt.Errorf("decoding the weights of experiment %q: %w", key, err)
	}
	out := make([]experiments.Weight, len(weights))
	for i, w := range weights {
		out[i] = experiments.Weight{Variation: w.Variation, Weight: w.Weight}
	}
	return id.FromBytes[experiments.Experiment](raw), out, nil
}

// EnvironmentOf returns the identifier of project's environment keyed key, or
// appevents.ErrNotFound.
func (s *Store) EnvironmentOf(ctx context.Context, project workspace.ProjectID, key string) (workspace.EnvironmentID, error) {
	var raw [16]byte
	err := s.db(ctx).QueryRow(ctx, `SELECT id FROM environments WHERE project_id = $1 AND key = $2`, project.Bytes(), key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.EnvironmentID{}, appevents.ErrNotFound
	}
	if err != nil {
		return workspace.EnvironmentID{}, fmt.Errorf("reading environment %q: %w", key, err)
	}
	return id.FromBytes[workspace.Environment](raw), nil
}

// ExposureCounts returns experiment's exposure counts in environment summed
// over every day, by variation.
func (s *Store) ExposureCounts(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID,
) (map[string]events.ExposureDayCounts, error) {
	days, err := s.ExposureTimeline(ctx, environment, experiment, nil, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]events.ExposureDayCounts{}
	for _, d := range days {
		sum := out[d.Variation]
		sum.Exposures += d.Exposures
		sum.Overridden += d.Overridden
		sum.FirstExposures += d.FirstExposures
		out[d.Variation] = sum
	}
	return out, nil
}

// ExposureTimeline returns experiment's exposure counts in environment per
// UTC day and variation, by day then variation key, from the day from to the
// day to, both included; nil leaves that end open.
func (s *Store) ExposureTimeline(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID,
	from, to *time.Time,
) ([]events.ExposureTimelineDay, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT day, variation_key, exposures, overridden, first_exposures FROM exposure_counts_daily
		WHERE experiment_id = $1 AND environment_id = $2
			AND ($3::date IS NULL OR day >= $3) AND ($4::date IS NULL OR day <= $4)
			AND (exposures, overridden, first_exposures) <> (0, 0, 0)
		ORDER BY day, variation_key COLLATE "C"`, experiment.Bytes(), environment.Bytes(), dateOf(from), dateOf(to))
	if err != nil {
		return nil, fmt.Errorf("reading the exposure timeline: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.ExposureTimelineDay, error) {
		var d events.ExposureTimelineDay
		scanErr := row.Scan(&d.Day, &d.Variation, &d.Exposures, &d.Overridden, &d.FirstExposures)
		d.Day = d.Day.UTC()
		return d, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading the exposure timeline: %w", err)
	}
	return out, nil
}

// dateOf returns the UTC date of at as PostgreSQL takes it, nil for nil.
func dateOf(at *time.Time) any {
	if at == nil {
		return nil
	}
	return at.UTC().Format(time.DateOnly)
}

// ConflictTotals returns how many exposures of experiment in environment
// conflicted with their unit's first exposure, and how many units had one.
func (s *Store) ConflictTotals(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID,
) (total, units int64, err error) {
	err = s.db(ctx).QueryRow(ctx, `SELECT coalesce(sum(conflicts), 0)::bigint, count(*) FROM first_exposures
		WHERE experiment_id = $1 AND environment_id = $2 AND conflicts > 0`, experiment.Bytes(), environment.Bytes()).Scan(&total, &units)
	if err != nil {
		return 0, 0, fmt.Errorf("counting conflicts: %w", err)
	}
	return total, units, nil
}

// Conflicts returns up to limit units of experiment in environment that
// conflicted, the most recent conflict first.
func (s *Store) Conflicts(ctx context.Context, environment workspace.EnvironmentID, experiment experiments.ExperimentID, limit int,
) ([]events.Conflict, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT unit_type, unit_id, variation_key, conflicting_variation_key, conflicting_at
		FROM first_exposures WHERE experiment_id = $1 AND environment_id = $2 AND conflicts > 0
		ORDER BY conflicting_at DESC, unit_type, unit_id LIMIT $3`, experiment.Bytes(), environment.Bytes(), limit)
	if err != nil {
		return nil, fmt.Errorf("reading conflicts: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.Conflict, error) {
		var c events.Conflict
		scanErr := row.Scan(&c.UnitType, &c.UnitID, &c.FirstVariation, &c.ConflictingVariation, &c.ConflictingAt)
		c.ConflictingAt = c.ConflictingAt.UTC()
		return c, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading conflicts: %w", err)
	}
	return out, nil
}
