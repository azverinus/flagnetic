// Package delivery is the PostgreSQL repository of the delivery context: for
// now, the ruleset version of each environment.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Store is the repository of ruleset versions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// BumpProject moves the ruleset version of every environment of project on
// to its next version at now (delivery.NextRulesetVersion). The rule is
// applied in the statement, on the row it locks, so two bumps racing on one
// environment never both start from the same previous version.
func (s *Store) BumpProject(ctx context.Context, project workspace.ProjectID, now time.Time) error {
	_, err := s.db(ctx).Exec(ctx,
		`UPDATE environments SET ruleset_version = greatest(ruleset_version + 1, $2) WHERE project_id = $1`,
		project.Bytes(), delivery.RulesetVersionFloor(now))
	return err
}

// BumpEnvironment moves the ruleset version of environment on to its next
// version at now, as BumpProject does.
func (s *Store) BumpEnvironment(ctx context.Context, environment workspace.EnvironmentID, now time.Time) error {
	_, err := s.db(ctx).Exec(ctx,
		`UPDATE environments SET ruleset_version = greatest(ruleset_version + 1, $2) WHERE id = $1`,
		environment.Bytes(), delivery.RulesetVersionFloor(now))
	return err
}

// RulesetVersions returns the ruleset version of every environment of
// project, by environment key.
func (s *Store) RulesetVersions(ctx context.Context, project workspace.ProjectID) (map[string]int64, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT key, ruleset_version FROM environments WHERE project_id = $1`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("reading ruleset versions: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			key string
			v   int64
		)
		if err := rows.Scan(&key, &v); err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, rows.Err()
}

// RulesetEnvironment returns the identifier and ruleset version of the
// environment of project keyed key, or appdelivery.ErrNotFound.
func (s *Store) RulesetEnvironment(ctx context.Context, project workspace.ProjectID, key string) (workspace.EnvironmentID, int64, error) {
	var (
		environment [16]byte
		version     int64
	)
	err := s.db(ctx).QueryRow(ctx, `SELECT id, ruleset_version FROM environments WHERE project_id = $1 AND key = $2`,
		project.Bytes(), key).Scan(&environment, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.EnvironmentID{}, 0, appdelivery.ErrNotFound
	}
	if err != nil {
		return workspace.EnvironmentID{}, 0, fmt.Errorf("reading an environment's ruleset version: %w", err)
	}
	return id.FromBytes[workspace.Environment](environment), version, nil
}

// RulesetEnvironmentByID returns the project, key and ruleset version of
// environment, or appdelivery.ErrNotFound.
func (s *Store) RulesetEnvironmentByID(ctx context.Context, environment workspace.EnvironmentID,
) (workspace.ProjectID, string, int64, error) {
	var (
		project [16]byte
		key     string
		version int64
	)
	err := s.db(ctx).QueryRow(ctx, `SELECT project_id, key, ruleset_version FROM environments WHERE id = $1`,
		environment.Bytes()).Scan(&project, &key, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.ProjectID{}, "", 0, appdelivery.ErrNotFound
	}
	if err != nil {
		return workspace.ProjectID{}, "", 0, fmt.Errorf("reading an environment's ruleset version: %w", err)
	}
	return id.FromBytes[workspace.Project](project), key, version, nil
}
