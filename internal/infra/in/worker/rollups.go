package worker

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// ExposureRollupName names the worker rolling exposures up in logs, metrics
// and Tick.
const ExposureRollupName = "exposure_rollup"

// SummaryRollupName names the worker rolling evaluation summaries up in
// logs, metrics and Tick.
const SummaryRollupName = "summary_rollup"

// AliasRollupName names the worker counting aliases in logs, metrics and
// Tick.
const AliasRollupName = "alias_rollup"

// rollupInterval is how often a rollup stream reads what was committed since
// its last round: the experiment pages are this far behind the SDKs at most,
// besides the time a long transaction holds the horizon back.
const rollupInterval = 10 * time.Second

// rollupTimeout bounds one round; lagReserve is the end of it kept for
// measuring the lag, so a round that catches up until its deadline still
// reports how far behind the stream is.
var (
	rollupTimeout = time.Minute
	lagReserve    = 5 * time.Second
)

// RollupMetrics are the series every rollup stream reports.
type RollupMetrics struct {
	lag *prometheus.GaugeVec
}

// NewRollupMetrics registers the rollup streams' series on reg.
func NewRollupMetrics(reg prometheus.Registerer) *RollupMetrics {
	m := &RollupMetrics{
		lag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metrics.Namespace, Subsystem: "rollup", Name: "lag_seconds",
			Help: "Age of the oldest committed raw event a rollup stream has not read yet, 0 when it has read them all; it grows while a long transaction holds the snapshot horizon back.",
		}, []string{"stream"}),
	}
	reg.MustRegister(m.lag)
	return m
}

// Rollup is the use case one rollup stream runs.
type Rollup interface {
	// Handle applies every row the stream has not read yet and returns how
	// many it read.
	Handle(ctx context.Context) (int, error)
	// Lag returns the age of the oldest row the stream has not read.
	Lag(ctx context.Context) (time.Duration, error)
}

// RollupStream returns the worker moving stream with r every ten seconds,
// named name, and measuring its lag after each round. Every replica runs it;
// they take turns on the stream's lock in PostgreSQL.
func RollupStream(name string, stream events.Stream, r Rollup, m *RollupMetrics) Worker {
	m.lag.WithLabelValues(string(stream))
	return Worker{
		Name: name, Interval: rollupInterval,
		// A stream far behind catches up in bounded batches, each committed
		// with its cursor: a round cut short by its timeout loses nothing,
		// and the next goes on from there.
		Timeout: rollupTimeout,
		Round: func(ctx context.Context) error {
			// Handle stops lagReserve before the round does, so the lag is
			// measured while a stream far behind is still catching up, which
			// is when the gauge is needed. Reaching that stop is the round's
			// plan, not a failure: the batches it committed moved the cursor.
			handleCtx, stop := context.WithTimeout(ctx, rollupTimeout-lagReserve)
			_, err := r.Handle(handleCtx)
			handleStopped := handleCtx.Err() != nil && ctx.Err() == nil
			stop()
			if handleStopped && errors.Is(err, context.DeadlineExceeded) {
				err = nil
			}
			// The lag is measured on a context of its own: the round's may
			// be cancelled by a stopping runner, which must not leave the
			// gauge stale.
			lagCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lagReserve)
			defer cancel()
			lag, lagErr := r.Lag(lagCtx)
			if lagErr == nil {
				m.lag.WithLabelValues(string(stream)).Set(lag.Seconds())
			}
			return errors.Join(err, lagErr)
		},
	}
}
