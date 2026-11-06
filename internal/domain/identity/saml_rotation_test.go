package identity_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

var rotationNow = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

// certificate returns a self-signed certificate valid until notAfter, PEM
// encoded; distinct calls give distinct certificates.
func certificate(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "test"}, NotBefore: notAfter.AddDate(-3, 0, 0), NotAfter: notAfter,
	}, &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "test"}}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func samlProvider(t *testing.T) identity.Provider {
	t.Helper()
	in, err := identity.CheckProviderInput(samlInput())
	if err != nil {
		t.Fatal(err)
	}
	idp := identity.IDPMetadata{
		EntityID: "https://idp.example.com/entity", SSOURL: "https://idp.example.com/sso",
		Certificates: []string{certificate(t, rotationNow.AddDate(1, 0, 0))},
	}
	sp := identity.SPKey{CertificatePEM: certificate(t, rotationNow.AddDate(3, 0, 0)), SealedKey: "sealed-current"}
	return identity.NewSAML(identity.ProviderID{}, in, idp, sp, rotationNow)
}

func TestStartingARotationKeepsTheCurrentPairAndAddsTheNext(t *testing.T) {
	p := samlProvider(t)
	next := identity.SPKey{CertificatePEM: certificate(t, rotationNow.AddDate(3, 0, 0)), SealedKey: "sealed-next"}
	later := rotationNow.Add(time.Hour)

	got, err := p.StartRotation(next, later)
	if err != nil {
		t.Fatal(err)
	}

	if got.SAML.SPCertificate != p.SAML.SPCertificate || got.SAML.SealedSPKey != "sealed-current" {
		t.Error("the current pair changed when the rotation started")
	}
	if got.SAML.NextSPCertificate != next.CertificatePEM || got.SAML.NextSealedSPKey != "sealed-next" || !got.SAML.RotationPending() {
		t.Errorf("next pair = %q, %q, pending %v", got.SAML.NextSPCertificate, got.SAML.NextSealedSPKey, got.SAML.RotationPending())
	}
	if got.Version != p.Version+1 || !got.UpdatedAt.Equal(later) {
		t.Errorf("version %d at %s, want %d at %s", got.Version, got.UpdatedAt, p.Version+1, later)
	}
	if p.SAML.RotationPending() {
		t.Error("starting a rotation changed the provider it was called on")
	}
}

func TestARotationAlreadyPendingIsNotStartedAgain(t *testing.T) {
	p := samlProvider(t)
	started, err := p.StartRotation(identity.SPKey{CertificatePEM: certificate(t, rotationNow.AddDate(3, 0, 0)), SealedKey: "sealed-next"}, rotationNow)
	if err != nil {
		t.Fatal(err)
	}

	_, err = started.StartRotation(identity.SPKey{CertificatePEM: certificate(t, rotationNow.AddDate(3, 0, 0)), SealedKey: "sealed-other"}, rotationNow)

	if code, _ := problemOf(t, err); code != "rotation_pending" {
		t.Errorf("refused %q, want rotation_pending", code)
	}
}

func TestCompletingARotationMakesTheNextPairCurrentAndDropsTheRetiredOne(t *testing.T) {
	p := samlProvider(t)
	next := identity.SPKey{CertificatePEM: certificate(t, rotationNow.AddDate(3, 0, 0)), SealedKey: "sealed-next"}
	started, err := p.StartRotation(next, rotationNow)
	if err != nil {
		t.Fatal(err)
	}
	later := rotationNow.Add(24 * time.Hour)

	got, err := started.CompleteRotation(later)
	if err != nil {
		t.Fatal(err)
	}

	if got.SAML.SPCertificate != next.CertificatePEM || got.SAML.SealedSPKey != "sealed-next" {
		t.Errorf("current pair = %q, %q; want the next one", got.SAML.SPCertificate, got.SAML.SealedSPKey)
	}
	if got.SAML.RotationPending() || got.SAML.NextSPCertificate != "" || got.SAML.NextSealedSPKey != "" {
		t.Error("a rotation is still pending after it completed")
	}
	if got.Version != started.Version+1 || !got.UpdatedAt.Equal(later) {
		t.Errorf("version %d at %s, want %d at %s", got.Version, got.UpdatedAt, started.Version+1, later)
	}
}

func TestCompletingWhenNoRotationIsPendingIsRefused(t *testing.T) {
	_, err := samlProvider(t).CompleteRotation(rotationNow)

	if code, _ := problemOf(t, err); code != "no_rotation_pending" {
		t.Errorf("refused %q, want no_rotation_pending", code)
	}
}

func TestOnlyASAMLProviderRotates(t *testing.T) {
	in, err := identity.CheckProviderInput(validInput())
	if err != nil {
		t.Fatal(err)
	}
	p := identity.New(identity.ProviderID{}, in, "sealed", rotationNow)

	_, startErr := p.StartRotation(identity.SPKey{}, rotationNow)
	_, completeErr := p.CompleteRotation(rotationNow)

	for name, err := range map[string]error{"start": startErr, "complete": completeErr} {
		if code, _ := problemOf(t, err); code != "not_saml" {
			t.Errorf("%s refused %q, want not_saml", name, code)
		}
	}
}

func TestTheSPCertificatesAreTheCurrentOneThenTheNext(t *testing.T) {
	p := samlProvider(t)
	if got := p.SAML.SPCertificates(); len(got) != 1 || got[0].Status != identity.SPCertificateCurrent ||
		len(got[0].SHA256) != 64 || !got[0].NotAfter.Equal(rotationNow.AddDate(3, 0, 0)) {
		t.Fatalf("certificates = %+v, want the current one", got)
	}
	started, err := p.StartRotation(identity.SPKey{CertificatePEM: certificate(t, rotationNow.AddDate(2, 0, 0)), SealedKey: "sealed-next"}, rotationNow)
	if err != nil {
		t.Fatal(err)
	}

	got := started.SAML.SPCertificates()

	if len(got) != 2 || got[0].Status != identity.SPCertificateCurrent || got[1].Status != identity.SPCertificateNext ||
		got[0].SHA256 == got[1].SHA256 || !got[1].NotAfter.Equal(rotationNow.AddDate(2, 0, 0)) {
		t.Errorf("certificates = %+v, want the current one then the next", got)
	}
}

func TestEditingARotatingProviderLeavesTheRotationAlone(t *testing.T) {
	p := samlProvider(t)
	started, err := p.StartRotation(identity.SPKey{CertificatePEM: certificate(t, rotationNow.AddDate(3, 0, 0)), SealedKey: "sealed-next"}, rotationNow)
	if err != nil {
		t.Fatal(err)
	}

	edited, err := started.CheckChange(identity.ProviderChange{DisplayName: new("Renamed")})
	if err != nil {
		t.Fatal(err)
	}

	if !edited.SAML.RotationPending() || edited.SAML.NextSealedSPKey != "sealed-next" {
		t.Error("an edit ended the pending rotation")
	}
	if !slices.Equal([]string{edited.SAML.SPCertificate}, []string{started.SAML.SPCertificate}) {
		t.Error("an edit changed the current certificate")
	}
}
