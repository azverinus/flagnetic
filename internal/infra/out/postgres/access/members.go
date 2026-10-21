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
