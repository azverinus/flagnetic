package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
)

// catchingUp is a rollup stream far behind: Handle commits batches until its
// context ends, and Lag fails on a context that has ended.
type catchingUp struct{ lag time.Duration }

func (catchingUp) Handle(ctx context.Context) (int, error) {
	<-ctx.Done()
	return 1, ctx.Err()
}

func (c catchingUp) Lag(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return c.lag, nil
}

func TestARollupRoundThatCatchesUpToItsDeadlineStillSetsTheLagGauge(t *testing.T) {
	defer worker.SetRollupTimeouts(400*time.Millisecond, 200*time.Millisecond)()
	reg := prometheus.NewRegistry()
	m := worker.NewRollupMetrics(reg)
	f := newRunner(t, worker.RollupStream("exposure_rollup", events.StreamExposures, catchingUp{lag: 90 * time.Second}, m))

	if err := f.runner.Tick(t.Context(), "exposure_rollup"); err != nil {
		t.Fatalf("Tick = %v, want a round that moved the cursor to count as done", err)
	}
	if got := testutil.ToFloat64(m.LagGauge(events.StreamExposures)); got != 90 {
		t.Errorf("flagnetic_rollup_lag_seconds = %v, want the stream's lag of 90", got)
	}
	f.expect(t, "exposure_rollup", 1, 0, 0)
}
