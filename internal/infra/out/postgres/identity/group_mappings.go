package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var _ appidentity.MappingStore = (*Store)(nil)

// collectRows reads the rows of a query that listed what, or its failure.
func collectRows[T any](rows pgx.Rows, err error, what string, scan func(pgx.CollectableRow) (T, error)) ([]T, error) {
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", what, err)
	}
	out, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", what, err)
	}
	return out, nil
}

// GroupMappings returns the provider's mappings in order, each project named
// by its key.
func (s *Store) GroupMappings(ctx context.Context, provider identity.ProviderID) ([]identity.GroupMapping, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT m."group", m.project_id, p.key, m.role, m.instance_admin
		FROM group_mappings m LEFT JOIN projects p ON p.id = m.project_id
		WHERE m.provider_id = $1 ORDER BY m.position`, provider.Bytes())
	return collectRows(rows, err, "group mappings",
		func(row pgx.CollectableRow) (identity.GroupMapping, error) {
			var (
				m         identity.GroupMapping
				projectID *[16]byte
				key, role *string
			)
			err := row.Scan(&m.Group, &projectID, &key, &role, &m.InstanceAdmin)
			if projectID != nil {
				m.Project, m.ProjectKey, m.Role = id.FromBytes[workspace.Project](*projectID), *key, *role
			}
			return m, err
		})
}

// ReplaceGroupMappings stores mappings as the provider's whole list, in one
// statement for all the new rows.
func (s *Store) ReplaceGroupMappings(ctx context.Context, provider identity.ProviderID, mappings []identity.GroupMapping) error {
	groups := make([]string, len(mappings))
	projects := make([]pgtype.UUID, len(mappings))
	roles := make([]*string, len(mappings))
	admins := make([]bool, len(mappings))
	for i, m := range mappings {
		groups[i], admins[i] = m.Group, m.InstanceAdmin
		if !m.InstanceAdmin {
			projects[i], roles[i] = pgtype.UUID{Bytes: m.Project.Bytes(), Valid: true}, &mappings[i].Role
		}
	}
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM group_mappings WHERE provider_id = $1`, provider.Bytes()); err != nil {
		return fmt.Errorf("clearing group mappings: %w", err)
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO group_mappings (provider_id, position, "group", project_id, role, instance_admin)
		SELECT $1::uuid, n.ord - 1, n."group", n.project_id, n.role, n.instance_admin
		FROM unnest($2::text[], $3::uuid[], $4::text[], $5::boolean[])
		     WITH ORDINALITY AS n("group", project_id, role, instance_admin, ord)`,
		provider.Bytes(), groups, projects, roles, admins)
	if err != nil {
		return fmt.Errorf("storing group mappings: %w", err)
	}
	return nil
}

// StoreGroupClaim stores groups as what member claimed at provider at at.
func (s *Store) StoreGroupClaim(ctx context.Context, provider identity.ProviderID, member identity.MemberID, groups []string, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO group_claims (member_id, provider_id, groups, at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (member_id, provider_id) DO UPDATE SET groups = EXCLUDED.groups, at = EXCLUDED.at`,
		member.Bytes(), provider.Bytes(), append([]string{}, groups...), at)
	if err != nil {
		return fmt.Errorf("storing a group claim: %w", err)
	}
	return nil
}

// GroupClaims returns what every member who claimed groups at the provider
// last claimed, ordered by member.
func (s *Store) GroupClaims(ctx context.Context, provider identity.ProviderID) ([]appidentity.GroupClaim, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT member_id, groups FROM group_claims WHERE provider_id = $1 ORDER BY member_id`,
		provider.Bytes())
	return collectRows(rows, err, "group claims", func(row pgx.CollectableRow) (appidentity.GroupClaim, error) {
		var (
			c      appidentity.GroupClaim
			member [16]byte
		)
		err := row.Scan(&member, &c.Groups)
		c.Member = id.FromBytes[identity.Member](member)
		return c, err
	})
}
