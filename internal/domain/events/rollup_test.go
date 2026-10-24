package events_test

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	rollupEnvironment = id.MustParse[workspace.Environment]("0190a000-0000-7000-8000-000000000001")
	rollupExperiment  = id.MustParse[experiments.Experiment]("0190a000-0000-7000-8000-000000000002")
	rollupProject     = id.MustParse[workspace.Project]("0190a000-0000-7000-8000-000000000003")
	day1              = time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
)

func unit(u string) events.UnitKey {
	return events.UnitKey{Environment: rollupEnvironment, Experiment: rollupExperiment, UnitType: "user", UnitID: u}
}

// exposureRow is an exposure of unit u to variation v numbered n, occurring
// minutes after the start of day1 and received hours after it.
func exposureRow(n int, u, v string, minutes, hours int) events.ExposureRow {
	return events.ExposureRow{
		Project: rollupProject, Unit: unit(u), EventID: id.MustParse[delivery.Event](eventIDAt(day1, n)), Variation: v,
		OccurredAt: day1.Add(time.Duration(minutes) * time.Minute), ReceivedAt: day1.Add(time.Duration(hours) * time.Hour),
	}
}

func override(r events.ExposureRow) events.ExposureRow {
	r.Override = true
	return r
}

// rollUp applies batches one after the other as the rollup does, each
// against the records the ones before it left, and returns the records and
// the counts summed over every batch.
func rollUp(batches ...[]events.ExposureRow) (map[events.UnitKey]events.FirstExposure, map[events.ExposureDay]events.ExposureDayCounts, map[events.EventDay]int64) {
	records := map[events.UnitKey]events.FirstExposure{}
	days := map[events.ExposureDay]events.ExposureDayCounts{}
	kinds := map[events.EventDay]int64{}
	for _, b := range batches {
		stored := map[events.UnitKey]events.FirstExposure{}
		for _, r := range b {
			if rec, ok := records[r.Unit]; ok {
				stored[r.Unit] = rec
			}
		}
		out := events.RollUpExposures(stored, b)
		maps.Copy(records, out.Records)
		for k, c := range out.Days {
			sum := days[k]
			sum.Exposures += c.Exposures
			sum.Overridden += c.Overridden
			sum.FirstExposures += c.FirstExposures
			days[k] = sum
		}
		for k, n := range out.Events {
			kinds[k] += n
		}
	}
	for k, c := range days {
		if c == (events.ExposureDayCounts{}) {
			delete(days, k)
		}
	}
	return records, days, kinds
}

func TestTheFirstExposureIsTheEarliestOccurrenceNotTheFirstArrival(t *testing.T) {
	records, _, _ := rollUp(
		[]events.ExposureRow{exposureRow(1, "u", "control", 1, 1), exposureRow(2, "u", "control", 2, 1)},
	)
	got := records[unit("u")]
	if got.Variation != "control" || got.Conflicts() != 0 {
		t.Fatalf("two exposures of one variation = %+v, want control with no conflict", got)
	}

	records, _, _ = rollUp(
		[]events.ExposureRow{exposureRow(1, "u", "control", 1, 1), exposureRow(2, "u", "control", 2, 1)},
		[]events.ExposureRow{exposureRow(3, "u", "treatment", 3, 1)},
	)
	got = records[unit("u")]
	if got.Variation != "control" || got.Conflicts() != 1 {
		t.Fatalf("after a later exposure of another variation = %+v, want control kept and 1 conflict", got)
	}
	if v, at, ok := got.Conflicting(); !ok || v != "treatment" || !at.Equal(day1.Add(3*time.Minute)) {
		t.Errorf("conflicting = %q at %v, want treatment at minute 3", v, at)
	}

	records, _, _ = rollUp(
		[]events.ExposureRow{exposureRow(1, "u", "control", 1, 1), exposureRow(2, "u", "control", 2, 1)},
		[]events.ExposureRow{exposureRow(3, "u", "treatment", 3, 1)},
		[]events.ExposureRow{exposureRow(4, "u", "treatment", 0, 2)},
	)
	got = records[unit("u")]
	if got.Variation != "treatment" || got.Conflicts() != 2 || !got.OccurredAt.Equal(day1) {
		t.Fatalf("after a late earlier exposure = %+v, want it replacing the record with 2 conflicts", got)
	}
	if v, at, ok := got.Conflicting(); !ok || v != "control" || !at.Equal(day1.Add(2*time.Minute)) {
		t.Errorf("conflicting = %q at %v, want control at minute 2", v, at)
	}
}

func TestATieInOccurrenceGoesToTheSmallerEventID(t *testing.T) {
	records, _, _ := rollUp([]events.ExposureRow{exposureRow(2, "u", "treatment", 5, 1), exposureRow(1, "u", "control", 5, 1)})
	if got := records[unit("u")]; got.Variation != "control" || got.EventID != id.MustParse[delivery.Event](eventIDAt(day1, 1)) {
		t.Errorf("record = %+v, want the exposure with the smaller event ID", got)
	}
}

