package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// resetColumns are the columns scanPasswordReset reads, prefixed by
// the password_resets table's alias r.
const resetColumns = `r.id, r.member_id, r.secret_hash, r.issued_by, r.created_at, r.expires_at, r.consumed_at`

// scanPasswordReset returns a scan target of resetColumns and the
// password reset it fills.
func scanPasswordReset() ([]any, func() access.PasswordReset) {
	var (
		reset           access.PasswordReset
		resetID, member [16]byte
		secretHash      []byte
		issuer          *[16]byte
	)
	dest := []any{&resetID, &member, &secretHash, &issuer, &reset.CreatedAt, &reset.ExpiresAt, &reset.ConsumedAt}
	return dest, func() access.PasswordReset {
		reset.ID, reset.Member, reset.Hash = id.FromBytes[access.PasswordReset](resetID), id.FromBytes[access.Member](member),
			access.PasswordResetHash(secretHash)
		if issuer != nil {
			reset.IssuedBy = id.FromBytes[access.Member](*issuer)
		}
		return reset
	}
}

// CreatePasswordReset stores reset.
func (s *Store) CreatePasswordReset(ctx context.Context, reset access.PasswordReset) error {
	var issuer any
	if !reset.IssuedBy.IsZero() {
		issuer = reset.IssuedBy.Bytes()
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO password_resets (id, member_id, secret_hash, issued_by, created_at, expires_at, consumed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		reset.ID.Bytes(), reset.Member.Bytes(), reset.Hash[:], issuer, reset.CreatedAt, reset.ExpiresAt, reset.ConsumedAt)
	if err != nil {
		return fmt.Errorf("storing a password reset: %w", err)
	}
	return nil
}

// PasswordResetByHash returns the password reset whose secret hashes to hash,
// with its member; ErrNotFound when there is none.
func (s *Store) PasswordResetByHash(ctx context.Context, hash access.PasswordResetHash) (access.PasswordReset, access.Member, error) {
	return s.passwordResetByHash(ctx, hash, "")
}

// LockPasswordResetByHash returns the password reset whose secret hashes to
// hash, locked until the transaction ends, with its member; ErrNotFound when
// there is none. Its member is to be locked first, as every change to a
// member's password resets locks them.
func (s *Store) LockPasswordResetByHash(ctx context.Context, hash access.PasswordResetHash,
) (access.PasswordReset, access.Member, error) {
	return s.passwordResetByHash(ctx, hash, " FOR UPDATE OF r")
}

func (s *Store) passwordResetByHash(ctx context.Context, hash access.PasswordResetHash, lock string,
) (access.PasswordReset, access.Member, error) {
	resetDest, reset := scanPasswordReset()
	dest, member := scanMember()
	err := s.db(ctx).QueryRow(ctx, `
		SELECT `+resetColumns+`, `+memberColumns+`
		FROM password_resets r JOIN members m ON m.id = r.member_id
		WHERE r.secret_hash = $1`+lock, hash[:]).Scan(append(resetDest, dest...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.PasswordReset{}, access.Member{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.PasswordReset{}, access.Member{}, fmt.Errorf("reading a password reset: %w", err)
	}
	return reset(), member(), nil
}

// ConsumePasswordResets ends every open password reset of member at at, and
// returns how many it ended.
func (s *Store) ConsumePasswordResets(ctx context.Context, member access.MemberID, at time.Time) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `UPDATE password_resets SET consumed_at = $2 WHERE member_id = $1 AND consumed_at IS NULL`,
		member.Bytes(), at)
	if err != nil {
		return 0, fmt.Errorf("consuming a member's password resets: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgePasswordResets deletes at most limit password resets consumed or
// expired at or before the cutoff.
func (s *Store) PurgePasswordResets(ctx context.Context, cut access.PasswordResetCutoffs, limit int) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `
		DELETE FROM password_resets WHERE id IN (
			SELECT id FROM password_resets WHERE LEAST(consumed_at, expires_at) <= $1 LIMIT $2)`, cut.EndedBy, limit)
	if err != nil {
		return 0, fmt.Errorf("purging password resets: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ClearSignInFailures ends the consecutive failed sign-ins counted under
// email, whatever its case, its own and those from the sources its member signed in from, which
// lifts any lockout of the email. The failures of an address, which are not
// the member's, stay.
func (s *Store) ClearSignInFailures(ctx context.Context, email string) error {
	for _, table := range []string{"sign_in_email_failures", "sign_in_known_source_failures"} {
		if _, err := s.db(ctx).Exec(ctx, `DELETE FROM `+table+` WHERE email = $1`, strings.ToLower(email)); err != nil {
			return fmt.Errorf("clearing an email's sign-in failures: %w", err)
		}
	}
	return nil
}
