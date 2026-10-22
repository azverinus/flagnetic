package postgres

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
)

// Listeners run the replica's notification listeners on the database of a
// pool, each on a connection of its own, and report each one's state as
// flagnetic_listener_up{channel}. A listener that is down leaves readiness
// alone: the replica still serves, and what depends on notifications stops
// trusting itself instead.
type Listeners struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	up   *prometheus.GaugeVec

	parent context.Context
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// started are the listeners started, for Restart.
	started []started
}

type started struct {
	channel string
	h       pglisten.Handler
	opts    pglisten.Options
}

// NewListeners registers the listener gauge on reg. Listeners run on ctx, the
// replica's own context, until Stop.
func NewListeners(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, reg prometheus.Registerer) *Listeners {
	up := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metrics.Namespace, Subsystem: "listener", Name: "up",
		Help: "Whether the replica is listening on a notification channel: 1 listening, 0 not.",
	}, []string{"channel"})
	reg.MustRegister(up)
	child, cancel := context.WithCancel(ctx)
	return &Listeners{pool: pool, log: log, up: up, parent: ctx, ctx: child, cancel: cancel}
}

// Start listens on channel until Stop, delivering to h, in a goroutine whose
// panics are recovered and counted. opts may shorten the intervals, as tests
// do, and carries the test kit's fault hook.
func (l *Listeners) Start(channel string, h pglisten.Handler, opts pglisten.Options) {
	l.started = append(l.started, started{channel, h, opts})
	l.start(channel, h, opts)
}

func (l *Listeners) start(channel string, h pglisten.Handler, opts pglisten.Options) {
	opts.Logger = l.log
	up := l.up.WithLabelValues(channel)
	up.Set(0)
	cfg := l.pool.Config().ConnConfig.Copy()
	l.wg.Add(1)
	safego.Go(l.ctx, "listener "+channel, l.log, func(ctx context.Context) {
		defer l.wg.Done()
		pglisten.Run(ctx, cfg, channel, gauged{h, up}, opts)
	})
}

// Restart ends every listener, waiting at most bound for each to close, and
// starts them again on new connections, as a database failover makes them
// do; it reports whether they all closed in time.
func (l *Listeners) Restart(bound time.Duration) bool {
	stopped := l.Stop(bound)
	l.ctx, l.cancel = context.WithCancel(l.parent)
	for _, s := range l.started {
		l.start(s.channel, s.h, s.opts)
	}
	return stopped
}

// Stop ends every listener and waits at most bound for their connections to
// close, so none outlives the pool. It reports whether they all did.
func (l *Listeners) Stop(bound time.Duration) bool {
	l.cancel()
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(bound):
		l.log.Warn("stopped with notification listeners still running", "bound", bound.String())
		return false
	}
}

// gauged sets a listener's gauge before telling its handler.
type gauged struct {
	pglisten.Handler
	up prometheus.Gauge
}

func (g gauged) Listening(up bool) {
	if up {
		g.up.Set(1)
	} else {
		g.up.Set(0)
	}
	g.Handler.Listening(up)
}
