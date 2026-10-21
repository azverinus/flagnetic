// Package testclock is the test-only clock of FLAGNETIC_TEST_CLOCK_FILE: the
// wall clock moved ahead by the duration a file holds, such as "25h". It lets
// the end-to-end harness move a running binary through time (a session's
// idle timeout, an expiry) by rewriting the file. It reads a file, so it is
// an outbound adapter behind the clock port rather than part of the kernel.
package testclock

import (
	"os"
	"strings"
	"time"
)

// Clock reads its offset from the file on every call, so a change applies
// to the next request; a missing, empty or malformed file is no offset.
type Clock struct{ path string }

// New returns the clock whose offset is read from path.
func New(path string) Clock { return Clock{path: path} }

// Now returns the wall clock plus the offset.
func (c Clock) Now() time.Time { return time.Now().Add(c.Offset()) }

// Offset returns the duration the file holds, or zero.
func (c Clock) Offset() time.Duration {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return 0
	}
	d, err := time.ParseDuration(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return d
}
