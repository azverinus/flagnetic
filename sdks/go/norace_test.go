//go:build !race

package flagnetic_test

// raceEnabled is true when the tests run under the race detector, whose
// overhead makes timing measurements meaningless.
const raceEnabled = false
