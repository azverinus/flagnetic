package sso_test

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// spCertificate returns the certificate of the SP metadata with the SHA-256
// sha.
func spCertificate(t *testing.T, metadata []byte, sha string) *x509.Certificate {
	t.Helper()
	for _, c := range testkit.SPCertificates(metadata) {
		if fingerprint(c) == sha {
			return c
		}
	}
	t.Fatalf("the SP metadata holds no certificate with SHA-256 %s:\n%s", sha, metadata)
	return nil
}

// metadataOf is an SP metadata document that holds only c, to tell which of
// the instance's certificates verifies a request's signature.
func metadataOf(c *x509.Certificate) []byte {
	return []byte(`<EntityDescriptor><SPSSODescriptor><KeyDescriptor><KeyInfo><X509Data><X509Certificate>` +
		base64.StdEncoding.EncodeToString(c.Raw) + `</X509Certificate></X509Data></KeyInfo></KeyDescriptor></SPSSODescriptor></EntityDescriptor>`)
}

// signedWith reports whether the request of trip carries a signature that c's
// key made.
func signedWith(t *testing.T, idp *testkit.ForgingSAMLIDP, trip samlTrip, c *x509.Certificate) bool {
	t.Helper()
	return idp.ParseRequest(t, &url.URL{RawQuery: trip.req.Query}, metadataOf(c)).Signed
}

func (e env) rotate(t *testing.T, slug string) *management.RotateAuthProviderCertificateResponse {
	t.Helper()
	res, err := e.admin.RotateAuthProviderCertificateWithResponse(t.Context(), slug)
	if err != nil {
		t.Fatalf("POST …/rotate-certificate: %v", err)
	}
	return res
}

func (e env) completeRotation(t *testing.T, slug string) *management.CompleteAuthProviderCertificateRotationResponse {
	t.Helper()
	res, err := e.admin.CompleteAuthProviderCertificateRotationWithResponse(t.Context(), slug)
	if err != nil {
		t.Fatalf("POST …/rotate-certificate/complete: %v", err)
	}
	return res
}

// signInEncryptedTo signs a fresh person in through slug with an assertion the
// identity provider encrypted to c, and reports whether a session opened.
func (e env) signInEncryptedTo(t *testing.T, idp *testkit.ForgingSAMLIDP, slug string, c *x509.Certificate) bool {
	t.Helper()
	a := samlPerson(uniqueEmail())
	a.Encryption, a.EncryptTo = testkit.SAMLAES128CBC, pemOf(c.Raw)
	b, _ := e.samlSignIn(t, idp, slug, "", a)
	return signedIn(t, b) != nil
}

func TestStartingARotationListsBothCertificatesAndEitherKeyDecryptsWhileTheCurrentOneSigns(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	created := e.createSAML(t, idp, nil)
	if created.RotationPending == nil || *created.RotationPending || created.SpCertificates == nil || len(*created.SpCertificates) != 1 ||
		(*created.SpCertificates)[0].Status != management.SPCertificateStatusCurrent {
		t.Fatalf("a new provider answers rotation_pending %v and sp_certificates %+v, want false and its current certificate",
			created.RotationPending, created.SpCertificates)
	}
	currentSHA := (*created.SpCertificates)[0].Sha256

	res := e.rotate(t, created.Slug)

	if res.JSON200 == nil {
		t.Fatalf("POST …/rotate-certificate = %d: %s", res.StatusCode(), res.Body)
	}
	rotating := *res.JSON200
	if rotating.RotationPending == nil || !*rotating.RotationPending || rotating.Version != created.Version+1 {
		t.Errorf("rotation_pending %v at version %d, want true at %d", rotating.RotationPending, rotating.Version, created.Version+1)
	}
	certs := *rotating.SpCertificates
	if len(certs) != 2 || certs[0].Sha256 != currentSHA || certs[0].Status != management.SPCertificateStatusCurrent ||
		certs[1].Status != management.SPCertificateStatusNext || certs[1].Sha256 == currentSHA || !certs[1].NotAfter.After(certs[0].NotAfter.AddDate(0, 0, -1)) {
		t.Fatalf("sp_certificates = %+v, want the unchanged current certificate, then a new next one", certs)
	}
	got, err := e.admin.GetAuthProviderWithResponse(t.Context(), created.Slug)
	if err != nil || got.JSON200 == nil || got.JSON200.RotationPending == nil || !*got.JSON200.RotationPending {
		t.Fatalf("GET after the rotation started = %v, %v", got.StatusCode(), err)
	}

	metadata := e.spMetadata(t, created.Slug)
	current, next := spCertificate(t, metadata, certs[0].Sha256), spCertificate(t, metadata, certs[1].Sha256)
	for name, c := range map[string]*x509.Certificate{"current": current, "next": next} {
		if !e.signInEncryptedTo(t, idp, created.Slug, c) {
			t.Errorf("an assertion encrypted to the %s key was refused", name)
		}
	}
	trip := e.samlStart(t, testkit.NewBrowser(t, e.srv.URL), idp, created.Slug, "")
	if !signedWith(t, idp, trip, current) || signedWith(t, idp, trip, next) {
		t.Error("the request was not signed by the current key alone while the rotation was pending")
	}
}

