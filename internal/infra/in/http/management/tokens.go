package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ListMyTokens answers the caller's personal access tokens.
func (a *api) ListMyTokens(ctx context.Context, _ ListMyTokensRequestObject) (ListMyTokensResponseObject, error) {
	tokens, err := a.h.ListPersonalTokens.Handle(ctx, callerOf(ctx), access.MemberID{})
	if err != nil {
		return nil, err
	}
	return ListMyTokens200JSONResponse{Items: each(tokens, apiTokenOf)}, nil
}

// CreateMyToken creates a personal access token for the caller and answers
// its value, this once.
func (a *api) CreateMyToken(ctx context.Context, req CreateMyTokenRequestObject) (CreateMyTokenResponseObject, error) {
	cmd := appaccess.CreatePersonalTokenCommand{
		Caller: callerOf(ctx), Name: req.Body.Name, ExpiresAt: req.Body.ExpiresAt, Request: requestOf(ctx),
	}
	if req.Body.Scopes != nil {
		cmd.Scopes = each(*req.Body.Scopes, func(s Scope) access.Scope { return access.Scope(s) })
	}
	created, err := a.h.CreatePersonalToken.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return CreateMyToken201JSONResponse{Token: apiTokenOf(created.Token), Value: created.Value.Reveal()}, nil
}

// RevokeMyToken revokes one of the caller's tokens.
func (a *api) RevokeMyToken(ctx context.Context, req RevokeMyTokenRequestObject) (RevokeMyTokenResponseObject, error) {
	if err := a.revokeToken(ctx, access.MemberID{}, req.Token); err != nil {
		return nil, err
	}
	return RevokeMyToken204Response{}, nil
}

// ListMemberTokens answers a member's personal access tokens.
func (a *api) ListMemberTokens(ctx context.Context, req ListMemberTokensRequestObject) (ListMemberTokensResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	tokens, err := a.h.ListPersonalTokens.Handle(ctx, callerOf(ctx), member)
	if err != nil {
		return nil, err
	}
	return ListMemberTokens200JSONResponse{Items: each(tokens, apiTokenOf)}, nil
}

// RevokeMemberToken revokes one of a member's tokens.
func (a *api) RevokeMemberToken(ctx context.Context, req RevokeMemberTokenRequestObject) (RevokeMemberTokenResponseObject, error) {
	member, err := memberIDOf(req.Member)
	if err != nil {
		return nil, err
	}
	if err := a.revokeToken(ctx, member, req.Token); err != nil {
		return nil, err
	}
	return RevokeMemberToken204Response{}, nil
}

// revokeToken revokes the token of member (the caller's own when zero) a
// path names; one that is not an identifier names no token.
func (a *api) revokeToken(ctx context.Context, member access.MemberID, token string) error {
	tokenID, err := id.Parse[access.APIToken](token)
	if err != nil {
		tokenID = access.APITokenID{}
	}
	return a.h.RevokePersonalToken.Handle(ctx, appaccess.RevokePersonalTokenCommand{
		Caller: callerOf(ctx), Member: member, Token: tokenID, Request: requestOf(ctx),
	})
}

func apiTokenOf(t access.APIToken) ApiToken {
	out := ApiToken{
		Id: openapi_types.UUID(t.ID.Bytes()), Name: t.Name, Hint: t.Hint,
		Scopes:    each(t.Scopes, func(s access.Scope) Scope { return Scope(s) }),
		ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt,
	}
	if t.LastUsedIP.IsValid() {
		ip := t.LastUsedIP.String()
		out.LastUsedIp = &ip
	}
	return out
}
