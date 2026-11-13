package sinks

import "time"

// Health is how a sink stands, read from its state on every read.
type Health string

// The healths, in the order they are judged: the first that holds is the
// sink's.
const (
	// HealthDisabled is a sink a member disabled, or never enabled.
	HealthDisabled Health = "disabled"
	// HealthStopped is a sink whose delivery stopped itself after the target
	// kept rejecting batches, until it is resumed.
	HealthStopped Health = "stopped"
	// HealthFailing is a sink with a stream that cannot reach its target.
	HealthFailing Health = "failing"
	// HealthDegraded is a sink with a dead letter waiting.
	HealthDegraded Health = "degraded"
	// HealthLagging is a sink whose oldest undelivered event is older than
	// the operator tolerates.
	HealthLagging Health = "lagging"
	// HealthHealthy is a sink with none of the above.
	HealthHealthy Health = "healthy"
)

// Health returns how s stands, judged from the state the store read with it:
// disabled or stopped, a stream that failed its last call, a dead letter
// waiting, the oldest undelivered row older than lagWarning, or none of these.
func (s Sink) Health(lagWarning time.Duration) Health {
	switch {
	case !s.Enabled:
		return HealthDisabled
	case s.StoppedAt != nil:
		return HealthStopped
	case s.Failing():
		return HealthFailing
	case s.DeadLetters > 0:
		return HealthDegraded
	case s.Lag != nil && *s.Lag > lagWarning:
		return HealthLagging
	default:
		return HealthHealthy
	}
}

// Failing reports whether any stream of s failed its last call.
func (s Sink) Failing() bool {
	for _, st := range s.Streams {
		if st.Failing() {
			return true
		}
	}
	return false
}

// AtRisk reports whether a sink whose oldest undelivered row is lag old is
// about to lose data: retention drops a row once it is older than retention,
// so a row that is within warning of that age will be dropped before it is
// delivered unless the sink catches up or retention grows. It is judged only
// for a sink that has an undelivered row, and independently of the sink's
// health, since a disabled or stopped sink loses rows just the same.
func AtRisk(lag, retention, warning time.Duration) bool {
	return lag >= retention-warning
}
