package access

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
)

var _ appaccess.ProvisionedMemberStore = (*Store)(nil)

// provisioningTaken returns err as the refusal of a uniqueness it violates:
// the email, or the provider's external ID.
func provisioningTaken(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "members_provider_external_id_key" {
		return appaccess.ErrExternalIDTaken
	}
	return emailTaken(err)
}

// CreateProvisionedMember stores m, a member of a provider, without a
// password.
func (s *Store) CreateProvisionedMember(ctx context.Context, m access.Member, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO members (id, email, display_name, status, instance_admin, provider, external_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, false, $5, NULLIF($6, ''), $7, $7)`,
		m.ID.Bytes(), m.Email, m.DisplayName, string(m.Status), m.Provider, m.ExternalID, at)
	return provisioningTaken(err)
}

// SaveProvisionedMember stores m's email, display name, status, provider and
// external ID.
func (s *Store) SaveProvisionedMember(ctx context.Context, m access.Member, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE members SET email = $2, display_name = $3, status = $4, provider = NULLIF($5, ''), external_id = NULLIF($6, ''),
		                   updated_at = $7
		WHERE id = $1`,
		m.ID.Bytes(), m.Email, m.DisplayName, string(m.Status), m.Provider, m.ExternalID, at)
	return provisioningTaken(err)
}

// ProvisionedMembers returns the page of provider's members that satisfy every
// condition, and how many do in all.
func (s *Store) ProvisionedMembers(ctx context.Context, provider string, conds []appaccess.ProvisionedCondition,
	startIndex, count int,
) ([]access.Member, int, error) {
	where, args := []string{"m.provider = $1"}, []any{provider}
	for _, c := range conds {
		args = append(args, c.Value)
		n := "$" + strconv.Itoa(len(args))
		switch c.Field {
		case appaccess.FieldEmail:
			where = append(where, "lower(m.email) = lower("+n+")")
		case appaccess.FieldDisplayName:
			where = append(where, "lower(m.display_name) = lower("+n+")")
		case appaccess.FieldExternalID:
			where = append(where, "m.external_id = "+n)
		case appaccess.FieldID:
			parsed, err := uuid.Parse(c.Value)
			if err != nil {
				// No member has an identifier that is none.
				return nil, 0, nil
			}
			args[len(args)-1] = [16]byte(parsed)
			where = append(where, "m.id = "+n)
		}
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM members m WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting provisioned members: %w", err)
	}
	if count == 0 || total == 0 {
		return nil, total, nil
	}
	args = append(args, count, startIndex-1)
	rows, err := s.db(ctx).Query(ctx, `SELECT `+memberColumns+` FROM members m WHERE `+clause+`
		ORDER BY lower(m.email) COLLATE "C", m.id LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing provisioned members: %w", err)
	}
	members, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (access.Member, error) {
		dest, member := scanMember()
		if scanErr := row.Scan(dest...); scanErr != nil {
			return access.Member{}, scanErr
		}
		return member(), nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("listing provisioned members: %w", err)
	}
	return members, total, nil
}

// ProviderProvisionedMembers reports whether the provider holds a member.
func (s *Store) ProviderProvisionedMembers(ctx context.Context, provider string) (bool, error) {
	var held bool
	err := s.db(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE provider = $1)`, provider).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("looking for an auth provider's provisioned members: %w", err)
	}
	return held, nil
}
