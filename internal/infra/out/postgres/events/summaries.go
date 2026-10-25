package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appevents "github.com/azverinus/flagnetic/internal/app/events"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// SummariesAfter returns the next batch of at most limit summary rows past
// from, of transactions below the snapshot horizon, and the cursor of the
// last; from itself when there is none. Each row names the project's flag of
// its key as it is now, none when the project has no such flag any more.
func (s *Store) SummariesAfter(ctx context.Context, from events.Cursor, limit int) ([]events.SummaryRow, events.Cursor, error) {
	b, err := bound(ctx, s.db(ctx), "evaluation_summary_events", from, limit)
	if err != nil || b.Rows == 0 {
		return nil, b.To, err
	}
	where, args := b.where("r")
	// A summary's rows are stored by one transaction, so they are read one
	// after the other, and only the first batch's cursor can fall among
	// them: a summary is first in a batch at its smallest seq there, unless
	// rows of it lie at or before the cursor, which an earlier batch
	// counted. Every row of a summary has the same received_at, which keeps
	// that look to one partition.
	rows, err := s.db(ctx).Query(ctx, `
		WITH b AS (
			SELECT r.xid, r.seq, r.project_id, r.environment_id, r.flag_key, r.source, r.event_id, r.variation_key,
				r.evaluation_reason, r.count, r.period_end, r.received_at,
				min(r.seq) OVER (PARTITION BY r.xid, r.event_id) AS first_seq
			FROM evaluation_summary_events r WHERE `+where+`)
		SELECT b.project_id, b.environment_id, f.id, b.source,
			b.event_id IS NOT NULL AND b.seq = b.first_seq AND NOT (b.xid = $1::text::xid8 AND EXISTS (
				SELECT 1 FROM evaluation_summary_events e
				WHERE e.xid = b.xid AND e.seq <= $2 AND e.event_id = b.event_id AND e.received_at = b.received_at)),
			coalesce(b.variation_key, ''), b.evaluation_reason, b.count, b.period_end, b.received_at
		FROM b LEFT JOIN flags f ON f.project_id = b.project_id AND f.key = b.flag_key
		ORDER BY b.xid, b.seq`, args...)
	if err != nil {
		return nil, events.Cursor{}, fmt.Errorf("reading summaries to roll up: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.SummaryRow, error) {
		var (
			r                    events.SummaryRow
			project, environment [16]byte
			flag                 *[16]byte
			source               string
		)
		scanErr := row.Scan(&project, &environment, &flag, &source, &r.First, &r.Variation, &r.Reason, &r.Count,
			&r.PeriodEnd, &r.ReceivedAt)
		r.Project, r.Environment = id.FromBytes[workspace.Project](project), id.FromBytes[workspace.Environment](environment)
		if flag != nil {
			r.Flag = id.FromBytes[flags.Flag](*flag)
		}
		r.Source = delivery.SummarySource(source)
		r.PeriodEnd, r.ReceivedAt = r.PeriodEnd.UTC(), r.ReceivedAt.UTC()
		return r, scanErr
	})
	if err != nil {
		return nil, events.Cursor{}, fmt.Errorf("reading summaries to roll up: %w", err)
	}
	return out, b.To, nil
}

// maxCount is the largest bigint, where an evaluation count stops.
const maxCount = "9223372036854775807"

