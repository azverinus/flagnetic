package testkit

import (
	"context"
	"crypto/rand"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/compose"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
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

// MemberToken returns the value of a personal access token carrying scopes
// (read alone when none) of a new active member of the instance on pool, who
// is no instance admin and holds no grant.
func MemberToken(t testing.TB, pool *pgxpool.Pool, scopes ...access.Scope) string {
	t.Helper()
	ids := id.NewSource(Clock())
	now := Clock().Now()
	m := access.Member{
		ID: id.New[access.Member](ids), Email: rand.Text() + "@example.com", DisplayName: "Test Member",
		Status: access.MemberActive, CreatedAt: now,
	}
	store := pgaccess.NewStore(pool)
	if err := store.CreateMember(t.Context(), m, ""); err != nil {
		t.Fatal(err)
	}
	tok, v, err := access.NewPersonalToken(id.New[access.APIToken](ids), access.NewPersonalTokenInput{
		Member: m.ID, Name: "test", Scopes: scopes, Now: now,
	}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateToken(t.Context(), tok); err != nil {
		t.Fatal(err)
	}
	return v.Value.Reveal()
}

// ProjectToken returns the value of a project token of project on pool
// holding role, with scopes (every scope a project token may carry when
// none).
func ProjectToken(t testing.TB, pool *pgxpool.Pool, project workspace.ProjectID, role access.RoleName, scopes ...access.Scope) string {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []access.Scope{access.ScopeRead, access.ScopeFlagsWrite, access.ScopeExperimentsWrite, access.ScopeProjectAdmin}
	}
	v := access.NewTokenValue(access.TokenProject, rand.Reader)
	tok := access.APIToken{
		ID: id.New[access.APIToken](id.NewSource(Clock())), Kind: access.TokenProject, Project: project, Role: role,
		Name: "pipeline", Hint: v.Hint, Hash: v.Hash, Scopes: scopes, CreatedAt: Clock().Now(),
	}
	if err := pgaccess.NewStore(pool).CreateToken(t.Context(), tok); err != nil {
		t.Fatal(err)
	}
	return v.Value.Reveal()
}
