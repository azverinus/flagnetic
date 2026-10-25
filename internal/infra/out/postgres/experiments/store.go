// Package experiments is the PostgreSQL repository of the experiments
// context.
package experiments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Store is the repository of experiments.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// LockProject locks project's row for the unit of work, or answers
// appexperiments.ErrNotFound when there is none. It is the lock the flags
// context's writes take, so the two contexts' writes of a project run one
// at a time.
func (s *Store) LockProject(ctx context.Context, project workspace.ProjectID) error {
	var one int
	err := s.db(ctx).QueryRow(ctx, `SELECT 1 FROM projects WHERE id = $1 FOR UPDATE`, project.Bytes()).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return appexperiments.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("locking a project: %w", err)
	}
	return nil
}

// weightDoc is a weight as the experiments table stores it.
type weightDoc struct {
	Variation string `json:"variation"`
	Weight    int    `json:"weight"`
}

// CreateExperiment stores e.
func (s *Store) CreateExperiment(ctx context.Context, e experiments.Experiment) error {
	docs := make([]weightDoc, len(e.Variations))
	for i, w := range e.Variations {
		docs[i] = weightDoc(w)
	}
	variations, err := json.Marshal(docs)
	if err != nil {
		return err
	}
	_, err = s.db(ctx).Exec(ctx, `
		INSERT INTO experiments (id, project_id, key, name, hypothesis, owner_id, tags, flag_id, unit_type, variations,
		    traffic_allocation, inclusion_salt, variation_salt, status, expected_end_at, started_at, stopped_at,
		    archived_at, created_by, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`,
		e.ID.Bytes(), e.Project.Bytes(), e.Key, e.Name, e.Hypothesis, e.Owner.Bytes(), e.Tags, e.Flag.Bytes(), e.UnitType,
		variations, e.TrafficAllocation, e.InclusionSalt, e.VariationSalt, string(e.Status), e.ExpectedEndAt, e.StartedAt,
		e.StoppedAt, e.ArchivedAt, optionalMember(e.CreatedBy), e.CreatedAt, e.UpdatedAt, e.Version)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "experiments_project_id_key_key" {
		return apperrors.Conflict("already_exists", "the project has an experiment with this key already").WithCause(err)
	}
	return err
}

func optionalMember(m *access.MemberID) any {
	if m == nil {
		return nil
	}
	return m.Bytes()
}

