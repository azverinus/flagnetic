package identity

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net/url"
	"slices"
	"strings"
	"time"
)

// ClockSkew is how far the times of a token or an assertion may be off the
// instance's clock, for both protocols.
const ClockSkew = 2 * time.Minute

// The name identifier formats a SAML provider may ask the identity provider
// for.
const (
	NameIDUnspecified  = "unspecified"
	NameIDEmailAddress = "emailAddress"
	NameIDPersistent   = "persistent"
	NameIDTransient    = "transient"
)

var nameIDFormats = []string{NameIDUnspecified, NameIDEmailAddress, NameIDPersistent, NameIDTransient}

// maxMetadataBytes bounds an identity provider's metadata, fetched or pasted.
const maxMetadataBytes = 1 << 20

// IDPMetadata is what an identity provider's metadata says that the instance
// uses: who it is, where a browser is sent, and the certificates its
// signatures are checked against.
type IDPMetadata struct {
	EntityID string
	// SSOURL is the identity provider's HTTP-Redirect single sign-on endpoint.
	SSOURL string
	// Certificates are the signing certificates, PEM encoded.
	Certificates []string
}

// CertificateInfo describes one of an identity provider's certificates.
type CertificateInfo struct {
	// SHA256 is the lowercase hex of the SHA-256 of the DER encoding.
	SHA256   string
	NotAfter time.Time
}

// CertificateInfos describes the certificates of m, in order; one that cannot
// be read is left out.
func (m IDPMetadata) CertificateInfos() []CertificateInfo {
	out := make([]CertificateInfo, 0, len(m.Certificates))
	for _, p := range m.Certificates {
		block, _ := pem.Decode([]byte(p))
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(block.Bytes)
		out = append(out, CertificateInfo{SHA256: hex.EncodeToString(sum[:]), NotAfter: cert.NotAfter.UTC()})
	}
	return out
}

// MetadataError is why an identity provider's metadata cannot be used.
type MetadataError struct {
	// Code is a violation code: invalid_metadata, no_redirect_binding or
	// no_signing_certificate.
	Code   string
	Detail string
}

func (e *MetadataError) Error() string { return e.Detail }

// SAML is what an auth provider of kind SAML is configured with.
type SAML struct {
	// MetadataURL is where the identity provider's metadata is fetched from,
	// MetadataXML the document pasted in its place: exactly one is set.
	MetadataURL string
	MetadataXML string
	// IdP is what the metadata said when it was last read, at RefreshedAt.
	IDP         IDPMetadata
	RefreshedAt time.Time
	// SignRequests is whether AuthnRequests are signed with the instance's
	// key.
	SignRequests bool
	// NameIDFormat is one of the NameID constants.
	NameIDFormat string
	// Claims name the assertion attributes the instance reads of a person.
	Claims Claims
	// SPCertificate is the instance's public certificate for this provider,
	// PEM encoded, and SealedSPKey its private key, sealed under the
	// instance's secret key.
	SPCertificate string
	SealedSPKey   string
	// NextSPCertificate and NextSealedSPKey are the key pair a pending rotation
	// will make current: both empty when none is pending.
	NextSPCertificate string
	NextSealedSPKey   string
}

// SAMLInput is the SAML part of a provider's creation.
type SAMLInput struct {
	// MetadataURL or MetadataXML, exactly one.
	MetadataURL  string
	MetadataXML  string
	SignRequests bool
	// NameIDFormat and the claim names left empty take their defaults.
	NameIDFormat string
	Claims       Claims
}

// SPKey is the key pair the instance presents to an identity provider.
type SPKey struct {
	// CertificatePEM is the self-signed certificate.
	CertificatePEM string
	// SealedKey is the private key, sealed.
	SealedKey string
}

// NewSAML returns the provider in names, of kind SAML, as its first version
// made at now. in must have passed CheckProviderInput.
func NewSAML(providerID ProviderID, in ProviderInput, idp IDPMetadata, sp SPKey, now time.Time) Provider {
	p := New(providerID, in, "", now)
	p.OIDC = nil
	p.SAML = &SAML{
		MetadataURL: in.SAML.MetadataURL, MetadataXML: in.SAML.MetadataXML, IDP: idp, RefreshedAt: now,
		SignRequests: in.SAML.SignRequests, NameIDFormat: in.SAML.NameIDFormat, Claims: in.SAML.Claims,
		SPCertificate: sp.CertificatePEM, SealedSPKey: sp.SealedKey,
	}
	return p
}

