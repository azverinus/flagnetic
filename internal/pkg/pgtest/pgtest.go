// Package pgtest gives a test package a real PostgreSQL, so that no test fakes
// the database.
//
// The server comes from DATABASE_URL when it is set, otherwise from a
// testcontainers postgres:17-alpine started for the package. Each test package
// gets one schema of its own, created and set up from TestMain before any test
// and dropped after the last, so packages running in parallel never see each
// other; the tests of a package share it and isolate by their own data.
//
//	func TestMain(m *testing.M) {
//		os.Exit(pgtest.Main(m, setup))
//	}
//
// The package knows nothing about the schema it sets up: the caller passes the
// set-up, typically applying its migrations.
package pgtest

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Image is the PostgreSQL started when DATABASE_URL is not set.
const Image = "postgres:17-alpine"

var pkg struct {
	mu       sync.Mutex
	started  bool
	baseURL  string
	url      string
	pool     *pgxpool.Pool
	cleanups []func()
}

// Main creates the package schema, sets it up with setup through a pool whose
// search_path points at it, runs the tests, then drops the schema and returns
// the exit code for os.Exit.
func Main(m *testing.M, setup func(ctx context.Context, pool *pgxpool.Pool) error) int {
	defer cleanUp()
	if err := start(setup); err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: %v\n", err)
		return 1
	}
	return m.Run()
}

func start(setup func(ctx context.Context, pool *pgxpool.Pool) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pkg.mu.Lock()
	defer pkg.mu.Unlock()
	base, err := serverURL(ctx)
	if err != nil {
		return err
	}
	pkg.baseURL = base
	pkg.started = true
	u, schema, err := newSchemaLocked(ctx)
	if err != nil {
		return err
	}
	pkg.cleanups = append(pkg.cleanups, func() { dropSchema(schema) })
	pool, err := pgxpool.New(ctx, u)
	if err != nil {
		return fmt.Errorf("open a pool on the package schema: %w", err)
	}
	pkg.cleanups = append(pkg.cleanups, pool.Close)
	if err := setup(ctx, pool); err != nil {
		return fmt.Errorf("set up the package schema: %w", err)
	}
	pkg.url, pkg.pool = u, pool
	return nil
}

// cleanUp runs the clean-ups last first: the pool closes before its schema is
// dropped, and the schema is dropped before the container stops.
func cleanUp() {
	pkg.mu.Lock()
	defer pkg.mu.Unlock()
	for i := len(pkg.cleanups) - 1; i >= 0; i-- {
		pkg.cleanups[i]()
	}
	pkg.cleanups = nil
}

// URL returns the connection string of the package schema.
func URL(t testing.TB) string {
	t.Helper()
	requireStarted(t)
	return pkg.url
}

// Pool returns the pool on the package schema that every test of the package
// shares. It is closed after the last test.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	requireStarted(t)
	return pkg.pool
}

// NewSchema creates an empty schema for one test, dropped when the test ends,
// and returns a connection string whose search_path points at it. Tests that
// need a database before any set-up, such as a migration runner's, use it.
func NewSchema(t testing.TB) string {
	t.Helper()
	requireStarted(t)
	pkg.mu.Lock()
	defer pkg.mu.Unlock()
	u, schema, err := newSchemaLocked(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dropSchema(schema) })
	return u
}

func requireStarted(t testing.TB) {
	t.Helper()
	pkg.mu.Lock()
	defer pkg.mu.Unlock()
	if !pkg.started {
		t.Fatal("pgtest: call pgtest.Main from the package's TestMain")
	}
}

// newSchemaLocked creates a uniquely named schema and returns a connection
// string whose search_path points at it, and its name for the caller to drop.
func newSchemaLocked(ctx context.Context) (connString, schema string, err error) {
	conn, err := pgx.Connect(ctx, pkg.baseURL)
	if err != nil {
		return "", "", fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	schema = "test_" + strings.ToLower(rand.Text())
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		return "", "", fmt.Errorf("create schema: %w", err)
	}
	connString, err = withSearchPath(pkg.baseURL, schema)
	return connString, schema, err
}

func dropSchema(schema string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, pkg.baseURL)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	_, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
}

// withSearchPath adds search_path to a URL. DATABASE_URL must be a URL here,
// not pgx's keyword/value form, which has no query to add the parameter to.
func withSearchPath(dbURL, schema string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("DATABASE_URL must be a postgres:// URL for tests, got %q", dbURL)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// serverURL returns DATABASE_URL when it is set, otherwise starts a container
// that is stopped after the package's tests.
func serverURL(ctx context.Context) (string, error) {
	if u := os.Getenv("DATABASE_URL"); u != "" {
		return u, nil
	}
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("flagnetic"),
		tcpostgres.WithUsername("flagnetic"),
		tcpostgres.WithPassword("flagnetic"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", fmt.Errorf("start PostgreSQL (is Docker running? or set DATABASE_URL): %w", err)
	}
	pkg.cleanups = append(pkg.cleanups, func() { _ = testcontainers.TerminateContainer(c) })
	return c.ConnectionString(ctx, "sslmode=disable")
}
