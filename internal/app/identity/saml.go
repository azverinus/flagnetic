package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// ErrIDPRefused is what verifying a SAML response fails with when the identity
// provider answered that the sign-in failed, rather than with an assertion.
var ErrIDPRefused = errors.New("the identity provider refused the sign-in")

// SAMLConfig is what the instance presents to a SAML identity provider as a
// service provider, and what it trusts of it.
type SAMLConfig struct {
	// EntityID is the instance's entity ID for the provider: the issuer of its
	// requests and the audience its responses must name.
	EntityID string
	// ACSURL is where the identity provider posts its response.
	ACSURL string
	// IDPEntityID is the identity provider's, IDPSSOURL its HTTP-Redirect
	// single sign-on endpoint and IDPCertificates, PEM encoded, the
	// certificates its signatures are checked against.
	IDPEntityID     string
	IDPSSOURL       string
	IDPCertificates []string
	SignRequests    bool
	NameIDFormat    string
	Claims          identity.Claims
	// SPCertificate is the instance's certificate for the provider, SPKey its
	// private key, both PEM encoded.
	SPCertificate string
	SPKey         secret.String
	// Now is the time responses are judged at.
	Now time.Time
}

// SAMLAssertion is what a verified SAML response asserts of a person.
type SAMLAssertion struct {
	// ID identifies the assertion; one is accepted once.
	ID string
	// ValidUntil is the last moment the assertion could be accepted, the
	// clock skew included; it is remembered until then.
	ValidUntil time.Time
	// Claims are read from the verified assertion only. Their email is not yet
	// verified: the provider's domains decide that.
	Claims identity.VerifiedClaims
}

// SAMLClient is the instance as a SAML service provider.
type SAMLClient interface {
	// AuthnRequestURL returns where a browser is sent to authenticate: the
	// identity provider's single sign-on endpoint with an AuthnRequest with
	// this ID, signed when cfg says so, and this RelayState.
	AuthnRequestURL(cfg SAMLConfig, requestID, relayState string) (string, error)
	// VerifyResponse checks a base64 encoded SAML response posted to the
	// ACS: that it was signed by one of the identity provider's certificates,
	// is from its entity, is addressed to the ACS and the audience, answers
	// the request with requestID, and is within its validity. It decrypts an
	// encrypted assertion with the instance's key. ErrIDPRefused when the
	// identity provider answered that the sign-in failed.
	VerifyResponse(cfg SAMLConfig, response, requestID string) (SAMLAssertion, error)
	// Metadata returns the instance's SAML metadata for the provider, to import
	// into the identity provider.
	Metadata(cfg SAMLConfig) ([]byte, error)
}

// SAMLMetadataReader fetches and reads an identity provider's metadata.
type SAMLMetadataReader interface {
	// Fetch returns the document at rawURL, at most a megabyte.
	Fetch(ctx context.Context, rawURL string) ([]byte, error)
	// Parse reads the identity provider's entity ID, HTTP-Redirect single
	// sign-on endpoint and signing certificates from a metadata document; an
	// *identity.MetadataError says why it cannot be used.
	Parse(raw []byte) (identity.IDPMetadata, error)
}

// SPKeyGenerator makes the key pair the instance presents to an identity
// provider.
type SPKeyGenerator interface {
	// GenerateSPKey returns a new RSA-2048 key and a self-signed certificate
	// over it valid for three years from now, both PEM encoded.
	GenerateSPKey(now time.Time) (certificatePEM string, keyPEM []byte, err error)
}

// ReplayStore remembers the SAML assertions accepted.
type ReplayStore interface {
	// RecordAssertion remembers the assertion until expires and reports
	// whether it was new; those expired before now are forgotten.
	RecordAssertion(ctx context.Context, provider identity.ProviderID, assertionID string, expires, now time.Time) (bool, error)
}

