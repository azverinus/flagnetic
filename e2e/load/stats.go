package load

import (
	"math"
	"slices"
	"time"
)

// Percentile returns the nearest-rank percentile q (0 to 1) of sorted, which
// must be in ascending order: the smallest sample that at least a fraction q
// of the samples do not exceed. It is zero for no samples.
func Percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(q * float64(len(sorted))))
	return sorted[min(max(rank, 1), len(sorted))-1]
}

// Result is what one scenario measured. Latencies are in milliseconds.
type Result struct {
	Scenario string `json:"scenario"`
	// Target is the scheduled arrival rate in requests per second; zero for
	// a scenario that is not a rate (the stream's fan-out).
	Target float64 `json:"target_rate,omitempty"`
	// Sent counts the samples taken, Errors those that failed.
	Sent   int `json:"sent"`
	Errors int `json:"errors"`
	// Achieved is the samples completed per second from the first
	// scheduled send to the last completion; below Target when the server
	// could not keep up.
	Achieved   float64 `json:"achieved_rate"`
	P50        float64 `json:"p50_ms"`
	P99        float64 `json:"p99_ms"`
	P999       float64 `json:"p999_ms"`
	Max        float64 `json:"max_ms"`
	FirstError string  `json:"first_error,omitempty"`
}

// Summarize turns the latencies of a scenario into its Result. elapsed is
// the time from the first scheduled send to the last completion.
func Summarize(scenario string, target float64, latencies []time.Duration, errs int, firstError string, elapsed time.Duration) Result {
	sorted := slices.Clone(latencies)
	slices.Sort(sorted)
	r := Result{
		Scenario: scenario, Target: target, Sent: len(sorted), Errors: errs, FirstError: firstError,
		P50: ms(Percentile(sorted, 0.50)), P99: ms(Percentile(sorted, 0.99)), P999: ms(Percentile(sorted, 0.999)),
	}
	if len(sorted) > 0 {
		r.Max = ms(sorted[len(sorted)-1])
	}
	if elapsed > 0 {
		r.Achieved = float64(len(sorted)) / elapsed.Seconds()
	}
	return r
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