// AddEvaluationCounts adds counts to the evaluations per day, each stopping
// at the largest bigint rather than failing the batch.
func (s *Store) AddEvaluationCounts(ctx context.Context, counts map[events.EvaluationDay]int64) error {
	if len(counts) == 0 {
		return nil
	}
	var (
		fs, environments                   [][16]byte
		days, sources, variations, reasons []string
		n                                  []int64
	)
	for k, c := range counts {
		fs, environments = append(fs, k.Flag.Bytes()), append(environments, k.Environment.Bytes())
		days, sources = append(days, k.Day.Format(time.DateOnly)), append(sources, string(k.Source))
		variations, reasons, n = append(variations, k.Variation), append(reasons, k.Reason), append(n, c)
	}
	// Rows are written in the key's order, so two writers never wait on each
	// other's rows in turn.
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO evaluation_counts_daily AS d
			(flag_id, environment_id, day, source, variation_key, evaluation_reason, evaluations)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::date[], $4::text[], $5::text[], $6::text[], $7::bigint[])
			AS t (flag_id, environment_id, day, source, variation_key, evaluation_reason, evaluations)
		ORDER BY flag_id, environment_id, day, source, variation_key, evaluation_reason
		ON CONFLICT (flag_id, environment_id, day, source, variation_key, evaluation_reason)
		DO UPDATE SET evaluations = least(d.evaluations::numeric + excluded.evaluations, `+maxCount+`)::bigint`,
		fs, environments, days, sources, variations, reasons, n)
	if err != nil {
		return fmt.Errorf("adding evaluation counts: %w", err)
	}
	return nil
}

// MoveLastEvaluated moves each flag's last evaluation in each environment to
// the time given, unless it holds a later one. Both rollups write it, so rows
// are written in the key's order.
func (s *Store) MoveLastEvaluated(ctx context.Context, last map[events.FlagEnvironment]time.Time) error {
	if len(last) == 0 {
		return nil
	}
	var (
		fs, environments [][16]byte
		at               []time.Time
	)
	for k, t := range last {
		fs, environments, at = append(fs, k.Flag.Bytes()), append(environments, k.Environment.Bytes()), append(at, t)
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO flag_last_evaluated AS l (flag_id, environment_id, last_evaluated_at)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::timestamptz[]) AS t (flag_id, environment_id, last_evaluated_at)
		ORDER BY flag_id, environment_id
		ON CONFLICT (flag_id, environment_id)
		DO UPDATE SET last_evaluated_at = greatest(l.last_evaluated_at, excluded.last_evaluated_at)`,
		fs, environments, at)
	if err != nil {
		return fmt.Errorf("moving when flags were last evaluated: %w", err)
	}
	return nil
}

// FlagOf returns project's flag keyed key, archived or not, or
// appevents.ErrNotFound.
func (s *Store) FlagOf(ctx context.Context, project workspace.ProjectID, key string) (flags.FlagID, error) {
	var b [16]byte
	err := s.db(ctx).QueryRow(ctx, `SELECT id FROM flags WHERE project_id = $1 AND key = $2`, project.Bytes(), key).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return flags.FlagID{}, appevents.ErrNotFound
	}
	if err != nil {
		return flags.FlagID{}, fmt.Errorf("reading the flag %q: %w", key, err)
	}
	return id.FromBytes[flags.Flag](b), nil
}

// EvaluationCounts returns the flag's evaluations in environment per day
// from first to last, both included, by day, source, variation and reason.
func (s *Store) EvaluationCounts(ctx context.Context, flag flags.FlagID, environment workspace.EnvironmentID,
	first, last time.Time,
) ([]events.FlagEvaluationDay, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT day, source, variation_key, evaluation_reason, evaluations FROM evaluation_counts_daily
		WHERE flag_id = $1 AND environment_id = $2 AND day BETWEEN $3::date AND $4::date
		ORDER BY day, source, variation_key, evaluation_reason`,
		flag.Bytes(), environment.Bytes(), first.Format(time.DateOnly), last.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("reading evaluation counts: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.FlagEvaluationDay, error) {
		var (
			d      events.FlagEvaluationDay
			source string
		)
		scanErr := row.Scan(&d.Day, &source, &d.Variation, &d.Reason, &d.Evaluations)
		d.Day, d.Source = d.Day.UTC(), delivery.SummarySource(source)
		return d, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading evaluation counts: %w", err)
	}
	return out, nil
}

// LastEvaluated returns when SDKs last evaluated each of fs, by flag then
// environment key; a flag never evaluated in an environment is absent.
func (s *Store) LastEvaluated(ctx context.Context, fs []flags.FlagID) (map[flags.FlagID]map[string]time.Time, error) {
	out := map[flags.FlagID]map[string]time.Time{}
	if len(fs) == 0 {
		return out, nil
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT l.flag_id, e.key, l.last_evaluated_at FROM flag_last_evaluated l
		JOIN environments e ON e.id = l.environment_id WHERE l.flag_id = ANY($1)`, bytesOf(fs))
	if err != nil {
		return nil, fmt.Errorf("reading when flags were last evaluated: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			flag        [16]byte
			environment string
			at          time.Time
		)
		if err = rows.Scan(&flag, &environment, &at); err != nil {
			return nil, fmt.Errorf("reading when flags were last evaluated: %w", err)
		}
		f := id.FromBytes[flags.Flag](flag)
		if out[f] == nil {
			out[f] = map[string]time.Time{}
		}
		out[f][environment] = at.UTC()
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("reading when flags were last evaluated: %w", err)
	}
	return out, nil
}
