// Package postgres is the server's PostgreSQL: the connection pool, the check
// that the server is a supported version, and the schema migrations embedded
// in the binary.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// MinServerVersionNum is the oldest supported PostgreSQL, as server_version_num
// reports it: 16. Migrations and queries may use anything it offers and
// nothing newer.
const MinServerVersionNum = 160000

//go:embed migrations
var embedded embed.FS

// Migrations returns the migration set embedded in the binary.
func Migrations() fs.FS {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}

// Open creates a pool for databaseURL. It does not wait for PostgreSQL to be
// reachable: the first query does, and readiness reports it.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// ServerVersionNum reads the server's server_version_num, such as 170004.
func ServerVersionNum(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var num int
	if err := pool.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&num); err != nil {
		return 0, fmt.Errorf("read the PostgreSQL version: %w", err)
	}
	return num, nil
}

// CheckServerVersion refuses a server older than MinServerVersionNum, naming
// both versions, so an unsupported database is found at start rather than at
// the first query that needs a newer feature.
func CheckServerVersion(num int) error {
	if num >= MinServerVersionNum {
		return nil
	}
	return fmt.Errorf("PostgreSQL %s is not supported: Flagnetic needs PostgreSQL %d or newer",
		versionName(num), MinServerVersionNum/10000)
}

// versionName turns server_version_num into the version PostgreSQL itself
// prints: major.minor from 10 on, major.minor.patch before.
func versionName(num int) string {
	if num >= 100000 {
		return fmt.Sprintf("%d.%d", num/10000, num%10000)
	}
	return fmt.Sprintf("%d.%d.%d", num/10000, num/100%100, num%100)
}

// MigrationReport says what Migrate did.
type MigrationReport struct {
	// Applied is the number of migrations this call applied.
	Applied int
	// Version is the schema's version afterwards: the last migration applied,
	// by this call or an earlier one.
	Version int64
}

// Migrate applies every pending migration of migrations to the schema that
// pool's search_path points at. Replicas that start together serialise on a
// PostgreSQL session advisory lock, so each migration is applied exactly once;
// the lock belongs to the session, so it is released even when a migration
// fails half way. An empty migration set is valid and applies nothing.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations fs.FS) (MigrationReport, error) {
	// A replica waiting for another's migrations probes the lock every second,
	// for up to five minutes, so a scaled deployment starts promptly once the
	// first replica is done.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return MigrationReport{}, fmt.Errorf("create the migration lock: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, goose.WithSessionLocker(locker))
	if errors.Is(err, goose.ErrNoMigrations) {
		return MigrationReport{}, nil
	}
	if err != nil {
		return MigrationReport{}, fmt.Errorf("load the migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("apply the migrations: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("read the schema version: %w", err)
	}
	return MigrationReport{Applied: len(results), Version: version}, nil
}