// ExperimentSalts returns both salts of every experiment of project.
func (s *Store) ExperimentSalts(ctx context.Context, project workspace.ProjectID) ([]string, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT inclusion_salt FROM experiments WHERE project_id = $1
		UNION ALL
		SELECT variation_salt FROM experiments WHERE project_id = $1`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading the salts of experiments: %w", err)
	}
	salts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("reading the salts of experiments: %w", err)
	}
	return salts, nil
}

const columns = `e.id, e.project_id, e.key, e.name, e.hypothesis, e.owner_id, e.tags, e.flag_id, f.key, e.unit_type,
	e.variations, e.traffic_allocation, e.inclusion_salt, e.variation_salt, e.status, e.expected_end_at, e.started_at,
	e.stopped_at, e.archived_at, e.created_by, e.created_at, e.updated_at, e.version, e.decision_outcome,
	e.decision_winner, e.decision_note`

// from joins an experiment to its flag, whose key it carries.
const from = ` FROM experiments e JOIN flags f ON f.id = e.flag_id`

func scan(row pgx.Row) (experiments.Experiment, error) {
	var (
		e                                experiments.Experiment
		ek, pk, owner, fk                [16]byte
		createdBy                        *[16]byte
		status                           string
		variations                       []byte
		expectedEnd                      *time.Time
		startedAt, stoppedAt, archivedAt *time.Time
		outcome, winner, note            *string
	)
	if err := row.Scan(&ek, &pk, &e.Key, &e.Name, &e.Hypothesis, &owner, &e.Tags, &fk, &e.FlagKey, &e.UnitType,
		&variations, &e.TrafficAllocation, &e.InclusionSalt, &e.VariationSalt, &status, &expectedEnd, &startedAt,
		&stoppedAt, &archivedAt, &createdBy, &e.CreatedAt, &e.UpdatedAt, &e.Version, &outcome, &winner, &note); err != nil {
		return experiments.Experiment{}, err
	}
	var docs []weightDoc
	if err := json.Unmarshal(variations, &docs); err != nil {
		return experiments.Experiment{}, fmt.Errorf("decoding weights: %w", err)
	}
	for _, d := range docs {
		e.Variations = append(e.Variations, experiments.Weight(d))
	}
	e.ID, e.Project = id.FromBytes[experiments.Experiment](ek), id.FromBytes[workspace.Project](pk)
	e.Owner, e.Flag = id.FromBytes[access.Member](owner), id.FromBytes[flags.Flag](fk)
	e.Status = experiments.Status(status)
	e.ExpectedEndAt, e.StartedAt, e.StoppedAt, e.ArchivedAt = utc(expectedEnd), utc(startedAt), utc(stoppedAt), utc(archivedAt)
	e.CreatedAt, e.UpdatedAt = e.CreatedAt.UTC(), e.UpdatedAt.UTC()
	if createdBy != nil {
		m := id.FromBytes[access.Member](*createdBy)
		e.CreatedBy = &m
	}
	if outcome != nil {
		e.Decision = &experiments.Decision{Outcome: experiments.Outcome(*outcome)}
		if winner != nil {
			e.Decision.WinnerVariation = *winner
		}
		if note != nil {
			e.Decision.Note = *note
		}
	}
	if e.Tags == nil {
		e.Tags = []string{}
	}
	return e, nil
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// ExperimentByKey returns project's experiment with key, or
// appexperiments.ErrNotFound.
func (s *Store) ExperimentByKey(ctx context.Context, project workspace.ProjectID, key string) (experiments.Experiment, error) {
	e, err := scan(s.db(ctx).QueryRow(ctx, `SELECT `+columns+from+` WHERE e.project_id = $1 AND e.key = $2`,
		project.Bytes(), key))
	if errors.Is(err, pgx.ErrNoRows) {
		return e, appexperiments.ErrNotFound
	}
	if err != nil {
		return e, fmt.Errorf("reading an experiment: %w", err)
	}
	return e, nil
}

// Experiments returns up to limit experiments of project matching filter by
// key in byte order, those whose key is greater than after when it is not "".
func (s *Store) Experiments(ctx context.Context, project workspace.ProjectID, filter appexperiments.ExperimentFilter,
	after string, limit int) ([]experiments.Experiment, error) {
	var owner any
	if filter.Owner != nil {
		owner = filter.Owner.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+columns+from+`
		WHERE e.project_id = $1 AND ($2 = '' OR e.key COLLATE "C" > $2)
		  AND ($3 = '' OR e.status = $3)
		  AND ($4 OR $3 = 'archived' OR e.status <> 'archived')
		  AND ($5 = '' OR f.key = $5)
		  AND ($6::uuid IS NULL OR e.owner_id = $6)
		  AND ($7 = '' OR $7 = ANY (e.tags))
		ORDER BY e.key COLLATE "C"
		LIMIT $8`, project.Bytes(), after, string(filter.Status), filter.IncludeArchived, filter.Flag, owner, filter.Tag, limit)
	if err != nil {
		return nil, fmt.Errorf("listing experiments: %w", err)
	}
	defer rows.Close()
	out := []experiments.Experiment{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateExperiment stores the changed definition e, which keeps its key,
// project and creation.
func (s *Store) UpdateExperiment(ctx context.Context, e experiments.Experiment) error {
	docs := make([]weightDoc, len(e.Variations))
	for i, w := range e.Variations {
		docs[i] = weightDoc(w)
	}
	variations, err := json.Marshal(docs)
	if err != nil {
		return err
	}
	var outcome, winner, note any
	if d := e.Decision; d != nil {
		outcome, note = string(d.Outcome), d.Note
		if d.WinnerVariation != "" {
			winner = d.WinnerVariation
		}
	}
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE experiments SET name = $2, hypothesis = $3, owner_id = $4, tags = $5, flag_id = $6, unit_type = $7,
		    variations = $8, traffic_allocation = $9, inclusion_salt = $10, variation_salt = $11, status = $12,
		    expected_end_at = $13, started_at = $14, stopped_at = $15, archived_at = $16, updated_at = $17, version = $18,
		    decision_outcome = $19, decision_winner = $20, decision_note = $21
		WHERE id = $1`,
		e.ID.Bytes(), e.Name, e.Hypothesis, e.Owner.Bytes(), e.Tags, e.Flag.Bytes(), e.UnitType, variations,
		e.TrafficAllocation, e.InclusionSalt, e.VariationSalt, string(e.Status), e.ExpectedEndAt, e.StartedAt,
		e.StoppedAt, e.ArchivedAt, e.UpdatedAt, e.Version, outcome, winner, note)
	if err != nil {
		return fmt.Errorf("updating an experiment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return appexperiments.ErrNotFound
	}
	return nil
}

// DeleteExperiment deletes the experiment.
func (s *Store) DeleteExperiment(ctx context.Context, experiment experiments.ExperimentID) error {
	tag, err := s.db(ctx).Exec(ctx, `DELETE FROM experiments WHERE id = $1`, experiment.Bytes())
	if err != nil {
		return fmt.Errorf("deleting an experiment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return appexperiments.ErrNotFound
	}
	return nil
}