// readIDPMetadata returns the metadata of an identity provider, fetched from
// metadataURL or else read from the pasted metadataXML. A provider that cannot
// be reached is provider_unreachable; metadata that cannot be used is an
// invalid_auth_provider naming the field it came from.
func readIDPMetadata(ctx context.Context, r SAMLMetadataReader, metadataURL, metadataXML string) (identity.IDPMetadata, error) {
	raw, field := []byte(metadataXML), "/idp_metadata_xml"
	if metadataURL != "" {
		var err error
		field = "/idp_metadata_url"
		if raw, err = r.Fetch(ctx, metadataURL); err != nil {
			return identity.IDPMetadata{}, apperrors.BadGateway("provider_unreachable", "the identity provider's metadata could not be fetched").
				WithCause(err).WithExtension("failure", clip(err.Error(), maxFailureBytes))
		}
	}
	meta, err := r.Parse(raw)
	var bad *identity.MetadataError
	switch {
	case errors.As(err, &bad):
		return meta, apperrors.Invalid("invalid_auth_provider", "the identity provider's metadata cannot be used: "+bad.Detail,
			apperrors.Violation{Pointer: field, Code: bad.Code, Detail: bad.Detail})
	case err != nil:
		return meta, err
	}
	return meta, nil
}

// sealSPKey generates the key pair the instance presents for a new SAML
// provider and seals its private key.
func sealSPKey(gen SPKeyGenerator, sealer Sealer, now time.Time) (identity.SPKey, error) {
	cert, key, err := gen.GenerateSPKey(now)
	if err != nil {
		return identity.SPKey{}, fmt.Errorf("generating a key pair: %w", err)
	}
	sealed, err := sealer.Seal(secret.PurposeSAMLSPKey, key)
	if err != nil {
		return identity.SPKey{}, err
	}
	return identity.SPKey{CertificatePEM: cert, SealedKey: sealed}, nil
}

// samlConfig is what p, a SAML provider, is presented and trusted with.
func (d SignInDeps) samlConfig(p identity.Provider, now time.Time) (SAMLConfig, error) {
	if d.BaseURL == nil {
		return SAMLConfig{}, errors.New("the instance has no public URL")
	}
	key, err := d.Opener.Open(secret.PurposeSAMLSPKey, p.SAML.SealedSPKey)
	if err != nil {
		return SAMLConfig{}, fmt.Errorf("opening the key of %s: %w", p.Slug, err)
	}
	s := p.SAML
	return SAMLConfig{
		EntityID: identity.MetadataURL(d.BaseURL, p.Slug), ACSURL: identity.ACSURL(d.BaseURL, p.Slug),
		IDPEntityID: s.IDP.EntityID, IDPSSOURL: s.IDP.SSOURL, IDPCertificates: s.IDP.Certificates,
		SignRequests: s.SignRequests, NameIDFormat: s.NameIDFormat, Claims: s.Claims,
		SPCertificate: s.SPCertificate, SPKey: secret.New(string(key)), Now: now,
	}, nil
}

// startSAML creates the attempt of a SAML sign-in and answers where the
// browser goes: the AuthnRequest, carrying the attempt's state as RelayState.
func (h *StartSignIn) startSAML(ctx context.Context, p identity.Provider, returnTo string) (Started, error) {
	now := h.d.now()
	cfg, err := h.d.samlConfig(p, now)
	if err != nil {
		return Started{}, fail(CodeSSOFailed, err)
	}
	secrets := identity.NewSecrets(h.d.Random)
	// An XML ID starts with a letter or an underscore; a token may start with
	// a digit or a hyphen.
	requestID := "_" + identity.NewToken(h.d.Random)
	attempt := identity.NewSAMLLoginAttempt(id.New[identity.Attempt](h.d.IDs), p.ID, returnTo, requestID, secrets, now)
	redirect, err := h.d.SAML.AuthnRequestURL(cfg, requestID, secrets.State)
	if err != nil {
		return Started{}, fail(CodeSSOFailed, err)
	}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error { return h.d.Attempts.CreateAttempt(ctx, attempt, now) })
	if err != nil {
		return Started{}, err
	}
	return Started{RedirectURL: redirect, Cookie: secret.New(secrets.Cookie), Lifetime: identity.AttemptLifetime}, nil
}

