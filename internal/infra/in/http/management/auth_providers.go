package management

import (
	"context"
	"slices"

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
	return identity.RedirectURI(o.BaseURL, p.Slug)
}

// samlURLs returns the addresses a SAML provider is registered with at its
// identity provider, empty while the instance has no public URL.
func (o Options) samlURLs(p identity.Provider) (acs, entity string) {
	if o.BaseURL == nil {
		return "", ""
	}
	return identity.ACSURL(o.BaseURL, p.Slug), identity.MetadataURL(o.BaseURL, p.Slug)
}

func idpCertificatesOut(m identity.IDPMetadata) []IdPCertificate {
	infos := m.CertificateInfos()
	out := make([]IdPCertificate, len(infos))
	for i, c := range infos {
		out[i] = IdPCertificate{Sha256: c.SHA256, NotAfter: c.NotAfter}
	}
	return out
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
	if c := p.SAML; c != nil {
		out.IdpEntityId, out.IdpSsoUrl = &c.IDP.EntityID, &c.IDP.SSOURL
		out.IdpCertificates, out.IdpMetadataRefreshedAt = new(idpCertificatesOut(c.IDP)), &c.RefreshedAt
		out.SignRequests, out.NameIdFormat = &c.SignRequests, new(SAMLNameIDFormat(c.NameIDFormat))
		out.Claims = &AuthProviderClaims{Email: c.Claims.Email, Name: c.Claims.Name, Groups: c.Claims.Groups}
		if c.MetadataURL != "" {
			out.IdpMetadataUrl = &c.MetadataURL
		}
		if acs, entity := a.opts.samlURLs(p); acs != "" {
			out.AcsUrl, out.EntityId, out.MetadataUrl = &acs, &entity, &entity
		}
		return out
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
	if err := identity.ForeignFields(identity.Kind(b.Kind), createdFields(b)); err != nil {
		return nil, err
	}
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
				Issuer: valueOf(b.Issuer), ClientID: valueOf(b.ClientId), ClientSecret: secret.New(valueOf(b.ClientSecret)),
				Scopes:               valueOr(b.Scopes, nil),
				Claims:               identity.Claims{Email: valueOf(claims.Email), Name: valueOf(claims.Name), Groups: valueOf(claims.Groups)},
				RequireVerifiedEmail: valueOr(b.RequireVerifiedEmail, true),
			},
			SAML: identity.SAMLInput{
				MetadataURL: valueOf(b.IdpMetadataUrl), MetadataXML: valueOf(b.IdpMetadataXml), SignRequests: valueOr(b.SignRequests, true),
				NameIDFormat: string(valueOr(b.NameIdFormat, "")),
				Claims:       identity.Claims{Email: valueOf(claims.Email), Name: valueOf(claims.Name), Groups: valueOf(claims.Groups)},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return CreateAuthProvider201JSONResponse{Body: a.authProviderOut(p), Headers: CreateAuthProvider201ResponseHeaders{ETag: etag(p.Version)}}, nil
}

// createdFields names the properties of a provider's creation that belong to
// one kind of provider only, which the body sets.
func createdFields(b AuthProviderCreate) []string {
	var out []string
	for name, set := range map[string]bool{
		"issuer": b.Issuer != nil, "client_id": b.ClientId != nil, "client_secret": b.ClientSecret != nil, "scopes": b.Scopes != nil,
		"require_verified_email": b.RequireVerifiedEmail != nil, "idp_metadata_url": b.IdpMetadataUrl != nil,
		"idp_metadata_xml": b.IdpMetadataXml != nil, "sign_requests": b.SignRequests != nil, "name_id_format": b.NameIdFormat != nil,
	} {
		if set {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
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
		IDPMetadataURL: b.IdpMetadataUrl, IDPMetadataXML: b.IdpMetadataXml, SignRequests: b.SignRequests,
	}
	if b.NameIdFormat != nil {
		change.NameIDFormat = new(string(*b.NameIdFormat))
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

// TestAuthProvider answers what an auth provider's discovery document or
// metadata says.
func (a *api) TestAuthProvider(ctx context.Context, req TestAuthProviderRequestObject) (TestAuthProviderResponseObject, error) {
	tested, err := a.h.TestAuthProvider.Handle(ctx, req.Provider)
	if err != nil {
		return nil, err
	}
	if m := tested.SAML; m != nil {
		return TestAuthProvider200JSONResponse{IdpEntityId: &m.EntityID, IdpSsoUrl: &m.SSOURL, IdpCertificates: new(idpCertificatesOut(*m))}, nil
	}
	d := tested.OIDC
	out := AuthProviderTest{
		Issuer: &d.Issuer, AuthorizationEndpoint: &d.AuthorizationEndpoint, TokenEndpoint: &d.TokenEndpoint, JwksUri: &d.JWKSURI,
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
