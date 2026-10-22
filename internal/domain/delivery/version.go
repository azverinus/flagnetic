package delivery

import "time"

// RulesetVersionFloor is the lowest version a ruleset changed at now may
// carry: the application clock in milliseconds since the Unix epoch.
func RulesetVersionFloor(now time.Time) int64 { return now.UnixMilli() }

// NextRulesetVersion returns the version a ruleset changed at now takes after
// previous: the greater of previous plus one and the clock in milliseconds.
//
// Counting one by one alone, a database restored or failed over to a state
// that lost commits would hand out again a version some SDK already holds
// with other content, and that SDK, which only ever accepts a higher
// version, would keep the lost content. Taking the clock as the floor numbers
// the first write after such a loss above every version handed out before
// it (as long as the clock has moved past them, which a few milliseconds of
// lost commits guarantee), while plus one keeps versions strictly increasing
// when the clock stands still or moves back. Millisecond readings stay far
// below 2^53, the largest integer every SDK reads exactly from JSON.
func NextRulesetVersion(previous int64, now time.Time) int64 {
	return max(previous+1, RulesetVersionFloor(now))
}
