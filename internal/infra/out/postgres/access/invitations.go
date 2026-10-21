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

// CreateInvitation stores inv.
func (s *Store) CreateInvitation(ctx context.Context, inv access.Invitation) error {
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO invitations (id, member_id, secret_hash, created_at, expires_at, consumed_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		inv.ID.Bytes(), inv.Member.Bytes(), inv.Hash[:], inv.CreatedAt, inv.ExpiresAt, inv.ConsumedAt)
	return err
}

// InvitationByHash returns the invitation whose secret hashes to hash, with
// its member; ErrNotFound when there is none.
func (s *Store) InvitationByHash(ctx context.Context, hash access.InvitationHash) (access.Invitation, access.Member, error) {
	return s.invitationByHash(ctx, hash, "")
}

// LockInvitationByHash returns the invitation whose secret hashes to hash,
// locked until the transaction ends, with its member; ErrNotFound when there
// is none. Its member is to be locked first, as every change to a member's
// invitations locks them.
func (s *Store) LockInvitationByHash(ctx context.Context, hash access.InvitationHash) (access.Invitation, access.Member, error) {
	return s.invitationByHash(ctx, hash, " FOR UPDATE OF i")
}

func (s *Store) invitationByHash(ctx context.Context, hash access.InvitationHash, lock string,
) (access.Invitation, access.Member, error) {
	var (
		inv                 access.Invitation
		invitationID, owner [16]byte
		secretHash          []byte
	)
	dest, member := scanMember()
	err := s.db(ctx).QueryRow(ctx, `
		SELECT i.id, i.member_id, i.secret_hash, i.created_at, i.expires_at, i.consumed_at, `+memberColumns+`
		FROM invitations i JOIN members m ON m.id = i.member_id
		WHERE i.secret_hash = $1`+lock, hash[:]).Scan(
		append([]any{&invitationID, &owner, &secretHash, &inv.CreatedAt, &inv.ExpiresAt, &inv.ConsumedAt}, dest...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Invitation{}, access.Member{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Invitation{}, access.Member{}, fmt.Errorf("reading an invitation: %w", err)
	}
	inv.ID, inv.Member, inv.Hash = id.FromBytes[access.Invitation](invitationID), id.FromBytes[access.Member](owner),
		access.InvitationHash(secretHash)
	return inv, member(), nil
}

// ConsumeInvitations ends every open invitation of member at at, and
// returns how many it ended.
func (s *Store) ConsumeInvitations(ctx context.Context, member access.MemberID, at time.Time) (int, error) {
	tag, err := s.db(ctx).Exec(ctx, `UPDATE invitations SET consumed_at = $2 WHERE member_id = $1 AND consumed_at IS NULL`,
		member.Bytes(), at)
	if err != nil {
		return 0, fmt.Errorf("consuming a member's invitations: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeInvitations deletes at most limit invitations consumed or expired at
// or before the cutoff, except each member's latest, which stays as the
// record of their last link.
func (s *Store) PurgeInvitations(ctx context.Context, cut access.InvitationCutoffs, limit int) (int, error) {
	const ended = `(i.consumed_at <= $1 OR i.expires_at <= $1) AND EXISTS (
		SELECT 1 FROM invitations later WHERE later.member_id = i.member_id AND (later.created_at, later.id) > (i.created_at, i.id))`
	tag, err := s.db(ctx).Exec(ctx, `
		DELETE FROM invitations i WHERE `+ended+`
		AND i.id IN (SELECT i.id FROM invitations i WHERE `+ended+` LIMIT $2)`, cut.EndedBy, limit)
	if err != nil {
		return 0, fmt.Errorf("purging invitations: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ReserveSourceAttempt counts a as a failure of its source alone, before
// what it presents is checked: an attempt naming no email, such as accepting
// an invitation, is throttled with the source's other attempts but locks no
// email. The window is counted as reserveAttempt counts it.
func (s *Store) ReserveSourceAttempt(ctx context.Context, a access.Attempt) (access.Reservation, error) {
	r := access.Reservation{Attempt: a}
	err := s.db(ctx).QueryRow(ctx, `
		INSERT INTO sign_in_source_failures AS s (source, window_start, failures) VALUES ($1, $2, 1)
		ON CONFLICT (source) DO UPDATE SET
			window_start = CASE WHEN s.window_start <= $3 THEN $2 ELSE s.window_start END,
			failures     = CASE WHEN s.window_start <= $3 THEN 1 ELSE s.failures + 1 END
		RETURNING window_start, failures`, a.Source, a.At, a.At.Add(-access.SourceWindow)).Scan(&r.WindowStart, &r.SourceFailures)
	if err != nil {
		return access.Reservation{}, fmt.Errorf("reserving an attempt of a source: %w", err)
	}
	return r, nil
}
