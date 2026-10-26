package experiments

import "time"

// The other kinds of alert, beside the sample-ratio mismatch.
const (
	// AlertKindNoExposures is a running experiment no SDK reported an
	// exposure of for a day in an environment holding its rule: the
	// exposure call is most likely missing from the code.
	AlertKindNoExposures AlertKind = "no_exposures"
	// AlertKindExposureConflicts is an environment where too many units
	// were exposed to another variation than their first one.
	AlertKindExposureConflicts AlertKind = "exposure_conflicts"
	// AlertKindPastExpectedEnd is a running experiment past its expected
	// end, about the experiment as a whole.
	AlertKindPastExpectedEnd AlertKind = "past_expected_end"
)

// NoExposuresAfter is how long a running experiment may go without an
// exposure in an environment before it is alerted: a day covers every
// hour of a daily traffic cycle, so a quiet night raises nothing.
const NoExposuresAfter = 24 * time.Hour

// NoExposures is what the no-exposures check found: when the server last
// received an exposure, nil when it never did, and since when none came,
// the later of that exposure and the start.
type NoExposures struct {
	LastExposureAt *time.Time
	Since          time.Time
}

// CheckNoExposures returns what the no-exposures check finds at now of an
// experiment started at started whose latest exposure in an environment, an
// override's excluded, was received at last, nil when none was, and whether
// it raises the alert: no exposure for NoExposuresAfter since the start or
// since the last one.
func CheckNoExposures(started time.Time, last *time.Time, now time.Time) (NoExposures, bool) {
	r := NoExposures{LastExposureAt: last, Since: started}
	if last != nil && last.After(started) {
		r.Since = *last
	}
	return r, !now.Before(r.Since.Add(NoExposuresAfter))
}

// The bounds of the conflicts threshold: a handful of conflicts is noise in
// any experiment, and ten is too many in the largest.
const (
	conflictThresholdFloor   = 3
	conflictThresholdCeiling = 10
	conflictThresholdPer     = 1000
)

// ConflictThreshold is how many conflicting exposures raise the conflicts
// alert in an environment with firstExposures first exposures: 0.1 % of
// them, rounded up, never below 3 and never above 10.
func ConflictThreshold(firstExposures int64) int64 {
	share := (firstExposures + conflictThresholdPer - 1) / conflictThresholdPer
	return max(conflictThresholdFloor, min(conflictThresholdCeiling, share))
}

// ExposureConflicts is what the conflicts check found in an environment:
// the exposures naming another variation than their unit's first one, the
// units with any, the first exposures and the threshold they make.
type ExposureConflicts struct {
	Conflicts        int64
	ConflictingUnits int64
	FirstExposures   int64
	Threshold        int64
}

// CheckExposureConflicts returns the conflicts check of conflicts
// exposures, from units units, against firstExposures first exposures.
func CheckExposureConflicts(conflicts, units, firstExposures int64) ExposureConflicts {
	return ExposureConflicts{
		Conflicts: conflicts, ConflictingUnits: units, FirstExposures: firstExposures,
		Threshold: ConflictThreshold(firstExposures),
	}
}

// Exceeded reports whether the conflicts reach the threshold.
func (c ExposureConflicts) Exceeded() bool { return c.Conflicts >= c.Threshold }

// PastExpectedEnd is the expected end a running experiment passed.
type PastExpectedEnd struct {
	ExpectedEndAt time.Time
}

// PastExpectedEnd reports whether e is running past its expected end at now:
// from the UTC day after the expected end, so the end day itself is still
// the experiment's.
func (e Experiment) PastExpectedEnd(now time.Time) bool {
	return e.Status == StatusRunning && e.ExpectedEndAt != nil && day(now).After(*e.ExpectedEndAt)
}

// PastExpectedEndDetails is what the past-expected-end alert records of e.
func (e Experiment) PastExpectedEndDetails() PastExpectedEnd {
	var r PastExpectedEnd
	if e.ExpectedEndAt != nil {
		r.ExpectedEndAt = *e.ExpectedEndAt
	}
	return r
}

// ConditionChange returns what a check whose condition holds or not does to
// the alert of its kind, active or not: raise one while it holds, clear it
// once it does not.
func ConditionChange(holds, active bool) AlertChange {
	switch {
	case holds && !active:
		return AlertRaise
	case !holds && active:
		return AlertClear
	}
	return AlertKeep
}
