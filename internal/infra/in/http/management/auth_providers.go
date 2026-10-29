package management

import (
	"context"
	"strings"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// redirectURI returns where the identity provider sends a browser back to
// after a sign-in through p, built from the instance's public URL; empty
// while the instance has none.
func (o Options) redirectURI(p identity.Provider) string {
	if o.BaseURL == nil {
		return ""
	}
	return strings.TrimSuffix(o.BaseURL.String(), "/") + "/sso/" + p.Slug + "/callback"
}

// authProviderOut returns p as the API answers it, never with its secret.
func (a *api) authProviderOut(p identity.Provider) AuthProvider {
	out := AuthProvider{
		Id: p.ID.Bytes(), Slug: p.Slug, Kind: AuthProviderKind(p.Kind), DisplayName: p.DisplayName, Enabled: p.Enabled, Domains: p.Domains,
		LinkByEmail: p.LinkByEmail, AutoProvision: p.AutoProvision, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		Version: p.Version,
	}
	if o := p.OIDC; o != nil {
		out.Issuer, out.ClientId, out.ClientSecretSet = &o.Issuer, &o.ClientID, new(true)
		out.Scopes, out.RequireVerifiedEmail = &o.Scopes, &o.RequireVerifiedEmail
		out.Claims = &AuthProviderClaims{Email: o.Claims.Email, Name: o.Claims.Name, Groups: o.Claims.Groups}
	}
	if uri := a.opts.redirectURI(p); uri != "" {
		out.RedirectUri = &uri
	}
	return out
}

// ListAuthProviders answers the instance's auth providers.
func (a *api) ListAuthProviders(ctx context.Context, _ ListAuthProvidersRequestObject) (ListAuthProvidersResponseObject, error) {
	list, err := a.h.ListAuthProviders.Handle(ctx)
	if err != nil {
		return nil, err
	}
	return ListAuthProviders200JSONResponse{Items: each(list, a.authProviderOut)}, nil
}

// CreateAuthProvider adds an auth provider.
func (a *api) CreateAuthProvider(ctx context.Context, req CreateAuthProviderRequestObject) (CreateAuthProviderResponseObject, error) {
	b := *req.Body
	var claims AuthProviderClaimsPatch
	if b.Claims != nil {
		claims = *b.Claims
	}
	p, err := a.h.CreateAuthProvider.Handle(ctx, appidentity.CreateProviderCommand{
		Request: requestOf(ctx),
		Input: identity.ProviderInput{
			Slug: b.Slug, Kind: identity.Kind(b.Kind), DisplayName: b.DisplayName, Enabled: valueOr(b.Enabled, true),
			Domains: b.Domains, LinkByEmail: valueOr(b.LinkByEmail, false), AutoProvision: valueOr(b.AutoProvision, false),
			OIDC: identity.OIDCInput{
				Issuer: b.Issuer, ClientID: b.ClientId, ClientSecret: secret.New(valueOf(b.ClientSecret)), Scopes: valueOr(b.Scopes, nil),
				Claims:               identity.Claims{Email: valueOf(claims.Email), Name: valueOf(claims.Name), Groups: valueOf(claims.Groups)},
				RequireVerifiedEmail: valueOr(b.RequireVerifiedEmail, true),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return CreateAuthProvider201JSONResponse{Body: a.authProviderOut(p), Headers: CreateAuthProvider201ResponseHeaders{ETag: etag(p.Version)}}, nil
}

// GetAuthProvider answers an auth provider.
func (a *api) GetAuthProvider(ctx context.Context, req GetAuthProviderRequestObject) (GetAuthProviderResponseObject, error) {
	p, err := a.h.GetAuthProvider.Handle(ctx, req.Provider)
	if err != nil {
		return nil, err
	}
	return GetAuthProvider200JSONResponse{Body: a.authProviderOut(p), Headers: GetAuthProvider200ResponseHeaders{ETag: etag(p.Version)}}, nil
}

// UpdateAuthProvider edits an auth provider.
func (a *api) UpdateAuthProvider(ctx context.Context, req UpdateAuthProviderRequestObject) (UpdateAuthProviderResponseObject, error) {
	expected, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := *req.Body
	change := identity.ProviderChange{
		DisplayName: b.DisplayName, Domains: b.Domains, LinkByEmail: b.LinkByEmail, AutoProvision: b.AutoProvision,
		Issuer: b.Issuer, ClientID: b.ClientId, Scopes: b.Scopes, RequireVerifiedEmail: b.RequireVerifiedEmail,
	}
	if b.ClientSecret != nil {
		change.ClientSecret = new(secret.New(*b.ClientSecret))
	}
	if c := b.Claims; c != nil {
		change.Claims = &identity.ClaimsChange{Email: c.Email, Name: c.Name, Groups: c.Groups}
	}
	p, err := a.h.UpdateAuthProvider.Handle(ctx, appidentity.UpdateProviderCommand{
		Slug: req.Provider, Change: change, Expected: expected, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UpdateAuthProvider200JSONResponse{Body: a.authProviderOut(p), Headers: UpdateAuthProvider200ResponseHeaders{ETag: etag(p.Version)}}, nil
}

// DeleteAuthProvider deletes an auth provider nothing depends on.
func (a *api) DeleteAuthProvider(ctx context.Context, req DeleteAuthProviderRequestObject) (DeleteAuthProviderResponseObject, error) {
	if err := a.h.DeleteAuthProvider.Handle(ctx, appidentity.ProviderCommand{Slug: req.Provider, Request: requestOf(ctx)}); err != nil {
		return nil, err
	}
	return DeleteAuthProvider204Response{}, nil
}

// EnableAuthProvider enables an auth provider.
func (a *api) EnableAuthProvider(ctx context.Context, req EnableAuthProviderRequestObject) (EnableAuthProviderResponseObject, error) {
	p, err := a.h.EnableAuthProvider.Handle(ctx, appidentity.ProviderCommand{Slug: req.Provider, Request: requestOf(ctx)})
	if err != nil {
		return nil, err
	}
	return EnableAuthProvider200JSONResponse{Body: a.authProviderOut(p), Headers: EnableAuthProvider200ResponseHeaders{ETag: etag(p.Version)}}, nil
}

// DisableAuthProvider disables an auth provider.
func (a *api) DisableAuthProvider(ctx context.Context, req DisableAuthProviderRequestObject) (DisableAuthProviderResponseObject, error) {
	p, err := a.h.DisableAuthProvider.Handle(ctx, appidentity.ProviderCommand{Slug: req.Provider, Request: requestOf(ctx)})
	if err != nil {
		return nil, err
	}
	return DisableAuthProvider200JSONResponse{Body: a.authProviderOut(p), Headers: DisableAuthProvider200ResponseHeaders{ETag: etag(p.Version)}}, nil
}

// TestAuthProvider answers what an auth provider's discovery document says.
func (a *api) TestAuthProvider(ctx context.Context, req TestAuthProviderRequestObject) (TestAuthProviderResponseObject, error) {
	d, err := a.h.TestAuthProvider.Handle(ctx, req.Provider)
	if err != nil {
		return nil, err
	}
	out := AuthProviderTest{
		Issuer: d.Issuer, AuthorizationEndpoint: d.AuthorizationEndpoint, TokenEndpoint: d.TokenEndpoint, JwksUri: d.JWKSURI,
	}
	if d.UserinfoEndpoint != "" {
		out.UserinfoEndpoint = &d.UserinfoEndpoint
	}
	if len(d.ScopesSupported) > 0 {
		out.ScopesSupported = &d.ScopesSupported
	}
	if len(d.CodeChallengeMethodsSupported) > 0 {
		out.CodeChallengeMethodsSupported = &d.CodeChallengeMethodsSupported
	}
	return TestAuthProvider200JSONResponse(out), nil
}
