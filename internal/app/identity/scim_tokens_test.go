package identity_test

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	pgidentity "github.com/azverinus/flagnetic/internal/infra/out/postgres/identity"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// noThrottle counts nothing: the tests below judge tokens, not sources.
type noThrottle struct{}

func (noThrottle) Reserve(context.Context, netip.Addr, time.Time) error { return nil }

type scimSetup struct {
	deps      appidentity.SCIMTokenDeps
	auth      *appidentity.SCIMAuthenticator
	keyring   *secret.Keyring
	pool      *pgxpool.Pool
	providerA identity.Provider
	providerB identity.Provider
}

func newSCIMSetup(t *testing.T) scimSetup {
	t.Helper()
	pd := deps(t)
	create := func() identity.Provider {
		p, err := appidentity.NewCreateProvider(pd).Handle(t.Context(), appidentity.CreateProviderCommand{Input: input(), Request: admin})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	pool := testkit.Pool(t)
	keyring := secret.NewKeyring(secret.New(testkit.SecretKey))
	d := appidentity.SCIMTokenDeps{
		Tx: pd.Tx, Providers: pd.Store, Store: pgidentity.NewStore(pool), Audit: pd.Audit, Clock: pd.Clock, IDs: pd.IDs, Random: rand.Reader, Sealer: keyring,
	}
	return scimSetup{
		deps: d, auth: appidentity.NewAuthenticateSCIM(d, noThrottle{}, keyring, slog.New(slog.DiscardHandler)),
		keyring: keyring, pool: pool, providerA: create(), providerB: create(),
	}
}

func (s scimSetup) create(t *testing.T, expiresAt *time.Time) appidentity.CreatedSCIMToken {
	t.Helper()
	created, err := appidentity.NewCreateSCIMToken(s.deps).Handle(t.Context(), appidentity.CreateSCIMTokenCommand{
		Provider: s.providerA.Slug, ExpiresAt: expiresAt, Request: admin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func (s scimSetup) present(t *testing.T, token appidentity.CreatedSCIMToken) (appidentity.SCIMCaller, error) {
	t.Helper()
	return s.auth.Handle(t.Context(), token.Value.Reveal(), netip.MustParseAddr("192.0.2.1"))
}

// A hash is sealed for what its token may do: a database writer who points a
// token at another provider, or moves its expiry, no longer opens it.
func TestASCIMTokenWhoseProviderOrExpiryWasChangedInTheDatabaseIsRefused(t *testing.T) {
	s := newSCIMSetup(t)
	expires := s.deps.Clock.Now().Add(24 * time.Hour)
	token := s.create(t, &expires)

	caller, err := s.present(t, token)
	if err != nil || caller.ProviderSlug != s.providerA.Slug {
		t.Fatalf("presenting a new token = %+v, %v; want its provider %s", caller, err, s.providerA.Slug)
	}

	if _, err = s.pool.Exec(t.Context(), `UPDATE scim_tokens SET provider_id = $2 WHERE id = $1`, token.Token.ID.Bytes(), s.providerB.ID.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.present(t, token); !errors.Is(err, identity.ErrInvalidSCIMToken) {
		t.Errorf("a token pointed at another provider = %v, want ErrInvalidSCIMToken", err)
	}

	if _, err = s.pool.Exec(t.Context(), `UPDATE scim_tokens SET provider_id = $2, expires_at = $3 WHERE id = $1`,
		token.Token.ID.Bytes(), s.providerA.ID.Bytes(), expires.Add(240*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.present(t, token); !errors.Is(err, identity.ErrInvalidSCIMToken) {
		t.Errorf("a token whose expiry was moved = %v, want ErrInvalidSCIMToken", err)
	}

	if _, err = s.pool.Exec(t.Context(), `UPDATE scim_tokens SET expires_at = $2 WHERE id = $1`, token.Token.ID.Bytes(), expires); err != nil {
		t.Fatal(err)
	}
	if _, err = s.present(t, token); err != nil {
		t.Errorf("a token as it was created = %v, want it accepted", err)
	}
}

// A hash sealed to its row alone, as before the provider and the expiry were
// bound, opens once and is bound at that first use.
func TestASCIMTokenSealedToItsRowAloneIsBoundAtItsFirstUse(t *testing.T) {
	s := newSCIMSetup(t)
	token := s.create(t, nil)
	legacy, err := s.keyring.Seal(secret.PurposeSCIMToken, token.Token.Hash[:], secret.Row("scim_tokens", token.Token.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE scim_tokens SET token_hash_sealed = $2 WHERE id = $1`, token.Token.ID.Bytes(), legacy); err != nil {
		t.Fatal(err)
	}

	if _, err = s.present(t, token); err != nil {
		t.Fatalf("a token sealed to its row alone = %v, want it accepted", err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE scim_tokens SET provider_id = $2 WHERE id = $1`, token.Token.ID.Bytes(), s.providerB.ID.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.present(t, token); !errors.Is(err, identity.ErrInvalidSCIMToken) {
		t.Errorf("the token pointed at another provider after its first use = %v, want ErrInvalidSCIMToken", err)
	}
}
