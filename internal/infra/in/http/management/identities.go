package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sso"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// authMethodsOf describes the ways a member holds to sign in.
func authMethodsOf(m appidentity.MemberAuthMethods) MemberAuthMethods {
	out := MemberAuthMethods{
		Password:   PasswordStatus{Set: m.PasswordSetAt != nil, SetAt: m.PasswordSetAt},
		Identities: make([]LinkedIdentity, 0, len(m.Identities)),
	}
	for _, i := range m.Identities {
		out.Identities = append(out.Identities, LinkedIdentity{
			Id: openapi_types.UUID(i.ID.Bytes()), Provider: i.ProviderSlug, ProviderName: i.ProviderName, Kind: AuthProviderKind(i.Kind),
			ProviderEnabled: i.ProviderEnabled, Subject: i.Subject, LinkedAt: i.LinkedAt, LastSignInAt: i.LastSignInAt,
		})
	}
	return out
}

// identityMemberOf is the identity context's name of a member.
func identityMemberOf(m access.MemberID) identity.MemberID {
	return id.FromBytes[identity.Member](m.Bytes())
}

// ListMyIdentities answers the caller's password status and identities.
func (a *api) ListMyIdentities(ctx context.Context, _ ListMyIdentitiesRequestObject) (ListMyIdentitiesResponseObject, error) {
	c := callerOf(ctx)
	if c.Member == nil {
		return nil, appaccess.ErrMemberNotFound
	}
	methods, err := a.h.ListMemberAuthMethods.Handle(ctx, identityMemberOf(c.Member.ID))
	if err != nil {
		return nil, err
	}
	return ListMyIdentities200JSONResponse(authMethodsOf(methods)), nil
}

// ListMemberIdentities answers a member's password status and identities.
func (a *api) ListMemberIdentities(ctx context.Context, req ListMemberIdentitiesRequestObject) (ListMemberIdentitiesResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	methods, err := a.h.ListMemberAuthMethods.Handle(ctx, identityMemberOf(member))
	if err != nil {
		return nil, err
	}
	return ListMemberIdentities200JSONResponse(authMethodsOf(methods)), nil
}

// LinkMyIdentity starts linking an identity at an auth provider to the
// caller, whose session the attempt is bound to.
func (a *api) LinkMyIdentity(ctx context.Context, req LinkMyIdentityRequestObject) (LinkMyIdentityResponseObject, error) {
	c := callerOf(ctx)
	if c.Member == nil || c.Session == nil {
		return nil, access.ErrSessionRequired
	}
	var returnTo string
	if req.Body != nil && req.Body.ReturnTo != nil {
		returnTo = *req.Body.ReturnTo
	}
	started, err := a.h.StartLink.Handle(ctx, appidentity.StartLinkCommand{
		Slug: req.Provider, ReturnTo: returnTo,
		Member: identityMemberOf(c.Member.ID), Session: id.FromBytes[identity.Session](c.Session.ID.Bytes()),
	})
	if err != nil {
		return nil, err
	}
	cookie := sso.AttemptCookie(started.Cookie.Reveal(), started.Lifetime, browserOf(ctx).secureCookies)
	return LinkMyIdentity200JSONResponse{
		Body:    IdentityLinkStarted{StartUrl: started.RedirectURL},
		Headers: LinkMyIdentity200ResponseHeaders{SetCookie: &cookie},
	}, nil
}

// UnlinkMyIdentity unlinks one of the caller's identities.
func (a *api) UnlinkMyIdentity(ctx context.Context, req UnlinkMyIdentityRequestObject) (UnlinkMyIdentityResponseObject, error) {
	c := callerOf(ctx)
	if c.Member == nil || c.Session == nil {
		return nil, access.ErrSessionRequired
	}
	err := a.h.UnlinkIdentity.Handle(ctx, appidentity.UnlinkCommand{
		Member: identityMemberOf(c.Member.ID), Identity: req.Identity, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UnlinkMyIdentity204Response{}, nil
}

// UnlinkMemberIdentity unlinks an identity of a member.
func (a *api) UnlinkMemberIdentity(ctx context.Context, req UnlinkMemberIdentityRequestObject) (UnlinkMemberIdentityResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	err = a.h.UnlinkIdentity.Handle(ctx, appidentity.UnlinkCommand{
		Member: identityMemberOf(member), Identity: req.Identity, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UnlinkMemberIdentity204Response{}, nil
}

// RemoveMyPassword removes the caller's password while another way in
// remains.
func (a *api) RemoveMyPassword(ctx context.Context, _ RemoveMyPasswordRequestObject) (RemoveMyPasswordResponseObject, error) {
	err := a.h.RemoveMyPassword.Handle(ctx, appaccess.RemoveMyPasswordCommand{Caller: callerOf(ctx), Request: requestOf(ctx)})
	if err != nil {
		return nil, err
	}
	return RemoveMyPassword204Response{}, nil
}
