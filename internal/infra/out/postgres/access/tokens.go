package access

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// tokenColumns are the columns scanToken reads, prefixed by the api_tokens
// table's alias t. They name the hash of a token's value, never a value.
//
//nolint:gosec // column names, not a credential
const tokenColumns = `t.id, t.kind, t.member_id, t.project_id, t.role, t.name, t.hint, t.token_hash, t.scopes,
	t.expires_at, t.revoked_at, t.created_at, t.last_used_at, t.last_used_ip`

// scanToken returns a scan target of tokenColumns and the token it fills.
func scanToken() ([]any, func() access.APIToken) {
	var (
		t         access.APIToken
		tokenID   [16]byte
		kind      string
		memberID  *[16]byte
		projectID *[16]byte
		role      *string
		hash      []byte
		scopes    []string
		ip        *netip.Addr
	)
	dest := []any{&tokenID, &kind, &memberID, &projectID, &role, &t.Name, &t.Hint, &hash, &scopes,
		&t.ExpiresAt, &t.RevokedAt, &t.CreatedAt, &t.LastUsedAt, &ip}
	fill := func() access.APIToken {
		t.ID, t.Kind, t.Hash = id.FromBytes[access.APIToken](tokenID), access.TokenKind(kind), access.TokenHash(hash)
		if memberID != nil {
			t.Member = id.FromBytes[access.Member](*memberID)
		}
		if projectID != nil {
			t.Project = id.FromBytes[workspace.Project](*projectID)
		}
		if role != nil {
			t.Role = access.RoleName(*role)
		}
		if ip != nil {
			t.LastUsedIP = *ip
		}
		t.Scopes = nil
		for _, sc := range scopes {
			t.Scopes = append(t.Scopes, access.Scope(sc))
		}
		return t
	}
	return dest, fill
}

func collectTokens(rows pgx.Rows, err error) ([]access.APIToken, error) {
	if err != nil {
		return nil, fmt.Errorf("listing API tokens: %w", err)
	}
	tokens, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (access.APIToken, error) {
		dest, token := scanToken()
		if scanErr := row.Scan(dest...); scanErr != nil {
			return access.APIToken{}, scanErr
		}
		return token(), nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing API tokens: %w", err)
	}
	return tokens, nil
}

// PersonalTokens returns every personal access token of member, the newest
// first.
func (s *Store) PersonalTokens(ctx context.Context, member access.MemberID) ([]access.APIToken, error) {
	return collectTokens(s.db(ctx).Query(ctx, `
		SELECT `+tokenColumns+` FROM api_tokens t
		WHERE t.kind = 'personal' AND t.member_id = $1
		ORDER BY t.created_at DESC, t.id DESC`, member.Bytes()))
}

// RevokePersonalToken revokes member's unrevoked personal access token at
// at and returns it.
func (s *Store) RevokePersonalToken(ctx context.Context, member access.MemberID, token access.APITokenID, at time.Time,
) (access.APIToken, error) {
	dest, revoked := scanToken()
	err := s.db(ctx).QueryRow(ctx, `
		UPDATE api_tokens t SET revoked_at = $3
		WHERE t.kind = 'personal' AND t.member_id = $1 AND t.id = $2 AND t.revoked_at IS NULL
		RETURNING `+tokenColumns, member.Bytes(), token.Bytes(), at).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.APIToken{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.APIToken{}, fmt.Errorf("revoking an API token: %w", err)
	}
	return revoked(), nil
}

// RecordTokenUse records the use of token at at from ip unless a use less
// than a minute before at is recorded already, so replicas recording the
// same token's use coalesce on one write a minute.
func (s *Store) RecordTokenUse(ctx context.Context, token access.APITokenID, at time.Time, ip netip.Addr) error {
	var addr *netip.Addr
	if ip.IsValid() {
		addr = &ip
	}
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE api_tokens SET last_used_at = $2, last_used_ip = $3
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at <= $2::timestamptz - interval '1 minute')`,
		token.Bytes(), at, addr)
	if err != nil {
		return fmt.Errorf("recording the use of an API token: %w", err)
	}
	return nil
}
