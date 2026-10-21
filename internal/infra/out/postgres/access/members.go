package access

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// memberColumns are the columns scanMember reads, prefixed by the members
// table's alias m.
const memberColumns = `m.id, m.email, m.display_name, m.status, m.instance_admin, m.locale, m.created_at`

// scanMember returns a scan target of memberColumns and the member it fills.
func scanMember() ([]any, func() access.Member) {
	var (
		memberID [16]byte
		m        access.Member
		status   string
		locale   *string
	)
	return []any{&memberID, &m.Email, &m.DisplayName, &status, &m.InstanceAdmin, &locale, &m.CreatedAt}, func() access.Member {
		m.ID, m.Status = id.FromBytes[access.Member](memberID), access.MemberStatus(status)
		if locale != nil {
			m.Locale = *locale
		}
		return m
	}
}

// MemberByEmail returns the member whose email is email whatever its case,
// with their password hash ("" for none); ErrNotFound when there is none.
func (s *Store) MemberByEmail(ctx context.Context, email string) (access.Member, string, error) {
	dest, member := scanMember()
	var hash *string
	err := s.db(ctx).QueryRow(ctx, `SELECT `+memberColumns+`, m.password_hash FROM members m WHERE lower(m.email) = lower($1)`,
		email).Scan(append(dest, &hash)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Member{}, "", appaccess.ErrNotFound
	}
	if err != nil {
		return access.Member{}, "", fmt.Errorf("reading a member by email: %w", err)
	}
	if hash == nil {
		return member(), "", nil
	}
	return member(), *hash, nil
}

// MemberByID returns the member with the identifier; ErrNotFound when
// there is none.
func (s *Store) MemberByID(ctx context.Context, member access.MemberID) (access.Member, error) {
	dest, m := scanMember()
	err := s.db(ctx).QueryRow(ctx, `SELECT `+memberColumns+` FROM members m WHERE m.id = $1`, member.Bytes()).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Member{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Member{}, fmt.Errorf("reading a member: %w", err)
	}
	return m(), nil
}

// SetPasswordHash replaces the password hash of member.
func (s *Store) SetPasswordHash(ctx context.Context, member access.MemberID, hash string, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE members SET password_hash = $2, updated_at = $3 WHERE id = $1`, member.Bytes(), hash, at)
	return err
}

// RecordSignIn records that member signed in at.
func (s *Store) RecordSignIn(ctx context.Context, member access.MemberID, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE members SET last_sign_in_at = $2 WHERE id = $1`, member.Bytes(), at)
	return err
}

// LockMemberByEmail returns the member whose email is email whatever its
// case, locked until the transaction ends; ErrNotFound when there is none.
func (s *Store) LockMemberByEmail(ctx context.Context, email string) (access.Member, error) {
	dest, member := scanMember()
	err := s.db(ctx).QueryRow(ctx, `SELECT `+memberColumns+` FROM members m WHERE lower(m.email) = lower($1) FOR UPDATE`,
		email).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Member{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Member{}, fmt.Errorf("locking a member by email: %w", err)
	}
	return member(), nil
}

