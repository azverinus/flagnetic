package experiments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// checksLock is the session advisory lock a replica holds for a round of the
// checks. The schema is part of its key, so instances sharing one database in
// separate schemas, as tests do, never wait on each other.
const checksLock = `hashtextextended('experiment_checks@' || current_schema(), 0)`

// unlockBound bounds letting the checks' lock go once a round ends, whatever
// became of the round's own context.
const unlockBound = 5 * time.Second

// TryLockChecks takes the checks' lock on a connection of its own, held
// across the round's units of work without keeping a transaction open, which
// would hold the rollups' snapshot horizon back for the whole round.
func (s *Store) TryLockChecks(ctx context.Context) (release func(), held bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquiring a connection for the checks' lock: %w", err)
	}
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(`+checksLock+`)`).Scan(&held); err != nil || !held {
		conn.Release()
		if err != nil {
			err = fmt.Errorf("taking the checks' lock: %w", err)
		}
		return nil, false, err
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), unlockBound)
		defer cancel()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(`+checksLock+`)`); err != nil {
			// A connection whose lock may still be held must not go back
			// to the pool: closing it lets the lock go.
			_ = conn.Hijack().Close(ctx)
			return
		}
		conn.Release()
	}, true, nil
}

// RunningExperiments returns the running experiments of every project.
func (s *Store) RunningExperiments(ctx context.Context) ([]experiments.Experiment, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+columns+from+` WHERE e.status = 'running' ORDER BY e.id`)
	if err != nil {
		return nil, fmt.Errorf("listing running experiments: %w", err)
	}
	defer rows.Close()
	var out []experiments.Experiment
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EnvironmentByKey returns project's environment keyed key, or
// appexperiments.ErrNotFound.
func (s *Store) EnvironmentByKey(ctx context.Context, project workspace.ProjectID, key string) (workspace.EnvironmentID, error) {
	var env [16]byte
	err := s.db(ctx).QueryRow(ctx, `SELECT id FROM environments WHERE project_id = $1 AND key = $2`, project.Bytes(), key).Scan(&env)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.EnvironmentID{}, appexperiments.ErrNotFound
	}
	if err != nil {
		return workspace.EnvironmentID{}, fmt.Errorf("reading an environment: %w", err)
	}
	return id.FromBytes[workspace.Environment](env), nil
}

