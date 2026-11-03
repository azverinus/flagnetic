package identity_test

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// samlProvider stores a SAML provider and returns it.
func samlProvider(t *testing.T, store *pgidentity.Store) identity.Provider {
	t.Helper()
	now := testkit.Start
	in, err := identity.CheckProviderInput(identity.ProviderInput{
		Slug: "saml-" + strings.ToLower(rand.Text()[:10]), Kind: identity.KindSAML,
		DisplayName: "SAML", Enabled: true, Domains: []string{"example.com"},
		SAML: identity.SAMLInput{MetadataURL: "https://idp.example.com/metadata", SignRequests: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	idp := identity.IDPMetadata{EntityID: "https://idp.example.com", SSOURL: "https://idp.example.com/sso", Certificates: []string{"pem"}}
	p := identity.NewSAML(randomProviderID(), in, idp, identity.SPKey{CertificatePEM: "cert", SealedKey: "sealed"}, now)
	if err = store.CreateProvider(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAnAssertionIsRememberedUntilItExpiresAndForgottenAfter(t *testing.T) {
	store := pgidentity.NewStore(testkit.Pool(t))
	p := samlProvider(t, store)
	other := samlProvider(t, store)
	now := testkit.Start
	until := now.Add(10 * time.Minute)

	record := func(provider identity.Provider, assertion string, at time.Time) bool {
		t.Helper()
		fresh, err := store.RecordAssertion(t.Context(), provider.ID, assertion, until, at)
		if err != nil {
			t.Fatal(err)
		}
		return fresh
	}

	if !record(p, "a1", now) {
		t.Fatal("a new assertion was not new")
	}
	if record(p, "a1", now.Add(time.Minute)) {
		t.Error("an assertion accepted before was new again")
	}
	if !record(p, "a2", now.Add(time.Minute)) || !record(other, "a1", now.Add(time.Minute)) {
		t.Error("another assertion, or the same ID at another provider, was taken for a repeat")
	}
	// Once the time it could be accepted until has passed, it is forgotten,
	// and the same ID is new: the identity provider reuses nothing in that
	// time, and the table holds no more than what could still be replayed.
	if !record(p, "a1", until.Add(time.Second)) {
		t.Error("an assertion that could no longer be accepted was still remembered")
	}
}

func TestAnAssertionOfAProviderThatDoesNotExistIsNotRemembered(t *testing.T) {
	store := pgidentity.NewStore(testkit.Pool(t))
	gone := randomProviderID()

	if _, err := store.RecordAssertion(t.Context(), gone, "a1", testkit.Start.Add(time.Minute), testkit.Start); err == nil {
		t.Error("an assertion was remembered for a provider that does not exist")
	}
}

func TestRememberingAnAssertionReportsAStoreThatFails(t *testing.T) {
	store := pgidentity.NewStore(testkit.Pool(t))
	p := samlProvider(t, store)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := store.RecordAssertion(ctx, p.ID, "a1", testkit.Start.Add(time.Minute), testkit.Start); err == nil {
		t.Error("an assertion was remembered with no database to remember it in")
	}
}

func randomProviderID() identity.ProviderID {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return id.FromBytes[identity.Provider](raw)
}
