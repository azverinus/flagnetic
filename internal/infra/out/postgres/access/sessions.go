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
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// CreateSession stores sess.
func (s *Store) CreateSession(ctx context.Context, sess access.Session) error {
	var ip *netip.Addr
	if sess.ClientIP.IsValid() {
		ip = &sess.ClientIP
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO sessions (id, secret_hash, member_id, method, client_ip, user_agent, created_at, last_seen_at, expires_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		sess.ID.Bytes(), sess.Hash[:], sess.Member.Bytes(), string(sess.Method), ip, sess.UserAgent,
		sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt, sess.RevokedAt)
	return err
}

// SessionByHash returns the session stored under hash with its member;
// ErrNotFound when there is none.
func (s *Store) SessionByHash(ctx context.Context, hash access.SessionHash) (access.Session, access.Member, error) {
	var (
		sess      access.Session
		sessionID [16]byte
		method    string
		ip        *netip.Addr
		secret    []byte
	)
	dest, member := scanMember()
	err := s.db(ctx).QueryRow(ctx, `
		SELECT s.id, s.secret_hash, s.method, s.client_ip, s.user_agent, s.created_at, s.last_seen_at, s.expires_at, s.revoked_at, `+memberColumns+`
		FROM sessions s JOIN members m ON m.id = s.member_id
		WHERE s.secret_hash = $1`, hash[:]).Scan(append([]any{
		&sessionID, &secret, &method, &ip, &sess.UserAgent, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &sess.RevokedAt,
	}, dest...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Session{}, access.Member{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Session{}, access.Member{}, fmt.Errorf("reading a session: %w", err)
	}
	m := member()
	sess.ID, sess.Hash, sess.Method, sess.Member = id.FromBytes[access.Session](sessionID), access.SessionHash(secret), access.SignInMethod(method), m.ID
	if ip != nil {
		sess.ClientIP = *ip
	}
	return sess, m, nil
}

// TouchSession records a request of session at, unless a later one is
// recorded already.
func (s *Store) TouchSession(ctx context.Context, session access.SessionID, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id = $1 AND last_seen_at < $2`, session.Bytes(), at)
	return err
}

// RevokeSession ends session at, unless it has ended already.
func (s *Store) RevokeSession(ctx context.Context, session access.SessionID, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, session.Bytes(), at)
	return err
}
