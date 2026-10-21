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

// sessionColumns are the columns scanSession reads, prefixed by the
// sessions table's alias s.
const sessionColumns = `s.id, s.secret_hash, s.member_id, s.method, s.client_ip, s.user_agent, s.created_at, s.last_seen_at, s.expires_at, s.revoked_at`

// scanSession returns a scan target of sessionColumns and the session it
// fills.
func scanSession() ([]any, func() access.Session) {
	var (
		sess              access.Session
		sessionID, member [16]byte
		method            string
		ip                *netip.Addr
		secret            []byte
	)
	dest := []any{
		&sessionID, &secret, &member, &method, &ip, &sess.UserAgent, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &sess.RevokedAt,
	}
	fill := func() access.Session {
		sess.ID, sess.Hash, sess.Method = id.FromBytes[access.Session](sessionID), access.SessionHash(secret), access.SignInMethod(method)
		sess.Member = id.FromBytes[access.Member](member)
		sess.ClientIP = netip.Addr{}
		if ip != nil {
			sess.ClientIP = *ip
		}
		return sess
	}
	return dest, fill
}

// SessionByHash returns the session stored under hash with its member;
// ErrNotFound when there is none.
func (s *Store) SessionByHash(ctx context.Context, hash access.SessionHash) (access.Session, access.Member, error) {
	sessDest, sess := scanSession()
	memberDest, member := scanMember()
	err := s.db(ctx).QueryRow(ctx, `
		SELECT `+sessionColumns+`, `+memberColumns+`
		FROM sessions s JOIN members m ON m.id = s.member_id
		WHERE s.secret_hash = $1`, hash[:]).Scan(append(sessDest, memberDest...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Session{}, access.Member{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Session{}, access.Member{}, fmt.Errorf("reading a session: %w", err)
	}
	return sess(), member(), nil
}

// MemberSessions returns the sessions of member not yet revoked, the newest
// first.
func (s *Store) MemberSessions(ctx context.Context, member access.MemberID) ([]access.Session, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+sessionColumns+` FROM sessions s
		WHERE s.member_id = $1 AND s.revoked_at IS NULL
		ORDER BY s.created_at DESC, s.id DESC`, member.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing a member's sessions: %w", err)
	}
	defer rows.Close()
	var out []access.Session
	for rows.Next() {
		dest, sess := scanSession()
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("reading a member's session: %w", err)
		}
		out = append(out, sess())
	}
	return out, rows.Err()
}

// RevokeSessions ends each of sessions not ended yet at at, and returns
// those it ended.
func (s *Store) RevokeSessions(ctx context.Context, sessions []access.SessionID, at time.Time) ([]access.SessionID, error) {
	ids := make([][16]byte, len(sessions))
	for i, sess := range sessions {
		ids[i] = sess.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `
		UPDATE sessions SET revoked_at = $2 WHERE id = ANY($1::uuid[]) AND revoked_at IS NULL RETURNING id`, ids, at)
	if err != nil {
		return nil, fmt.Errorf("revoking sessions: %w", err)
	}
	revoked, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (access.SessionID, error) {
		var b [16]byte
		scanErr := row.Scan(&b)
		return id.FromBytes[access.Session](b), scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("revoking sessions: %w", err)
	}
	return revoked, nil
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
