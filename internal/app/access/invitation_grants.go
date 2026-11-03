package access

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// GrantingInvitation is an invitation that grants a role in a project, with
// the member it invites.
type GrantingInvitation struct {
	Invitation access.Invitation
	Member     access.Member
}

// InvitationGrantStore is what judging whether invitations may still grant
// what they carry needs.
type InvitationGrantStore interface {
	// PendingInvitation returns the latest invitation of member not consumed
	// yet, expired or not, locked until the unit of work ends; ErrNotFound
	// when the member has none.
	PendingInvitation(ctx context.Context, member access.MemberID) (access.Invitation, error)
	// GrantingInvitations returns the invitations issued by inviter, not
	// consumed yet, that grant a role in project, or in any project when it
	// is zero, each with its member, locked until the unit of work ends.
	GrantingInvitations(ctx context.Context, inviter access.MemberID, project workspace.ProjectID) ([]GrantingInvitation, error)
	// DropInvitationGrant clears the project and role of the invitation,
	// which still activates its member.
	DropInvitationGrant(ctx context.Context, invitation access.InvitationID) error
	// MemberGrants returns the grants member holds in project, oldest
	// first.
	MemberGrants(ctx context.Context, member access.MemberID, project workspace.ProjectID) ([]access.Grant, error)
}

// dropInvitationGrants drops the grant of every invitation inviter issued, to
// project or to any when it is zero, that inviter may no longer hand out
// as they now are, in the unit of work ctx carries, and returns the audit
// record of each drop. The invitations still activate the people they
// invite.
func dropInvitationGrants(ctx context.Context, store InvitationGrantStore, inviter access.Member, project workspace.ProjectID,
) ([]governance.Record, error) {
	pending, err := store.GrantingInvitations(ctx, inviter.ID, project)
	if err != nil {
		return nil, err
	}
	var records []governance.Record
	for _, p := range pending {
		held, err := store.MemberGrants(ctx, inviter.ID, p.Invitation.Grant.Project)
		if err != nil {
			return nil, err
		}
		reason, drop := access.GrantDrop(inviter, held)
		if !drop {
			continue
		}
		if err = store.DropInvitationGrant(ctx, p.Invitation.ID); err != nil {
			return nil, err
		}
		records = append(records, grantRevokedRecord(p.Invitation, p.Member, reason))
	}
	return records, nil
}

// carriedOrigin returns where the fresh invitation of invited, issued in
// place of their pending one, comes from: the member who issued the pending
// one, bringing its grant along only while that member may still hand it
// out. A grant they may not is not carried and its drop is recorded.
func carriedOrigin(ctx context.Context, store ReissueStore, invited access.Member) (invitationOrigin, []governance.Record, error) {
	pending, err := store.PendingInvitation(ctx, invited.ID)
	if errors.Is(err, ErrNotFound) {
		return invitationOrigin{}, nil, nil
	}
	if err != nil {
		return invitationOrigin{}, nil, err
	}
	origin := invitationOrigin{InvitedBy: pending.InvitedBy}
	if pending.Grant == nil {
		return origin, nil, nil
	}
	inviter, err := store.MemberByID(ctx, pending.InvitedBy)
	if err != nil {
		return invitationOrigin{}, nil, err
	}
	held, err := store.MemberGrants(ctx, inviter.ID, pending.Grant.Project)
	if err != nil {
		return invitationOrigin{}, nil, err
	}
	reason, drop := access.GrantDrop(inviter, held)
	if drop {
		return origin, []governance.Record{grantRevokedRecord(pending, invited, reason)}, nil
	}
	origin.Grant = pending.Grant
	return origin, nil, nil
}

// grantRevokedRecord is the audit record of dropping the grant of inv, which
// invites invitee, for reason. It belongs to the project the grant was in.
func grantRevokedRecord(inv access.Invitation, invitee access.Member, reason access.GrantDropReason) governance.Record {
	return governance.Record{
		Project: inv.Grant.Project, Action: "invitation.grant_revoked", Entity: memberEntity(invitee),
		Before: map[string]any{
			"invitation": inv.ID.String(), "email": invitee.Email, "role": inv.Grant.Role, "invited_by": inv.InvitedBy.String(),
		},
		After: map[string]any{"reason": string(reason)},
	}
}
