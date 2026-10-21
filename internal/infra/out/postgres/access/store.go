// Package access is the PostgreSQL repository of the access context: members,
// API tokens and sessions.
package access

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Store is the repository of members and API tokens.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns the repository on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) db(ctx context.Context) pgxtx.DB { return pgxtx.Querier(ctx, s.pool) }

// bootstrapLock is the advisory lock bootstraps take, a fixed key of the
// access context.
const bootstrapLock = `SELECT pg_advisory_xact_lock(hashtext('flagnetic.access.bootstrap'))`

// LockBootstrap serializes bootstraps until the transaction ends.
func (s *Store) LockBootstrap(ctx context.Context) error {
	_, err := s.db(ctx).Exec(ctx, bootstrapLock)
	return err
}

// AnyInstanceAdmin reports whether the instance has an instance admin,
// whatever their status.
func (s *Store) AnyInstanceAdmin(ctx context.Context) (bool, error) {
	var exists bool
	err := s.db(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE instance_admin)`).Scan(&exists)
	return exists, err
}

// errEmailTaken refuses a member whose email the instance already holds.
var errEmailTaken = apperrors.Conflict("already_exists", "a member with this email already exists")

// CreateMember stores m with its password hash ("" for none).
func (s *Store) CreateMember(ctx context.Context, m access.Member, passwordHash string) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO members (id, email, display_name, status, instance_admin, locale, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, $8)`,
		m.ID.Bytes(), m.Email, m.DisplayName, string(m.Status), m.InstanceAdmin, m.Locale, passwordHash, m.CreatedAt)
	return emailTaken(err)
}

// emailTaken returns err as errEmailTaken when it violates the uniqueness
// of members' emails, and as it is otherwise.
func emailTaken(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "members_email_key" {
		return errEmailTaken.WithCause(err)
	}
	return err
}

// CreateToken stores t.
func (s *Store) CreateToken(ctx context.Context, t access.APIToken) error {
	var member, project, createdBy any
	if !t.Member.IsZero() {
		member = t.Member.Bytes()
	}
	if !t.Project.IsZero() {
		project = t.Project.Bytes()
	}
	if !t.CreatedBy.IsZero() {
		createdBy = t.CreatedBy.Bytes()
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO api_tokens (id, kind, member_id, project_id, role, name, hint, token_hash, scopes, expires_at, revoked_at,
		                        created_at, created_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, $13)`,
		t.ID.Bytes(), string(t.Kind), member, project, string(t.Role), t.Name, t.Hint, t.Hash[:], scopeStrings(t.Scopes),
		t.ExpiresAt, t.RevokedAt, t.CreatedAt, createdBy)
	return err
}

// TokenByHash returns the token whose value hashes to hash and, for a
// personal access token, its member.
func (s *Store) TokenByHash(ctx context.Context, hash access.TokenHash) (access.APIToken, *access.Member, error) {
	tokenDest, token := scanToken()
	var (
		memberID        *[16]byte
		email, name     *string
		status, locale  *string
		instanceAdmin   *bool
		memberCreatedAt *time.Time
	)
	err := s.db(ctx).QueryRow(ctx, `
		SELECT `+tokenColumns+`,
		       m.id, m.email, m.display_name, m.status, m.instance_admin, m.locale, m.created_at
		FROM api_tokens t
		LEFT JOIN members m ON m.id = t.member_id
		WHERE t.token_hash = $1`, hash[:]).Scan(append(tokenDest,
		&memberID, &email, &name, &status, &instanceAdmin, &locale, &memberCreatedAt)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.APIToken{}, nil, appaccess.ErrNotFound
	}
	if err != nil {
		return access.APIToken{}, nil, fmt.Errorf("reading an API token: %w", err)
	}
	t := token()
	if memberID == nil {
		return t, nil, nil
	}
	m := &access.Member{
		ID: id.FromBytes[access.Member](*memberID), Email: *email, DisplayName: *name,
		Status: access.MemberStatus(*status), InstanceAdmin: *instanceAdmin, CreatedAt: *memberCreatedAt,
	}
	if locale != nil {
		m.Locale = *locale
	}
	return t, m, nil
}

func scopeStrings(scopes []access.Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}
