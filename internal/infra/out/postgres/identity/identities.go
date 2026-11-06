package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

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
	if err != nil {
		return fmt.Errorf("creating an identity: %w", err)
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
