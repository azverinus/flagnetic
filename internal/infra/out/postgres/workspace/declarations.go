package workspace

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// CreateEnvironment stores e.
func (s *Store) CreateEnvironment(ctx context.Context, e workspace.Environment) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO environments (id, project_id, key, name, sort_order, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.ID.Bytes(), e.Project.Bytes(), e.Key, e.Name, e.SortOrder, e.CreatedAt, e.UpdatedAt)
	return keyTaken(err)
}

const environmentColumns = `id, project_id, key, name, sort_order, created_at, updated_at`

func scanEnvironment(row pgx.Row) (workspace.Environment, error) {
	var (
		e      workspace.Environment
		ek, pk [16]byte
	)
	if err := row.Scan(&ek, &pk, &e.Key, &e.Name, &e.SortOrder, &e.CreatedAt, &e.UpdatedAt); err != nil {
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
		INSERT INTO unit_types (id, project_id, key, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		u.ID.Bytes(), u.Project.Bytes(), u.Key, u.Name, u.Description, u.CreatedAt, u.UpdatedAt)
	return keyTaken(err)
}

const unitTypeColumns = `id, project_id, key, name, description, created_at, updated_at`

func scanUnitType(row pgx.Row) (workspace.UnitType, error) {
	var (
		u      workspace.UnitType
		uk, pk [16]byte
	)
	if err := row.Scan(&uk, &pk, &u.Key, &u.Name, &u.Description, &u.CreatedAt, &u.UpdatedAt); err != nil {
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
		INSERT INTO attributes (id, project_id, key, type, name, description, standard, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID.Bytes(), a.Project.Bytes(), a.Key, string(a.Type), a.Name, a.Description, a.Standard, a.CreatedAt, a.UpdatedAt)
	return keyTaken(err)
}

const attributeColumns = `id, project_id, key, type, name, description, standard, created_at, updated_at`

func scanAttribute(row pgx.Row) (workspace.Attribute, error) {
	var (
		a      workspace.Attribute
		ak, pk [16]byte
		typ    string
	)
	if err := row.Scan(&ak, &pk, &a.Key, &typ, &a.Name, &a.Description, &a.Standard, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
