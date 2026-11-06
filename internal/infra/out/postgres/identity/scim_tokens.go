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

var _ appidentity.SCIMTokenStore = (*Store)(nil)

const scimRowColumns = `t.id, t.provider_id, p.slug, t.hint, t.expires_at, t.revoked_at, t.created_at, t.last_used_at`

func scanSCIMToken(row pgx.Row, extra ...any) (appidentity.SCIMTokenRecord, error) {
	var (
		r        appidentity.SCIMTokenRecord
		tid, pid [16]byte
	)
	dest := append([]any{&tid, &pid, &r.ProviderSlug, &r.Hint, &r.ExpiresAt, &r.RevokedAt, &r.CreatedAt, &r.LastUsedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return r, err
	}
	r.ID, r.Provider = id.FromBytes[identity.SCIMToken](tid), id.FromBytes[identity.Provider](pid)
	r.CreatedAt = r.CreatedAt.UTC()
	for _, t := range []**time.Time{&r.ExpiresAt, &r.RevokedAt, &r.LastUsedAt} {
		if *t != nil {
			u := (**t).UTC()
			*t = &u
		}
	}
	return r, nil
}

// CreateSCIMToken stores t with its hash sealed.
func (s *Store) CreateSCIMToken(ctx context.Context, t identity.SCIMToken, sealedHash string) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO scim_tokens (id, provider_id, hint, token_hash_sealed, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID.Bytes(), t.Provider.Bytes(), t.Hint, sealedHash, t.ExpiresAt, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("creating a SCIM token: %w", err)
	}
	return nil
}

// SCIMTokens returns every SCIM token, the newest first.
func (s *Store) SCIMTokens(ctx context.Context) ([]appidentity.SCIMTokenRecord, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+scimRowColumns+` FROM scim_tokens t JOIN auth_providers p ON p.id = t.provider_id
		ORDER BY t.created_at DESC, t.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing SCIM tokens: %w", err)
	}
	defer rows.Close()
	var out []appidentity.SCIMTokenRecord
	for rows.Next() {
		r, err := scanSCIMToken(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a SCIM token: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevokeSCIMToken revokes the token at at and returns it.
func (s *Store) RevokeSCIMToken(ctx context.Context, token identity.SCIMTokenID, at time.Time) (appidentity.SCIMTokenRecord, error) {
	r, err := scanSCIMToken(s.db(ctx).QueryRow(ctx, `
		WITH revoked AS (
			UPDATE scim_tokens SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL RETURNING *
		)
		SELECT `+scimRowColumns+` FROM revoked t JOIN auth_providers p ON p.id = t.provider_id`, token.Bytes(), at))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, appidentity.ErrNotFound
	}
	if err != nil {
		return r, fmt.Errorf("revoking a SCIM token: %w", err)
	}
	return r, nil
}

// UnrevokedSCIMTokens returns every token not revoked, with its sealed hash.
func (s *Store) UnrevokedSCIMTokens(ctx context.Context) ([]appidentity.SealedSCIMToken, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+scimRowColumns+`, t.token_hash_sealed, p.enabled
		FROM scim_tokens t JOIN auth_providers p ON p.id = t.provider_id WHERE t.revoked_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("reading the live SCIM tokens: %w", err)
	}
	defer rows.Close()
	var out []appidentity.SealedSCIMToken
	for rows.Next() {
		var sealed appidentity.SealedSCIMToken
		r, err := scanSCIMToken(rows, &sealed.SealedHash, &sealed.ProviderEnabled)
		if err != nil {
			return nil, fmt.Errorf("reading a live SCIM token: %w", err)
		}
		sealed.SCIMTokenRecord = r
		out = append(out, sealed)
	}
	return out, rows.Err()
}

// RecordSCIMTokenUse records that token was presented at at.
func (s *Store) RecordSCIMTokenUse(ctx context.Context, token identity.SCIMTokenID, at, notBefore time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE scim_tokens SET last_used_at = $2 WHERE id = $1 AND (last_used_at IS NULL OR last_used_at <= $3)`,
		token.Bytes(), at, notBefore)
	if err != nil {
		return fmt.Errorf("recording the use of a SCIM token: %w", err)
	}
	return nil
}
