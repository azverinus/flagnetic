package access

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// invitationColumns are the columns scanInvitation reads, prefixed by the
// invitations table's alias i.
const invitationColumns = `i.id, i.member_id, i.secret_hash, i.created_at, i.expires_at, i.consumed_at, i.project_id, i.role, i.invited_by`

// scanInvitation returns a scan target of invitationColumns and the
// invitation it fills.
func scanInvitation() ([]any, func() access.Invitation) {
	var (
		inv                   access.Invitation
		invitationID, invitee [16]byte
		secretHash            []byte
		project, inviter      *[16]byte
		role                  *string
	)
	dest := []any{&invitationID, &invitee, &secretHash, &inv.CreatedAt, &inv.ExpiresAt, &inv.ConsumedAt, &project, &role, &inviter}
	return dest, func() access.Invitation {
		inv.ID, inv.Member, inv.Hash = id.FromBytes[access.Invitation](invitationID), id.FromBytes[access.Member](invitee),
			access.InvitationHash(secretHash)
		if inviter != nil {
			inv.InvitedBy = id.FromBytes[access.Member](*inviter)
		}
		if project != nil && role != nil {
			inv.Grant = &access.InvitationGrant{Project: id.FromBytes[workspace.Project](*project), Role: access.RoleName(*role)}
		}
		return inv
	}
}

