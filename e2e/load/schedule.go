// Package load measures the SDK API's latency under a fixed arrival rate.
//
// The generator is an open model: requests are sent on a schedule fixed
// before the run, whatever the answers do, and each latency is measured from
// the scheduled send time. A closed model (a fixed number of workers, each
// sending when its last request returns) slows down together with the server
// and so leaves the queue it builds out of the numbers.
package load

import (
	"math"
	"time"
)

// Offsets returns the send times of a run of the given length at rate
// requests per second, as offsets from its start: request i is due at
// i/rate. A run sends rate*duration requests, rounded down, and none at a
// rate or duration that is not positive.
func Offsets(rate float64, duration time.Duration) []time.Duration {
	if rate <= 0 || duration <= 0 {
		return nil
	}
	n := int(math.Floor(rate*duration.Seconds() + 1e-9))
	offsets := make([]time.Duration, n)
	for i := range offsets {
		offsets[i] = time.Duration(float64(i) / rate * float64(time.Second))
	}
	return offsets
}
