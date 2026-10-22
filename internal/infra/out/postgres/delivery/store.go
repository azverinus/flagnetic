// Package delivery is the PostgreSQL repository of the delivery context: for
// now, the ruleset version of each environment.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// RulesetsChannel is the notification channel on which every ruleset
// version committed is announced, as "<environment>:<version>:<bumped at in
// Unix milliseconds>", and the deletion of an environment, as
// "<environment>" alone, by a trigger on the environments table so a
// deletion cascading from a project is announced too.
const RulesetsChannel = "rulesets"

// Store is the repository of ruleset versions.
type Store struct {
	pool *pgxpool.Pool
	// committed is told every version this process commits, nil when no one
	// listens.
	committed func(environment workspace.EnvironmentID, version int64, bumpedAt time.Time)
	// partitions are the days whose event partitions this process knows
	// exist, as "2006-01-02", so that it creates a missing one at most once.
	partitions sync.Map
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// OnCommitted tells f every ruleset version a bump through the store
// commits, once it commits and before the unit of work returns, so the
// replica that made a write serves it without waiting for the notification.
// It is set once, while the replica is assembled.
func (s *Store) OnCommitted(f func(environment workspace.EnvironmentID, version int64, bumpedAt time.Time)) {
	s.committed = f
}

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// BumpProject moves the ruleset version of every environment of project on
// to its next version at now (delivery.NextRulesetVersion).
func (s *Store) BumpProject(ctx context.Context, project workspace.ProjectID, now time.Time) error {
	return s.bump(ctx, `project_id = $1`, project.Bytes(), now)
}

// BumpEnvironment moves the ruleset version of environment on to its next
// version at now, as BumpProject does.
func (s *Store) BumpEnvironment(ctx context.Context, environment workspace.EnvironmentID, now time.Time) error {
	return s.bump(ctx, `id = $1`, environment.Bytes(), now)
}

// bump moves on the ruleset version of the environments where selects. The
// rule is applied in the statement, on the row it locks, so two bumps
// racing on one environment never both start from the same previous
// version. Each new version is announced on RulesetsChannel in the same
// statement, which PostgreSQL delivers once the transaction commits and
// never if it rolls back, with now, so a replica can tell how long the
// version took to reach an SDK.
func (s *Store) bump(ctx context.Context, where string, arg [16]byte, now time.Time) error {
	rows, err := s.db(ctx).Query(ctx, `
		WITH bumped AS (
			UPDATE environments SET ruleset_version = greatest(ruleset_version + 1, $2)
			WHERE `+where+`
			RETURNING id, ruleset_version
		)
		SELECT id, ruleset_version, pg_notify($3, id::text || ':' || ruleset_version || ':' || $4::bigint) FROM bumped`,
		arg, delivery.RulesetVersionFloor(now), RulesetsChannel, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("bumping ruleset versions: %w", err)
	}
	type bumped struct {
		environment workspace.EnvironmentID
		version     int64
	}
	versions, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (bumped, error) {
		var (
			environment [16]byte
			b           bumped
		)
		scanErr := row.Scan(&environment, &b.version, nil)
		b.environment = id.FromBytes[workspace.Environment](environment)
		return b, scanErr
	})
	if err != nil {
		return fmt.Errorf("bumping ruleset versions: %w", err)
	}
	if s.committed != nil {
		pgxtx.AfterCommit(ctx, func() {
			for _, b := range versions {
				s.committed(b.environment, b.version, now)
			}
		})
	}
	return nil
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
