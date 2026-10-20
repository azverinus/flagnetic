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
	"math"
	"regexp"

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

// DefaultMaxConns is the pool size when neither the configuration nor
// pool_max_conns in DATABASE_URL sets one: enough for a replica's requests
// and workers on a small container, a known number on a large node.
const DefaultMaxConns = 10

// setsPoolMaxConns finds pool_max_conns as a URL query parameter or as a
// keyword/value pair, spaces around the equals sign included.
var setsPoolMaxConns = regexp.MustCompile(`(?:^|[?&\s])pool_max_conns\s*=`)

// PoolConfig is the pool configuration of databaseURL with maxConns
// connections. maxConns 0 keeps pool_max_conns of the URL, or DefaultMaxConns
// when the URL has none. overrode reports a maxConns that replaced the URL's
// own pool_max_conns, for the caller to log: an existing URL is never
// overridden silently.
func PoolConfig(databaseURL string, maxConns int) (cfg *pgxpool.Config, overrode bool, err error) {
	cfg, err = pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, false, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	inURL := setsPoolMaxConns.MatchString(databaseURL)
	switch {
	case maxConns > 0:
		cfg.MaxConns = int32(min(maxConns, math.MaxInt32)) //nolint:gosec // clamped
		overrode = inURL
	case !inURL:
		cfg.MaxConns = DefaultMaxConns
	}
	return cfg, overrode, nil
}

// Open creates a pool. It does not wait for PostgreSQL to be reachable: the
// first query does, and readiness reports it.
func Open(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
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

// SchemaVersions returns the version of the schema in the database, the last
// migration applied (0 when none ever was), and the version of migrations,
// its latest migration (0 for an empty set).
func SchemaVersions(ctx context.Context, pool *pgxpool.Pool, migrations fs.FS) (database, binary int64, err error) {
	files, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		return 0, 0, fmt.Errorf("list the migrations: %w", err)
	}
	for _, f := range files {
		if v, vErr := goose.NumericComponent(f); vErr == nil {
			binary = max(binary, v)
		}
	}
	// goose records every applied migration in this table, created by the
	// first migrate; a database never migrated has none.
	var migratedBefore bool
	if err = pool.QueryRow(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&migratedBefore); err != nil {
		return 0, 0, fmt.Errorf("read the schema version: %w", err)
	}
	if !migratedBefore {
		return 0, binary, nil
	}
	err = pool.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&database)
	if err != nil {
		return 0, 0, fmt.Errorf("read the schema version: %w", err)
	}
	return database, binary, nil
}
