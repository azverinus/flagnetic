// Package saml is the outbound adapter to SAML identity providers: the
// instance as a service provider, built on gosaml2 and goxmldsig.
package saml

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	saml2 "github.com/russellhaering/gosaml2"
	dsig "github.com/russellhaering/goxmldsig"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/infra/out/idphttp"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// Client is the instance as a SAML service provider, and reads identity
// providers' metadata.
type Client struct{ http *http.Client }

var (
	_ appidentity.SAMLClient         = (*Client)(nil)
	_ appidentity.SAMLMetadataReader = (*Client)(nil)
	_ appidentity.SPKeyGenerator     = (*Client)(nil)
)

// NewClient returns a client that fetches metadata as idphttp's client does.
func NewClient() *Client { return &Client{http: idphttp.NewClient()} }

// Fetch returns the document at rawURL, which must be https unless it is on
// a loopback address.
func (c *Client) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !identity.IsSecureIDPURL(u) {
		return nil, errors.New("the metadata URL is not https")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/samlmetadata+xml, application/xml;q=0.9, text/xml;q=0.8")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the metadata URL answered %s", res.Status)
	}
	return io.ReadAll(res.Body)
}

// The name identifier formats, as the URNs a request asks for.
var nameIDFormatURNs = map[string]string{
	identity.NameIDUnspecified:  "urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified",
	identity.NameIDEmailAddress: "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
	identity.NameIDPersistent:   "urn:oasis:names:tc:SAML:2.0:nameid-format:persistent",
	identity.NameIDTransient:    "urn:oasis:names:tc:SAML:2.0:nameid-format:transient",
}

// serviceProvider returns the gosaml2 service provider cfg describes with the
// instance's current key pair, judging the validity of certificates and subject
// confirmations at cfg.Now.
func serviceProvider(cfg appidentity.SAMLConfig) (*saml2.SAMLServiceProvider, error) {
	return serviceProviderWith(cfg, cfg.SPCertificate, cfg.SPKey)
}

// serviceProviders returns a service provider for each key pair a response may
// have been encrypted to: the current one, then the next while a rotation is
// pending.
func serviceProviders(cfg appidentity.SAMLConfig) ([]*saml2.SAMLServiceProvider, error) {
	current, err := serviceProvider(cfg)
	if err != nil {
		return nil, err
	}
	out := []*saml2.SAMLServiceProvider{current}
	if cfg.NextSPCertificate != "" {
		next, err := serviceProviderWith(cfg, cfg.NextSPCertificate, cfg.NextSPKey)
		if err != nil {
			return nil, fmt.Errorf("the next key pair: %w", err)
		}
		out = append(out, next)
	}
	return out, nil
}

// serviceProviderWith is serviceProvider with the key pair certificatePEM and key.
func serviceProviderWith(cfg appidentity.SAMLConfig, certificatePEM string, spKey secret.String) (*saml2.SAMLServiceProvider, error) {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil {
		return nil, errors.New("the instance's certificate is not PEM")
	}
	keyBlock, _ := pem.Decode([]byte(spKey.Reveal()))
	if keyBlock == nil {
		return nil, errors.New("the instance's key is not PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading the instance's key: %w", err)
	}
	if _, ok := key.(*rsa.PrivateKey); !ok {
		return nil, errors.New("the instance's key is not an RSA key")
	}
	var roots []*x509.Certificate
	for _, p := range cfg.IDPCertificates {
		b, _ := pem.Decode([]byte(p))
		if b == nil {
			continue
		}
		cert, certErr := x509.ParseCertificate(b.Bytes)
		if certErr != nil {
			return nil, fmt.Errorf("reading an identity provider certificate: %w", certErr)
		}
		roots = append(roots, cert)
	}
	if len(roots) == 0 {
		return nil, errors.New("the provider has no identity provider certificate")
	}
	return &saml2.SAMLServiceProvider{
		IdentityProviderSSOURL:      cfg.IDPSSOURL,
		IdentityProviderIssuer:      cfg.IDPEntityID,
		ServiceProviderIssuer:       cfg.EntityID,
		AudienceURI:                 cfg.EntityID,
		AssertionConsumerServiceURL: cfg.ACSURL,
		SignAuthnRequests:           cfg.SignRequests,
		SignAuthnRequestsAlgorithm:  dsig.RSASHA256SignatureMethod,
		NameIdFormat:                nameIDFormatURNs[cfg.NameIDFormat],
		IDPCertificateStore:         &dsig.MemoryX509CertificateStore{Roots: roots},
		SPKeyStore:                  dsig.TLSCertKeyStore(tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: key}),
		Clock:                       dsig.NewFakeClockAt(cfg.Now),
		// A response is a few kilobytes; a megabyte leaves room for the groups
		// of a person in a large directory.
		MaximumDecompressedBodySize: 1 << 20,
	}, nil
}

// AuthnRequestURL returns the identity provider's single sign-on endpoint with
// an AuthnRequest asking to authenticate the browser, with the ID requestID,
// by the HTTP-Redirect binding: signed in the query when cfg says so.
func (c *Client) AuthnRequestURL(cfg appidentity.SAMLConfig, requestID, relayState string) (string, error) {
	sp, err := serviceProvider(cfg)
	if err != nil {
		return "", err
	}
	doc, err := sp.BuildAuthRequestDocumentNoSig()
	if err != nil {
		return "", fmt.Errorf("building the AuthnRequest: %w", err)
	}
	// By the Redirect binding the request itself is not signed, its query is,
	// so naming the request is no change a signature covers.
	root := doc.Root()
	root.RemoveAttr("ID")
	root.CreateAttr("ID", requestID)
	return sp.BuildAuthURLRedirect(relayState, doc)
}

// Metadata returns the instance's SAML metadata for the provider: the current
// certificate, and the next one too while a rotation is pending, so the
// identity provider can import it before it takes the current one's place.
func (c *Client) Metadata(cfg appidentity.SAMLConfig) ([]byte, error) {
	sp, err := serviceProvider(cfg)
	if err != nil {
		return nil, err
	}
	descriptor, err := sp.Metadata()
	if err != nil {
		return nil, fmt.Errorf("building the metadata: %w", err)
	}
	if format := nameIDFormatURNs[cfg.NameIDFormat]; format != "" {
		descriptor.SPSSODescriptor.NameIDFormats = []string{format}
	}
	if cfg.NextSPCertificate != "" {
		if err = listNextCertificate(descriptor, cfg.NextSPCertificate); err != nil {
			return nil, err
		}
	}
	return marshalMetadata(descriptor)
}

// pemOf returns the PEM encoding of the certificate der.
func pemOf(der []byte) string {
	return strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
}
