package identity_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// references is something that refers to providers, as identities will.
type references map[identity.ProviderID][]string

func (r references) ProviderUses(_ context.Context, p identity.ProviderID) ([]string, error) {
	return r[p], nil
}

// accessSettings reads the auth settings the access context keeps, as the
// composition root lets the identity context.
type accessSettings struct{ store *pgaccess.Store }

func (a accessSettings) LockProviderSettings(ctx context.Context, slug string) (appidentity.SettingsOfProvider, error) {
	s, err := a.store.LockAuthSettings(ctx)
	return appidentity.SettingsOfProvider{LocalLoginEnabled: s.LocalLoginEnabled, EnforcedDomains: s.EnforcedDomainsOf(slug)}, err
}

func (a accessSettings) RevokeProviderSessions(ctx context.Context, slug string, at time.Time) (int, error) {
	return a.store.RevokeAllProviderSessions(ctx, slug, at)
}

func codeOf(err error) string {
	if e, ok := apperrors.From(err); ok {
		return e.Code
	}
	return ""
}

var admin = governance.Request{Actor: governance.CLIActor("test")}

func deps(t *testing.T, refs ...appidentity.ProviderReference) appidentity.ProviderDeps {
	t.Helper()
	pool := testkit.Pool(t)
	c := testkit.Clock()
	return appidentity.ProviderDeps{
		Tx: postgres.NewTransactor(pool), Store: pgidentity.NewStore(pool), Audit: testkit.Record(pool), Clock: c,
		IDs: id.NewSource(c), Sealer: secret.NewKeyring(secret.New(testkit.SecretKey)),
		BaseURL: &url.URL{Scheme: "https", Host: "flags.example.com"}, References: refs,
		Settings: accessSettings{store: pgaccess.NewStore(pool)},
	}
}

func input() identity.ProviderInput {
	return identity.ProviderInput{
		Slug: "p" + strings.ToLower(rand.Text()[:10]), Kind: identity.KindOIDC, DisplayName: "Corporate SSO", Enabled: true,
		Domains: []string{"example.com"},
		OIDC: identity.OIDCInput{
			Issuer: "https://idp.example.com", ClientID: "flagnetic", ClientSecret: secret.New("client-secret-" + rand.Text()),
			RequireVerifiedEmail: true,
		},
	}
}

func TestAProviderSomethingReferencesIsNotDeletedAndItsReferencesAreNamed(t *testing.T) {
	refs := references{}
	d := deps(t, refs)
	p, err := appidentity.NewCreateProvider(d).Handle(t.Context(), appidentity.CreateProviderCommand{Input: input(), Request: admin})
	if err != nil {
		t.Fatal(err)
	}
	refs[p.ID] = []string{"identities", "SCIM tokens"}
	del := appidentity.NewDeleteProvider(d)
	err = del.Handle(t.Context(), appidentity.ProviderCommand{Slug: p.Slug, Request: admin})
	var e *apperrors.Error
	if !errors.As(err, &e) || e.Kind != apperrors.KindConflict || e.Code != "provider_in_use" ||
		!slices.Equal(e.Extensions["used_by"].([]string), []string{"identities", "SCIM tokens"}) {
		t.Fatalf("deleting a provider in use = %v, want 409 provider_in_use naming what refers to it", err)
	}
	// Disabling keeps everything that refers to it.
	if off, err := appidentity.NewDisableProvider(d).Handle(t.Context(), appidentity.ProviderCommand{Slug: p.Slug, Request: admin}); err != nil || off.Enabled {
		t.Fatalf("disabling a provider in use = %+v, %v", off, err)
	}

	delete(refs, p.ID)
	if err := del.Handle(t.Context(), appidentity.ProviderCommand{Slug: p.Slug, Request: admin}); err != nil {
		t.Fatalf("deleting once nothing refers to it: %v", err)
	}
	if _, err := appidentity.NewGetProvider(d.Store).Handle(t.Context(), p.Slug); codeOf(err) != "not_found" {
		t.Errorf("reading a deleted provider = %v", err)
	}
}

func TestTwoEditsBasedOnTheSameVersionLeaveOneOfThem(t *testing.T) {
	d := deps(t)
	p, err := appidentity.NewCreateProvider(d).Handle(t.Context(), appidentity.CreateProviderCommand{Input: input(), Request: admin})
	if err != nil {
		t.Fatal(err)
	}
	update := appidentity.NewUpdateProvider(d)
	codes := make([]string, 2)
	done := make(chan int)
	for i, name := range []string{"First", "Second"} {
		go func() {
			_, err := update.Handle(t.Context(), appidentity.UpdateProviderCommand{
				Slug: p.Slug, Change: identity.ProviderChange{DisplayName: &name}, Expected: precondition.On(1), Request: admin,
			})
			codes[i] = codeOf(err)
			done <- i
		}()
	}
	<-done
	<-done
	slices.Sort(codes)
	if !slices.Equal(codes, []string{"", "version_mismatch"}) {
		t.Errorf("edits based on version 1 = %q, want one accepted and one version_mismatch", codes)
	}
}

