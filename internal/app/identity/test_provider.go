package identity

import (
	"context"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// OIDCDiscovery is what an OIDC provider's discovery document says about
// where and how to talk to it.
type OIDCDiscovery struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	JWKSURI               string
	// UserinfoEndpoint is empty when the provider offers none.
	UserinfoEndpoint string
	// ScopesSupported and CodeChallengeMethodsSupported are empty when the
	// document does not list them.
	ScopesSupported               []string
	CodeChallengeMethodsSupported []string
}

// OIDCDiscoverer fetches and reads an OIDC provider's discovery document.
type OIDCDiscoverer interface {
	// Discover fetches the document of issuer and checks it names issuer
	// itself. It answers an error for a provider that cannot be reached, that
	// answers anything but a document, or whose document is another's.
	Discover(ctx context.Context, issuer string) (OIDCDiscovery, error)
}

// TestProvider checks an auth provider can be reached.
type TestProvider struct {
	store      ProviderStore
	discoverer OIDCDiscoverer
}

// NewTestProvider returns the query.
func NewTestProvider(store ProviderStore, discoverer OIDCDiscoverer) *TestProvider {
	return &TestProvider{store: store, discoverer: discoverer}
}

// Handle fetches the discovery document of the provider with slug, enabled or
// not, and answers what it found, or refuses provider_unreachable with why.
// Nothing is stored.
func (h *TestProvider) Handle(ctx context.Context, slug string) (OIDCDiscovery, error) {
	p, err := providerOf(ctx, h.store, slug)
	if err != nil {
		return OIDCDiscovery{}, err
	}
	found, err := h.discoverer.Discover(ctx, p.OIDC.Issuer)
	if err != nil {
		return OIDCDiscovery{}, apperrors.BadGateway("provider_unreachable",
			"the identity provider's discovery document could not be fetched or is not the issuer's").
			WithCause(err).WithExtension("failure", err.Error())
	}
	return found, nil
}
