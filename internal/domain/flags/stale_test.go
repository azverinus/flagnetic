package flags_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// staleNow is the instant the stale tests judge at, mid-day so a day's
// boundary and an instant's differ.
var staleNow = time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)

// on is a configuration switched on, serving its fallthrough true to
// everyone.
func on() flags.Configuration {
	return flags.Configuration{
		On: true, OffVariation: "false", Fallthrough: "true",
		Prerequisites: []flags.Prerequisite{}, Overrides: []flags.Override{}, Rules: []flags.Rule{},
	}
}

// partial is a configuration switched on serving true to half the users
// and false to the others.
func partial() flags.Configuration {
	c := on()
	c.Fallthrough = "false"
	c.Rules = []flags.Rule{{Serve: flags.Serve{Kind: flags.ServeRollout, Variation: "true", UnitType: "user", Percentage: 5_000}}}
	return c
}

// healthy are the facts of a permanent flag created long ago, owned by an
// active member, evaluated an hour ago in a project receiving summaries,
// partly rolled out: no reason applies to it.
func healthy() flags.StaleFacts {
	created := staleNow.AddDate(-1, 0, 0)
	evaluated := staleNow.Add(-time.Hour)
	today := staleNow.Truncate(24 * time.Hour)
	return flags.StaleFacts{
		Flag: flags.Flag{
			ID: id.New[flags.Flag](ids), Key: "checkout", Lifetime: flags.Permanent, CreatedAt: created,
		},
		Configurations:  []flags.Configuration{partial(), partial()},
		LastEvaluatedAt: &evaluated,
		LastSummaryDay:  &today,
		OwnerActive:     true,
	}
}

func reasonsOf(f flags.StaleFacts) []flags.StaleReason { return flags.JudgeStale(f, staleNow).Reasons }

func TestAHealthyFlagIsNotStale(t *testing.T) {
	if got := reasonsOf(healthy()); len(got) != 0 {
		t.Errorf("reasons = %v, want none", got)
	}
}

