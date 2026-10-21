package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ListProjectTokens answers the project's tokens.
func (a *api) ListProjectTokens(ctx context.Context, _ ListProjectTokensRequestObject) (ListProjectTokensResponseObject, error) {
	tokens, err := a.h.ListProjectTokens.Handle(ctx, projectOf(ctx).project.ID)
	if err != nil {
		return nil, err
	}
	return ListProjectTokens200JSONResponse{Items: each(tokens, projectTokenOf)}, nil
}

// CreateProjectToken creates a token of the project and answers its value,
// this once.
func (a *api) CreateProjectToken(ctx context.Context, req CreateProjectTokenRequestObject) (CreateProjectTokenResponseObject, error) {
	cmd := appaccess.CreateProjectTokenCommand{
		Caller: callerOf(ctx), Project: projectRefOf(ctx), Name: req.Body.Name, Role: access.RoleName(req.Body.Role),
		ExpiresAt: req.Body.ExpiresAt, Request: requestOf(ctx),
	}
	if req.Body.Scopes != nil {
		cmd.Scopes = each(*req.Body.Scopes, func(s Scope) access.Scope { return access.Scope(s) })
	}
	created, err := a.h.CreateProjectToken.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return CreateProjectToken201JSONResponse{Token: projectTokenOf(created.Token), Value: created.Value.Reveal()}, nil
}

// RevokeProjectToken revokes one of the project's tokens.
func (a *api) RevokeProjectToken(ctx context.Context, req RevokeProjectTokenRequestObject) (RevokeProjectTokenResponseObject, error) {
	token, err := id.Parse[access.APIToken](req.Token)
	if err != nil {
		// Not an identifier, so no token has it.
		return nil, appaccess.ErrTokenNotFound
	}
	if err := a.h.RevokeProjectToken.Handle(ctx, appaccess.RevokeProjectTokenCommand{
		Project: projectRefOf(ctx), Token: token, Request: requestOf(ctx),
	}); err != nil {
		return nil, err
	}
	return RevokeProjectToken204Response{}, nil
}

func projectTokenOf(t access.APIToken) ProjectToken {
	pt := apiTokenOf(t)
	out := ProjectToken{
		Id: pt.Id, Name: pt.Name, Hint: pt.Hint, Role: RoleName(t.Role), Scopes: pt.Scopes, ExpiresAt: pt.ExpiresAt,
		CreatedAt: pt.CreatedAt, LastUsedAt: pt.LastUsedAt, LastUsedIp: pt.LastUsedIp, RevokedAt: pt.RevokedAt,
	}
	if !t.CreatedBy.IsZero() {
		by := openapi_types.UUID(t.CreatedBy.Bytes())
		out.CreatedBy = &by
	}
	return out
}
