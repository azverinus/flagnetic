package events

import "time"

// SetPartitionLockTimeout shortens how long creating partitions waits for a
// lock, and returns what restores it.
func SetPartitionLockTimeout(d time.Duration) func() {
	old := partitionLockTimeout
	partitionLockTimeout = d
	return func() { partitionLockTimeout = old }
}