// SaveSampleRatio stores r, checked at, as the latest check of experiment in
// environment.
func (s *Store) SaveSampleRatio(ctx context.Context, experiment experiments.ExperimentID, environment workspace.EnvironmentID,
	r experiments.SampleRatio, at time.Time,
) error {
	var skipped any
	if r.Skipped != "" {
		skipped = string(r.Skipped)
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO experiment_sample_ratios (experiment_id, environment_id, n, chi_square, p_value, skipped_reason, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (experiment_id, environment_id) DO UPDATE SET
		    n = excluded.n, chi_square = excluded.chi_square, p_value = excluded.p_value,
		    skipped_reason = excluded.skipped_reason, checked_at = excluded.checked_at`,
		experiment.Bytes(), environment.Bytes(), r.N, r.ChiSquare, r.PValue, skipped, at)
	if err != nil {
		return fmt.Errorf("storing a sample-ratio check: %w", err)
	}
	return nil
}

// SampleRatioOf returns the latest sample-ratio check of experiment in
// environment, nil before the first.
func (s *Store) SampleRatioOf(ctx context.Context, experiment experiments.ExperimentID, environment workspace.EnvironmentID,
) (*appexperiments.SampleRatioState, error) {
	var (
		st      appexperiments.SampleRatioState
		skipped *string
	)
	err := s.db(ctx).QueryRow(ctx, `
		SELECT r.n, r.chi_square, r.p_value, r.skipped_reason, r.checked_at,
		    EXISTS (SELECT 1 FROM experiment_alerts a
		        WHERE a.experiment_id = r.experiment_id AND a.environment_id = r.environment_id
		          AND a.kind = $3 AND a.cleared_at IS NULL)
		FROM experiment_sample_ratios r
		WHERE r.experiment_id = $1 AND r.environment_id = $2`,
		experiment.Bytes(), environment.Bytes(), string(experiments.AlertKindSampleRatio),
	).Scan(&st.N, &st.ChiSquare, &st.PValue, &skipped, &st.CheckedAt, &st.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading a sample-ratio check: %w", err)
	}
	if skipped != nil {
		st.Skipped = experiments.SampleRatioSkip(*skipped)
	}
	st.CheckedAt = st.CheckedAt.UTC()
	return &st, nil
}

// sampleRatioDoc is a sample-ratio check as an alert's details store it.
type sampleRatioDoc struct {
	N          int64                   `json:"n"`
	ChiSquare  *float64                `json:"chi_square"`
	PValue     *float64                `json:"p_value"`
	Variations []sampleRatioVariationDoc `json:"variations"`
}

type sampleRatioVariationDoc struct {
	Variation     string  `json:"variation"`
	Observed      int64   `json:"observed"`
	Expected      float64 `json:"expected"`
	ObservedShare float64 `json:"observed_share"`
	ExpectedShare float64 `json:"expected_share"`
}

// detailsDoc is an alert's details as stored: one block per kind, the
// alert's own set.
type detailsDoc struct {
	SampleRatio *sampleRatioDoc `json:"sample_ratio"`
}

func encodeDetails(d experiments.AlertDetails) ([]byte, error) {
	var doc detailsDoc
	if r := d.SampleRatio; r != nil {
		doc.SampleRatio = &sampleRatioDoc{N: r.N, ChiSquare: r.ChiSquare, PValue: r.PValue}
		for _, v := range r.Variations {
			doc.SampleRatio.Variations = append(doc.SampleRatio.Variations, sampleRatioVariationDoc(v))
		}
	}
	return json.Marshal(doc)
}

func decodeDetails(b []byte) (experiments.AlertDetails, error) {
	var doc detailsDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return experiments.AlertDetails{}, fmt.Errorf("decoding an alert's details: %w", err)
	}
	var d experiments.AlertDetails
	if r := doc.SampleRatio; r != nil {
		d.SampleRatio = &experiments.SampleRatio{N: r.N, ChiSquare: r.ChiSquare, PValue: r.PValue}
		for _, v := range r.Variations {
			d.SampleRatio.Variations = append(d.SampleRatio.Variations, experiments.SampleRatioVariation(v))
		}
	}
	return d, nil
}

// SaveAlert stores a, new or changed. Only when it clears and when it is
// acknowledged does an alert change.
func (s *Store) SaveAlert(ctx context.Context, a experiments.Alert) error {
	details, err := encodeDetails(a.Details)
	if err != nil {
		return err
	}
	var env any
	if a.Environment != nil {
		env = a.Environment.Bytes()
	}
	_, err = s.db(ctx).Exec(ctx, `
		INSERT INTO experiment_alerts (id, experiment_id, environment_id, kind, raised_at, cleared_at, acknowledged_at, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET cleared_at = excluded.cleared_at, acknowledged_at = excluded.acknowledged_at`,
		a.ID.Bytes(), a.Experiment.Bytes(), env, string(a.Kind), a.RaisedAt, a.ClearedAt, a.AcknowledgedAt, details)
	if err != nil {
		return fmt.Errorf("storing an alert: %w", err)
	}
	return nil
}

const alertColumns = `a.id, a.experiment_id, a.environment_id, env.key, a.kind, a.raised_at, a.cleared_at, a.acknowledged_at, a.details`

const alertFrom = ` FROM experiment_alerts a LEFT JOIN environments env ON env.id = a.environment_id`

func scanAlert(row pgx.Row) (appexperiments.AlertView, error) {
	var (
		v              appexperiments.AlertView
		aid, xid       [16]byte
		env            *[16]byte
		kind           string
		details        []byte
		cleared, acked *time.Time
	)
	if err := row.Scan(&aid, &xid, &env, &v.EnvironmentKey, &kind, &v.RaisedAt, &cleared, &acked, &details); err != nil {
		return v, err
	}
	v.ID, v.Experiment = id.FromBytes[experiments.Alert](aid), id.FromBytes[experiments.Experiment](xid)
	if env != nil {
		e := id.FromBytes[workspace.Environment](*env)
		v.Environment = &e
	}
	v.Kind = experiments.AlertKind(kind)
	v.RaisedAt, v.ClearedAt, v.AcknowledgedAt = v.RaisedAt.UTC(), utc(cleared), utc(acked)
	var err error
	v.Details, err = decodeDetails(details)
	return v, err
}

// ActiveAlert returns the active alert of kind of experiment in environment,
// nil for the experiment as a whole, locked until the unit of work ends; nil
// when there is none.
func (s *Store) ActiveAlert(ctx context.Context, experiment experiments.ExperimentID, environment *workspace.EnvironmentID,
	kind experiments.AlertKind,
) (*experiments.Alert, error) {
	var env any
	if environment != nil {
		env = environment.Bytes()
	}
	v, err := scanAlert(s.db(ctx).QueryRow(ctx, `SELECT `+alertColumns+alertFrom+`
		WHERE a.experiment_id = $1 AND a.environment_id IS NOT DISTINCT FROM $2::uuid AND a.kind = $3 AND a.cleared_at IS NULL
		FOR UPDATE OF a`, experiment.Bytes(), env, string(kind)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading an active alert: %w", err)
	}
	return &v.Alert, nil
}

// Alerts returns experiment's alerts, the most recently raised first; active,
// when not nil, keeps only the active (true) or the cleared (false) ones.
func (s *Store) Alerts(ctx context.Context, experiment experiments.ExperimentID, active *bool) ([]appexperiments.AlertView, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+alertColumns+alertFrom+`
		WHERE a.experiment_id = $1 AND ($2::boolean IS NULL OR (a.cleared_at IS NULL) = $2)
		ORDER BY a.raised_at DESC, a.id DESC`, experiment.Bytes(), active)
	if err != nil {
		return nil, fmt.Errorf("listing alerts: %w", err)
	}
	defer rows.Close()
	out := []appexperiments.AlertView{}
	for rows.Next() {
		v, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("listing alerts: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AlertOf returns experiment's alert alert, locked until the unit of work
// ends, or appexperiments.ErrNotFound.
func (s *Store) AlertOf(ctx context.Context, experiment experiments.ExperimentID, alert experiments.AlertID) (appexperiments.AlertView, error) {
	v, err := scanAlert(s.db(ctx).QueryRow(ctx, `SELECT `+alertColumns+alertFrom+`
		WHERE a.id = $1 AND a.experiment_id = $2 FOR UPDATE OF a`, alert.Bytes(), experiment.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, appexperiments.ErrNotFound
	}
	if err != nil {
		return v, fmt.Errorf("reading an alert: %w", err)
	}
	return v, nil
}
