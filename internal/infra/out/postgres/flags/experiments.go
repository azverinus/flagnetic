package flags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ExperimentFlag returns the facts about project's flag with key that an
// experiment is judged against, or appexperiments.ErrNotFound.
func (s *Store) ExperimentFlag(ctx context.Context, project workspace.ProjectID, key string) (experiments.FlagFacts, error) {
	f, err := s.FlagByKey(ctx, project, key)
	if errors.Is(err, appflags.ErrNotFound) {
		return experiments.FlagFacts{}, appexperiments.ErrNotFound
	}
	if err != nil {
		return experiments.FlagFacts{}, err
	}
	facts := experiments.FlagFacts{ID: f.ID, Key: f.Key, Archived: f.ArchivedAt != nil}
	for _, v := range f.Variations {
		facts.Variations = append(facts.Variations, v.Key)
	}
	return facts, nil
}

// RolloutSalts returns the salt of every rollout in every flag
// configuration of project, archived flags included: an archived flag can
// be restored, and its rollouts with it.
func (s *Store) RolloutSalts(ctx context.Context, project workspace.ProjectID) ([]string, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT r->'serve'->>'salt'
		FROM flag_configurations c
		JOIN flags f ON f.id = c.flag_id
		CROSS JOIN LATERAL jsonb_array_elements(c.document->'rules') r
		WHERE f.project_id = $1 AND r->'serve'->>'salt' IS NOT NULL`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading rollout salts: %w", err)
	}
	salts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("reading rollout salts: %w", err)
	}
	return salts, nil
}

// ProjectExperiments returns every experiment of project, whatever its
// status, by key in byte order.
func (s *Store) ProjectExperiments(ctx context.Context, project workspace.ProjectID) ([]flags.ExperimentRef, error) {
	return s.experimentRefs(ctx, `e.project_id = $1`, project.Bytes())
}

// FlagExperiments returns the experiments of flag, whatever their status,
// by key in byte order.
func (s *Store) FlagExperiments(ctx context.Context, flag flags.FlagID) ([]flags.ExperimentRef, error) {
	return s.experimentRefs(ctx, `e.flag_id = $1`, flag.Bytes())
}

// experimentRefs returns the experiments where holds, by key in byte order.
func (s *Store) experimentRefs(ctx context.Context, where string, arg any) ([]flags.ExperimentRef, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT e.key, e.flag_id, e.status, e.unit_type, e.inclusion_salt, e.variation_salt, e.traffic_allocation, e.variations
		FROM experiments e
		WHERE `+where+`
		ORDER BY e.key COLLATE "C"`, arg)
	if err != nil {
		return nil, fmt.Errorf("reading experiments: %w", err)
	}
	defer rows.Close()
	var out []flags.ExperimentRef
	for rows.Next() {
		var (
			e       flags.ExperimentRef
			flag    [16]byte
			weights []byte
			docs    []struct {
				Variation string `json:"variation"`
				Weight    int    `json:"weight"`
			}
		)
		if err := rows.Scan(&e.Key, &flag, &e.Status, &e.UnitType, &e.InclusionSalt, &e.VariationSalt, &e.TrafficAllocation, &weights); err != nil {
			return nil, fmt.Errorf("reading experiments: %w", err)
		}
		if err := json.Unmarshal(weights, &docs); err != nil {
			return nil, fmt.Errorf("decoding the weights of experiment %s: %w", e.Key, err)
		}
		e.Flag = id.FromBytes[flags.Flag](flag)
		for _, d := range docs {
			e.Weights = append(e.Weights, flags.ExperimentWeight{Variation: d.Variation, Weight: d.Weight})
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HoldingEnvironments returns the environments of project whose
// configuration of some flag has a rule serving the experiment keyed key,
// each once.
func (s *Store) HoldingEnvironments(ctx context.Context, project workspace.ProjectID, key string) ([]workspace.EnvironmentID, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT DISTINCT c.environment_id
		FROM flag_configurations c
		JOIN flags f ON f.id = c.flag_id
		WHERE f.project_id = $1
		  AND c.document @> jsonb_build_object('rules', jsonb_build_array(
		      jsonb_build_object('serve', jsonb_build_object('kind', 'experiment', 'experiment', $2::text))))`,
		project.Bytes(), key)
	if err != nil {
		return nil, fmt.Errorf("reading the environments holding an experiment: %w", err)
	}
	envs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (workspace.EnvironmentID, error) {
		var env [16]byte
		scanErr := row.Scan(&env)
		return id.FromBytes[workspace.Environment](env), scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("reading the environments holding an experiment: %w", err)
	}
	return envs, nil
}
