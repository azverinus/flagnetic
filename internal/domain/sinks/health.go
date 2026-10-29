package sinks

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

// Health returns how s stands: disabled, stopped or healthy, until the
// streams that judge the rest exist.
func (s Sink) Health() Health {
	switch {
	case !s.Enabled:
		return HealthDisabled
	case s.StoppedAt != nil:
		return HealthStopped
	default:
		return HealthHealthy
	}
}
