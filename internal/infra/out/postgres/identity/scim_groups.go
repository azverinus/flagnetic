package identity

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var _ appidentity.SCIMGroupStore = (*Store)(nil)

// groupRefusal answers a taken name or external ID as the refusal a SCIM
// client is told.
func groupRefusal(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "scim_groups_provider_display_name_key":
			return appidentity.ErrGroupNameTaken
		case "scim_groups_provider_external_id_key":
			return appidentity.ErrGroupExternalIDTaken
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

func nullableText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func memberBytes(members []identity.MemberID) [][16]byte {
	out := make([][16]byte, len(members))
	for i, m := range members {
		out[i] = m.Bytes()
	}
	return out
}

// CreateSCIMGroup stores g and its members.
func (s *Store) CreateSCIMGroup(ctx context.Context, g appidentity.SCIMGroupRecord) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO scim_groups (id, provider_id, display_name, external_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		g.ID.Bytes(), g.Provider.Bytes(), g.DisplayName, nullableText(g.ExternalID), g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return groupRefusal(err, "creating a SCIM group")
	}
	return s.addGroupMembers(ctx, g)
}

func (s *Store) addGroupMembers(ctx context.Context, g appidentity.SCIMGroupRecord) error {
	if len(g.Members) == 0 {
		return nil
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO scim_group_members (group_id, member_id) SELECT $1, unnest($2::uuid[])`,
		g.ID.Bytes(), memberBytes(g.Members))
	if err != nil {
		return fmt.Errorf("storing a SCIM group's members: %w", err)
	}
	return nil
}

// SaveSCIMGroup stores g in place of the group with its identifier, its
// members replaced.
func (s *Store) SaveSCIMGroup(ctx context.Context, g appidentity.SCIMGroupRecord) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE scim_groups SET display_name = $2, external_id = $3, updated_at = $4 WHERE id = $1`,
		g.ID.Bytes(), g.DisplayName, nullableText(g.ExternalID), g.UpdatedAt)
	if err != nil {
		return groupRefusal(err, "saving a SCIM group")
	}
	if _, err = s.db(ctx).Exec(ctx, `DELETE FROM scim_group_members WHERE group_id = $1`, g.ID.Bytes()); err != nil {
		return fmt.Errorf("clearing a SCIM group's members: %w", err)
	}
	return s.addGroupMembers(ctx, g)
}

// DeleteSCIMGroup removes the group; its memberships go with it.
func (s *Store) DeleteSCIMGroup(ctx context.Context, group identity.SCIMGroupID) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM scim_groups WHERE id = $1`, group.Bytes()); err != nil {
		return fmt.Errorf("deleting a SCIM group: %w", err)
	}
	return nil
}

// SCIMGroup returns the provider's group with its members.
func (s *Store) SCIMGroup(ctx context.Context, provider identity.ProviderID, group identity.SCIMGroupID) (appidentity.SCIMGroupRecord, error) {
	groups, _, err := s.scimGroups(ctx, `WHERE g.provider_id = $1 AND g.id = $2`, []any{provider.Bytes(), group.Bytes()}, 1, 1)
	if err != nil {
		return appidentity.SCIMGroupRecord{}, err
	}
	if len(groups) == 0 {
		return appidentity.SCIMGroupRecord{}, identity.ErrGroupNotFound
	}
	return groups[0], nil
}

// SCIMGroups returns the page of the provider's groups the filter selects,
// ordered by name, and how many it selects in all. Every attribute is
// compared exactly; an id that is none selects nothing.
func (s *Store) SCIMGroups(ctx context.Context, provider identity.ProviderID, f identity.GroupFilter, startIndex, count int) ([]appidentity.SCIMGroupRecord, int, error) {
	where := []string{"g.provider_id = $1"}
	args := []any{provider.Bytes()}
	for _, c := range f.Conditions {
		var column string
		var value any = c.Value
		switch c.Attribute {
		case identity.GroupDisplayName:
			column = "g.display_name"
		case identity.GroupExternalID:
			column = "g.external_id"
		case identity.GroupID:
			gid, err := id.Parse[identity.SCIMGroup](c.Value)
			if err != nil {
				return nil, 0, nil
			}
			column, value = "g.id", gid.Bytes()
		}
		args = append(args, value)
		where = append(where, column+" = $"+strconv.Itoa(len(args)))
	}
	return s.scimGroups(ctx, "WHERE "+strings.Join(where, " AND "), args, startIndex, count)
}

// scimGroups returns the page of groups where selects, with their members,
// and how many it selects in all.
func (s *Store) scimGroups(ctx context.Context, where string, args []any, startIndex, count int) ([]appidentity.SCIMGroupRecord, int, error) {
	var total int
	if err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM scim_groups g `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting SCIM groups: %w", err)
	}
	if count <= 0 || total == 0 {
		return nil, total, nil
	}
	n := len(args)
	rows, err := s.db(ctx).Query(ctx, `
		SELECT g.id, g.provider_id, g.display_name, g.external_id, g.created_at, g.updated_at,
		       ARRAY(SELECT sm.member_id FROM scim_group_members sm WHERE sm.group_id = g.id ORDER BY sm.member_id)
		FROM scim_groups g `+where+`
		ORDER BY g.display_name, g.id
		OFFSET $`+strconv.Itoa(n+1)+` LIMIT $`+strconv.Itoa(n+2),
		append(args, startIndex-1, count)...)
	groups, err := collectRows(rows, err, "SCIM groups", func(row pgx.CollectableRow) (appidentity.SCIMGroupRecord, error) {
		var (
			g        appidentity.SCIMGroupRecord
			gid, pid [16]byte
			external *string
			members  [][16]byte
		)
		if scanErr := row.Scan(&gid, &pid, &g.DisplayName, &external, &g.CreatedAt, &g.UpdatedAt, &members); scanErr != nil {
			return g, scanErr
		}
		g.ID, g.Provider, g.ExternalID = id.FromBytes[identity.SCIMGroup](gid), id.FromBytes[identity.Provider](pid), deref(external)
		g.CreatedAt, g.UpdatedAt = g.CreatedAt.UTC(), g.UpdatedAt.UTC()
		for _, m := range members {
			g.Members = append(g.Members, id.FromBytes[identity.Member](m))
		}
		return g, nil
	})
	return groups, total, err
}

