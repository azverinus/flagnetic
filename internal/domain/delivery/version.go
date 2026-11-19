package delivery

import "time"

// RulesetVersionFloor is the lowest version a ruleset changed at now may
// carry: the application clock in milliseconds since the Unix epoch.
func RulesetVersionFloor(now time.Time) int64 { return now.UnixMilli() }
