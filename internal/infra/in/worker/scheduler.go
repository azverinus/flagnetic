package worker

import (
	"context"
	"time"
)

// ChangeRequestSchedulerName names the worker applying scheduled change
// requests in logs, metrics and Tick.
const ChangeRequestSchedulerName = "change_request_scheduler"

// ChangeRequestScheduler returns the worker applying the approved change
// requests whose apply time has come, once every interval. Every replica
// runs it: each application claims its change request by its project's row,
// so a change request is applied once however many replicas find it due.
func ChangeRequestScheduler(interval time.Duration, run func(ctx context.Context) error) Worker {
	// Each change request is applied in a transaction of its own, so a
	// round cut short leaves the rest to the next.
	return Worker{Name: ChangeRequestSchedulerName, Interval: interval, Timeout: time.Minute, Round: run}
}
