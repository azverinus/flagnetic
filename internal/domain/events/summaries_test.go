package events_test

import (
	"errors"
	"maps"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	checkout = id.MustParse[flags.Flag]("0190a000-0000-7000-8000-000000000004")
	banner   = id.MustParse[flags.Flag]("0190a000-0000-7000-8000-000000000005")
)

// summaryRow counts n evaluations of flag serving variation for reason,
// reported by an SDK over an interval ending minutes after the start of day1
// and received hours after it; first marks the summary's first row.
func summaryRow(flag flags.FlagID, variation, reason string, n int64, minutes, hours int, first bool) events.SummaryRow {
	return events.SummaryRow{
		Project: rollupProject, Environment: rollupEnvironment, Flag: flag, Source: delivery.SourceSDK, First: first,
		Variation: variation, Reason: reason, Count: n,
		PeriodEnd: day1.Add(time.Duration(minutes) * time.Minute), ReceivedAt: day1.Add(time.Duration(hours) * time.Hour),
	}
}

func remote(r events.SummaryRow) events.SummaryRow {
	r.Source, r.First = delivery.SourceRemoteEvaluation, false
	return r
}

func evaluationDay(flag flags.FlagID, variation, reason string, source delivery.SummarySource, day time.Time) events.EvaluationDay {
	return events.EvaluationDay{
		Environment: rollupEnvironment, Flag: flag, Variation: variation, Reason: reason, Source: source, Day: day,
	}
}

func TestSummaryRowsAreAddedUpPerFlagVariationReasonSourceAndDayOfReceipt(t *testing.T) {
	day2 := day1.AddDate(0, 0, 1)
	out := events.RollUpSummaries([]events.SummaryRow{
		summaryRow(checkout, "on", "rule", 10, 0, 1, true),
		summaryRow(checkout, "off", "fallthrough", 5, 0, 1, false),
		summaryRow(banner, "", "error", 2, 0, 1, false),
		summaryRow(checkout, "on", "rule", 3, 0, 2, true),
		summaryRow(checkout, "on", "rule", 4, 0, 25, true),
		remote(summaryRow(checkout, "on", "rule", 7, 0, 1, false)),
	})
	want := map[events.EvaluationDay]int64{
		evaluationDay(checkout, "on", "rule", delivery.SourceSDK, day1):              13,
		evaluationDay(checkout, "off", "fallthrough", delivery.SourceSDK, day1):      5,
		evaluationDay(banner, "", "error", delivery.SourceSDK, day1):                 2,
		evaluationDay(checkout, "on", "rule", delivery.SourceSDK, day2):              4,
		evaluationDay(checkout, "on", "rule", delivery.SourceRemoteEvaluation, day1): 7,
	}
	if !maps.Equal(out.Days, want) {
		t.Errorf("days = %+v, want %+v", out.Days, want)
	}
}

func TestADayCountSaturatesAtTheLargestInt64(t *testing.T) {
	out := events.RollUpSummaries([]events.SummaryRow{
		summaryRow(checkout, "on", "rule", math.MaxInt64, 0, 1, true),
		summaryRow(checkout, "on", "rule", 1, 0, 1, false),
		summaryRow(checkout, "on", "rule", math.MaxInt64, 0, 1, true),
	})
	if got := out.Days[evaluationDay(checkout, "on", "rule", delivery.SourceSDK, day1)]; got != math.MaxInt64 {
		t.Errorf("day = %d, want the largest int64", got)
	}
	if got := events.SaturatingAdd(math.MaxInt64-1, 5); got != math.MaxInt64 {
		t.Errorf("SaturatingAdd = %d, want the largest int64", got)
	}
}

func TestOnlySummariesAnSDKReportedMoveWhenAFlagWasLastEvaluated(t *testing.T) {
	out := events.RollUpSummaries([]events.SummaryRow{
		summaryRow(checkout, "on", "rule", 1, 30, 1, true),
		summaryRow(checkout, "on", "rule", 1, 50, 1, true),
		summaryRow(checkout, "off", "rule", 1, 40, 1, false),
		// An interval ending after the summary arrived is taken as ending
		// when it arrived: nothing was evaluated later than that.
		summaryRow(banner, "on", "rule", 1, 600, 2, true),
		remote(summaryRow(banner, "on", "rule", 1, 900, 20, false)),
		// The server's counts alone never make a flag evaluated.
		remote(summaryRow(id.MustParse[flags.Flag]("0190a000-0000-7000-8000-000000000006"), "on", "rule", 1, 0, 1, false)),
	})
	want := map[events.FlagEnvironment]time.Time{
		{Flag: checkout, Environment: rollupEnvironment}: day1.Add(50 * time.Minute),
		{Flag: banner, Environment: rollupEnvironment}:   day1.Add(2 * time.Hour),
	}
	if !maps.EqualFunc(out.LastEvaluated, want, time.Time.Equal) {
		t.Errorf("last evaluated = %+v, want %+v", out.LastEvaluated, want)
	}
}

