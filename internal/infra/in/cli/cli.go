// Package cli is the flagnetic command line. Its entry takes everything a
// process gets from outside (arguments, environment, output streams and
// signals) as parameters, so tests run every command in-process.
//
// Exit codes: 0 success, 1 a runtime failure, 2 a usage or configuration
// error, so tooling can tell a bad deploy from a crash.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	"github.com/azverinus/flagnetic/internal/pkg/buildinfo"
)

const usage = `Usage: flagnetic <command>

Commands:
  migrate   Apply pending database migrations and exit
  version   Print the version and exit
  help      Print this help
`

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// Option changes what a command reads from the binary or the database.
type Option func(*options)

type options struct {
	migrations       fs.FS
	serverVersionNum func(context.Context, *pgxpool.Pool) (int, error)
}

func withMigrations(migrations fs.FS) Option {
	return func(o *options) { o.migrations = migrations }
}

func withServerVersionNum(f func(context.Context, *pgxpool.Pool) (int, error)) Option {
	return func(o *options) { o.serverVersionNum = f }
}

// Main runs the command named by args[0] and returns the process's exit code.
// lookupEnv reads the environment, as os.LookupEnv does; a value on signals
// asks the command to stop.
func Main(args []string, lookupEnv func(string) (string, bool), stdout, stderr io.Writer, signals <-chan os.Signal, opts ...Option) int {
	o := options{
		migrations:       postgres.Migrations(),
		serverVersionNum: postgres.ServerVersionNum,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()

	switch args[0] {
	case "migrate":
		return migrate(ctx, lookupEnv, slog.New(slog.NewJSONHandler(stderr, nil)), o)
	case "version":
		fmt.Fprintln(stdout, buildinfo.Version())
		return exitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "flagnetic: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

// errConfig marks an error in what the operator configured, answered with
// exit code 2.
var errConfig = errors.New("configuration error")

func migrate(ctx context.Context, lookupEnv func(string) (string, bool), log *slog.Logger, o options) int {
	pool, err := openDatabase(ctx, lookupEnv, o)
	if err != nil {
		return fail(log, "migrate failed", err)
	}
	defer pool.Close()
	report, err := postgres.Migrate(ctx, pool, o.migrations)
	if err != nil {
		return fail(log, "migrate failed", err)
	}
	log.InfoContext(ctx, "migrations applied", "applied", report.Applied, "version", report.Version)
	return exitOK
}

// openDatabase opens the pool and refuses a PostgreSQL older than the
// supported minimum, which every command touching the database checks first.
func openDatabase(ctx context.Context, lookupEnv func(string) (string, bool), o options) (*pgxpool.Pool, error) {
	url, _ := lookupEnv("DATABASE_URL")
	if url == "" {
		return nil, fmt.Errorf("%w: DATABASE_URL is required, for example postgres://flagnetic:secret@localhost:5432/flagnetic", errConfig)
	}
	pool, err := postgres.Open(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errConfig, err)
	}
	num, err := o.serverVersionNum(ctx, pool)
	if err == nil {
		err = postgres.CheckServerVersion(num)
	}
	if err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func fail(log *slog.Logger, msg string, err error) int {
	log.Error(msg, "error", err.Error())
	if errors.Is(err, errConfig) {
		return exitUsage
	}
	return exitFailure
}
