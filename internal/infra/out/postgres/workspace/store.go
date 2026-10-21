// Package workspace is the PostgreSQL repository of the workspace context:
// projects, their environments, unit types and attributes.
package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Store is the repository of projects and what they declare.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// errKeyTaken refuses an entity whose key its project, or the instance for a
// project, already uses.
var errKeyTaken = apperrors.Conflict("already_exists", "the key is already in use")

// keyTaken returns errKeyTaken for a unique violation of a key, err
// otherwise.
func keyTaken(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errKeyTaken.WithCause(err)
	}
	return err
}

// CreateProject stores p.
func (s *Store) CreateProject(ctx context.Context, p workspace.Project) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO projects (id, key, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		p.ID.Bytes(), p.Key, p.Name, p.Description, p.CreatedAt, p.UpdatedAt)
	return keyTaken(err)
}

// UpdateProject stores p's name and description.
func (s *Store) UpdateProject(ctx context.Context, p workspace.Project) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE projects SET name = $2, description = $3, updated_at = $4 WHERE id = $1`,
		p.ID.Bytes(), p.Name, p.Description, p.UpdatedAt)
	return err
}

const projectColumns = `id, key, name, description, created_at, updated_at`

func scanProject(row pgx.Row) (workspace.Project, error) {
	var (
		p  workspace.Project
		pk [16]byte
	)
	if err := row.Scan(&pk, &p.Key, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return workspace.Project{}, err
	}
	p.ID = id.FromBytes[workspace.Project](pk)
	return p, nil
}

// ProjectByKey returns the project with key, or appworkspace.ErrNotFound.
func (s *Store) ProjectByKey(ctx context.Context, key string) (workspace.Project, error) {
	return s.oneProject(ctx, `SELECT `+projectColumns+` FROM projects WHERE key = $1`, key)
}

// LockProject returns the project with projectID, locking its row until
// the unit of work ends, so changes to the project and to what it holds are
// made one at a time.
func (s *Store) LockProject(ctx context.Context, projectID workspace.ProjectID) (workspace.Project, error) {
	return s.oneProject(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = $1 FOR UPDATE`, projectID.Bytes())
}

func (s *Store) oneProject(ctx context.Context, sql string, arg any) (workspace.Project, error) {
	p, err := scanProject(s.db(ctx).QueryRow(ctx, sql, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.Project{}, appworkspace.ErrNotFound
	}
	if err != nil {
		return workspace.Project{}, fmt.Errorf("reading a project: %w", err)
	}
	return p, nil
}

// Projects returns every project when all is set, else those of ids,
// ordered by key.
func (s *Store) Projects(ctx context.Context, all bool, ids []workspace.ProjectID) ([]workspace.Project, error) {
	keys := make([][16]byte, len(ids))
	for i, p := range ids {
		keys[i] = p.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+projectColumns+` FROM projects
		WHERE $1 OR id = ANY($2)
		ORDER BY key COLLATE "C"`, all, keys)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	return collect(rows, scanProject)
}

func collect[T any](rows pgx.Rows, scan func(pgx.Row) (T, error)) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