func TestAClientSecretSealedBeforeRowsWereBoundIsBoundByTheNextEditOfItsProvider(t *testing.T) {
	d := deps(t)
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	in, err := identity.CheckProviderInput(input())
	if err != nil {
		t.Fatal(err)
	}
	p := identity.New(id.New[identity.Provider](d.IDs), in, unboundSeal(t, secret.PurposeOIDCClientSecret, in.OIDC.ClientSecret.Reveal()), testkit.Start)
	if err = d.Store.CreateProvider(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	name := "Renamed SSO"
	if _, err = appidentity.NewUpdateProvider(d).Handle(t.Context(), appidentity.UpdateProviderCommand{
		Slug: p.Slug, Change: identity.ProviderChange{DisplayName: &name}, Expected: precondition.On(p.Version), Request: admin,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := appidentity.NewGetProvider(d.Store).Handle(t.Context(), p.Slug)
	if err != nil {
		t.Fatal(err)
	}
	sealed := got.OIDC.SealedClientSecret
	if !strings.HasPrefix(sealed, "v2:") {
		t.Fatalf("stored client secret %q, want it sealed again, bound", sealed)
	}
	if plain, err := keyring.Open(secret.PurposeOIDCClientSecret, sealed, secret.Row("auth_providers", p.ID.String())); err != nil ||
		string(plain) != in.OIDC.ClientSecret.Reveal() {
		t.Errorf("the instance opens %q, %v; want the client secret", plain, err)
	}
	if _, err := keyring.Open(secret.PurposeOIDCClientSecret, sealed, secret.Row("auth_providers", "another")); err == nil {
		t.Error("the rebound client secret opens for another provider")
	}
}

// unboundSeal seals plaintext for purpose under the test secret key the way
// values were sealed before they were bound to a row: "v1:" and AES-256-GCM
// with no additional data.
func unboundSeal(t *testing.T, purpose secret.Purpose, plaintext string) string {
	t.Helper()
	key, err := hkdf.Key(sha256.New, []byte(testkit.SecretKey), nil, "flagnetic secret v1 "+string(purpose), 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	return "v1:" + base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))
}

type spKeys struct{}

func (spKeys) GenerateSPKey(time.Time) (string, []byte, error) {
	return "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n", []byte("private key " + rand.Text()), nil
}

func TestACertificateRotationBindsTheKeysOfItsProvider(t *testing.T) {
	d := deps(t)
	d.SPKeys = spKeys{}
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	raw := input()
	raw.Kind, raw.OIDC = identity.KindSAML, identity.OIDCInput{}
	raw.SAML = identity.SAMLInput{MetadataXML: "<EntityDescriptor/>", SignRequests: true}
	in, err := identity.CheckProviderInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	pid := id.New[identity.Provider](d.IDs)
	p := identity.NewSAML(pid, in, identity.IDPMetadata{
		EntityID: "https://idp.example.com", SSOURL: "https://idp.example.com/sso", Certificates: []string{"idp certificate"},
	},
		identity.SPKey{CertificatePEM: "current", SealedKey: unboundSeal(t, secret.PurposeSAMLSPKey, "current key")}, testkit.Start)
	if err = d.Store.CreateProvider(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	cmd := appidentity.ProviderCommand{Slug: p.Slug, Request: admin}

	started, err := appidentity.NewRotateCertificate(d).Handle(t.Context(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	binding := secret.Row("auth_providers", pid.String())
	for name, sealed := range map[string]string{"current": started.SAML.SealedSPKey, "next": started.SAML.NextSealedSPKey} {
		if !strings.HasPrefix(sealed, "v2:") {
			t.Errorf("the %s key is %.3s, want it bound to its provider", name, sealed)
		}
		if _, err = keyring.Open(secret.PurposeSAMLSPKey, sealed, binding); err != nil {
			t.Errorf("the %s key does not open for its provider: %v", name, err)
		}
	}

	if _, err = appidentity.NewRotateCertificate(d).Handle(t.Context(), cmd); codeOf(err) != "rotation_pending" {
		t.Errorf("rotating again = %v, want rotation_pending", err)
	}
	missing := appidentity.ProviderCommand{Slug: "nothing-here", Request: admin}
	if _, err = appidentity.NewRotateCertificate(d).Handle(t.Context(), missing); codeOf(err) != "not_found" {
		t.Errorf("rotating an unknown provider = %v, want not_found", err)
	}
	if _, err = appidentity.NewCompleteCertificateRotation(d).Handle(t.Context(), missing); codeOf(err) != "not_found" {
		t.Errorf("completing the rotation of an unknown provider = %v, want not_found", err)
	}
	oidc, err := appidentity.NewCreateProvider(d).Handle(t.Context(), appidentity.CreateProviderCommand{Input: input(), Request: admin})
	if err != nil {
		t.Fatal(err)
	}
	notSAML := appidentity.ProviderCommand{Slug: oidc.Slug, Request: admin}
	if _, err = appidentity.NewRotateCertificate(d).Handle(t.Context(), notSAML); codeOf(err) != "not_saml" {
		t.Errorf("rotating an OIDC provider = %v, want not_saml", err)
	}
	if _, err = appidentity.NewCompleteCertificateRotation(d).Handle(t.Context(), notSAML); codeOf(err) != "not_saml" {
		t.Errorf("completing the rotation of an OIDC provider = %v, want not_saml", err)
	}

	done, err := appidentity.NewCompleteCertificateRotation(d).Handle(t.Context(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if got, openErr := keyring.Open(secret.PurposeSAMLSPKey, done.SAML.SealedSPKey, binding); openErr != nil || !strings.HasPrefix(string(got), "private key ") {
		t.Errorf("after completing, the current key opens as %q, %v, want the next key", got, openErr)
	}
	if _, err = appidentity.NewCompleteCertificateRotation(d).Handle(t.Context(), cmd); codeOf(err) != "no_rotation_pending" {
		t.Errorf("completing again = %v, want no_rotation_pending", err)
	}
}