func TestATemporaryFlagPastItsExpectedRemovalExceedsItsLifetime(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lifetime flags.Lifetime
		removal  time.Time
		want     bool
	}{
		{"temporary past removal", flags.Temporary, staleNow.Add(-time.Second), true},
		{"temporary due now", flags.Temporary, staleNow, false},
		{"temporary due later", flags.Temporary, staleNow.Add(time.Hour), false},
		{"permanent", flags.Permanent, staleNow.Add(-time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthy()
			f.Flag.Lifetime = tc.lifetime
			if tc.lifetime == flags.Temporary {
				f.Flag.ExpectedRemovalAt = &tc.removal
			}
			if got := slices.Contains(reasonsOf(f), flags.StaleLifetimeExceeded); got != tc.want {
				t.Errorf("lifetime_exceeded = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAFlagNoSDKEvaluatedForThirtyDaysIsNotEvaluated(t *testing.T) {
	window := staleNow.Add(-flags.NotEvaluatedWindow)
	for _, tc := range []struct {
		name      string
		evaluated *time.Time
		want      bool
	}{
		{"never evaluated", nil, true},
		{"evaluated just before the window", ptr(window.Add(-time.Second)), true},
		{"evaluated exactly at the window's start", ptr(window), false},
		{"evaluated within the window", ptr(staleNow.Add(-time.Hour)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthy()
			f.LastEvaluatedAt = tc.evaluated
			if got := slices.Contains(reasonsOf(f), flags.StaleNotEvaluated); got != tc.want {
				t.Errorf("not_evaluated = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNotEvaluatedIsJudgedOnlyInAProjectReceivingSummariesAndForAFlagOlderThanTheWindow(t *testing.T) {
	windowDay := staleNow.Add(-flags.NotEvaluatedWindow).Truncate(24 * time.Hour)
	for _, tc := range []struct {
		name       string
		summaryDay *time.Time
		created    time.Time
		want       bool
	}{
		{"no summary ever", nil, staleNow.AddDate(-1, 0, 0), false},
		{"last summary the day before the window", ptr(windowDay.AddDate(0, 0, -1)), staleNow.AddDate(-1, 0, 0), false},
		{"last summary on the window's first day", ptr(windowDay), staleNow.AddDate(-1, 0, 0), true},
		{"flag created within the window", ptr(windowDay), staleNow.Add(-flags.NotEvaluatedWindow + time.Second), false},
		{"flag created at the window's start", ptr(windowDay), staleNow.Add(-flags.NotEvaluatedWindow), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthy()
			f.LastEvaluatedAt = nil
			f.LastSummaryDay = tc.summaryDay
			f.Flag.CreatedAt = tc.created
			if got := slices.Contains(reasonsOf(f), flags.StaleNotEvaluated); got != tc.want {
				t.Errorf("not_evaluated = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAFlagWhoseOwnerMayNoLongerOwnItHasAnInactiveOwner(t *testing.T) {
	f := healthy()
	f.OwnerActive = false
	if got := reasonsOf(f); !slices.Equal(got, []flags.StaleReason{flags.StaleOwnerInactive}) {
		t.Errorf("reasons = %v, want [owner_inactive]", got)
	}
}

func TestARolloutIsFinishedWhenEveryEnvironmentServesTheSameVariationToEveryone(t *testing.T) {
	full := func() flags.Configuration {
		c := partial()
		c.Rules[0].Serve.Percentage = 10_000
		return c
	}
	with := func(c flags.Configuration, change func(*flags.Configuration)) flags.Configuration {
		change(&c)
		return c
	}
	for _, tc := range []struct {
		name    string
		configs []flags.Configuration
		want    bool
	}{
		{"no rule left everywhere", []flags.Configuration{on(), on()}, true},
		{"a rollout at 100 % everywhere", []flags.Configuration{full(), full()}, true},
		{"no rule in one, 100 % of the same variation in the other", []flags.Configuration{on(), full()}, true},
		{"rules after a full rollout are never reached", []flags.Configuration{with(full(), func(c *flags.Configuration) {
			c.Rules = append(c.Rules, flags.Rule{Serve: flags.Serve{Kind: flags.ServeVariation, Variation: "false"}})
		}), on()}, true},
		{"one environment switched off", []flags.Configuration{on(), with(on(), func(c *flags.Configuration) { c.On = false })}, false},
		{"one environment at 50 %", []flags.Configuration{on(), partial()}, false},
		{"a full rollout behind a condition", []flags.Configuration{on(), with(full(), func(c *flags.Configuration) {
			c.Rules[0].Conditions = []flags.Condition{{Kind: "attribute", Attribute: "country", Op: "in", Value: []any{"FR"}}}
		})}, false},
		{"a rule serving a variation", []flags.Configuration{on(), with(on(), func(c *flags.Configuration) {
			c.Rules = []flags.Rule{{Serve: flags.Serve{Kind: flags.ServeVariation, Variation: "true"}}}
		})}, false},
		{"an override", []flags.Configuration{on(), with(on(), func(c *flags.Configuration) {
			c.Overrides = []flags.Override{{UnitType: "user", UnitID: "qa-1", Variation: "false"}}
		})}, false},
		{"a prerequisite", []flags.Configuration{on(), with(on(), func(c *flags.Configuration) {
			c.Prerequisites = []flags.Prerequisite{{Flag: "billing", Variation: "true"}}
		})}, false},
		{"different variations in two environments", []flags.Configuration{on(), with(on(), func(c *flags.Configuration) {
			c.Fallthrough = "false"
		})}, false},
		{"no environment", []flags.Configuration{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthy()
			f.Configurations = tc.configs
			if got := slices.Contains(reasonsOf(f), flags.StaleFinishedRollout); got != tc.want {
				t.Errorf("finished_rollout = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAnOverdueExperimentOnTheFlagNamesTheOneDueEarliest(t *testing.T) {
	f := healthy()
	f.OverdueExperiments = []flags.OverdueExperiment{
		{Key: "late_b", ExpectedEndAt: staleNow.AddDate(0, 0, -2)},
		{Key: "late_c", ExpectedEndAt: staleNow.AddDate(0, 0, -5)},
		{Key: "late_a", ExpectedEndAt: staleNow.AddDate(0, 0, -5)},
	}
	got := flags.JudgeStale(f, staleNow)
	if !slices.Equal(got.Reasons, []flags.StaleReason{flags.StaleExperimentOverdue}) {
		t.Errorf("reasons = %v, want [experiment_overdue]", got.Reasons)
	}
	if got.Experiment == nil || got.Experiment.Key != "late_a" {
		t.Errorf("experiment = %+v, want late_a", got.Experiment)
	}
	if none := flags.JudgeStale(healthy(), staleNow); none.Experiment != nil {
		t.Errorf("experiment without an overdue one = %+v, want nil", none.Experiment)
	}
}

func TestSeveralReasonsComeInTheirFixedOrder(t *testing.T) {
	f := healthy()
	f.Flag.Lifetime, f.Flag.ExpectedRemovalAt = flags.Temporary, ptr(staleNow.Add(-time.Hour))
	f.LastEvaluatedAt = nil
	f.OwnerActive = false
	f.Configurations = []flags.Configuration{on()}
	f.OverdueExperiments = []flags.OverdueExperiment{{Key: "late", ExpectedEndAt: staleNow.AddDate(0, 0, -1)}}
	if got := reasonsOf(f); !slices.Equal(got, flags.StaleReasons) {
		t.Errorf("reasons = %v, want %v", got, flags.StaleReasons)
	}
}

func TestAnArchivedFlagIsNeverStale(t *testing.T) {
	f := healthy()
	f.Flag.Lifetime, f.Flag.ExpectedRemovalAt = flags.Temporary, ptr(staleNow.Add(-time.Hour))
	f.OwnerActive = false
	f.Flag.ArchivedAt = ptr(staleNow.Add(-time.Minute))
	if got := reasonsOf(f); len(got) != 0 {
		t.Errorf("reasons = %v, want none", got)
	}
}
