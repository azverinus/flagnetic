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
		Timeout: time.Minute,
		Round: func(ctx context.Context) error {
			_, err := r.Handle(ctx)
			lag, lagErr := r.Lag(ctx)
			if lagErr == nil {
				m.lag.WithLabelValues(string(stream)).Set(lag.Seconds())
			}
			return errors.Join(err, lagErr)
		},
	}
}