func TestAnOverrideExposureIsCountedApartAndNeverTouchesTheFirstExposure(t *testing.T) {
	records, days, _ := rollUp([]events.ExposureRow{
		exposureRow(1, "u", "control", 1, 1), override(exposureRow(2, "u", "treatment", 0, 1)),
		override(exposureRow(3, "v", "control", 0, 1)),
	})
	if got := records[unit("u")]; got.Variation != "control" || got.Conflicts() != 0 {
		t.Errorf("record of u = %+v, want control with no conflict", got)
	}
	if _, ok := records[unit("v")]; ok {
		t.Error("a unit seen only through an override has a first exposure")
	}
	key := func(v string) events.ExposureDay {
		return events.ExposureDay{Environment: rollupEnvironment, Experiment: rollupExperiment, Variation: v, Day: day1}
	}
	if got := days[key("control")]; got != (events.ExposureDayCounts{Exposures: 1, Overridden: 1, FirstExposures: 1}) {
		t.Errorf("control's day = %+v, want 1 exposure, 1 overridden, 1 first exposure", got)
	}
	if got := days[key("treatment")]; got != (events.ExposureDayCounts{Overridden: 1}) {
		t.Errorf("treatment's day = %+v, want 1 overridden", got)
	}
}

func TestAReplacementMovesTheFirstExposureToItsOwnDay(t *testing.T) {
	// The first arrives on day 1; a second, received a day later, occurred a
	// minute before it with the other variation.
	_, days, kinds := rollUp(
		[]events.ExposureRow{exposureRow(1, "u", "control", 0, 1)},
		[]events.ExposureRow{exposureRow(2, "u", "treatment", -1, 25)},
	)
	day0, day2 := day1.AddDate(0, 0, -1), day1.AddDate(0, 0, 1)
	want := map[events.ExposureDay]events.ExposureDayCounts{
		{Environment: rollupEnvironment, Experiment: rollupExperiment, Variation: "control", Day: day1}:   {Exposures: 1},
		{Environment: rollupEnvironment, Experiment: rollupExperiment, Variation: "treatment", Day: day2}: {Exposures: 1},
		{Environment: rollupEnvironment, Experiment: rollupExperiment, Variation: "treatment", Day: day0}: {FirstExposures: 1},
	}
	if !maps.Equal(days, want) {
		t.Errorf("days = %+v, want %+v", days, want)
	}
	wantKinds := map[events.EventDay]int64{
		{Project: rollupProject, Day: day1, Type: events.EventExposure}: 1,
		{Project: rollupProject, Day: day2, Type: events.EventExposure}: 1,
	}
	if !maps.Equal(kinds, wantKinds) {
		t.Errorf("event counts = %+v, want %+v", kinds, wantKinds)
	}
}

// The first exposures, conflicts and counts of a set of exposures are the same
// whatever order they arrive in and however they are split into batches.
func TestTheRollupIsTheSameForEveryOrderAndEverySplit(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // a reproducible property test
	for trial := range 300 {
		var rows []events.ExposureRow
		for n := range 1 + rng.IntN(30) {
			r := exposureRow(n+1, fmt.Sprintf("u-%d", rng.IntN(4)), []string{"a", "b", "c"}[rng.IntN(3)],
				rng.IntN(5)-2, rng.IntN(72))
			if rng.IntN(5) == 0 {
				r = override(r)
			}
			rows = append(rows, r)
		}
		wantRecords, wantDays, wantKinds := rollUp(rows)

		shuffled := append([]events.ExposureRow(nil), rows...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		var batches [][]events.ExposureRow
		for len(shuffled) > 0 {
			k := 1 + rng.IntN(len(shuffled))
			batches = append(batches, shuffled[:k])
			shuffled = shuffled[k:]
		}
		records, days, kinds := rollUp(batches...)

		if !maps.EqualFunc(records, wantRecords, sameRecord) {
			t.Fatalf("trial %d: records %+v from %d batches, want %+v", trial, records, len(batches), wantRecords)
		}
		if !maps.Equal(days, wantDays) {
			t.Fatalf("trial %d: days %+v from %d batches, want %+v", trial, days, len(batches), wantDays)
		}
		if !maps.Equal(kinds, wantKinds) {
			t.Fatalf("trial %d: event counts %+v, want %+v", trial, kinds, wantKinds)
		}
	}
}

func sameRecord(a, b events.FirstExposure) bool {
	av, aat, aok := a.Conflicting()
	bv, bat, bok := b.Conflicting()
	return a.EventID == b.EventID && a.OccurredAt.Equal(b.OccurredAt) && a.Variation == b.Variation &&
		maps.Equal(a.Seen, b.Seen) && a.Conflicts() == b.Conflicts() && av == bv && aat.Equal(bat) && aok == bok
}

func TestTheSharesOfFirstExposuresFollowTheExperimentsVariationsThenTheOthers(t *testing.T) {
	weights := []experiments.Weight{{Variation: "b", Weight: 2500}, {Variation: "a", Weight: 7500}}
	counts := map[string]events.ExposureDayCounts{
		"a": {Exposures: 4, FirstExposures: 3}, "z": {Overridden: 2}, "b": {Exposures: 1, FirstExposures: 1},
	}
	got := events.VariationExposuresOf(weights, counts)
	want := []events.VariationExposures{
		{Variation: "b", ExposureDayCounts: events.ExposureDayCounts{Exposures: 1, FirstExposures: 1}, ObservedShare: 0.25, ExpectedShare: 0.25},
		{Variation: "a", ExposureDayCounts: events.ExposureDayCounts{Exposures: 4, FirstExposures: 3}, ObservedShare: 0.75, ExpectedShare: 0.75},
		{Variation: "z", ExposureDayCounts: events.ExposureDayCounts{Overridden: 2}},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("variations = %+v, want %+v", got, want)
	}
	if got := events.VariationExposuresOf(weights, nil); got[0].ObservedShare != 0 || len(got) != 2 {
		t.Errorf("with no exposures = %+v, want both listed at zero", got)
	}
}
