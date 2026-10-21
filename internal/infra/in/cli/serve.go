package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/infra/compose"
	"github.com/azverinus/flagnetic/internal/infra/config"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	"github.com/azverinus/flagnetic/internal/pkg/buildinfo"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// workerStopBound is how long the stop waits for worker rounds, and for
// notification listeners, to end once cancelled. Each round is bounded by its own timeout too; this keeps the stop
// finite even for a round that ignores its context.
const workerStopBound = 10 * time.Second

// poolCloseBound is how long the stop waits for the pool to close once every
// request has been cancelled. Code that ignores its context could hold a
// connection forever; the bound keeps the stop finite even then.
const poolCloseBound = 5 * time.Second

// serve runs a replica until stop is cancelled, then stops it within a known
// bound: it drains, in-flight requests get the grace period, then their
// contexts are cancelled, and the pool closes last. Cancelling kill skips
// whatever of the drain and the grace is left.
func serve(ctx, kill context.Context, cfg config.Config, log *slog.Logger, o options) int {
	log.InfoContext(ctx, "starting", "version", buildinfo.Version())
	if cfg.SecretKey.IsZero() {
		// Sessions would be signed with an empty key, and stored secrets
		// sealed under one: a replica without its key never starts.
		return fail(log, "start failed", configError(errors.New(
			"FLAGNETIC_SECRET_KEY is required by serve; for example the output of `openssl rand -base64 32`, kept for the life of the instance")))
	}
	if n := len(cfg.SecretKey.Reveal()); n < secret.MinKeyLength {
		return fail(log, "start failed", configError(fmt.Errorf(
			"FLAGNETIC_SECRET_KEY is %d bytes long and must be at least %d bytes; for example the output of `openssl rand -base64 32`",
			n, secret.MinKeyLength)))
	}
	pool, err := openDatabase(ctx, cfg, log, o)
	if err != nil {
		return failStart(ctx, log, err)
	}
	defer closePool(pool, log)
	if cfg.AutoMigrate {
		err = applyMigrations(ctx, pool, log, o)
	} else {
		err = checkSchema(ctx, pool, log, o)
	}
	if err != nil {
		return failStart(ctx, log, err)
	}
	if o.canary {
		if err = checkCanary(ctx, pool, secret.NewKeyring(cfg.SecretKey)); err != nil {
			return failStart(ctx, log, err)
		}
	}

	// The registry every part of the replica registers its metrics on.
	reg := metrics.NewRegistry()
	reg.MustRegister(postgres.NewPoolCollector(pool))

	runner := worker.NewRunner(log, reg)
	for _, w := range o.workers {
		if err = runner.Register(w); err != nil {
			return fail(log, "start failed", err)
		}
	}

	edgeOpts := flaghttp.OptionsFrom(cfg)
	edgeOpts.Logger, edgeOpts.DB, edgeOpts.Metrics = log, pool, flaghttp.NewMetrics(reg)
	edge := flaghttp.New(edgeOpts)
	system := clock.System{}
	compose.Management(edge, compose.Deps{Pool: pool, Clock: system, IDs: id.NewSource(system)}, reg)
	for _, r := range o.routes {
		edge.Handle(r.pattern, r.handler)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return fail(log, "start failed", err)
	}
	var metricsLn *metrics.Listener
	if cfg.MetricsListenAddr != "" {
		if metricsLn, err = metrics.Listen(ctx, cfg.MetricsListenAddr, reg); err != nil {
			_ = ln.Close()
			return fail(log, "start failed", fmt.Errorf("metrics listener: %w", err))
		}
		// Stopped on every way out, after the public listener and before the
		// pool closes.
		defer metricsLn.Stop()
		go func() {
			if err := metricsLn.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics listener failed", "error", err.Error())
			}
		}()
		log.InfoContext(ctx, "metrics listening", "addr", metricsLn.Addr().String())
	}

	// Every request's context derives from base, never from ctx: a request
	// keeps running after the stop signal until the grace period is over, and
	// cancelling base is what ends the requests still running then.
	base, cancelRequests := context.WithCancel(kill)
	defer cancelRequests()
	srv := edge.HTTPServer()
	srv.BaseContext = func(net.Listener) context.Context { return base }
	srv.ErrorLog = slog.NewLogLogger(log.Handler(), slog.LevelWarn)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	log.InfoContext(ctx, "listening", "addr", ln.Addr().String())

	// Workers and notification listeners run on the replica's own context,
	// never a request's: they outlive the stop signal until the requests are
	// done, and stop before the metrics listener and the pool. Goroutines
	// spawned from it have their panics counted.
	replica := runner.PanicHook(kill)
	runner.Start(replica)
	defer runner.Stop(workerStopBound)
	listeners := postgres.NewListeners(replica, pool, log, reg)
	defer listeners.Stop(workerStopBound)

	select {
	case err := <-served:
		return fail(log, "serving failed", err)
	case <-ctx.Done():
	}

	if cfg.ShutdownDrain > 0 {
		edge.Drain()
		log.Info("draining", "drain", cfg.ShutdownDrain.String())
		select {
		case <-time.After(cfg.ShutdownDrain):
		case <-kill.Done():
		}
	}

	log.Info("stopping", "grace", cfg.ShutdownGrace.String())
	graceCtx, cancelGrace := context.WithTimeout(kill, cfg.ShutdownGrace)
	defer cancelGrace()
	if err := srv.Shutdown(graceCtx); err != nil {
		log.Warn("grace period over; cancelling the requests still running", "error", err.Error())
		cancelRequests()
		_ = srv.Close()
	}
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Warn("serving ended with an error", "error", err.Error())
	}
	runner.Stop(workerStopBound)
	listeners.Stop(workerStopBound)
	log.Info("stopped")
	return exitOK
}

