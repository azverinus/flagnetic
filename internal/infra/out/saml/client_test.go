package saml_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/infra/out/saml"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

var now = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

// config returns a working service provider configuration, changed by change.
func config(t *testing.T, change func(*appidentity.SAMLConfig)) appidentity.SAMLConfig {
	t.Helper()
	c := saml.NewClient()
	spCert, spKey, err := c.GenerateSPKey(now)
	if err != nil {
		t.Fatal(err)
	}
	idpCert, _, err := c.GenerateSPKey(now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := appidentity.SAMLConfig{
		EntityID: "https://flags.example.com/sso/corp/metadata", ACSURL: "https://flags.example.com/sso/corp/acs",
		IDPEntityID: "https://idp.example.com", IDPSSOURL: "https://idp.example.com/sso", IDPCertificates: []string{idpCert},
		SignRequests: true, NameIDFormat: identity.NameIDEmailAddress, Claims: identity.Claims{Email: "email", Name: "name", Groups: "groups"},
		SPCertificate: spCert, SPKey: secret.New(string(spKey)), Now: now,
	}
	if change != nil {
		change(&cfg)
	}
	return cfg
}

func TestAConfigurationTheClientCannotUseIsRefusedEverywhereItIsNeeded(t *testing.T) {
	c := saml.NewClient()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*appidentity.SAMLConfig){
		"a certificate that is not PEM": func(cfg *appidentity.SAMLConfig) { cfg.SPCertificate = "not pem" },
		"a key that is not PEM":         func(cfg *appidentity.SAMLConfig) { cfg.SPKey = secret.New("not pem") },
		"a key that cannot be read": func(cfg *appidentity.SAMLConfig) {
			cfg.SPKey = secret.New(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("garbage")})))
		},
		"a key that is not RSA": func(cfg *appidentity.SAMLConfig) {
			cfg.SPKey = secret.New(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
		},
		"an identity provider certificate that cannot be read": func(cfg *appidentity.SAMLConfig) {
			cfg.IDPCertificates = []string{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")}))}
		},
		"no identity provider certificate": func(cfg *appidentity.SAMLConfig) { cfg.IDPCertificates = []string{"not pem"} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config(t, change)
			if _, err := c.AuthnRequestURL(cfg, "_request", "state"); err == nil {
				t.Error("a request was built")
			}
			if _, err := c.Metadata(cfg); err == nil {
				t.Error("metadata was built")
			}
			if _, err := c.VerifyResponse(cfg, "PHJlc3BvbnNlLz4=", "_request"); err == nil {
				t.Error("a response was verified")
			}
		})
	}
}

func TestAResponseThatIsNotBase64OrNotXMLIsRefused(t *testing.T) {
	c := saml.NewClient()
	cfg := config(t, nil)
	for name, response := range map[string]string{
		"not base64": "***", "empty": "", "not XML": "bm90IHhtbA==", "a processing instruction": "PD94bWwtc3R5bGVzaGVldCBocmVmPSJ4Ij8+PGEvPg==",
	} {
		if _, err := c.VerifyResponse(cfg, response, "_request"); err == nil {
			t.Errorf("%s: a response was verified", name)
		}
	}
}

func TestMetadataIsFetchedOnlyFromAnHTTPSAddressOrALoopbackOne(t *testing.T) {
	c := saml.NewClient()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("<EntityDescriptor/>"))
	}))
	t.Cleanup(srv.Close)

	if got, err := c.Fetch(t.Context(), srv.URL+"/metadata"); err != nil || string(got) != "<EntityDescriptor/>" {
		t.Errorf("fetching from loopback = %q, %v", got, err)
	}
	for name, address := range map[string]string{
		"plain http away from loopback": "http://10.0.0.5/metadata", "not an address": "://", "another scheme": "ftp://idp.example.com/metadata",
		"an answer that is not a document": srv.URL + "/missing",
	} {
		if _, err := c.Fetch(t.Context(), address); err == nil {
			t.Errorf("%s: fetched", name)
		}
	}
	if _, err := c.Fetch(t.Context(), "https://127.0.0.1:1/metadata"); err == nil {
		t.Error("fetched from an address nothing listens at")
	}
}

func TestMetadataThatCannotBeUsedSaysWhy(t *testing.T) {
	c := saml.NewClient()
	cert, _, err := c.GenerateSPKey(now)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Join(strings.Fields(strings.NewReplacer("-----BEGIN CERTIFICATE-----", "", "-----END CERTIFICATE-----", "").Replace(cert)), "")
	sso := `<md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="%s"/>`
	key := `<md:KeyDescriptor use="signing"><ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:X509Data><ds:X509Certificate>%s</ds:X509Certificate></ds:X509Data></ds:KeyInfo></md:KeyDescriptor>`
	document := func(entity, service, keys string) []byte {
		return []byte(`<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="` + entity + `">` +
			`<md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">` + keys + service +
			`</md:IDPSSODescriptor></md:EntityDescriptor>`)
	}
	good := strings.Replace(sso, "%s", "https://idp.example.com/sso", 1)
	goodKey := strings.Replace(key, "%s", body, 1)

	meta, err := c.Parse(document("https://idp.example.com", good, goodKey+goodKey))
	if err != nil || meta.EntityID != "https://idp.example.com" || meta.SSOURL != "https://idp.example.com/sso" || len(meta.Certificates) != 1 {
		t.Fatalf("a usable document = %+v, %v (the same certificate twice counts once)", meta, err)
	}

	for name, tc := range map[string]struct {
		raw  []byte
		code string
	}{
		"an insecure sign-on address": {
			document("https://idp.example.com", strings.Replace(sso, "%s", "http://idp.example.com/sso", 1), goodKey), "invalid_metadata",
		},
		"a certificate that is not base64": {document("https://idp.example.com", good, strings.Replace(key, "%s", "***", 1)), "invalid_metadata"},
		"a certificate that cannot be read": {
			document("https://idp.example.com", good, strings.Replace(key, "%s", "Z2FyYmFnZQ==", 1)), "invalid_metadata",
		},
		"no entity ID":   {document("", good, goodKey), "invalid_metadata"},
		"no service":     {document("https://idp.example.com", "", goodKey), "no_redirect_binding"},
		"no key":         {document("https://idp.example.com", good, ""), "no_signing_certificate"},
		"not XML":        {[]byte("not xml"), "invalid_metadata"},
		"a DOCTYPE":      {append([]byte(`<!DOCTYPE a [<!ENTITY x "y">]>`), document("https://idp.example.com", good, goodKey)...), "invalid_metadata"},
		"an instruction": {append([]byte(`<?xml-stylesheet href="x"?>`), document("https://idp.example.com", good, goodKey)...), "invalid_metadata"},
		"another root":   {[]byte(`<html xmlns="urn:oasis:names:tc:SAML:2.0:metadata"/>`), "invalid_metadata"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.Parse(tc.raw)
			var bad *identity.MetadataError
			if !asMetadataError(err, &bad) || bad.Code != tc.code {
				t.Errorf("parse = %v, want %s", err, tc.code)
			}
		})
	}
}

func asMetadataError(err error, target **identity.MetadataError) bool {
	return errors.As(err, target)
}
