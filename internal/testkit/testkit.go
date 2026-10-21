// Package testkit is the Flagnetic test kit: a real, migrated PostgreSQL for
// every test package, and the fake clock, deterministic identifiers and log
// sink a test hands to the code under test.
//
// A package whose tests need the database starts with
//
//	func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }
package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	"github.com/azverinus/flagnetic/internal/pkg/pgtest"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Start is the time a test's fake clock reads until the test moves it.
var Start = time.Date(2026, time.January, 1, 9, 0, 0, 0, time.UTC)

// Main gives the package one schema with the server's migrations applied,
// runs its tests, fails the package if its audit log holds a secret value the
// tests recorded, and drops the schema. It returns the exit code for os.Exit.
func Main(m *testing.M) int {
	return pgtest.Main(m, func(ctx context.Context, pool *pgxpool.Pool) error {
		_, err := postgres.Migrate(ctx, pool, postgres.Migrations())
		return err
	}, auditHoldsNoRecordedSecret)
}

// Pool returns the pool on the package's migrated schema, shared by the
// package's tests: a test isolates by its own data, never by emptying tables.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return pgtest.Pool(t)
}

// DatabaseURL returns the connection string of the package's migrated schema.
func DatabaseURL(t testing.TB) string {
	t.Helper()
	return pgtest.URL(t)
}

// EmptyDatabase returns the connection string of a new schema with nothing in
// it, not even the migrations, dropped when the test ends. If the test
// migrates it, its audit log is guarded as the package's is.
func EmptyDatabase(t testing.TB) string {
	t.Helper()
	url := pgtest.NewSchema(t)
	guardDatabase(t, url)
	return url
}

// Clock returns a fake clock reading Start.
func Clock() *clock.Fake { return clock.NewFake(Start) }

// IDs returns a deterministic identifier source reading c.
func IDs(c clock.Clock) *id.Source { return id.NewDeterministicSource(c) }