// WithIDPMetadata returns p with what its identity provider's metadata says
// replaced by idp, read at now.
func (p Provider) WithIDPMetadata(idp IDPMetadata, now time.Time) Provider {
	p = p.clone()
	p.SAML.IDP, p.SAML.RefreshedAt = idp, now
	return p
}

// IsSecureIDPURL reports whether u is where an identity provider may be
// talked to: https, or http on a loopback address.
func IsSecureIDPURL(u *url.URL) bool {
	return u.Scheme == "https" || (u.Scheme == "http" && IsLoopbackHost(u.Hostname()))
}

// MetadataURL is where the instance serves its SAML metadata for the
// provider with slug; it is also the instance's entity ID for it.
func MetadataURL(baseURL *url.URL, slug string) string {
	return strings.TrimSuffix(baseURL.String(), "/") + "/sso/" + slug + "/metadata"
}

// ACSURL is where the identity provider posts the responses of a sign-in
// through the provider with slug.
func ACSURL(baseURL *url.URL, slug string) string {
	return strings.TrimSuffix(baseURL.String(), "/") + "/sso/" + slug + "/acs"
}

// The names of the fields that belong to one kind of provider, as the API
// calls them.
var (
	oidcFields = []string{"issuer", "client_id", "client_secret", "scopes", "require_verified_email"}
	samlFields = []string{"idp_metadata_url", "idp_metadata_xml", "sign_requests", "name_id_format"}
)

// ForeignFields refuses the fields of given that do not belong to a provider
// of kind: a field of another kind is not ignored, it is an error.
func ForeignFields(kind Kind, given []string) error {
	foreign := samlFields
	if kind == KindSAML {
		foreign = oidcFields
	}
	var v violations
	for _, f := range given {
		if slices.Contains(foreign, f) {
			v.add("/"+f, "not_for_kind", "this field belongs to a provider of another kind than "+string(kind), nil)
		}
	}
	return v.err()
}

// checkMetadataSource returns the URL and the document trimmed, noting each
// way the source is not one: neither or both given, a document over a
// megabyte, a URL that is not an http or https address.
func checkMetadataSource(v *violations, metadataURL, metadataXML string) (string, string) {
	metadataURL, metadataXML = strings.TrimSpace(metadataURL), strings.TrimSpace(metadataXML)
	switch {
	case metadataURL == "" && metadataXML == "":
		v.add("/idp_metadata_url", "metadata_required", "give the identity provider's metadata, as a URL in idp_metadata_url or a document in idp_metadata_xml", nil)
	case metadataURL != "" && metadataXML != "":
		v.add("/idp_metadata_xml", "metadata_conflict", "give the metadata as a URL or as a document, not both", nil)
	case metadataXML != "" && len(metadataXML) > maxMetadataBytes:
		v.add("/idp_metadata_xml", "metadata_too_large", "the metadata is at most 1 MiB", map[string]any{"max": maxMetadataBytes})
	case metadataURL != "":
		u, err := url.Parse(metadataURL)
		if err != nil || !u.IsAbs() || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
			(u.Scheme != "https" && u.Scheme != "http") {
			v.add("/idp_metadata_url", "invalid_metadata_url", "the metadata URL is an http or https address without credentials or a fragment", nil)
		}
	}
	return metadataURL, metadataXML
}

// checkMetadataScheme refuses a metadata URL that is http where the address is
// not a loopback one: certificates fetched over it could be swapped in
// transit.
func checkMetadataScheme(metadataURL string) error {
	u, err := url.Parse(metadataURL)
	if err != nil || u.Scheme != "http" || IsLoopbackHost(u.Hostname()) {
		return nil
	}
	return insecureURL("/idp_metadata_url", "the metadata URL must be https unless it is on a loopback address")
}

func checkNameIDFormat(v *violations, format string) string {
	format = strings.TrimSpace(format)
	if format == "" {
		return NameIDUnspecified
	}
	if !slices.Contains(nameIDFormats, format) {
		v.add("/name_id_format", "unknown_name_id_format", "the name identifier format is one of the allowed", map[string]any{"allowed": nameIDFormats})
	}
	return format
}
