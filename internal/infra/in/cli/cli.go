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
	"net/http"
	"os"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/infra/config"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	"github.com/azverinus/flagnetic/internal/pkg/buildinfo"
)

const usage = `Usage: flagnetic <command>

Commands:
  serve         Run the server
  migrate       Apply pending database migrations and exit
  healthcheck   Exit 0 when the local server is ready, 1 otherwise
  admin         Administer the instance from the command line (admin help)
  hash-check    Check the bucketing hash's uniformity on unit IDs, offline
                (hash-check -h)
  version       Print the version and exit
  help          Print this help

Configuration is read from environment variables and an optional YAML file
named by FLAGNETIC_CONFIG_FILE; see docs/configuration.md.
`

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// Option changes what a command reads from the binary or the database, or
// what it serves.
type Option func(*options)

type options struct {
	migrations       fs.FS
	serverVersionNum func(context.Context, *pgxpool.Pool) (int, error)
	routes           []route
	workers          []worker.Worker
	// stdin is what a command reads its input from.
	stdin io.Reader
	// canary is whether serve checks the secret key against the canary,
	// which the server's own migrations make a table for.
	canary bool
}

type route struct {
	pattern string
	handler http.Handler
}

// withMigrations replaces the server's migrations. The schema they make has
// no canary table, so serve skips the canary.
func withMigrations(migrations fs.FS) Option {
	return func(o *options) { o.migrations, o.canary = migrations, false }
}

func withServerVersionNum(f func(context.Context, *pgxpool.Pool) (int, error)) Option {
	return func(o *options) { o.serverVersionNum = f }
}

func withRoute(pattern string, h http.Handler) Option {
	return func(o *options) { o.routes = append(o.routes, route{pattern, h}) }
}

func withStdin(r io.Reader) Option {
	return func(o *options) { o.stdin = r }
}

func withWorker(w worker.Worker) Option {
	return func(o *options) { o.workers = append(o.workers, w) }
}

// Main runs the command named by args[0] and returns the process's exit code.
// lookupEnv reads the environment, as os.LookupEnv does; a value on signals
// asks the command to stop.
func Main(args []string, lookupEnv func(string) (string, bool), stdout, stderr io.Writer, signals <-chan os.Signal, opts ...Option) int {
	o := options{
		migrations:       postgres.Migrations(),
		serverVersionNum: postgres.ServerVersionNum,
		canary:           true,
		stdin:            os.Stdin,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}

	// The first signal asks the command to stop as it normally would; a
	// second one, from an operator who does not want to wait, ends it at once.
	stop, stopNow := context.WithCancel(context.Background())
	defer stopNow()
	kill, killNow := context.WithCancel(context.Background())
	defer killNow()
	go func() {
		for first := true; ; first = false {
			select {
			case <-signals:
				if !first {
					killNow()
					return
				}
				stopNow()
			case <-kill.Done():
				return
			}
		}
	}()

	var logger atomic.Pointer[slog.Logger]
	logger.Store(newLogger(stderr, config.Config{LogFormat: config.LogJSON}))
	withConfig := func(cmd func(config.Config, *slog.Logger) int) int {
		cfg, err := config.Load(lookupEnv)
		if err != nil {
			return fail(logger.Load(), "configuration refused", configError(err))
		}
		log := newLogger(stderr, cfg)
		logger.Store(log)
		return cmd(cfg, log)
	}

	exited := make(chan int, 1)
	go func() {
		switch args[0] {
		case "serve":
			exited <- withConfig(func(cfg config.Config, log *slog.Logger) int {
				return serve(stop, kill, cfg, log, o)
			})
		case "migrate":
			exited <- withConfig(func(cfg config.Config, log *slog.Logger) int {
				return migrate(stop, cfg, log, o)
			})
		case "admin":
			if code, done := adminHelp(args[1:], stdout, stderr); done {
				exited <- code
				break
			}
			cl := commandLine{args: args[1:], lookupEnv: lookupEnv, stdout: stdout, stderr: stderr}
			exited <- withConfig(func(cfg config.Config, log *slog.Logger) int {
				return admin(stop, cl, cfg, log, o)
			})
		case "hash-check":
			exited <- hashCheck(args[1:], o.stdin, stdout, stderr)
		case "healthcheck":
			code := withConfig(func(cfg config.Config, log *slog.Logger) int {
				return healthcheck(stop, cfg, log)
			})
			// A container runtime reads only 0 and 1; a refused configuration
			// is a replica that is not healthy.
			exited <- min(code, exitFailure)
		case "version":
			fmt.Fprintln(stdout, buildinfo.Version())
			exited <- exitOK
		case "help", "-h", "--help":
			fmt.Fprint(stdout, usage)
			exited <- exitOK
		default:
			fmt.Fprintf(stderr, "flagnetic: unknown command %q\n\n%s", args[0], usage)
			exited <- exitUsage
		}
	}()

	select {
	case code := <-exited:
		return code
	case <-kill.Done():
		logger.Load().Warn("second signal: exiting at once")
		return exitFailure
	}
}

// newLogger writes cfg's format at cfg's level to w.
func newLogger(w io.Writer, cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == config.LogText {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

func migrate(ctx context.Context, cfg config.Config, log *slog.Logger, o options) int {
	pool, err := openDatabase(ctx, cfg, log, o)
	if err != nil {
		return fail(log, "migrate failed", err)
	}
	defer pool.Close()
	if err := applyMigrations(ctx, pool, log, o); err != nil {
		return fail(log, "migrate failed", err)
	}
	return exitOK
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, o options) error {
	report, err := postgres.Migrate(ctx, pool, o.migrations)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "migrations applied", "applied", report.Applied, "version", report.Version)
	return nil
}

// openDatabase opens the pool and refuses a PostgreSQL older than the
// supported minimum, which every command touching the database checks first.
func openDatabase(ctx context.Context, cfg config.Config, log *slog.Logger, o options) (*pgxpool.Pool, error) {
	poolCfg, overrode, err := postgres.PoolConfig(cfg.DatabaseURL.Reveal(), cfg.DBMaxConns)
	if err != nil {
		return nil, configError(err)
	}
	if overrode {
		log.InfoContext(ctx, "FLAGNETIC_DB_MAX_CONNS overrides pool_max_conns of DATABASE_URL", "max_conns", poolCfg.MaxConns)
	}
	log.InfoContext(ctx, "database pool", "database", cfg.DatabaseURLForLog(), "max_conns", poolCfg.MaxConns)
	pool, err := postgres.Open(ctx, poolCfg)
	if err != nil {
		return nil, configError(err)
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

// errConfig marks an error in what the operator configured, answered with
// exit code 2.
var errConfig = errors.New("configuration error")

func configError(err error) error { return fmt.Errorf("%w: %w", errConfig, err) }

func fail(log *slog.Logger, msg string, err error) int {
	log.Error(msg, "error", err.Error())
	if errors.Is(err, errConfig) {
		return exitUsage
	}
	return exitFailure
}