func TestEverySummaryAnSDKReportedIsOneEventReceived(t *testing.T) {
	out := events.RollUpSummaries([]events.SummaryRow{
		summaryRow(checkout, "on", "rule", 1, 0, 1, true),
		summaryRow(checkout, "off", "rule", 1, 0, 1, false),
		summaryRow(banner, "on", "rule", 1, 0, 1, false),
		summaryRow(checkout, "on", "rule", 1, 0, 1, true),
		summaryRow(checkout, "on", "rule", 1, 0, 25, true),
		remote(summaryRow(checkout, "on", "rule", 1, 0, 1, false)),
	})
	want := map[events.EventDay]int64{
		{Project: rollupProject, Day: day1, Type: events.EventEvaluationSummary}:                  2,
		{Project: rollupProject, Day: day1.AddDate(0, 0, 1), Type: events.EventEvaluationSummary}: 1,
	}
	if !maps.Equal(out.Events, want) {
		t.Errorf("events = %+v, want %+v", out.Events, want)
	}
}

// The day counts summed over batches and the latest evaluation are the same
// however a stream of rows is split into batches.
func TestTheSummaryRollupIsTheSameForEverySplit(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // a reproducible property test
	for trial := range 200 {
		var rows []events.SummaryRow
		for range 1 + rng.IntN(30) {
			r := summaryRow([]flags.FlagID{checkout, banner}[rng.IntN(2)], []string{"on", "off"}[rng.IntN(2)], "rule",
				int64(1+rng.IntN(3)), rng.IntN(600), rng.IntN(72), rng.IntN(3) == 0)
			if rng.IntN(4) == 0 {
				r = remote(r)
			}
			rows = append(rows, r)
		}
		whole := events.RollUpSummaries(rows)
		days, last, evs := map[events.EvaluationDay]int64{}, map[events.FlagEnvironment]time.Time{}, map[events.EventDay]int64{}
		for rest := rows; len(rest) > 0; {
			k := 1 + rng.IntN(len(rest))
			out := events.RollUpSummaries(rest[:k])
			rest = rest[k:]
			for d, n := range out.Days {
				days[d] = events.SaturatingAdd(days[d], n)
			}
			for f, at := range out.LastEvaluated {
				if at.After(last[f]) {
					last[f] = at
				}
			}
			for d, n := range out.Events {
				evs[d] += n
			}
		}
		if !maps.Equal(days, whole.Days) || !maps.EqualFunc(last, whole.LastEvaluated, time.Time.Equal) || !maps.Equal(evs, whole.Events) {
			t.Fatalf("trial %d: split %+v %+v %+v, whole %+v", trial, days, last, evs, whole)
		}
	}
}

func TestAnExposureMovesWhenItsFlagWasLastEvaluated(t *testing.T) {
	r := func(n, minutes, hours int, flag flags.FlagID) events.ExposureRow {
		row := exposureRow(n, "u", "control", minutes, hours)
		row.Flag = flag
		return row
	}
	got := events.LastExposed([]events.ExposureRow{
		r(1, 10, 1, checkout), override(r(2, 20, 1, checkout)), r(3, 5, 1, checkout),
		// An exposure occurring after it arrived counts when it arrived.
		r(4, 300, 2, banner),
	})
	want := map[events.FlagEnvironment]time.Time{
		{Flag: checkout, Environment: rollupEnvironment}: day1.Add(20 * time.Minute),
		{Flag: banner, Environment: rollupEnvironment}:   day1.Add(2 * time.Hour),
	}
	if !maps.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("last evaluated = %+v, want %+v", got, want)
	}
}

func TestTheEvaluationDaysDefaultToTheLastThirtyAndSpanNinetyAtMost(t *testing.T) {
	today := day1.Add(13 * time.Hour)
	at := func(days int) *time.Time {
		d := day1.AddDate(0, 0, days)
		return &d
	}
	for name, c := range map[string]struct {
		from, to         *time.Time
		wantFrom, wantTo time.Time
		wantErr          bool
	}{
		"neither":             {wantFrom: day1.AddDate(0, 0, -29), wantTo: day1},
		"to only":             {to: at(-10), wantFrom: day1.AddDate(0, 0, -39), wantTo: day1.AddDate(0, 0, -10)},
		"from only":           {from: at(-5), wantFrom: day1.AddDate(0, 0, -5), wantTo: day1},
		"one day":             {from: at(-3), to: at(-3), wantFrom: day1.AddDate(0, 0, -3), wantTo: day1.AddDate(0, 0, -3)},
		"ninety days":         {from: at(-89), to: at(0), wantFrom: day1.AddDate(0, 0, -89), wantTo: day1},
		"ninety-one days":     {from: at(-90), to: at(0), wantErr: true},
		"from only, too long": {from: at(-90), wantErr: true},
		"reversed":            {from: at(1), to: at(0), wantErr: true},
	} {
		from, to, err := events.EvaluationDays(c.from, c.to, today)
		if c.wantErr {
			var rangeErr *events.RangeError
			if !errors.As(err, &rangeErr) {
				t.Errorf("%s: error %v, want a range error", name, err)
			}
			continue
		}
		if err != nil || !from.Equal(c.wantFrom) || !to.Equal(c.wantTo) {
			t.Errorf("%s: %v to %v, %v; want %v to %v", name, from, to, err, c.wantFrom, c.wantTo)
		}
	}
}
