package saml

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/russellhaering/gosaml2/types"
	dsigtypes "github.com/russellhaering/goxmldsig/types"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

const (
	bindingRedirect = "urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect"
	// spCertificateYears is how long the certificate the instance presents to
	// an identity provider is valid.
	spCertificateYears = 3
)

// Parse reads an identity provider's entity ID, HTTP-Redirect single sign-on
// endpoint and signing certificates from its metadata document.
func (c *Client) Parse(raw []byte) (identity.IDPMetadata, error) {
	var none identity.IDPMetadata
	if err := refuseDangerousXML(raw); err != nil {
		return none, &identity.MetadataError{Code: "invalid_metadata", Detail: err.Error()}
	}
	var doc types.EntityDescriptor
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return none, &identity.MetadataError{Code: "invalid_metadata", Detail: "the metadata is not an EntityDescriptor document"}
	}
	idp := doc.IDPSSODescriptor
	if doc.EntityID == "" || idp == nil {
		return none, &identity.MetadataError{Code: "invalid_metadata", Detail: "the metadata describes no identity provider with an entity ID"}
	}
	meta := identity.IDPMetadata{EntityID: strings.TrimSpace(doc.EntityID)}
	for _, s := range idp.SingleSignOnServices {
		if s.Binding != bindingRedirect {
			continue
		}
		meta.SSOURL = strings.TrimSpace(s.Location)
		break
	}
	if meta.SSOURL == "" {
		return none, &identity.MetadataError{Code: "no_redirect_binding", Detail: "the identity provider offers no single sign-on service with the HTTP-Redirect binding"}
	}
	if u, err := url.Parse(meta.SSOURL); err != nil || u.Host == "" || !identity.IsSecureIDPURL(u) {
		return none, &identity.MetadataError{Code: "invalid_metadata", Detail: "the single sign-on address is not https"}
	}
	for _, kd := range idp.KeyDescriptors {
		if kd.Use == "encryption" {
			continue
		}
		for _, x := range kd.KeyInfo.X509Data.X509Certificates {
			der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(x.Data), ""))
			if err != nil {
				return none, &identity.MetadataError{Code: "invalid_metadata", Detail: "a certificate in the metadata is not base64"}
			}
			if _, err = x509.ParseCertificate(der); err != nil {
				return none, &identity.MetadataError{Code: "invalid_metadata", Detail: "a certificate in the metadata cannot be read"}
			}
			if p := pemOf(der); !slices.Contains(meta.Certificates, p) {
				meta.Certificates = append(meta.Certificates, p)
			}
		}
	}
	if len(meta.Certificates) == 0 {
		return none, &identity.MetadataError{Code: "no_signing_certificate", Detail: "the metadata holds no signing certificate"}
	}
	return meta, nil
}

// marshalMetadata writes the instance's metadata document.
func marshalMetadata(d *types.EntityDescriptor) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	enc := xml.NewEncoder(&b)
	enc.Indent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, fmt.Errorf("writing the metadata: %w", err)
	}
	return b.Bytes(), nil
}

// GenerateSPKey returns a new RSA-2048 key and a certificate signed by it for
// three years from now, PEM encoded.
func (c *Client) GenerateSPKey(now time.Time) (string, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return "", nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Flagnetic SAML service provider"},
		// An identity provider whose clock is behind ours must still accept it.
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(spCertificateYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", nil, err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", nil, err
	}
	return pemOf(der), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), nil
}

// refuseDangerousXML refuses a document with a DOCTYPE, which could declare
// entities, and one that is not well formed.
func refuseDangerousXML(raw []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the document is not well formed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return errors.New("the document has a DOCTYPE, which is refused")
		case xml.ProcInst:
			if t.Target != "xml" {
				return errors.New("the document has a processing instruction, which is refused")
			}
		}
	}
}

// listNextCertificate adds to the instance's metadata the certificate of the
// next key pair, for the uses the current one has: an identity provider that
// imports the metadata then verifies requests signed with, and encrypts to,
// either.
func listNextCertificate(d *types.EntityDescriptor, certificatePEM string) error {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil {
		return errors.New("the instance's next certificate is not PEM")
	}
	data := base64.StdEncoding.EncodeToString(block.Bytes)
	sp := d.SPSSODescriptor
	for _, kd := range slices.Clone(sp.KeyDescriptors) {
		kd.KeyInfo.X509Data.X509Certificates = []dsigtypes.X509Certificate{{Data: data}}
		sp.KeyDescriptors = append(sp.KeyDescriptors, kd)
	}
	return nil
}
