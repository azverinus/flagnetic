package identity

import (
	"context"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/identity"

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

// ProviderTested is what testing an auth provider found: the discovery
// document of an OIDC provider, the metadata of a SAML one.
type ProviderTested struct {
	OIDC *OIDCDiscovery
	SAML *identity.IdPMetadata
}

// TestProvider checks an auth provider can be reached.
type TestProvider struct {
	store      ProviderStore
	discoverer OIDCDiscoverer
	saml       SAMLMetadataReader
}

// NewTestProvider returns the query.
func NewTestProvider(store ProviderStore, discoverer OIDCDiscoverer, saml SAMLMetadataReader) *TestProvider {
	return &TestProvider{store: store, discoverer: discoverer, saml: saml}
}

// Handle fetches the discovery document of the OIDC provider with slug, or
// fetches again the metadata of the SAML provider, or reads the metadata
// pasted, enabled or not, and answers what it found, or refuses
// provider_unreachable with why. Nothing is stored.
func (h *TestProvider) Handle(ctx context.Context, slug string) (ProviderTested, error) {
	p, err := providerOf(ctx, h.store, slug)
	if err != nil {
		return ProviderTested{}, err
	}
	if p.SAML != nil {
		meta, samlErr := h.readMetadata(ctx, p.SAML)
		if samlErr != nil {
			return ProviderTested{}, apperrors.BadGateway("provider_unreachable",
				"the identity provider's metadata could not be fetched or cannot be used").
				WithCause(samlErr).WithExtension("failure", clip(samlErr.Error(), maxFailureBytes))
		}
		return ProviderTested{SAML: &meta}, nil
	}
	found, err := h.discoverer.Discover(ctx, p.OIDC.Issuer)
	if err != nil {
		return ProviderTested{}, apperrors.BadGateway("provider_unreachable",
			"the identity provider's discovery document could not be fetched or is not the issuer's").
			WithCause(err).WithExtension("failure", clip(err.Error(), maxFailureBytes))
	}
	return ProviderTested{OIDC: &found}, nil
}

func (h *TestProvider) readMetadata(ctx context.Context, s *identity.SAML) (identity.IdPMetadata, error) {
	raw := []byte(s.MetadataXML)
	if s.MetadataURL != "" {
		var err error
		if raw, err = h.saml.Fetch(ctx, s.MetadataURL); err != nil {
			return identity.IdPMetadata{}, err
		}
	}
	return h.saml.Parse(raw)
}

// maxFailureBytes bounds the failure answered for an unreachable provider. The
// discovery error can carry the whole body the address answered, up to a
// megabyte; a few hundred bytes say why, and the instance admin is not handed
// whatever an arbitrary address on the network returns.
const maxFailureBytes = 512

// clip cuts s to n bytes without splitting a character and marks the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}
