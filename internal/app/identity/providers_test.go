package identity_test

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
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
