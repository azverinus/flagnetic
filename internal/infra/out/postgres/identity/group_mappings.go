package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var _ appidentity.MappingStore = (*Store)(nil)

// GroupMappings returns the provider's mappings in order, each project named
// by its key.
func (s *Store) GroupMappings(ctx context.Context, provider identity.ProviderID) ([]identity.GroupMapping, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT m."group", m.project_id, p.key, m.role, m.instance_admin
		FROM group_mappings m LEFT JOIN projects p ON p.id = m.project_id
		WHERE m.provider_id = $1 ORDER BY m.position`, provider.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing group mappings: %w", err)
	}
	mappings, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (identity.GroupMapping, error) {
		var (
			m         identity.GroupMapping
			projectID *[16]byte
			key, role *string
		)
		if scanErr := row.Scan(&m.Group, &projectID, &key, &role, &m.InstanceAdmin); scanErr != nil {
			return m, scanErr
		}
		if projectID != nil {
			m.Project, m.ProjectKey, m.Role = id.FromBytes[workspace.Project](*projectID), *key, *role
		}
		return m, nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing group mappings: %w", err)
	}
	return mappings, nil
}

// ReplaceGroupMappings stores mappings as the provider's whole list.
func (s *Store) ReplaceGroupMappings(ctx context.Context, provider identity.ProviderID, mappings []identity.GroupMapping) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM group_mappings WHERE provider_id = $1`, provider.Bytes()); err != nil {
		return fmt.Errorf("clearing group mappings: %w", err)
	}
	for i, m := range mappings {
		var project, role any
		if !m.InstanceAdmin {
			project, role = m.Project.Bytes(), m.Role
		}
		_, err := s.db(ctx).Exec(ctx, `
			INSERT INTO group_mappings (provider_id, position, "group", project_id, role, instance_admin)
			VALUES ($1, $2, $3, $4, $5, $6)`, provider.Bytes(), i, m.Group, project, role, m.InstanceAdmin)
		if err != nil {
			return fmt.Errorf("storing a group mapping: %w", err)
		}
	}
	return nil
}

// StoreGroupClaim stores groups as what member claimed at provider at at.
func (s *Store) StoreGroupClaim(ctx context.Context, provider identity.ProviderID, member identity.MemberID, groups []string, at time.Time) error {
	if groups == nil {
		groups = []string{}
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO group_claims (member_id, provider_id, groups, at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (member_id, provider_id) DO UPDATE SET groups = EXCLUDED.groups, at = EXCLUDED.at`,
		member.Bytes(), provider.Bytes(), groups, at)
	if err != nil {
		return fmt.Errorf("storing a group claim: %w", err)
	}
	return nil
}

// GroupClaims returns what every member who claimed groups at the provider
// last claimed, ordered by member.
func (s *Store) GroupClaims(ctx context.Context, provider identity.ProviderID) ([]appidentity.GroupClaim, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT member_id, groups FROM group_claims WHERE provider_id = $1 ORDER BY member_id`, provider.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing group claims: %w", err)
	}
	claims, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (appidentity.GroupClaim, error) {
		var (
			c      appidentity.GroupClaim
			member [16]byte
		)
		scanErr := row.Scan(&member, &c.Groups)
		c.Member = id.FromBytes[identity.Member](member)
		return c, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("listing group claims: %w", err)
	}
	return claims, nil
}
