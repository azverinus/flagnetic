package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// ListMembers answers the instance's members.
func (a *api) ListMembers(ctx context.Context, req ListMembersRequestObject) (ListMembersResponseObject, error) {
	var f appaccess.MemberFilter
	if s := req.Params.Status; s != nil {
		switch access.MemberStatus(*s) {
		case access.MemberInvited, access.MemberActive, access.MemberDeactivated:
			f.Status = access.MemberStatus(*s)
		default:
			return nil, flaghttp.InvalidRequest("status must be invited, active or deactivated")
		}
	}
	if req.Params.Q != nil {
		f.Q = *req.Params.Q
	}
	views, err := a.h.ListMembers.Handle(ctx, f)
	if err != nil {
		return nil, err
	}
	items := make([]Member, len(views))
	for i, v := range views {
		items[i] = memberOf(v)
	}
	return ListMembers200JSONResponse(MemberList{Items: items}), nil
}

// GetMember answers one member.
func (a *api) GetMember(ctx context.Context, req GetMemberRequestObject) (GetMemberResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	v, err := a.h.GetMember.Handle(ctx, member)
	if err != nil {
		return nil, err
	}
	return GetMember200JSONResponse(memberOf(v)), nil
}

// UpdateMember edits a member.
func (a *api) UpdateMember(ctx context.Context, req UpdateMemberRequestObject) (UpdateMemberResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	v, err := a.h.UpdateMember.Handle(ctx, appaccess.UpdateMemberCommand{
		Member: member,
		Change: access.MemberChange{
			Email: req.Body.Email, DisplayName: req.Body.DisplayName, InstanceAdmin: req.Body.InstanceAdmin,
		},
		Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UpdateMember200JSONResponse(memberOf(v)), nil
}

// DeactivateMember offboards a member.
func (a *api) DeactivateMember(ctx context.Context, req DeactivateMemberRequestObject) (DeactivateMemberResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	v, err := a.h.DeactivateMember.Handle(ctx, member, requestOf(ctx))
	if err != nil {
		return nil, err
	}
	return DeactivateMember200JSONResponse(memberOf(v)), nil
}

// ReactivateMember brings a deactivated member back.
func (a *api) ReactivateMember(ctx context.Context, req ReactivateMemberRequestObject) (ReactivateMemberResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	res, err := a.h.ReactivateMember.Handle(ctx, member, requestOf(ctx))
	if err != nil {
		return nil, err
	}
	return ReactivateMember200JSONResponse(memberInvitationOf(ctx, res)), nil
}

// InviteMember invites someone and answers their one-time link.
func (a *api) InviteMember(ctx context.Context, req InviteMemberRequestObject) (InviteMemberResponseObject, error) {
	res, err := a.h.InviteMember.Handle(ctx, appaccess.InviteMemberCommand{
		Email: req.Body.Email, DisplayName: req.Body.DisplayName, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return InviteMember201JSONResponse(memberInvitationOf(ctx, res)), nil
}

// ReissueInvitation replaces an invited member's link and answers the new
// one.
func (a *api) ReissueInvitation(ctx context.Context, req ReissueInvitationRequestObject) (ReissueInvitationResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	res, err := a.h.ReissueInvitation.Handle(ctx, member, requestOf(ctx))
	if err != nil {
		return nil, err
	}
	return ReissueInvitation201JSONResponse(memberInvitationOf(ctx, res)), nil
}

// memberInvitationOf answers res to the request ctx belongs to, with the
// link of the invitation issued, which the console opens at
// /invitations/<secret>.
func memberInvitationOf(ctx context.Context, res appaccess.InvitedMember) MemberInvitation {
	out := MemberInvitation{Member: memberOf(res.Member)}
	if inv := res.Invitation; inv != nil {
		out.Invitation = &IssuedInvitation{
			Url: browserOf(ctx).console + "/invitations/" + inv.Secret.Reveal(), ExpiresAt: inv.ExpiresAt,
		}
	}
	return out
}

func memberOf(v appaccess.MemberView) Member {
	return Member{
		Id: openapi_types.UUID(v.ID.Bytes()), Email: v.Email, DisplayName: v.DisplayName, Status: MemberStatus(v.Status),
		InstanceAdmin: v.InstanceAdmin, CreatedAt: v.CreatedAt, LastSignInAt: v.LastSignInAt, ProjectCount: v.ProjectCount,
	}
}