func TestCompletingARotationMakesTheNextKeyCurrentAndDestroysTheRetiredOne(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	created := e.createSAML(t, idp, nil)
	rotating := e.rotate(t, created.Slug)
	if rotating.JSON200 == nil {
		t.Fatalf("POST …/rotate-certificate = %d: %s", rotating.StatusCode(), rotating.Body)
	}
	certs := *rotating.JSON200.SpCertificates
	metadata := e.spMetadata(t, created.Slug)
	retired, next := spCertificate(t, metadata, certs[0].Sha256), spCertificate(t, metadata, certs[1].Sha256)

	res := e.completeRotation(t, created.Slug)

	if res.JSON200 == nil {
		t.Fatalf("POST …/rotate-certificate/complete = %d: %s", res.StatusCode(), res.Body)
	}
	done := *res.JSON200
	if done.RotationPending == nil || *done.RotationPending || done.Version != rotating.JSON200.Version+1 {
		t.Errorf("rotation_pending %v at version %d, want false at %d", done.RotationPending, done.Version, rotating.JSON200.Version+1)
	}
	if now := *done.SpCertificates; len(now) != 1 || now[0].Sha256 != certs[1].Sha256 || now[0].Status != management.SPCertificateStatusCurrent {
		t.Fatalf("sp_certificates = %+v, want only the certificate the rotation generated, current", now)
	}
	after := e.spMetadata(t, created.Slug)
	if listed := testkit.SPCertificates(after); len(listed) == 0 {
		t.Fatalf("the metadata lists no certificate:\n%s", after)
	} else {
		for _, c := range listed {
			if fingerprint(c) != certs[1].Sha256 {
				t.Errorf("the metadata still lists the certificate %s", fingerprint(c))
			}
		}
	}
	if e.signInEncryptedTo(t, idp, created.Slug, retired) {
		t.Error("an assertion encrypted to the retired key was accepted")
	}
	if !e.signInEncryptedTo(t, idp, created.Slug, next) {
		t.Error("an assertion encrypted to the new key was refused")
	}
	trip := e.samlStart(t, testkit.NewBrowser(t, e.srv.URL), idp, created.Slug, "")
	if !signedWith(t, idp, trip, next) || signedWith(t, idp, trip, retired) {
		t.Error("the request was not signed by the new key alone")
	}
}

func TestARotationStartsOnceAndCompletesOnce(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	created := e.createSAML(t, idp, nil)

	if res := e.completeRotation(t, created.Slug); res.StatusCode() != http.StatusConflict {
		t.Fatalf("completing with none pending = %d, want 409: %s", res.StatusCode(), res.Body)
	} else if p := problemOf(t, "complete", res.StatusCode(), res.Body, http.StatusConflict); p.Code != "no_rotation_pending" {
		t.Errorf("code = %s, want no_rotation_pending", p.Code)
	}
	first := e.rotate(t, created.Slug)
	if first.JSON200 == nil {
		t.Fatalf("POST …/rotate-certificate = %d: %s", first.StatusCode(), first.Body)
	}
	again := e.rotate(t, created.Slug)
	if p := problemOf(t, "rotate again", again.StatusCode(), again.Body, http.StatusConflict); p.Code != "rotation_pending" {
		t.Errorf("code = %s, want rotation_pending", p.Code)
	}
	got, err := e.admin.GetAuthProviderWithResponse(t.Context(), created.Slug)
	if err != nil || got.JSON200 == nil {
		t.Fatalf("GET provider = %v, %v", got.StatusCode(), err)
	}
	if was, now := (*first.JSON200.SpCertificates)[1].Sha256, (*got.JSON200.SpCertificates)[1].Sha256; was != now {
		t.Error("the refused second rotation replaced the pending certificate")
	}
	if res := e.completeRotation(t, created.Slug); res.JSON200 == nil {
		t.Fatalf("completing = %d: %s", res.StatusCode(), res.Body)
	}
	last := e.completeRotation(t, created.Slug)
	if p := problemOf(t, "complete again", last.StatusCode(), last.Body, http.StatusConflict); p.Code != "no_rotation_pending" {
		t.Errorf("code = %s, want no_rotation_pending", p.Code)
	}
}

func TestOnlyASAMLProviderIsRotated(t *testing.T) {
	e := start(t)
	oidc := e.create(t, provider{issuer: "https://idp.example.com", clientID: "flagnetic", clientSecret: "s3cret-oidc-value", domains: []string{"example.com"}})

	rotate, complete := e.rotate(t, oidc), e.completeRotation(t, oidc)

	if p := problemOf(t, "rotate", rotate.StatusCode(), rotate.Body, http.StatusUnprocessableEntity); p.Code != "not_saml" {
		t.Errorf("rotating an OIDC provider: code %s, want not_saml", p.Code)
	}
	if p := problemOf(t, "complete", complete.StatusCode(), complete.Body, http.StatusUnprocessableEntity); p.Code != "not_saml" {
		t.Errorf("completing on an OIDC provider: code %s, want not_saml", p.Code)
	}
	if res := e.rotate(t, "no-such-provider"); res.StatusCode() != http.StatusNotFound {
		t.Errorf("rotating an unknown provider = %d, want 404", res.StatusCode())
	}
}

