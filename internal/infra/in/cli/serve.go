package cli

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/infra/config"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/pkg/buildinfo"
)

// poolCloseBound is how long the stop waits for the pool to close once every
// request has been cancelled. Code that ignores its context could hold a
// connection forever; the bound keeps the stop finite even then.
const poolCloseBound = 5 * time.Second

// readHeaderTimeout bounds how long a client may take to send its request
// headers, so slow clients cannot hold connections open.
const readHeaderTimeout = 10 * time.Second

// serve runs a replica until stop is cancelled, then stops it within a known
// bound: it drains, in-flight requests get the grace period, then their
// contexts are cancelled, and the pool closes last. Cancelling kill skips
// whatever of the drain and the grace is left.
func serve(ctx, kill context.Context, cfg config.Config, log *slog.Logger, o options) int {
	log.InfoContext(ctx, "starting", "version", buildinfo.Version())
	pool, err := openDatabase(ctx, cfg, log, o)
	if err != nil {
		return fail(log, "start failed", err)
	}
	defer closePool(pool, log)
	if cfg.AutoMigrate {
		if err = applyMigrations(ctx, pool, log, o); err != nil {
			return fail(log, "start failed", err)
		}
	}

	edge := flaghttp.New(flaghttp.Options{Logger: log, DB: pool})
	for _, r := range o.routes {
		edge.Handle(r.pattern, r.handler)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return fail(log, "start failed", err)
	}

	// Every request's context derives from base, never from ctx: a request
	// keeps running after the stop signal until the grace period is over, and
	// cancelling base is what ends the requests still running then.
	base, cancelRequests := context.WithCancel(kill)
	defer cancelRequests()
	srv := &http.Server{
		Handler:           edge,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return base },
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	log.InfoContext(ctx, "listening", "addr", ln.Addr().String())

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
	case <-time.After(poolCloseBound):
		log.Warn("stopped with database connections still held", "bound", poolCloseBound.String())
	}
}