// CreateInvitation stores inv.
func (s *Store) CreateInvitation(ctx context.Context, inv access.Invitation) error {
	var inviter, project, role any
	if !inv.InvitedBy.IsZero() {
		inviter = inv.InvitedBy.Bytes()
	}
	if g := inv.Grant; g != nil {
		project, role = g.Project.Bytes(), string(g.Role)
	}
	_, err := s.db(ctx).Exec(ctx, `
		INSERT INTO invitations (id, member_id, secret_hash, created_at, expires_at, consumed_at, project_id, role, invited_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		inv.ID.Bytes(), inv.Member.Bytes(), inv.Hash[:], inv.CreatedAt, inv.ExpiresAt, inv.ConsumedAt, project, role, inviter)
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
	invDest, invitation := scanInvitation()
	dest, member := scanMember()
	err := s.db(ctx).QueryRow(ctx, `
		SELECT `+invitationColumns+`, `+memberColumns+`
		FROM invitations i JOIN members m ON m.id = i.member_id
		WHERE i.secret_hash = $1`+lock, hash[:]).Scan(append(invDest, dest...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Invitation{}, access.Member{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Invitation{}, access.Member{}, fmt.Errorf("reading an invitation: %w", err)
	}
	return invitation(), member(), nil
}

// PendingInvitation returns the latest invitation of member not consumed yet,
// expired or not, locked until the transaction ends; ErrNotFound when the
// member has none. Its member is to be locked first.
func (s *Store) PendingInvitation(ctx context.Context, member access.MemberID) (access.Invitation, error) {
	dest, invitation := scanInvitation()
	err := s.db(ctx).QueryRow(ctx, `
		SELECT `+invitationColumns+` FROM invitations i
		WHERE i.member_id = $1 AND i.consumed_at IS NULL
		ORDER BY i.created_at DESC, i.id DESC LIMIT 1 FOR UPDATE`, member.Bytes()).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Invitation{}, appaccess.ErrNotFound
	}
	if err != nil {
		return access.Invitation{}, fmt.Errorf("reading a member's pending invitation: %w", err)
	}
	return invitation(), nil
}

// ProjectInvitations returns the invited members whose pending invitation,
// expired or not, grants a role in project, ordered by email, each with that
// invitation. A member's pending invitation is their latest not consumed
// yet, so an earlier one is passed over even when it grants a role here.
func (s *Store) ProjectInvitations(ctx context.Context, project workspace.ProjectID) ([]appaccess.ProjectInvitation, error) {
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+invitationColumns+`, `+memberColumns+`
		FROM invitations i JOIN members m ON m.id = i.member_id
		WHERE i.project_id = $1 AND i.role IS NOT NULL AND i.consumed_at IS NULL AND m.status = 'invited'
			AND NOT EXISTS (
				SELECT 1 FROM invitations later
				WHERE later.member_id = i.member_id AND later.consumed_at IS NULL
					AND (later.created_at, later.id) > (i.created_at, i.id))
		ORDER BY lower(m.email) COLLATE "C", m.id`, project.Bytes())
	if err != nil {
		return nil, fmt.Errorf("listing a project's invitations: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (appaccess.ProjectInvitation, error) {
		invDest, invitation := scanInvitation()
		dest, member := scanMember()
		if scanErr := row.Scan(append(invDest, dest...)...); scanErr != nil {
			return appaccess.ProjectInvitation{}, scanErr
		}
		// The query keeps only invitations granting a role, so each has one.
		inv := invitation()
		return appaccess.ProjectInvitation{Member: member(), Role: inv.Grant.Role, InvitedBy: inv.InvitedBy, ExpiresAt: inv.ExpiresAt}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing a project's invitations: %w", err)
	}
	return out, nil
}

// GrantingInvitations returns the invitations issued by inviter, not
// consumed yet, that would grant a role in project, or in any project when
// project is zero, each with its member, locked until the transaction ends.
func (s *Store) GrantingInvitations(ctx context.Context, inviter access.MemberID, project workspace.ProjectID,
) ([]appaccess.GrantingInvitation, error) {
	var only any
	if !project.IsZero() {
		only = project.Bytes()
	}
	rows, err := s.db(ctx).Query(ctx, `
		SELECT `+invitationColumns+`, `+memberColumns+`
		FROM invitations i JOIN members m ON m.id = i.member_id
		WHERE i.invited_by = $1 AND i.consumed_at IS NULL AND i.project_id IS NOT NULL AND ($2::uuid IS NULL OR i.project_id = $2)
		ORDER BY i.created_at, i.id FOR UPDATE OF i`, inviter.Bytes(), only)
	if err != nil {
		return nil, fmt.Errorf("listing an inviter's invitations: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (appaccess.GrantingInvitation, error) {
		invDest, invitation := scanInvitation()
		dest, member := scanMember()
		if scanErr := row.Scan(append(invDest, dest...)...); scanErr != nil {
			return appaccess.GrantingInvitation{}, scanErr
		}
		return appaccess.GrantingInvitation{Invitation: invitation(), Member: member()}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing an inviter's invitations: %w", err)
	}
	return out, nil
}

// DropInvitationGrant clears the project and role of the invitation, which
// still activates its member.
func (s *Store) DropInvitationGrant(ctx context.Context, invitation access.InvitationID) error {
	_, err := s.db(ctx).Exec(ctx, `UPDATE invitations SET project_id = NULL, role = NULL WHERE id = $1`, invitation.Bytes())
	if err != nil {
		return fmt.Errorf("dropping an invitation's grant: %w", err)
	}
	return nil
}

// InvitationDetails returns what the page of the invitation shows besides
// its member: the project it grants a role in, if it still does, and who
// issued it.
func (s *Store) InvitationDetails(ctx context.Context, invitation access.InvitationID) (appaccess.InvitationDetails, error) {
	var (
		d                       appaccess.InvitationDetails
		projectKey, projectName *string
		inviter, inviterLocale  *string
	)
	err := s.db(ctx).QueryRow(ctx, `
		SELECT p.key, p.name, m.display_name, m.locale
		FROM invitations i
		LEFT JOIN projects p ON p.id = i.project_id AND i.role IS NOT NULL
		LEFT JOIN members m ON m.id = i.invited_by
		WHERE i.id = $1`, invitation.Bytes()).Scan(&projectKey, &projectName, &inviter, &inviterLocale)
	if errors.Is(err, pgx.ErrNoRows) {
		return appaccess.InvitationDetails{}, appaccess.ErrNotFound
	}
	if err != nil {
		return appaccess.InvitationDetails{}, fmt.Errorf("reading an invitation's details: %w", err)
	}
	if projectKey != nil && projectName != nil {
		d.ProjectKey, d.ProjectName = *projectKey, *projectName
	}
	if inviter != nil {
		d.InvitedBy = *inviter
	}
	if inviterLocale != nil {
		d.InviterLocale = *inviterLocale
	}
	return d, nil
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
