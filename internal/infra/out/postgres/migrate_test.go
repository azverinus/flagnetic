package postgres_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func emptyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), testkit.EmptyDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func migration(up string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("-- +goose Up\n" + up + "\n")}
}

func TestAMigrationWaitingForALockFailsInsteadOfStallingTheTable(t *testing.T) {
	pool := emptyPool(t)
	first := fstest.MapFS{"00001_x.sql": migration("CREATE TABLE x (id int);")}
	both := fstest.MapFS{
		"00001_x.sql": first["00001_x.sql"],
		"00002_y.sql": migration("ALTER TABLE x ADD COLUMN y int;"),
	}
	if _, err := postgres.Migrate(t.Context(), pool, first); err != nil {
		t.Fatal(err)
	}

	// A long reader of the table: ACCESS SHARE conflicts with the ACCESS
	// EXCLUSIVE lock the ALTER takes.
	reader, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = reader.Rollback(context.WithoutCancel(t.Context()))
		}
	}
	t.Cleanup(release)
	if _, err = reader.Exec(t.Context(), "LOCK TABLE x IN ACCESS SHARE MODE"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = postgres.Migrate(t.Context(), pool, both)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("Migrate with a reader holding the table: error = %v, want a lock timeout (55P03)", err)
	}
	if elapsed := time.Since(start); elapsed > postgres.MigrationLockTimeout+10*time.Second {
		t.Errorf("Migrate took %s to give up, want about %s", elapsed, postgres.MigrationLockTimeout)
	}

	// The failed migration left nothing behind: once the reader is gone, a
	// second run applies it.
	release()
	report, err := postgres.Migrate(t.Context(), pool, both)
	if err != nil || report.Applied != 1 || report.Version != 2 {
		t.Errorf("Migrate after the reader ended = %+v, %v; want 1 applied, version 2", report, err)
	}
}

func TestMigrationsOfOneSchemaNeverWaitForAnotherSchemasMigrations(t *testing.T) {
	a, b := emptyPool(t), emptyPool(t)
	set := fstest.MapFS{"00001_x.sql": migration("CREATE TABLE x (id int);")}

	// Another replica of schema A is in the middle of a long migration.
	idA, err := postgres.MigrationLockID(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	idB, err := postgres.MigrationLockID(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if idA == idB {
		t.Fatalf("schemas share the migration lock %d", idA)
	}
	holder, err := a.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Closing the pool at the end of the test ends the session and its lock.
	t.Cleanup(holder.Release)
	if _, err := holder.Exec(t.Context(), "SELECT pg_advisory_lock($1)", idA); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if report, err := postgres.Migrate(ctx, b, set); err != nil || report.Applied != 1 {
		t.Fatalf("Migrate in schema B while schema A is locked = %+v, %v; want 1 applied", report, err)
	}

	waiting, cancelWaiting := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelWaiting()
	if _, err := postgres.Migrate(waiting, a, set); err == nil {
		t.Error("Migrate in schema A completed while its lock was held")
	}
}
