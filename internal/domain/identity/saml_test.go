package identity_test

import (
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

func samlInput() identity.ProviderInput {
	return identity.ProviderInput{
		Slug: "corp-saml", Kind: identity.KindSAML, DisplayName: "Corporate SAML", Enabled: true, Domains: []string{"example.com"},
		SAML: identity.SAMLInput{MetadataURL: "https://idp.example.com/metadata", SignRequests: true},
	}
}

func TestAWellFormedSAMLProviderTakesItsDefaults(t *testing.T) {
	got, err := identity.CheckProviderInput(samlInput())
	if err != nil {
		t.Fatal(err)
	}
	if got.SAML.NameIDFormat != identity.NameIDUnspecified {
		t.Errorf("name identifier format = %q", got.SAML.NameIDFormat)
	}
	if got.SAML.Claims != (identity.Claims{Email: "email", Name: "name", Groups: "groups"}) {
		t.Errorf("claims = %+v", got.SAML.Claims)
	}
}

func TestAMalformedSAMLProviderNamesEveryFieldAtOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		change   func(*identity.ProviderInput)
		code     string
		pointers []string
	}{
		"no metadata": {func(in *identity.ProviderInput) { in.SAML.MetadataURL = "" }, "invalid_auth_provider", []string{"/idp_metadata_url"}},
		"both metadata": {
			func(in *identity.ProviderInput) { in.SAML.MetadataXML = "<a/>" }, "invalid_auth_provider", []string{"/idp_metadata_xml"},
		},
		"a relative metadata URL": {
			func(in *identity.ProviderInput) { in.SAML.MetadataURL = "idp.example.com/metadata" }, "invalid_auth_provider", []string{"/idp_metadata_url"},
		},
		"a metadata URL with credentials": {
			func(in *identity.ProviderInput) { in.SAML.MetadataURL = "https://u:p@idp.example.com/metadata" }, "invalid_auth_provider", []string{"/idp_metadata_url"},
		},
		"a document over a megabyte": {
			func(in *identity.ProviderInput) {
				in.SAML.MetadataURL, in.SAML.MetadataXML = "", strings.Repeat("x", 1<<20+1)
			}, "invalid_auth_provider", []string{"/idp_metadata_xml"},
		},
		"an unknown name identifier format": {
			func(in *identity.ProviderInput) { in.SAML.NameIDFormat = "kerberos" }, "invalid_auth_provider", []string{"/name_id_format"},
		},
		"a blank attribute name": {
			func(in *identity.ProviderInput) { in.SAML.Claims = identity.Claims{Email: strings.Repeat("e", 257)} }, "invalid_auth_provider", []string{"/claims/email"},
		},
		"a plain http metadata URL": {
			func(in *identity.ProviderInput) { in.SAML.MetadataURL = "http://idp.example.com/metadata" }, "insecure_url", []string{"/idp_metadata_url"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := samlInput()
			tc.change(&in)
			_, err := identity.CheckProviderInput(in)
			code, pointers := problemOf(t, err)
			if code != tc.code || !slices.Equal(pointers, tc.pointers) {
				t.Errorf("refused %s at %v, want %s at %v", code, pointers, tc.code, tc.pointers)
			}
		})
	}
	in := samlInput()
	in.SAML.MetadataURL = "http://127.0.0.1:8080/metadata"
	if _, err := identity.CheckProviderInput(in); err != nil {
		t.Errorf("a loopback http metadata URL: %v", err)
	}
}

