package experiments

import (
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The sample-ratio check's bounds. Below a thousand first exposures a split
// is too noisy to judge; an expected count under five is where the
// chi-square approximation stops holding. The two thresholds apart keep an
// alert from flapping while the p-value hovers around one of them.
const (
	SampleRatioMinimumSample   = 1000
	SampleRatioMinimumExpected = 5
	SampleRatioRaiseBelow      = 0.001
	SampleRatioClearAbove      = 0.01
)

// SampleRatioSkip is why a sample-ratio check computed no statistic; "" when
// it did.
type SampleRatioSkip string

// The reasons a check is skipped.
const (
	SkippedBelowMinimumSample SampleRatioSkip = "below_minimum_sample"
	SkippedExpectedBelow5     SampleRatioSkip = "expected_below_5"
)

// SampleRatioVariation is one variation's side of a sample-ratio check: its
// first exposures and the count its weight expects of all of them.
type SampleRatioVariation struct {
	Variation     string
	Observed      int64
	Expected      float64
	ObservedShare float64
	ExpectedShare float64
}

// SampleRatio is one sample-ratio check of an experiment in one environment.
// ChiSquare and PValue are nil when the check was skipped, and Skipped says
// why.
type SampleRatio struct {
	N          int64
	ChiSquare  *float64
	PValue     *float64
	Skipped    SampleRatioSkip
	Variations []SampleRatioVariation
}

// CheckSampleRatio compares the first exposures per variation with weights:
// X² = Σ (O − E)² / E over the weighed variations, E = N × weight / 10,000,
// with one degree of freedom fewer than there are variations. First
// exposures of a variation the experiment does not weigh, which only an
// override exposure names, are no part of it.
func CheckSampleRatio(weights []Weight, firstExposures map[string]int64) SampleRatio {
	var r SampleRatio
	for _, w := range weights {
		r.N += firstExposures[w.Variation]
	}
	r.Variations = make([]SampleRatioVariation, len(weights))
	for i, w := range weights {
		v := SampleRatioVariation{
			Variation: w.Variation, Observed: firstExposures[w.Variation],
			Expected:      float64(r.N) * float64(w.Weight) / TotalWeight,
			ExpectedShare: float64(w.Weight) / TotalWeight,
		}
		if r.N > 0 {
			v.ObservedShare = float64(v.Observed) / float64(r.N)
		}
		r.Variations[i] = v
	}
	if r.N < SampleRatioMinimumSample {
		r.Skipped = SkippedBelowMinimumSample
		return r
	}
	var x2 float64
	for _, v := range r.Variations {
		if v.Expected < SampleRatioMinimumExpected {
			r.Skipped = SkippedExpectedBelow5
			return r
		}
		d := float64(v.Observed) - v.Expected
		x2 += d * d / v.Expected
	}
	p := ChiSquarePValue(x2, len(weights)-1)
	r.ChiSquare, r.PValue = &x2, &p
	return r
}

// AlertChange is what a check does to the alert of its kind.
type AlertChange int

// The changes.
const (
	AlertKeep AlertChange = iota
	AlertRaise
	AlertClear
)

// AlertChange returns what r does to the sample-ratio alert, active or not:
// raise one below the raise threshold, clear the active one above the clear
// threshold, and otherwise, a skipped check included, leave it as it is.
func (r SampleRatio) AlertChange(active bool) AlertChange {
	switch {
	case r.PValue == nil:
		return AlertKeep
	case !active && *r.PValue < SampleRatioRaiseBelow:
		return AlertRaise
	case active && *r.PValue > SampleRatioClearAbove:
		return AlertClear
	}
	return AlertKeep
}

// AlertID identifies an alert.
type AlertID = id.Of[Alert]

// AlertKind is the condition an alert is about.
type AlertKind string

// AlertKindSampleRatio is a sample-ratio mismatch.
const AlertKindSampleRatio AlertKind = "srm"

// AlertDetails is what the check found when it raised an alert: the block of
// the alert's kind, every other one nil.
type AlertDetails struct {
	SampleRatio       *SampleRatio
	NoExposures       *NoExposures
	ExposureConflicts *ExposureConflicts
	PastExpectedEnd   *PastExpectedEnd
}

// Alert is a condition a check found on an experiment in one environment, or
// in the experiment as a whole when Environment is nil. It is active until it
// clears; an acknowledgement hides it until then, and a condition returning
// after it cleared is a new alert.
type Alert struct {
	ID             AlertID
	Experiment     ExperimentID
	Environment    *workspace.EnvironmentID
	Kind           AlertKind
	RaisedAt       time.Time
	ClearedAt      *time.Time
	AcknowledgedAt *time.Time
	Details        AlertDetails
}

// RaiseAlert returns a new active alert of kind raised at now.
func RaiseAlert(alert AlertID, experiment ExperimentID, environment *workspace.EnvironmentID, kind AlertKind, now time.Time,
	details AlertDetails,
) Alert {
	return Alert{ID: alert, Experiment: experiment, Environment: environment, Kind: kind, RaisedAt: now, Details: details}
}

// Active reports whether the alert has not cleared.
func (a Alert) Active() bool { return a.ClearedAt == nil }

// Clear returns the alert cleared at now.
func (a Alert) Clear(now time.Time) Alert {
	a.ClearedAt = &now
	return a
}

// Acknowledge returns the alert acknowledged at now, and whether that changed
// it: the first acknowledgement is the one kept.
func (a Alert) Acknowledge(now time.Time) (Alert, bool) {
	if a.AcknowledgedAt != nil {
		return a, false
	}
	a.AcknowledgedAt = &now
	return a, true
}
