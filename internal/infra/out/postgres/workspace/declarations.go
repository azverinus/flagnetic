package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// CreateEnvironment stores e.
func (s *Store) CreateEnvironment(ctx context.Context, e workspace.Environment) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO environments (id, project_id, key, name, sort_order, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.ID.Bytes(), e.Project.Bytes(), e.Key, e.Name, e.SortOrder, e.CreatedAt, e.UpdatedAt, e.Version)
	return keyTaken(err)
}

const environmentColumns = `id, project_id, key, name, sort_order, created_at, updated_at, version`

func scanEnvironment(row pgx.Row) (workspace.Environment, error) {
	var (
		e      workspace.Environment
		ek, pk [16]byte
	)
	if err := row.Scan(&ek, &pk, &e.Key, &e.Name, &e.SortOrder, &e.CreatedAt, &e.UpdatedAt, &e.Version); err != nil {
		return workspace.Environment{}, err
	}
	e.ID, e.Project = id.FromBytes[workspace.Environment](ek), id.FromBytes[workspace.Project](pk)
	return e, nil
}

// Environments returns the environments of project by sort order.
func (s *Store) Environments(ctx context.Context, project workspace.ProjectID) ([]workspace.Environment, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+environmentColumns+` FROM environments
		WHERE project_id = $1 ORDER BY sort_order, key COLLATE "C"`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing environments: %w", err)
	}
	return collect(rows, scanEnvironment)
}

// CreateUnitType stores u.
func (s *Store) CreateUnitType(ctx context.Context, u workspace.UnitType) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO unit_types (id, project_id, key, name, description, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		u.ID.Bytes(), u.Project.Bytes(), u.Key, u.Name, u.Description, u.CreatedAt, u.UpdatedAt, u.Version)
	return keyTaken(err)
}

const unitTypeColumns = `id, project_id, key, name, description, created_at, updated_at, version`

func scanUnitType(row pgx.Row) (workspace.UnitType, error) {
	var (
		u      workspace.UnitType
		uk, pk [16]byte
	)
	if err := row.Scan(&uk, &pk, &u.Key, &u.Name, &u.Description, &u.CreatedAt, &u.UpdatedAt, &u.Version); err != nil {
		return workspace.UnitType{}, err
	}
	u.ID, u.Project = id.FromBytes[workspace.UnitType](uk), id.FromBytes[workspace.Project](pk)
	return u, nil
}

// UnitTypes returns the unit types of project by key.
func (s *Store) UnitTypes(ctx context.Context, project workspace.ProjectID) ([]workspace.UnitType, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+unitTypeColumns+` FROM unit_types
		WHERE project_id = $1 ORDER BY key COLLATE "C"`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing unit types: %w", err)
	}
	return collect(rows, scanUnitType)
}

// CreateAttribute stores a.
func (s *Store) CreateAttribute(ctx context.Context, a workspace.Attribute) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO attributes (id, project_id, key, type, name, description, standard, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		a.ID.Bytes(), a.Project.Bytes(), a.Key, string(a.Type), a.Name, a.Description, a.Standard, a.CreatedAt, a.UpdatedAt, a.Version)
	return keyTaken(err)
}

const attributeColumns = `id, project_id, key, type, name, description, standard, created_at, updated_at, version`

func scanAttribute(row pgx.Row) (workspace.Attribute, error) {
	var (
		a      workspace.Attribute
		ak, pk [16]byte
		typ    string
	)
	if err := row.Scan(&ak, &pk, &a.Key, &typ, &a.Name, &a.Description, &a.Standard, &a.CreatedAt, &a.UpdatedAt, &a.Version); err != nil {
		return workspace.Attribute{}, err
	}
	a.ID, a.Project, a.Type = id.FromBytes[workspace.Attribute](ak), id.FromBytes[workspace.Project](pk), workspace.AttributeType(typ)
	return a, nil
}

// Attributes returns the attributes of project by key.
func (s *Store) Attributes(ctx context.Context, project workspace.ProjectID) ([]workspace.Attribute, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+attributeColumns+` FROM attributes
		WHERE project_id = $1 ORDER BY key COLLATE "C"`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing attributes: %w", err)
	}
	return collect(rows, scanAttribute)
}

// lockClause returns the row lock a read takes: none, or the row's until
// the unit of work ends.
func lockClause(lock bool) string {
	if lock {
		return ` FOR UPDATE`
	}
	return ``
}