// SCIMGroupSummaries returns every group of the provider, ordered by name,
// with how many members each holds.
func (s *Store) SCIMGroupSummaries(ctx context.Context, provider identity.ProviderID) ([]appidentity.SCIMGroupSummary, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT g.id, g.display_name, g.external_id, count(sm.member_id)
		FROM scim_groups g LEFT JOIN scim_group_members sm ON sm.group_id = g.id
		WHERE g.provider_id = $1
		GROUP BY g.id
		ORDER BY g.display_name, g.id`, provider.Bytes())
	return collectRows(rows, err, "SCIM group summaries", func(row pgx.CollectableRow) (appidentity.SCIMGroupSummary, error) {
		var (
			g        appidentity.SCIMGroupSummary
			gid      [16]byte
			external *string
		)
		if scanErr := row.Scan(&gid, &g.DisplayName, &external, &g.MemberCount); scanErr != nil {
			return g, scanErr
		}
		g.ID, g.ExternalID = id.FromBytes[identity.SCIMGroup](gid), deref(external)
		return g, nil
	})
}

// GroupsOfMembers returns the provider's groups each of members is in,
// ordered by name.
func (s *Store) GroupsOfMembers(ctx context.Context, provider identity.ProviderID, members []identity.MemberID) (map[identity.MemberID][]appidentity.GroupRef, error) {
	type membership struct {
		member identity.MemberID
		group  appidentity.GroupRef
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT sm.member_id, g.id, g.display_name
		FROM scim_group_members sm JOIN scim_groups g ON g.id = sm.group_id
		WHERE g.provider_id = $1 AND sm.member_id = ANY($2::uuid[])
		ORDER BY g.display_name, g.id`, provider.Bytes(), memberBytes(members))
	found, err := collectRows(rows, err, "the SCIM groups of members", func(row pgx.CollectableRow) (membership, error) {
		var (
			m        membership
			mid, gid [16]byte
		)
		scanErr := row.Scan(&mid, &gid, &m.group.DisplayName)
		m.member, m.group.ID = id.FromBytes[identity.Member](mid), id.FromBytes[identity.SCIMGroup](gid)
		return m, scanErr
	})
	if err != nil {
		return nil, err
	}
	out := make(map[identity.MemberID][]appidentity.GroupRef)
	for _, m := range found {
		out[m.member] = append(out[m.member], m.group)
	}
	return out, nil
}
