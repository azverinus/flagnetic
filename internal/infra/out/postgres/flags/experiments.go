package flags

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
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