func one[T any](row pgx.Row, scan func(pgx.Row) (T, error), what string) (T, error) {
	v, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, appworkspace.ErrNotFound
	}
	if err != nil {
		return v, fmt.Errorf("reading %s: %w", what, err)
	}
	return v, nil
}

// EnvironmentByKey returns project's environment with key, locking its row
// when lock is set, or appworkspace.ErrNotFound.
func (s *Store) EnvironmentByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Environment, error) {
	return one(s.db(ctx).QueryRow(ctx, `SELECT `+environmentColumns+` FROM environments
		WHERE project_id = $1 AND key = $2`+lockClause(lock), project.Bytes(), key), scanEnvironment, "an environment")
}

// NextSortOrder returns the sort order that places a new environment of
// project after its others.
func (s *Store) NextSortOrder(ctx context.Context, project workspace.ProjectID) (int, error) {
	var next int
	err := s.db(ctx).QueryRow(ctx, `SELECT coalesce(max(sort_order), 0) + 1 FROM environments WHERE project_id = $1`,
		project.Bytes()).Scan(&next)
	return next, err
}

// CountEnvironments returns how many environments project has.
func (s *Store) CountEnvironments(ctx context.Context, project workspace.ProjectID) (int, error) {
	var n int
	err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM environments WHERE project_id = $1`, project.Bytes()).Scan(&n)
	return n, err
}

// UpdateEnvironment stores e's name and version.
func (s *Store) UpdateEnvironment(ctx context.Context, e workspace.Environment) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE environments SET name = $2, updated_at = $3, version = $4 WHERE id = $1`,
		e.ID.Bytes(), e.Name, e.UpdatedAt, e.Version)
	return err
}

// DeleteEnvironment deletes the environment.
func (s *Store) DeleteEnvironment(ctx context.Context, environment workspace.EnvironmentID) error {
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM environments WHERE id = $1`, environment.Bytes())
	return err
}

// DeclarationKeyTaken reports whether a unit type or an attribute of
// project has key.
func (s *Store) DeclarationKeyTaken(ctx context.Context, project workspace.ProjectID, key string) (bool, error) {
	var taken bool
	err := s.db(ctx).QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM unit_types WHERE project_id = $1 AND key = $2)
		    OR EXISTS (SELECT 1 FROM attributes WHERE project_id = $1 AND key = $2)`, project.Bytes(), key).Scan(&taken)
	return taken, err
}

// UnitTypeByKey returns project's unit type with key, locking its row when
// lock is set, or appworkspace.ErrNotFound.
func (s *Store) UnitTypeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.UnitType, error) {
	return one(s.db(ctx).QueryRow(ctx, `SELECT `+unitTypeColumns+` FROM unit_types
		WHERE project_id = $1 AND key = $2`+lockClause(lock), project.Bytes(), key), scanUnitType, "a unit type")
}

// UpdateUnitType stores u's name, description and version.
func (s *Store) UpdateUnitType(ctx context.Context, u workspace.UnitType) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE unit_types SET name = $2, description = $3, updated_at = $4, version = $5 WHERE id = $1`,
		u.ID.Bytes(), u.Name, u.Description, u.UpdatedAt, u.Version)
	return err
}

// DeleteUnitType deletes the unit type.
func (s *Store) DeleteUnitType(ctx context.Context, unitType workspace.UnitTypeID) error {
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM unit_types WHERE id = $1`, unitType.Bytes())
	return err
}

// AttributeByKey returns project's attribute with key, locking its row when
// lock is set, or appworkspace.ErrNotFound.
func (s *Store) AttributeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Attribute, error) {
	return one(s.db(ctx).QueryRow(ctx, `SELECT `+attributeColumns+` FROM attributes
		WHERE project_id = $1 AND key = $2`+lockClause(lock), project.Bytes(), key), scanAttribute, "an attribute")
}

// UpdateAttribute stores a's name, description and version.
func (s *Store) UpdateAttribute(ctx context.Context, a workspace.Attribute) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE attributes SET name = $2, description = $3, updated_at = $4, version = $5 WHERE id = $1`,
		a.ID.Bytes(), a.Name, a.Description, a.UpdatedAt, a.Version)
	return err
}

// DeleteAttribute deletes the attribute.
func (s *Store) DeleteAttribute(ctx context.Context, attribute workspace.AttributeID) error {
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM attributes WHERE id = $1`, attribute.Bytes())
	return err
}
