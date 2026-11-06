package management

import (
	"context"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func scimTokenOut(r appidentity.SCIMTokenRecord) ScimToken {
	return ScimToken{
		Id: r.ID.Bytes(), Provider: r.ProviderSlug, Hint: r.Hint, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
		LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt,
	}
}

// ListScimTokens answers the instance's SCIM tokens.
func (a *api) ListScimTokens(ctx context.Context, _ ListScimTokensRequestObject) (ListScimTokensResponseObject, error) {
	list, err := a.h.ListSCIMTokens.Handle(ctx)
	if err != nil {
		return nil, err
	}
	return ListScimTokens200JSONResponse{Items: each(list, scimTokenOut)}, nil
}

// CreateScimToken creates a SCIM token bound to an auth provider.
func (a *api) CreateScimToken(ctx context.Context, req CreateScimTokenRequestObject) (CreateScimTokenResponseObject, error) {
	created, err := a.h.CreateSCIMToken.Handle(ctx, appidentity.CreateSCIMTokenCommand{
		Provider: req.Body.Provider, ExpiresAt: req.Body.ExpiresAt, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CreateScimToken201JSONResponse{Token: scimTokenOut(created.Token), Value: created.Value.Reveal()}, nil
}

// RevokeScimToken revokes a SCIM token.
func (a *api) RevokeScimToken(ctx context.Context, req RevokeScimTokenRequestObject) (RevokeScimTokenResponseObject, error) {
	token, err := id.Parse[identity.SCIMToken](req.Token)
	if err != nil {
		// Not an identifier, so no token has it.
		return nil, appidentity.ErrSCIMTokenNotFound
	}
	if err := a.h.RevokeSCIMToken.Handle(ctx, token, requestOf(ctx)); err != nil {
		return nil, err
	}
	return RevokeScimToken204Response{}, nil
}
