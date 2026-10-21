package testkit

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/compose"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// The instance admin every test kit database is bootstrapped with.
const (
	InstanceAdminEmail    = "admin@example.com"
	InstanceAdminName     = "Test Admin"
	InstanceAdminPassword = "correct horse battery staple"
)

var (
	adminsMu sync.Mutex
	// admins holds the bootstrap token of each pool bootstrapped, since a
	// database has one bootstrap: the tests of a package share it.
	admins = map[*pgxpool.Pool]string{}
)

// InstanceAdminToken returns the bootstrap token of the package's database,
// bootstrapping its instance admin on first use.
func InstanceAdminToken(t testing.TB) string {
	t.Helper()
	return Bootstrap(t, Pool(t))
}

// Bootstrap returns the bootstrap token of the instance on pool, running
// `admin bootstrap` as InstanceAdminEmail on first use, at the test kit's
// Start.
func Bootstrap(t testing.TB, pool *pgxpool.Pool) string {
	t.Helper()
	adminsMu.Lock()
	defer adminsMu.Unlock()
	if tok, ok := admins[pool]; ok {
		return tok
	}
	// Identifiers are drawn at random rather than deterministically, since
	// the tests sharing the database mint their own from the same instant.
	res, err := compose.Bootstrap(compose.NewDeps(pool, Clock())).Handle(context.Background(), appaccess.BootstrapCommand{
		Email: InstanceAdminEmail, DisplayName: InstanceAdminName, Password: secret.New(InstanceAdminPassword),
		Request: governance.Request{Actor: governance.CLIActor("admin bootstrap")},
	})
	if err != nil {
		t.Fatalf("bootstrapping the instance admin: %v", err)
	}
	admins[pool] = res.Token.Reveal()
	return admins[pool]
}

// FreshPool returns a pool on a new migrated database of its own, dropped
// when the test ends, for a test that needs an instance nobody else touches,
// such as one not yet bootstrapped.
func FreshPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), EmptyDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := postgres.Migrate(t.Context(), pool, postgres.Migrations()); err != nil {
		t.Fatal(err)
	}
	return pool
}

// Client returns a client of the management API of srv presenting token as
// a bearer, or no credential when token is "".
func Client(t testing.TB, srv *Server, token string) *management.ClientWithResponses {
	t.Helper()
	var opts []management.ClientOption
	if token != "" {
		opts = append(opts, management.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	}
	c, err := management.NewClientWithResponses(srv.URL+management.BaseURL, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
