package access

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// InviteToProjectStore is what inviting someone to a project needs.
type InviteToProjectStore interface {
	InviteStore
	// LockMemberByEmail returns the member whose email is email whatever its
	// case, locked until the unit of work ends; ErrNotFound when there is
	// none.
	LockMemberByEmail(ctx context.Context, email string) (access.Member, error)
	// PendingInvitation returns the latest invitation of member not consumed
	// yet, locked until the unit of work ends; ErrNotFound when the member
	// has none.
	PendingInvitation(ctx context.Context, member access.MemberID) (access.Invitation, error)
}

// errEmailHeld refuses inviting to a project someone the instance holds
// already: one person has one account, and holding one they are granted a
// role in the project instead.
var errEmailHeld = apperrors.Conflict("already_exists",
	"a member with this email already exists; grant them a role in the project instead")

// InviteToProject invites a person to the instance with a role in a project.
type InviteToProject struct {
	tx     Transactor
	store  InviteToProjectStore
	audit  AuditLog
	clock  clock.Clock
	ids    *id.Source
	random io.Reader
	mailer *InvitationMailer
}

// NewInviteToProject returns the handler; random draws invitation secrets
// and mailer mails them when the instance can.
func NewInviteToProject(tx Transactor, store InviteToProjectStore, audit AuditLog, c clock.Clock, ids *id.Source, random io.Reader,
	mailer *InvitationMailer,
) *InviteToProject {
	return &InviteToProject{tx: tx, store: store, audit: audit, clock: c, ids: ids, random: random, mailer: mailer}
}

// InviteToProjectCommand names the person to invite and the role to invite
// them with.
type InviteToProjectCommand struct {
	// Caller is the member inviting, who holds grant.manage in Project.
	Caller      access.Caller
	Project     ProjectRef
	Email       string
	DisplayName string
	Role        access.RoleName
	Request     governance.Request
}

// Handle invites the person to the instance with the role, which accepting
// the invitation grants them in the project, recording member.invited in the
// project's log, and mails the invitation, naming the project, the role and
// the caller, when the instance can. A role holding a permission the caller lacks in the project
// is refused role_not_allowed. A person the instance holds is refused
// already_exists, except a member invited who has a pending invitation to this
// project, who is invited again with the new role: a fresh invitation then
// replaces theirs, recorded as member.invitation_reissued.
func (h *InviteToProject) Handle(ctx context.Context, cmd InviteToProjectCommand) (InvitedMember, error) {
	grant, err := access.NewInvitationGrant(cmd.Project.ID, cmd.Role, "/role")
	if err != nil {
		return InvitedMember{}, err
	}
	role, _ := access.RoleNamed(cmd.Role)
	if err = access.CheckGrantorCap(cmd.Project.Held, role); err != nil {
		return InvitedMember{}, err
	}
	now := h.clock.Now()
	invited, err := access.NewInvitedMember(id.New[access.Member](h.ids), cmd.Email, cmd.DisplayName, now)
	if err != nil {
		return InvitedMember{}, err
	}
	origin := invitationOrigin{InvitedBy: creatorOf(cmd.Caller), Grant: &grant}
	var res InvitedMember
	err = h.tx.Within(ctx, func(ctx context.Context) error {
		member, action, memberErr := h.memberToInvite(ctx, invited, cmd.Project)
		if memberErr != nil {
			return memberErr
		}
		var issued IssuedInvitation
		if issued, err = issueInvitation(ctx, h.store, h.ids, h.random, member.ID, now, origin); err != nil {
			return err
		}
		after := map[string]any{}
		if action == "member.invited" {
			after = map[string]any{
				"email": member.Email, "display_name": member.DisplayName, "status": member.Status, "instance_admin": member.InstanceAdmin,
			}
		}
		if err = recordAll(ctx, h.audit, h.ids, now, cmd.Request, invitationRecord(action, member, issued, after)); err != nil {
			return err
		}
		if issued, err = h.mailer.mail(ctx, member, issued, cmd.Request.ID); err != nil {
			return err
		}
		res.Invitation = &issued
		res.Member, err = viewOf(ctx, h.store, member.ID)
		return err
	})
	if err != nil {
		return InvitedMember{}, err
	}
	return res, nil
}

// memberToInvite returns the member to issue an invitation to, locked, and
// the audit action issuing it records: a new invited member, created, or the
// invited one whose pending invitation is to project.
func (h *InviteToProject) memberToInvite(ctx context.Context, invited access.Member, project ProjectRef) (access.Member, string, error) {
	existing, err := h.store.LockMemberByEmail(ctx, invited.Email)
	if errors.Is(err, ErrNotFound) {
		return invited, "member.invited", h.store.CreateMember(ctx, invited, "")
	}
	if err != nil {
		return access.Member{}, "", err
	}
	if existing.Status != access.MemberInvited {
		return access.Member{}, "", errEmailHeld
	}
	pending, err := h.store.PendingInvitation(ctx, existing.ID)
	if errors.Is(err, ErrNotFound) {
		return access.Member{}, "", errEmailHeld
	}
	if err != nil {
		return access.Member{}, "", err
	}
	if g := pending.Grant; g == nil || g.Project != project.ID {
		return access.Member{}, "", errEmailHeld
	}
	return existing, "member.invitation_reissued", nil
}

// ProjectInvitation is a member invited with a role in a project, which
// their pending invitation grants once accepted.
type ProjectInvitation struct {
	Member access.Member
	Role   access.RoleName
	// InvitedBy is the member who issued the invitation; zero when none did.
	InvitedBy access.MemberID
	ExpiresAt time.Time
}

// ProjectInvitationDirectory reads who is invited to projects.
type ProjectInvitationDirectory interface {
	// ProjectInvitations returns the invited members whose pending
	// invitation, expired or not, grants a role in project, ordered by
	// email, each with that invitation, read in one query.
	ProjectInvitations(ctx context.Context, project workspace.ProjectID) ([]ProjectInvitation, error)
}

// ListProjectInvitations answers who is invited to a project.
type ListProjectInvitations struct{ store ProjectInvitationDirectory }

// NewListProjectInvitations returns the query.
func NewListProjectInvitations(store ProjectInvitationDirectory) *ListProjectInvitations {
	return &ListProjectInvitations{store: store}
}

// Handle returns the members invited to project with the role their pending
// invitation grants there. A member is listed by their pending invitation
// alone, as only it can still be accepted: an earlier one granting a role
// here was replaced, and one whose grant was dropped grants nothing.
func (h *ListProjectInvitations) Handle(ctx context.Context, project workspace.ProjectID) ([]ProjectInvitation, error) {
	return h.store.ProjectInvitations(ctx, project)
}
