package worker

import (
	"context"
	"time"
)

// StaleFlagDigestName names the stale-flag digest worker in logs, metrics
// and Tick.
const StaleFlagDigestName = "stale_flag_digest"

// StaleFlagDigest returns the worker sending the weekly stale-flag digest,
// which run does only within the hour of the slot: every replica runs it
// every minute, so a slot is found within a minute of its start, and the
// round's lock and the slot's row keep it from being sent twice.
func StaleFlagDigest(run func(ctx context.Context) error) Worker {
	// A round reads the stale flags of every project, one after the other.
	return Worker{Name: StaleFlagDigestName, Interval: time.Minute, Timeout: 5 * time.Minute, Round: run}
}
