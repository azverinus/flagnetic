package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// IdentityBySubject returns the identity linking subject at provider, locked
// until the unit of work ends; ErrNotFound when there is none.
func (s *Store) IdentityBySubject(ctx context.Context, provider identity.ProviderID, subject string) (identity.Identity, error) {
	var (
		i                      identity.Identity
		identityID, providerID [16]byte
		member                 [16]byte
	)
	err := s.db(ctx).QueryRow(ctx, `
		SELECT id, provider_id, subject, member_id, linked_at, last_sign_in_at FROM identities
		WHERE provider_id = $1 AND subject = $2 FOR UPDATE`, provider.Bytes(), subject).
		Scan(&identityID, &providerID, &i.Subject, &member, &i.LinkedAt, &i.LastSignInAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, appidentity.ErrNotFound
	}
	if err != nil {
		return i, fmt.Errorf("reading an identity: %w", err)
	}
	i.ID, i.Provider, i.Member = id.FromBytes[identity.Identity](identityID), id.FromBytes[identity.Provider](providerID), id.FromBytes[identity.Member](member)
	i.LinkedAt = i.LinkedAt.UTC()
	return i, nil
}

// CreateIdentity stores i.
func (s *Store) CreateIdentity(ctx context.Context, i identity.Identity) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO identities (id, provider_id, subject, member_id, linked_at) VALUES ($1, $2, $3, $4, $5)`,
		i.ID.Bytes(), i.Provider.Bytes(), i.Subject, i.Member.Bytes(), i.LinkedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "identities_provider_subject_key" {
		// Another member linked the subject between the check and the insert.
		return appidentity.ErrIdentityLinked
	}
	if err != nil {
		return fmt.Errorf("creating an identity: %w", err)
	}
	return nil
}

// MemberIdentities returns the identities member holds, oldest first, each
// with the provider it is at.
func (s *Store) MemberIdentities(ctx context.Context, member identity.MemberID) ([]appidentity.IdentityView, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT i.id, i.provider_id, i.subject, i.member_id, i.linked_at, i.last_sign_in_at, p.slug, p.display_name, p.kind, p.enabled
		FROM identities i JOIN auth_providers p ON p.id = i.provider_id
		WHERE i.member_id = $1 ORDER BY i.linked_at, i.id`, member.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing a member's identities: %w", err)
	}
	defer rows.Close()
	var out []appidentity.IdentityView
	for rows.Next() {
		var (
			v                              appidentity.IdentityView
			identityID, providerID, holder [16]byte
			kind                           string
		)
		if err = rows.Scan(&identityID, &providerID, &v.Subject, &holder, &v.LinkedAt, &v.LastSignInAt, &v.ProviderSlug, &v.ProviderName,
			&kind, &v.ProviderEnabled); err != nil {
			return nil, fmt.Errorf("reading a member's identity: %w", err)
		}
		v.ID, v.Provider, v.Member = id.FromBytes[identity.Identity](identityID), id.FromBytes[identity.Provider](providerID), id.FromBytes[identity.Member](holder)
		v.Kind, v.LinkedAt = identity.Kind(kind), v.LinkedAt.UTC()
		if v.LastSignInAt != nil {
			at := v.LastSignInAt.UTC()
			v.LastSignInAt = &at
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteIdentity deletes the identity.
func (s *Store) DeleteIdentity(ctx context.Context, i identity.ID) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM identities WHERE id = $1`, i.Bytes()); err != nil {
		return fmt.Errorf("deleting an identity: %w", err)
	}
	return nil
}

// RecordIdentitySignIn stores that the identity signed in at at.
func (s *Store) RecordIdentitySignIn(ctx context.Context, i identity.ID, at time.Time) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE identities SET last_sign_in_at = $2 WHERE id = $1`, i.Bytes(), at); err != nil {
		return fmt.Errorf("recording an identity's sign-in: %w", err)
	}
	return nil
}

// ProviderUses names "identities" when any identity is linked through
// provider, and "SCIM tokens" while it holds a token not revoked.
func (s *Store) ProviderUses(ctx context.Context, provider identity.ProviderID) ([]string, error) {
	var identities, tokens bool
	err := s.db(ctx).QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM identities WHERE provider_id = $1),
		       EXISTS (SELECT 1 FROM scim_tokens WHERE provider_id = $1 AND revoked_at IS NULL)`, provider.Bytes()).Scan(&identities, &tokens)
	if err != nil {
		return nil, fmt.Errorf("counting what refers to an auth provider: %w", err)
	}
	var uses []string
	if identities {
		uses = append(uses, "identities")
	}
	if tokens {
		uses = append(uses, "SCIM tokens")
	}
	return uses, nil
}