func TestTheFieldsOfAnotherKindAreRefusedNotIgnored(t *testing.T) {
	err := identity.ForeignFields(identity.KindSAML, []string{"idp_metadata_url", "issuer", "scopes"})
	if code, pointers := problemOf(t, err); code != "invalid_auth_provider" || !slices.Equal(pointers, []string{"/issuer", "/scopes"}) {
		t.Errorf("an OIDC field on a SAML provider: %s %v", code, pointers)
	}
	err = identity.ForeignFields(identity.KindOIDC, []string{"issuer", "sign_requests"})
	if code, pointers := problemOf(t, err); code != "invalid_auth_provider" || !slices.Equal(pointers, []string{"/sign_requests"}) {
		t.Errorf("a SAML field on an OIDC provider: %s %v", code, pointers)
	}
	if err = identity.ForeignFields(identity.KindSAML, []string{"idp_metadata_xml", "sign_requests", "name_id_format"}); err != nil {
		t.Errorf("a SAML provider's own fields: %v", err)
	}

	p := newSAMLProvider(t)
	issuer := "https://idp.example.com"
	if _, err = p.CheckChange(identity.ProviderChange{Issuer: &issuer}); err == nil {
		t.Error("an edit of a SAML provider took an issuer")
	}
	oidc := newProvider(t)
	sign := false
	if _, err = oidc.CheckChange(identity.ProviderChange{SignRequests: &sign}); err == nil {
		t.Error("an edit of an OIDC provider took sign_requests")
	}
}

func newSAMLProvider(t *testing.T) identity.Provider {
	t.Helper()
	in, err := identity.CheckProviderInput(samlInput())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idp := identity.IDPMetadata{EntityID: "https://idp.example.com", SSOURL: "https://idp.example.com/sso", Certificates: []string{"pem"}}
	return identity.NewSAML(identity.ProviderID{}, in, idp, identity.SPKey{CertificatePEM: "cert", SealedKey: "sealed"}, now)
}

func TestAnEditToWhereTheMetadataComesFromClearsTheOtherSource(t *testing.T) {
	p := newSAMLProvider(t)
	xml := "<EntityDescriptor/>"
	got, err := p.CheckChange(identity.ProviderChange{IDPMetadataXML: &xml})
	if err != nil {
		t.Fatal(err)
	}
	if got.SAML.MetadataURL != "" || got.SAML.MetadataXML != xml {
		t.Errorf("source after pasting = %q, %q", got.SAML.MetadataURL, got.SAML.MetadataXML)
	}
	fetched := "https://idp.example.com/other"
	back, err := got.CheckChange(identity.ProviderChange{IDPMetadataURL: &fetched})
	if err != nil || back.SAML.MetadataURL != fetched || back.SAML.MetadataXML != "" {
		t.Errorf("source after fetching again = %+v, %v", back.SAML, err)
	}
	if _, err = p.CheckChange(identity.ProviderChange{IDPMetadataURL: &fetched, IDPMetadataXML: &xml}); err == nil {
		t.Error("an edit naming both sources was accepted")
	}
	if p.SAML.MetadataURL == "" {
		t.Error("the provider changed in place")
	}
}

func TestASAMLProviderVouchesForTheEmailsInItsDomainsAlone(t *testing.T) {
	p := newSAMLProvider(t)
	for email, want := range map[string]bool{
		"ada@example.com": true, "Ada@EXAMPLE.com": true, "ada@example.org": false, "ada@mail.example.com": false, "ada": false,
	} {
		// A SAML provider has no verification flag to read: the domains decide.
		if got := p.Vouches(identity.VerifiedClaims{Email: email}); got != want {
			t.Errorf("vouches for %q = %v, want %v", email, got, want)
		}
	}
}

func TestResolutionSignsInASAMLSubjectLikeAnOIDCOne(t *testing.T) {
	p := newSAMLProvider(t)
	p.AutoProvision = true
	got, err := identity.Resolve(identity.ResolveInput{Provider: p, Claims: identity.VerifiedClaims{Subject: "s", Email: "ada@example.com"}})
	if err != nil || got.Action != identity.Provision {
		t.Errorf("resolution = %+v, %v; want a provisioned member", got, err)
	}
}

func TestTheURLsOfASAMLProviderAreBuiltFromTheInstancesAddress(t *testing.T) {
	base := mustURL(t, "https://flags.example.com/")
	if got := identity.ACSURL(base, "corp"); got != "https://flags.example.com/sso/corp/acs" {
		t.Errorf("ACS URL = %s", got)
	}
	if got := identity.MetadataURL(base, "corp"); got != "https://flags.example.com/sso/corp/metadata" {
		t.Errorf("metadata URL = %s", got)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