// ServeMetadata answers the instance's SAML metadata for a provider.
type ServeMetadata struct{ d SignInDeps }

// NewServeMetadata returns the query.
func NewServeMetadata(d SignInDeps) *ServeMetadata { return &ServeMetadata{d: d} }

// Handle returns the metadata of the enabled SAML provider with slug; a
// disabled, unknown or OIDC provider is not found.
func (h *ServeMetadata) Handle(ctx context.Context, slug string) ([]byte, error) {
	p, err := h.d.enabledProvider(ctx, slug, identity.KindSAML)
	if err != nil {
		return nil, err
	}
	cfg, err := h.d.samlConfig(p, h.d.now())
	if err != nil {
		return nil, err
	}
	return h.d.SAML.Metadata(cfg)
}

// CompleteACS takes the response of a SAML identity provider.
type CompleteACS struct{ d SignInDeps }

// NewCompleteACS returns the handler.
func NewCompleteACS(d SignInDeps) *CompleteACS { return &CompleteACS{d: d} }

// ACSCommand is what the identity provider posted to the ACS.
type ACSCommand struct {
	Slug       string
	RelayState string
	// Response is the base64 encoded SAML response.
	Response string
}

// Handle verifies the identity provider's response and stores the person's
// claims on the attempt, changing nothing about any member, and returns the
// token the browser completes the sign-in with. The attempt's RelayState names
// it and is taken once: a RelayState seen again, unknown or expired is
// SignInFailure sso_failed, as is a response that does not verify, which ends
// the attempt. So is an assertion accepted before.
func (h *CompleteACS) Handle(ctx context.Context, cmd ACSCommand) (token string, err error) {
	p, err := h.d.enabledProvider(ctx, cmd.Slug, identity.KindSAML)
	if err != nil {
		return "", err
	}
	defer func() { err = inKind(p.Kind, err) }()
	now := h.d.now()
	attempt, err := h.d.Attempts.TakeCallback(ctx, identity.HashSecret(cmd.RelayState), now)
	if errors.Is(err, ErrNotFound) {
		return "", fail(CodeSSOFailed, errors.New("the relay state names no live attempt"))
	}
	if err != nil {
		return "", err
	}
	if attempt.Provider != p.ID {
		return "", h.end(ctx, attempt, fail(CodeSSOFailed, errors.New("the relay state belongs to another provider")))
	}
	cfg, err := h.d.samlConfig(p, now)
	if err != nil {
		return "", h.end(ctx, attempt, fail(CodeSSOFailed, err))
	}
	assertion, err := h.d.SAML.VerifyResponse(cfg, cmd.Response, attempt.RequestID)
	if errors.Is(err, ErrIDPRefused) {
		return "", h.end(ctx, attempt, fail(CodeIDPError, err))
	}
	if err != nil {
		return "", h.end(ctx, attempt, fail(CodeSSOFailed, err))
	}
	fresh, err := h.d.Replays.RecordAssertion(ctx, p.ID, assertion.ID, assertion.ValidUntil, now)
	if err != nil {
		return "", err
	}
	if !fresh {
		return "", h.end(ctx, attempt, fail(CodeSSOFailed, errors.New("the assertion was accepted before")))
	}
	claims := assertion.Claims
	// A SAML provider has no verification of its own to read: it vouches for
	// the emails in its domains, and for no other.
	claims.EmailVerified = p.VouchesForEmail(claims.Email)
	token = identity.NewToken(h.d.Random)
	if err = h.d.Attempts.StoreClaims(ctx, attempt.ID, identity.HashSecret(token), claims); err != nil {
		return "", err
	}
	return token, nil
}

// end deletes an attempt that cannot go on, and returns why.
func (h *CompleteACS) end(ctx context.Context, a identity.Attempt, why error) error {
	return errors.Join(why, h.d.Attempts.DeleteAttempt(context.WithoutCancel(ctx), a.ID))
}