// SetInstanceAdmin makes member an instance admin or not.
func (s *Store) SetInstanceAdmin(ctx context.Context, member access.MemberID, instanceAdmin bool, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE members SET instance_admin = $2, updated_at = $3 WHERE id = $1`, member.Bytes(), instanceAdmin, at)
	return err
}

// memberViewQuery reads members as an instance admin sees them, with the
// projects each holds a grant in counted in the same statement, so a page
// of members costs one query whatever its length. A member holds no grant
// in any project until grants can be made, so each counts none.
const memberViewQuery = `SELECT ` + memberColumns + `, m.last_sign_in_at, 0 AS project_count FROM members m`

func scanMemberView(row pgx.Row) (appaccess.MemberView, error) {
	dest, member := scanMember()
	var v appaccess.MemberView
	if err := row.Scan(append(dest, &v.LastSignInAt, &v.ProjectCount)...); err != nil {
		return appaccess.MemberView{}, err
	}
	v.Member = member()
	return v, nil
}

// Members returns the members f selects, ordered by email whatever its
// case.
func (s *Store) Members(ctx context.Context, f appaccess.MemberFilter) ([]appaccess.MemberView, error) {
	rows, err := s.db(ctx).Query(ctx, memberViewQuery+`
		WHERE ($1 = '' OR m.status = $1)
		AND ($2 = '' OR position(lower($2) in lower(m.email)) > 0 OR position(lower($2) in lower(m.display_name)) > 0)
		ORDER BY lower(m.email) COLLATE "C", m.id`, string(f.Status), f.Q)
	if err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}
	views, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (appaccess.MemberView, error) { return scanMemberView(row) })
	if err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}
	return views, nil
}

// MemberView returns one member as an instance admin sees them;
// ErrNotFound when none has the identifier.
func (s *Store) MemberView(ctx context.Context, member access.MemberID) (appaccess.MemberView, error) {
	v, err := scanMemberView(s.db(ctx).QueryRow(ctx, memberViewQuery+` WHERE m.id = $1`, member.Bytes()))
	if errors.Is(err, pgx.ErrNoRows) {
		return appaccess.MemberView{}, appaccess.ErrNotFound
	}
	if err != nil {
		return appaccess.MemberView{}, fmt.Errorf("reading a member: %w", err)
	}
	return v, nil
}

// LockInstanceAdmins takes, until the transaction ends, the lock every
// change that could leave the instance without an active instance admin
// holds. The key names the schema too, so instances sharing a database in
// separate schemas do not wait on each other.
func (s *Store) LockInstanceAdmins(ctx context.Context) error {
	_, err := s.db(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('flagnetic.access.instance_admins@' || current_schema()))`)
	return err
}

// LockMember returns the member, locked until the transaction ends, and
// whether they hold a password; ErrNotFound when none has the identifier.
func (s *Store) LockMember(ctx context.Context, member access.MemberID) (access.Member, bool, error) {
	dest, m := scanMember()
	var holdsPassword bool
	err := s.db(ctx).QueryRow(ctx, `SELECT `+memberColumns+`, m.password_hash IS NOT NULL FROM members m WHERE m.id = $1 FOR UPDATE`,
		member.Bytes()).Scan(append(dest, &holdsPassword)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Member{}, false, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Member{}, false, fmt.Errorf("locking a member: %w", err)
	}
	return m(), holdsPassword, nil
}

// OtherActiveInstanceAdmins counts the active instance admins other than
// member.
func (s *Store) OtherActiveInstanceAdmins(ctx context.Context, member access.MemberID) (int, error) {
	var n int
	err := s.db(ctx).QueryRow(ctx, `SELECT count(*) FROM members WHERE instance_admin AND status = 'active' AND id <> $1`,
		member.Bytes()).Scan(&n)
	return n, err
}

// SaveMember stores m's email, display name, status and instance admin
// switch.
func (s *Store) SaveMember(ctx context.Context, m access.Member, at time.Time) error {
	_, err := s.db(ctx).Exec(ctx, `
		UPDATE members SET email = $2, display_name = $3, status = $4, instance_admin = $5, updated_at = $6 WHERE id = $1`,
		m.ID.Bytes(), m.Email, m.DisplayName, string(m.Status), m.InstanceAdmin, at)
	return emailTaken(err)
}

// RevokeMemberSessions ends every session of member not ended yet at at,
// and returns how many it ended.
func (s *Store) RevokeMemberSessions(ctx context.Context, member access.MemberID, at time.Time) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE member_id = $1 AND revoked_at IS NULL`, member.Bytes(), at)
	if err != nil {
		return 0, fmt.Errorf("revoking a member's sessions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RevokePersonalTokens revokes every personal access token of member not
// revoked yet at at, and returns how many it revoked.
func (s *Store) RevokePersonalTokens(ctx context.Context, member access.MemberID, at time.Time) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		UPDATE api_tokens SET revoked_at = $2 WHERE kind = 'personal' AND member_id = $1 AND revoked_at IS NULL`, member.Bytes(), at)
	if err != nil {
		return 0, fmt.Errorf("revoking a member's tokens: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
