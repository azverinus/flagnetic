package worker

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/domain/events"
)

// Jitter is exported for its bounds test alone; nothing outside the runner
// needs to draw a delay.
var Jitter = jitter

// SetRollupTimeouts shortens a rollup round and the part of it kept for the
// lag, so the test of a round that runs to its deadline does not wait a
// minute. It returns the restoring function.
func SetRollupTimeouts(round, reserve time.Duration) func() {
	oldRound, oldReserve := rollupTimeout, lagReserve
	rollupTimeout, lagReserve = round, reserve
	return func() { rollupTimeout, lagReserve = oldRound, oldReserve }
}

// LagGauge returns the lag gauge of stream, for the test reading it.
func (m *RollupMetrics) LagGauge(stream events.Stream) prometheus.Gauge {
	return m.lag.WithLabelValues(string(stream))
}

// AttemptsAtOnce and AttemptsPerProject are exported for the test that the
// delivery lease outlasts what one claim takes.
const (
	AttemptsAtOnce     = attemptsAtOnce
	AttemptsPerProject = attemptsPerProject
)
