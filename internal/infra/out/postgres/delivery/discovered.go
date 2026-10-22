package delivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// LockDiscoveredAttributes holds, until the unit of work ends, the right to
// add discovered attributes to project, so that replicas writing at once
// never take a project past its bound. The lock is advisory, keyed by the
// project, and touches no row a member's write needs.
func (s *Store) LockDiscoveredAttributes(ctx context.Context, project workspace.ProjectID) error {
	_, err := s.db(ctx).Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('discovered_attributes:' || $1::uuid::text, 0))`, project.Bytes())
	if err != nil {
		return fmt.Errorf("lock discovered attributes: %w", err)
	}
	return nil
}

// discoveredColumns are the columns scanDiscovered reads.
const discoveredColumns = `d.project_id, d.key, d.observed_types, d.first_seen, d.last_seen, d.count, d.dismissed`

// undeclared keeps the rows of d whose key the project has not declared
// since, as an attribute or a unit type.
const undeclared = `NOT EXISTS (SELECT 1 FROM attributes a WHERE a.project_id = d.project_id AND a.key = d.key)
	AND NOT EXISTS (SELECT 1 FROM unit_types u WHERE u.project_id = d.project_id AND u.key = d.key)`

func scanDiscovered(row pgx.Row) (delivery.DiscoveredAttribute, error) {
	var (
		d       delivery.DiscoveredAttribute
		project [16]byte
		types   []string
	)
	if err := row.Scan(&project, &d.Key, &types, &d.FirstSeen, &d.LastSeen, &d.Count, &d.Dismissed); err != nil {
		return delivery.DiscoveredAttribute{}, err
	}
	d.Project = id.FromBytes[workspace.Project](project)
	for _, t := range types {
		d.Types = append(d.Types, delivery.ObservedType(t))
	}
	return d, nil
}

// DiscoveredAttributes returns the discovered attributes of project keyed
// keys, dismissed ones included, that the project has not declared since.
func (s *Store) DiscoveredAttributes(ctx context.Context, project workspace.ProjectID, keys []string,
) ([]delivery.DiscoveredAttribute, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+discoveredColumns+` FROM discovered_attributes d
		WHERE d.project_id = $1 AND d.key = ANY($2) AND `+undeclared, project.Bytes(), keys)
	if err != nil {
		return nil, fmt.Errorf("discovered attributes: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (delivery.DiscoveredAttribute, error) { return scanDiscovered(r) })
	if err != nil {
		return nil, fmt.Errorf("discovered attributes: %w", err)
	}
	return out, nil
}

// DeclaredKeys returns which of keys project declares, as an attribute or
// a unit type.
func (s *Store) DeclaredKeys(ctx context.Context, project workspace.ProjectID, keys []string) (map[string]bool, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT key FROM attributes WHERE project_id = $1 AND key = ANY($2)
		UNION SELECT key FROM unit_types WHERE project_id = $1 AND key = ANY($2)`, project.Bytes(), keys)
	if err != nil {
		return nil, fmt.Errorf("declared keys: %w", err)
	}
	declared, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("declared keys: %w", err)
	}
	out := make(map[string]bool, len(declared))
	for _, k := range declared {
		out[k] = true
	}
	return out, nil
}

// CountDiscoveredAttributes counts the discovered attributes of project,
// dismissed ones included, that the project has not declared since.
func (s *Store) CountDiscoveredAttributes(ctx context.Context, project workspace.ProjectID) (int, error) {
	var n int
	err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM discovered_attributes d WHERE d.project_id = $1 AND `+undeclared,
		project.Bytes()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count discovered attributes: %w", err)
	}
	return n, nil
}

// SaveDiscoveredAttribute stores d, inserted or replacing what is stored
// of its key.
func (s *Store) SaveDiscoveredAttribute(ctx context.Context, d delivery.DiscoveredAttribute) error {
	types := make([]string, len(d.Types))
	for i, t := range d.Types {
		types[i] = string(t)
	}
	_, err := s.db(ctx).Exec(ctx, `INSERT INTO discovered_attributes
		(project_id, key, observed_types, first_seen, last_seen, count, dismissed) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (project_id, key) DO UPDATE SET observed_types = EXCLUDED.observed_types, first_seen = EXCLUDED.first_seen,
			last_seen = EXCLUDED.last_seen, count = EXCLUDED.count, dismissed = EXCLUDED.dismissed`,
		d.Project.Bytes(), d.Key, types, d.FirstSeen, d.LastSeen, d.Count, d.Dismissed)
	if err != nil {
		return fmt.Errorf("save discovered attribute: %w", err)
	}
	return nil
}

// ListedDiscoveredAttributes returns the discovered attributes of project a
// member sees, by key: neither dismissed nor declared since.
func (s *Store) ListedDiscoveredAttributes(ctx context.Context, project workspace.ProjectID) ([]delivery.DiscoveredAttribute, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+discoveredColumns+` FROM discovered_attributes d
		WHERE d.project_id = $1 AND NOT d.dismissed AND `+undeclared+` ORDER BY d.key`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("list discovered attributes: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (delivery.DiscoveredAttribute, error) { return scanDiscovered(r) })
	if err != nil {
		return nil, fmt.Errorf("list discovered attributes: %w", err)
	}
	return out, nil
}

// DiscoveredAttributeForUpdate returns the discovered attribute of project
// keyed key, locked until the unit of work ends, or appdelivery.ErrNotFound
// when there is none the project has not declared since.
func (s *Store) DiscoveredAttributeForUpdate(ctx context.Context, project workspace.ProjectID, key string,
) (delivery.DiscoveredAttribute, error) {
	d, err := scanDiscovered(s.db(ctx).QueryRow(ctx, `SELECT `+discoveredColumns+` FROM discovered_attributes d
		WHERE d.project_id = $1 AND d.key = $2 AND `+undeclared+` FOR UPDATE`, project.Bytes(), key))
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery.DiscoveredAttribute{}, appdelivery.ErrNotFound
	}
	if err != nil {
		return delivery.DiscoveredAttribute{}, fmt.Errorf("discovered attribute: %w", err)
	}
	return d, nil
}

// DeleteDiscoveredAttribute removes the discovered attribute of project
// keyed key, if any.
func (s *Store) DeleteDiscoveredAttribute(ctx context.Context, project workspace.ProjectID, key string) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM discovered_attributes WHERE project_id = $1 AND key = $2`,
		project.Bytes(), key); err != nil {
		return fmt.Errorf("delete discovered attribute: %w", err)
	}
	return nil
}
