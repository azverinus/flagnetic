package sso_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// problem is what a refused write answers, as the tests read it.
type problem struct {
	Code    string `json:"code"`
	Failure string `json:"failure"`
	Errors  []struct {
		Pointer string `json:"pointer"`
		Code    string `json:"code"`
	} `json:"errors"`
}

func problemOf(t *testing.T, name string, status int, body []byte, wantStatus int) problem {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("%s = %d, want %d: %s", name, status, wantStatus, body)
	}
	var p problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("%s: %v: %s", name, err, body)
	}
	return p
}

func (p problem) violates(pointer, code string) bool {
	for _, v := range p.Errors {
		if v.Pointer == pointer && v.Code == code {
			return true
		}
	}
	return false
}

func TestASAMLProviderIsCreatedFromMetadataAndAnswersWhatToRegisterAtTheIdentityProvider(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)

	p := e.createSAML(t, idp, nil)

	if p.Kind != management.AuthProviderKindSaml || !p.Enabled || p.IdpEntityId == nil || *p.IdpEntityId != idp.EntityID ||
		p.IdpSsoUrl == nil || *p.IdpSsoUrl != idp.SSOURL || p.IdpMetadataUrl == nil || *p.IdpMetadataUrl != idp.MetadataURL {
		t.Errorf("provider = %+v", p)
	}
	if p.SignRequests == nil || !*p.SignRequests || p.NameIdFormat == nil || *p.NameIdFormat != management.SAMLNameIDFormatUnspecified {
		t.Errorf("defaults: sign_requests %v, name_id_format %v; want true and unspecified", p.SignRequests, p.NameIdFormat)
	}
	if p.Claims == nil || *p.Claims != (management.AuthProviderClaims{Email: "email", Name: "name", Groups: "groups"}) {
		t.Errorf("claims = %+v, want the defaults", p.Claims)
	}
	if p.IdpCertificates == nil || len(*p.IdpCertificates) != 1 || len((*p.IdpCertificates)[0].Sha256) != 64 {
		t.Errorf("idp_certificates = %+v", p.IdpCertificates)
	}
	base := publicURL + "/sso/" + p.Slug
	if p.AcsUrl == nil || *p.AcsUrl != base+"/acs" || p.EntityId == nil || *p.EntityId != base+"/metadata" ||
		p.MetadataUrl == nil || *p.MetadataUrl != base+"/metadata" {
		t.Errorf("acs_url %v, entity_id %v, metadata_url %v; want them under %s", p.AcsUrl, p.EntityId, p.MetadataUrl, base)
	}
	if p.RedirectUri != nil {
		t.Errorf("a SAML provider answers a redirect URI: %s", *p.RedirectUri)
	}

	// The metadata the instance serves names the entity, the ACS and the
	// certificate it signs with, valid for three years.
	doc := string(e.spMetadata(t, p.Slug))
	for _, want := range []string{`entityID="` + base + `/metadata"`, `Location="` + base + `/acs"`, `AuthnRequestsSigned="true"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("the metadata lacks %s:\n%s", want, doc)
		}
	}
	certs := testkit.SPCertificates([]byte(doc))
	if len(certs) == 0 {
		t.Fatalf("the metadata holds no certificate:\n%s", doc)
	}
	if years := certs[0].NotAfter.Sub(certs[0].NotBefore).Hours() / 24 / 365; years < 2.9 || years > 3.1 {
		t.Errorf("the certificate is valid for %.2f years, want 3", years)
	}

	// The key is sealed, and the audit log holds neither key nor secret.
	for _, entry := range e.auditJSON(t, "auth_provider.created", p.Id.String()) {
		if strings.Contains(entry, "PRIVATE KEY") || strings.Contains(entry, "sp_key") {
			t.Errorf("the audit entry of the provider holds its key: %s", entry)
		}
		if !strings.Contains(entry, `"idp_entity_id"`) {
			t.Errorf("the audit entry of the provider does not say what it is: %s", entry)
		}
	}
}

func (e env) auditJSON(t *testing.T, action, entity string) []string {
	t.Helper()
	res, err := e.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action, EntityId: &entity})
	if err != nil || res.JSON200 == nil {
		t.Fatalf("GET /audit = %v, %v", res.StatusCode(), err)
	}
	var out []string
	for _, entry := range res.JSON200.Items {
		raw, _ := json.Marshal(entry)
		out = append(out, string(raw))
	}
	return out
}

func TestASAMLProviderIsCreatedFromPastedMetadataAndSignsPeopleIn(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, func(b *management.AuthProviderCreate) {
		b.IdpMetadataUrl, b.IdpMetadataXml = nil, ptr(idp.Metadata(testkit.SAMLMetadataOptions{}))
	})
	if p.IdpMetadataUrl != nil || p.IdpEntityId == nil || *p.IdpEntityId != idp.EntityID {
		t.Errorf("provider = %+v", p)
	}

	b, v := e.samlSignIn(t, idp, p.Slug, "", samlPerson(uniqueEmail()))

	if signedIn(t, b) == nil {
		t.Errorf("a provider made from pasted metadata did not sign anybody in: %s", v.URL)
	}
}

func TestASAMLProviderIsRefusedForEachRuleItsMetadataBreaks(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	for name, tc := range map[string]struct {
		change  func(*management.AuthProviderCreate)
		status  int
		code    string
		pointer string
		vcode   string
	}{
		"both a URL and a document": {
			func(b *management.AuthProviderCreate) {
				b.IdpMetadataXml = ptr(idp.Metadata(testkit.SAMLMetadataOptions{}))
			},
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/idp_metadata_xml", "metadata_conflict",
		},
		"neither": {
			func(b *management.AuthProviderCreate) { b.IdpMetadataUrl = nil },
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/idp_metadata_url", "metadata_required",
		},
		"a plain http URL away from loopback": {
			func(b *management.AuthProviderCreate) { b.IdpMetadataUrl = ptr("http://10.0.0.5/metadata") },
			http.StatusUnprocessableEntity, "insecure_url", "/idp_metadata_url", "insecure_url",
		},
		"a URL that is no address": {
			func(b *management.AuthProviderCreate) { b.IdpMetadataUrl = ptr("idp.example.com/metadata") },
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/idp_metadata_url", "invalid_metadata_url",
		},
		"a document over a megabyte": {
			func(b *management.AuthProviderCreate) {
				b.IdpMetadataUrl, b.IdpMetadataXml = nil, ptr(strings.Repeat("x", 1<<20+1))
			},
			http.StatusRequestEntityTooLarge, "request_too_large", "", "",
		},
		"a document that is no metadata": {
			func(b *management.AuthProviderCreate) { b.IdpMetadataUrl, b.IdpMetadataXml = nil, ptr("<html></html>") },
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/idp_metadata_xml", "invalid_metadata",
		},
		"a document with a DOCTYPE": {
			func(b *management.AuthProviderCreate) {
				b.IdpMetadataUrl = nil
				b.IdpMetadataXml = ptr(`<!DOCTYPE a [<!ENTITY x "y">]>` + idp.Metadata(testkit.SAMLMetadataOptions{}))
			},
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/idp_metadata_xml", "invalid_metadata",
		},
		"metadata that offers no Redirect binding": {
			func(b *management.AuthProviderCreate) {
				b.IdpMetadataUrl, b.IdpMetadataXml = nil, ptr(idp.Metadata(testkit.SAMLMetadataOptions{NoRedirectBinding: true}))
			},
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/idp_metadata_xml", "no_redirect_binding",
		},
		"metadata without a signing certificate": {
			func(b *management.AuthProviderCreate) {
				b.IdpMetadataUrl, b.IdpMetadataXml = nil, ptr(idp.Metadata(testkit.SAMLMetadataOptions{NoCertificate: true}))
			},
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/idp_metadata_xml", "no_signing_certificate",
		},
		"an unknown name identifier format": {
			func(b *management.AuthProviderCreate) { b.NameIdFormat = ptr(management.SAMLNameIDFormat("kerberos")) },
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/name_id_format", "unknown_name_id_format",
		},
		"OIDC fields": {
			func(b *management.AuthProviderCreate) {
				b.Issuer, b.ClientId, b.ClientSecret = ptr("https://idp.example.com"), ptr("client"), ptr("secret-of-a-client")
			},
			http.StatusUnprocessableEntity, "invalid_auth_provider", "/issuer", "not_for_kind",
		},
		"nothing listening at the URL": {
			func(b *management.AuthProviderCreate) { b.IdpMetadataUrl = ptr("http://127.0.0.1:1/metadata") },
			http.StatusBadGateway, "provider_unreachable", "", "",
		},
		"a URL answering an error": {
			func(b *management.AuthProviderCreate) { b.IdpMetadataUrl = ptr(idp.URL("/broken")) },
			http.StatusBadGateway, "provider_unreachable", "", "",
		},
		"a URL answering over a megabyte": {
			func(b *management.AuthProviderCreate) { b.IdpMetadataUrl = ptr(idp.URL("/big")) },
			http.StatusBadGateway, "provider_unreachable", "", "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := management.AuthProviderCreate{
				Slug: slug(), Kind: management.AuthProviderKindSaml, DisplayName: "Corporate SAML", Domains: []string{"example.com"},
				IdpMetadataUrl: ptr(idp.MetadataURL),
			}
			tc.change(&body)

			res, err := e.admin.CreateAuthProviderWithResponse(t.Context(), body)
			if err != nil {
				t.Fatal(err)
			}

			p := problemOf(t, "create", res.StatusCode(), res.Body, tc.status)
			if p.Code != tc.code {
				t.Errorf("code = %s, want %s: %s", p.Code, tc.code, res.Body)
			}
			if tc.pointer != "" && !p.violates(tc.pointer, tc.vcode) {
				t.Errorf("violations = %+v, want %s at %s", p.Errors, tc.vcode, tc.pointer)
			}
			if tc.status == http.StatusBadGateway && p.Failure == "" {
				t.Errorf("no failure in %s", res.Body)
			}
		})
	}

	// Loopback http, which a test identity provider is, is fine.
	e.createSAML(t, idp, func(b *management.AuthProviderCreate) { b.IdpMetadataUrl = ptr(idp.MetadataURL) })
}

func TestAnOIDCProviderRefusesTheFieldsOfASAMLOne(t *testing.T) {
	oidc := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, oidc, nil)
	before, err := e.admin.GetAuthProviderWithResponse(t.Context(), slug)
	if err != nil || before.JSON200 == nil {
		t.Fatalf("GET provider = %v, %v", before.StatusCode(), err)
	}

	res, err := e.admin.UpdateAuthProviderWithResponse(t.Context(), slug, &management.UpdateAuthProviderParams{IfMatch: ptr(`"1"`)},
		management.AuthProviderPatch{SignRequests: ptr(false), IdpMetadataUrl: ptr("https://idp.example.com/metadata")})
	if err != nil {
		t.Fatal(err)
	}
	p := problemOf(t, "PATCH", res.StatusCode(), res.Body, http.StatusUnprocessableEntity)
	if !p.violates("/sign_requests", "not_for_kind") || !p.violates("/idp_metadata_url", "not_for_kind") {
		t.Errorf("violations = %+v", p.Errors)
	}
}

func TestEditingASAMLProviderReadsItsMetadataAgainAndChangesWhatItSigns(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	other := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	p := e.createSAML(t, idp, nil)
	update := func(version string, patch management.AuthProviderPatch) *management.UpdateAuthProviderResponse {
		t.Helper()
		res, err := e.admin.UpdateAuthProviderWithResponse(t.Context(), p.Slug, &management.UpdateAuthProviderParams{IfMatch: &version}, patch)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	res := update(`"1"`, management.AuthProviderPatch{
		SignRequests: ptr(false), NameIdFormat: ptr(management.SAMLNameIDFormatEmailAddress),
		Claims: &management.AuthProviderClaimsPatch{Email: ptr("mail")},
	})
	if res.JSON200 == nil || res.JSON200.SignRequests == nil || *res.JSON200.SignRequests || *res.JSON200.NameIdFormat != management.SAMLNameIDFormatEmailAddress ||
		res.JSON200.Claims.Email != "mail" || res.JSON200.Claims.Name != "name" {
		t.Fatalf("PATCH = %d: %s", res.StatusCode(), res.Body)
	}
	trip := e.samlStart(t, testkit.NewBrowser(t, e.srv.URL), idp, p.Slug, "")
	if trip.req.Signed || trip.req.NameIDFormat != "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress" {
		t.Errorf("request after the edit = %+v, want it unsigned and asking for an email address", trip.req)
	}

	// Another metadata URL is fetched and read, and replaces the identity
	// provider; a pasted document replaces the URL.
	res = update(`"2"`, management.AuthProviderPatch{IdpMetadataUrl: ptr(other.MetadataURL)})
	if res.JSON200 == nil || *res.JSON200.IdpEntityId != other.EntityID || *res.JSON200.IdpMetadataUrl != other.MetadataURL {
		t.Fatalf("PATCH metadata URL = %d: %s", res.StatusCode(), res.Body)
	}
	res = update(`"3"`, management.AuthProviderPatch{IdpMetadataXml: ptr(idp.Metadata(testkit.SAMLMetadataOptions{}))})
	if res.JSON200 == nil || *res.JSON200.IdpEntityId != idp.EntityID || res.JSON200.IdpMetadataUrl != nil {
		t.Fatalf("PATCH pasted metadata = %d: %s", res.StatusCode(), res.Body)
	}
	if got := len(e.auditJSON(t, "auth_provider.updated", p.Id.String())); got != 3 {
		t.Errorf("%d auth_provider.updated entries, want 3", got)
	}

	// Metadata that cannot be used leaves the provider as it was.
	res = update(`"4"`, management.AuthProviderPatch{IdpMetadataXml: ptr(idp.Metadata(testkit.SAMLMetadataOptions{NoRedirectBinding: true}))})
	if res.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("PATCH unusable metadata = %d: %s", res.StatusCode(), res.Body)
	}
	if got, err := e.admin.GetAuthProviderWithResponse(t.Context(), p.Slug); err != nil || got.JSON200 == nil || got.JSON200.Version != 4 {
		t.Errorf("after the refused edit: %v, %v", got, err)
	}
}

func TestTestingASAMLProviderReadsItsMetadataAgain(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	byURL := e.createSAML(t, idp, nil)
	pasted := e.createSAML(t, idp, func(b *management.AuthProviderCreate) {
		b.IdpMetadataUrl, b.IdpMetadataXml, b.Enabled = nil, ptr(idp.Metadata(testkit.SAMLMetadataOptions{})), ptr(false)
	})

	for name, slug := range map[string]string{"fetched": byURL.Slug, "pasted, and disabled": pasted.Slug} {
		res, err := e.admin.TestAuthProviderWithResponse(t.Context(), slug)
		if err != nil || res.JSON200 == nil {
			t.Fatalf("%s: POST .../test = %v: %s (%v)", name, res.StatusCode(), res.Body, err)
		}
		got := res.JSON200
		if got.IdpEntityId == nil || *got.IdpEntityId != idp.EntityID || got.IdpSsoUrl == nil || *got.IdpSsoUrl != idp.SSOURL ||
			got.IdpCertificates == nil || len(*got.IdpCertificates) != 1 || (*got.IdpCertificates)[0].NotAfter.Before(time.Now()) {
			t.Errorf("%s: found %+v", name, got)
		}
		if got.Issuer != nil || got.JwksUri != nil {
			t.Errorf("%s: a SAML provider answers OIDC discovery fields: %+v", name, got)
		}
	}
	if got := len(e.auditJSON(t, "auth_provider.updated", byURL.Id.String())); got != 0 {
		t.Errorf("testing a provider was audited as %d edits", got)
	}

	// Once the identity provider stops answering, the test says why.
	idp2 := testkit.StartForgingSAMLIDP(t)
	gone := e.createSAML(t, idp2, nil)
	idp2.Close()
	res, err := e.admin.TestAuthProviderWithResponse(t.Context(), gone.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if p := problemOf(t, "test of a provider that stopped answering", res.StatusCode(), res.Body, http.StatusBadGateway); p.Code != "provider_unreachable" || p.Failure == "" {
		t.Errorf("problem = %+v", p)
	}
}
