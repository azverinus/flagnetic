package testkit

import "time"

// Eventually polls cond every 10 ms until it holds or within passes,
// reporting whether it held: a test waits for what it expects to happen and
// fails only past a deadline long enough for a loaded runner, instead of
// asserting how soon it happened.
func Eventually(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
