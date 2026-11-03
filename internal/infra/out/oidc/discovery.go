// Package oidc is the outbound adapter to OpenID Connect providers, built on
// go-oidc.
package oidc

import (
	"context"
	"fmt"
	"net/http"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/infra/out/idphttp"
)

// Discoverer reads OIDC providers' discovery documents.
type Discoverer struct{ client *http.Client }

var _ appidentity.OIDCDiscoverer = (*Discoverer)(nil)

// NewDiscoverer returns a discoverer that fetches as idphttp's client does.
func NewDiscoverer() *Discoverer { return &Discoverer{client: idphttp.NewClient()} }

// Discover fetches the discovery document of issuer and checks it names
// issuer itself.
func (d *Discoverer) Discover(ctx context.Context, issuer string) (appidentity.OIDCDiscovery, error) {
	p, err := gooidc.NewProvider(gooidc.ClientContext(ctx, d.client), issuer)
	if err != nil {
		return appidentity.OIDCDiscovery{}, err
	}
	var doc struct {
		JWKSURI                       string   `json:"jwks_uri"`
		ScopesSupported               []string `json:"scopes_supported"`
		CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	}
	if err = p.Claims(&doc); err != nil {
		return appidentity.OIDCDiscovery{}, fmt.Errorf("reading the discovery document: %w", err)
	}
	endpoint := p.Endpoint()
	return appidentity.OIDCDiscovery{
		Issuer: issuer, AuthorizationEndpoint: endpoint.AuthURL, TokenEndpoint: endpoint.TokenURL, JWKSURI: doc.JWKSURI,
		UserinfoEndpoint: p.UserInfoEndpoint(), ScopesSupported: doc.ScopesSupported,
		CodeChallengeMethodsSupported: doc.CodeChallengeMethodsSupported,
	}, nil
}