func TestARotationIsAuditedByFingerprintAndNeverWithAKey(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	created := e.createSAML(t, idp, nil)
	rotating := e.rotate(t, created.Slug)
	if rotating.JSON200 == nil {
		t.Fatalf("POST …/rotate-certificate = %d: %s", rotating.StatusCode(), rotating.Body)
	}
	certs := *rotating.JSON200.SpCertificates
	if res := e.completeRotation(t, created.Slug); res.JSON200 == nil {
		t.Fatalf("completing = %d: %s", res.StatusCode(), res.Body)
	}

	for action, tc := range map[string]struct{ present, absent []string }{
		"auth_provider.certificate_rotation_started":   {present: []string{certs[0].Sha256, certs[1].Sha256}},
		"auth_provider.certificate_rotation_completed": {present: []string{certs[1].Sha256}, absent: nil},
	} {
		entries := e.auditJSON(t, action, created.Id.String())
		if len(entries) != 1 {
			t.Errorf("%s: %d audit entries, want one", action, len(entries))
			continue
		}
		for _, want := range tc.present {
			if !strings.Contains(entries[0], want) {
				t.Errorf("%s does not name the certificate %s: %s", action, want, entries[0])
			}
		}
		for _, leaked := range []string{"PRIVATE KEY", "sealed", "BEGIN CERTIFICATE"} {
			if strings.Contains(entries[0], leaked) {
				t.Errorf("%s holds %q: %s", action, leaked, entries[0])
			}
		}
	}
	// What the completed rotation records as the value after it names the new
	// certificate alone: the retired key is gone.
	completed := e.auditJSON(t, "auth_provider.certificate_rotation_completed", created.Id.String())
	if len(completed) == 1 {
		after, _, _ := strings.Cut(completed[0][strings.Index(completed[0], `"after"`):], `,"before"`)
		if strings.Contains(after, certs[0].Sha256) {
			t.Errorf("the value after the completed rotation still names the retired certificate: %s", after)
		}
	}
}

func TestEditingAProviderWhileItRotatesLeavesTheRotationPending(t *testing.T) {
	idp := testkit.StartForgingSAMLIDP(t)
	e := start(t)
	created := e.createSAML(t, idp, nil)
	rotating := e.rotate(t, created.Slug)
	if rotating.JSON200 == nil {
		t.Fatalf("POST …/rotate-certificate = %d: %s", rotating.StatusCode(), rotating.Body)
	}

	res, err := e.admin.UpdateAuthProviderWithResponse(t.Context(), created.Slug,
		&management.UpdateAuthProviderParams{IfMatch: ptr(fmt.Sprintf(`"%d"`, rotating.JSON200.Version))},
		management.AuthProviderPatch{DisplayName: ptr("Renamed")})

	if err != nil || res.JSON200 == nil {
		t.Fatalf("PATCH = %v, %v: %s", res.StatusCode(), err, res.Body)
	}
	if res.JSON200.RotationPending == nil || !*res.JSON200.RotationPending || len(*res.JSON200.SpCertificates) != 2 {
		t.Errorf("after an edit: rotation_pending %v, sp_certificates %+v; want the rotation pending still",
			res.JSON200.RotationPending, res.JSON200.SpCertificates)
	}
}

func TestSigningInThroughKeycloakGoesOnWhileACertificateRotatesAndAfterIt(t *testing.T) {
	kc := testkit.StartKeycloak(t)
	e := start(t)
	// Other tests of the package sign Ada in too, through providers of their own,
	// so the member exists already: linking by email finds it.
	p := e.createKeycloak(t, kc, func(b *management.AuthProviderCreate) { b.LinkByEmail = ptr(true) })
	ada := kc.Users[0]
	signInAda := func(stage string) {
		t.Helper()
		b := testkit.NewBrowser(t, e.srv.URL)
		o := e.opts()
		o.Login = testkit.KeycloakLogin(ada)
		v := e.signIn(t, b, p.Slug, "", o)
		if me := signedIn(t, b); me == nil || me.Member.Email != ada.Email {
			t.Fatalf("%s: Ada was not signed in: %s", stage, v.URL)
		}
	}
	signInAda("before the rotation")

	if res := e.rotate(t, p.Slug); res.JSON200 == nil {
		t.Fatalf("POST …/rotate-certificate = %d: %s", res.StatusCode(), res.Body)
	}
	signInAda("while the rotation is pending")
	if listed := testkit.SPCertificates(e.spMetadata(t, p.Slug)); len(listed) != 4 {
		t.Errorf("the metadata Keycloak would import lists %d certificate elements, want a signing and an encryption one for each of two", len(listed))
	}

	if res := e.completeRotation(t, p.Slug); res.JSON200 == nil {
		t.Fatalf("completing = %d: %s", res.StatusCode(), res.Body)
	}
	signInAda("after the rotation")
}
