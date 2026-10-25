package worker

import (
	"context"
	"time"
)

// ExperimentChecksName names the worker running the experiments' checks in
// logs, metrics and Tick.
const ExperimentChecksName = "experiment_checks"

// ExperimentChecks returns the worker running the checks of every running
// experiment once every interval. Every replica runs it; the one holding the
// checks' lock in PostgreSQL runs a round, the others skip theirs.
func ExperimentChecks(interval time.Duration, run func(ctx context.Context) error) Worker {
	// A round checks each experiment under a bound of its own; the round's
	// bound only keeps a stuck round from holding the lock past the next.
	return Worker{Name: ExperimentChecksName, Interval: interval, Timeout: interval / 2, Round: run}
}