// closePool closes the pool, waiting at most poolCloseBound for connections
// still held.
func closePool(pool *pgxpool.Pool, log *slog.Logger) {
	closed := make(chan struct{})
	go func() {
		pool.Close()
		close(closed)
	}()
	select {
	case <-closed:
		log.Info("database pool closed")
	case <-time.After(poolCloseBound):
		log.Warn("stopped with database connections still held", "bound", poolCloseBound.String())
	}
}

// checkSchema compares the database's schema with the binary's migrations
// when migrations run as a separate job. A schema behind the binary is
// refused, since new code would meet an old schema. A schema ahead of it is
// accepted with a warning: rolling a binary back after a migration that only
// added to the schema must keep working.
func checkSchema(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, o options) error {
	database, binary, err := postgres.SchemaVersions(ctx, pool, o.migrations)
	switch {
	case err != nil:
		return err
	case database < binary:
		return fmt.Errorf("the database schema is at version %d, behind this binary's version %d; "+
			"run `flagnetic migrate` with this binary first, or set FLAGNETIC_AUTO_MIGRATE=true", database, binary)
	case database > binary:
		log.WarnContext(ctx, "database schema is ahead of this binary", "database_version", database, "binary_version", binary)
	}
	return nil
}

// checkCanary stores the instance's canary on its first start and proves on
// every start that the secret key opens it, so a mistyped key stops the
// replica rather than silently failing to read what the instance stored.
func checkCanary(ctx context.Context, pool *pgxpool.Pool, keyring *secret.Keyring) error {
	fresh, err := keyring.NewCanary()
	if err != nil {
		return err
	}
	stored, err := postgres.Canary(ctx, pool, fresh, clock.System{}.Now())
	if err != nil {
		return fmt.Errorf("reading the secret key canary: %w", err)
	}
	return keyring.CheckCanary(stored)
}

// failStart reports a start that did not complete. A start cut short by the
// stop signal is a stop, not a failure.
func failStart(ctx context.Context, log *slog.Logger, err error) int {
	if ctx.Err() != nil {
		log.Info("stopped before starting")
		return exitOK
	}
	return fail(log, "start failed", err)
}
