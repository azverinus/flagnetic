// Package delivery is the PostgreSQL repository of the delivery context: for
// now, the ruleset version of each environment.
package delivery

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
)

// Store is the repository of ruleset versions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// BumpEnvironment increments the ruleset version of environment.
func (s *Store) BumpEnvironment(ctx context.Context, environment workspace.EnvironmentID) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE environments SET ruleset_version = ruleset_version + 1 WHERE id = $1`,
		environment.Bytes())
	return err
}

// BumpProject increments the ruleset version of every environment of
// project.
func (s *Store) BumpProject(ctx context.Context, project workspace.ProjectID) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE environments SET ruleset_version = ruleset_version + 1 WHERE project_id = $1`,
		project.Bytes())
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
