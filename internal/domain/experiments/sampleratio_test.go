package experiments_test

import (
	"math"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var evenSplit = []experiments.Weight{{Variation: "control", Weight: 5000}, {Variation: "treatment", Weight: 5000}}

func near(got, want, relative float64) bool {
	return math.Abs(got-want) <= relative*math.Abs(want)
}

// The worked example: 5,200 and 4,800 first exposures against an even split
// are X² 16 at one degree of freedom, p ≈ 6.3·10⁻⁵.
func TestSampleRatioOfTheWorkedExample(t *testing.T) {
	r := experiments.CheckSampleRatio(evenSplit, map[string]int64{"control": 5200, "treatment": 4800})
	if r.N != 10000 || r.ChiSquare == nil || *r.ChiSquare != 16 || r.PValue == nil || !near(*r.PValue, 6.334248e-5, 1e-5) ||
		r.Skipped != "" {
		t.Fatalf("result = %+v, want N 10000, X² 16, p ≈ 6.334e-5", r)
	}
	want := []experiments.SampleRatioVariation{
		{Variation: "control", Observed: 5200, Expected: 5000, ObservedShare: 0.52, ExpectedShare: 0.5},
		{Variation: "treatment", Observed: 4800, Expected: 5000, ObservedShare: 0.48, ExpectedShare: 0.5},
	}
	for i, v := range r.Variations {
		if v != want[i] {
			t.Errorf("variation %d = %+v, want %+v", i, v, want[i])
		}
	}
}

func TestSampleRatioCountsOnlyTheExperimentsVariations(t *testing.T) {
	// A variation the experiment does not weigh, named by an override
	// exposure, is no part of the comparison.
	r := experiments.CheckSampleRatio(evenSplit, map[string]int64{"control": 5050, "treatment": 4950, "other": 700})
	if r.N != 10000 || *r.ChiSquare != 1 || !near(*r.PValue, 0.3173105, 1e-6) || len(r.Variations) != 2 {
		t.Fatalf("result = %+v, want N 10000, X² 1 over the two weighed variations", r)
	}
}

func TestSampleRatioIsSkippedBelowItsMinimums(t *testing.T) {
	for _, c := range []struct {
		name    string
		weights []experiments.Weight
		counts  map[string]int64
		want    experiments.SampleRatioSkip
	}{
		{"no exposure", evenSplit, nil, experiments.SkippedBelowMinimumSample},
		{"999 first exposures", evenSplit, map[string]int64{"control": 999}, experiments.SkippedBelowMinimumSample},
		// 9,960/40 expects 4 treatment units in 1,000, too few for the
		// chi-square approximation, however lopsided the counts are.
		{"an expected count of 4", []experiments.Weight{{Variation: "control", Weight: 9960}, {Variation: "treatment", Weight: 40}},
			map[string]int64{"control": 1000}, experiments.SkippedExpectedBelow5},
		// A variation weighed 0 expects nothing whatever N is.
		{"a zero weight", []experiments.Weight{{Variation: "control", Weight: 10000}, {Variation: "treatment", Weight: 0}},
			map[string]int64{"control": 50000}, experiments.SkippedExpectedBelow5},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := experiments.CheckSampleRatio(c.weights, c.counts)
			if r.Skipped != c.want || r.ChiSquare != nil || r.PValue != nil {
				t.Errorf("result = %+v, want skipped as %s with no statistic", r, c.want)
			}
		})
	}
	// Exactly at both minimums the check runs.
	r := experiments.CheckSampleRatio([]experiments.Weight{{Variation: "control", Weight: 9950}, {Variation: "treatment", Weight: 50}},
		map[string]int64{"control": 995, "treatment": 5})
	if r.Skipped != "" || r.PValue == nil || *r.ChiSquare != 0 {
		t.Errorf("at 1,000 and an expected 5 = %+v, want a check run at X² 0", r)
	}
}

func TestSampleRatioObservedSharesWithoutExposures(t *testing.T) {
	r := experiments.CheckSampleRatio(evenSplit, nil)
	for _, v := range r.Variations {
		if v.ObservedShare != 0 || v.Expected != 0 || v.ExpectedShare != 0.5 {
			t.Errorf("variation = %+v, want no observed share and half expected", v)
		}
	}
}

func pValue(p float64) experiments.SampleRatio {
	return experiments.SampleRatio{N: 10000, ChiSquare: new(float64), PValue: &p}
}

// An alert is raised below 0.001 and cleared above 0.01; between the two, and
// on a skipped check, it stays as it is.
func TestSampleRatioAlertHysteresis(t *testing.T) {
	for _, c := range []struct {
		r      experiments.SampleRatio
		active bool
		want   experiments.AlertChange
	}{
		{pValue(0.0009), false, experiments.AlertRaise},
		{pValue(0.0009), true, experiments.AlertKeep},
		{pValue(0.001), false, experiments.AlertKeep},
		{pValue(0.005), false, experiments.AlertKeep},
		{pValue(0.005), true, experiments.AlertKeep},
		{pValue(0.01), true, experiments.AlertKeep},
		{pValue(0.0101), true, experiments.AlertClear},
		{pValue(0.5), false, experiments.AlertKeep},
		{experiments.SampleRatio{N: 10, Skipped: experiments.SkippedBelowMinimumSample}, true, experiments.AlertKeep},
		{experiments.SampleRatio{N: 10, Skipped: experiments.SkippedBelowMinimumSample}, false, experiments.AlertKeep},
	} {
		if got := c.r.AlertChange(c.active); got != c.want {
			t.Errorf("p %v, active %v: %v, want %v", c.r.PValue, c.active, got, c.want)
		}
	}
}

func TestAlertAcknowledgedOnce(t *testing.T) {
	raised := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	a := experiments.RaiseAlert(id.Of[experiments.Alert]{}, experiments.ExperimentID{}, nil, experiments.AlertKindSampleRatio, raised,
		experiments.AlertDetails{})
	if !a.Active() || a.AcknowledgedAt != nil {
		t.Fatalf("raised alert = %+v, want active and unacknowledged", a)
	}
	first, changed := a.Acknowledge(raised.Add(time.Minute))
	if !changed || first.AcknowledgedAt == nil || !first.AcknowledgedAt.Equal(raised.Add(time.Minute)) {
		t.Fatalf("acknowledged = %+v, %v, want acknowledged a minute later", first, changed)
	}
	again, changed := first.Acknowledge(raised.Add(time.Hour))
	if changed || !again.AcknowledgedAt.Equal(*first.AcknowledgedAt) {
		t.Errorf("a second acknowledgement = %+v, %v, want the first kept", again, changed)
	}
	cleared := first.Clear(raised.Add(2 * time.Hour))
	if cleared.Active() || !cleared.ClearedAt.Equal(raised.Add(2*time.Hour)) {
		t.Errorf("cleared = %+v, want inactive since then", cleared)
	}
}
