package experiments_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// startable returns a draft every start precondition holds for.
func startable(t *testing.T) experiments.Experiment {
	t.Helper()
	in := input()
	in.Hypothesis = "Short titles raise completed checkouts."
	in.ExpectedEndAt = ptr(time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC))
	e, err := experiments.New(in)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

var ready = experiments.StartFacts{Flag: flag, DeclaredUnitTypes: []string{"user", "anonymous"}}

func running(t *testing.T) experiments.Experiment {
	t.Helper()
	e, err := startable(t).Start(ready, now)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// problem returns err's kind, code and "pointer code" for each of its
// violations.
func problem(t *testing.T, err error) (apperrors.Kind, string, []string) {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok {
		t.Fatalf("err = %v, want an error of a kind", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return e.Kind, e.Code, out
}

func TestStartingADraftRunsIt(t *testing.T) {
	e := startable(t)
	got, err := e.Start(ready, later)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != experiments.StatusRunning || got.StartedAt == nil || !got.StartedAt.Equal(later) ||
		!got.UpdatedAt.Equal(later) || got.Version != e.Version+1 {
		t.Errorf("started = status %s, started %v, updated %v, version %d", got.Status, got.StartedAt, got.UpdatedAt, got.Version)
	}
	if got.InclusionSalt != e.InclusionSalt || !slices.Equal(got.Variations, e.Variations) || got.TrafficAllocation != e.TrafficAllocation {
		t.Errorf("start changed the split: %+v", got)
	}
}

func TestAStartNamesEveryPreconditionItFails(t *testing.T) {
	bare := draft(t) // no hypothesis, no expected end date
	archived := flag
	archived.Archived = true
	for _, c := range []struct {
		what  string
		e     experiments.Experiment
		facts experiments.StartFacts
		want  []string
	}{
		{"a bare draft", bare, ready, []string{"/hypothesis required", "/expected_end_at required"}},
		{"an archived flag", startable(t), experiments.StartFacts{Flag: archived, DeclaredUnitTypes: ready.DeclaredUnitTypes},
			[]string{"/flag flag_archived"}},
		{"a unit type no longer declared", startable(t), experiments.StartFacts{Flag: flag, DeclaredUnitTypes: []string{"anonymous"}},
			[]string{"/unit_type undeclared_unit_type"}},
		{"a listed variation the flag no longer has", startable(t),
			experiments.StartFacts{Flag: experiments.FlagFacts{ID: flag.ID, Key: flag.Key, Variations: []string{"long"}}, DeclaredUnitTypes: ready.DeclaredUnitTypes},
			[]string{"/variations/1/variation unknown_variation"}},
		{"everything at once", bare, experiments.StartFacts{Flag: archived},
			[]string{"/flag flag_archived", "/unit_type undeclared_unit_type", "/hypothesis required", "/expected_end_at required"}},
	} {
		_, err := c.e.Start(c.facts, later)
		kind, code, got := problem(t, err)
		if kind != apperrors.KindInvalid || code != "experiment_not_ready" || !slices.Equal(got, c.want) {
			t.Errorf("%s: %s %s %v, want experiment_not_ready %v", c.what, kind, code, got, c.want)
		}
	}
}

func TestOnlyADraftStarts(t *testing.T) {
	for _, s := range []experiments.Status{experiments.StatusRunning, experiments.StatusStopped, experiments.StatusArchived} {
		e := startable(t)
		e.Status = s
		// A status that cannot start is told first, even when the
		// preconditions fail too.
		_, err := e.Start(experiments.StartFacts{Flag: flag}, later)
		if kind, code, _ := problem(t, err); kind != apperrors.KindConflict || code != "not_draft" {
			t.Errorf("start of a %s experiment = %s %s, want conflict not_draft", s, kind, code)
		}
	}
}

func TestARunningExperimentChangesOnlyItsDescriptionAndGrowsItsAllocation(t *testing.T) {
	e := running(t)
	bella := id.New[access.Member](ids)
	got, changed, err := e.Change(experiments.Change{
		Name: ptr("Checkout v2, running"), Hypothesis: ptr("Sharper."), Owner: &experiments.Owner{Member: bella, Eligible: true},
		Tags: &[]string{"web"}, ExpectedEndAt: ptr(time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)),
		TrafficAllocation: ptr(10000),
		// The frozen fields, named with the values they have, change
		// nothing and are not refused.
		FlagKey: ptr(e.FlagKey), Flag: &flag, UnitType: ptr(e.UnitType), Variations: &e.Variations,
	}, later)
	if err != nil || !changed {
		t.Fatalf("Change = %v, changed %v", err, changed)
	}
	if got.Name != "Checkout v2, running" || got.Owner != bella || got.TrafficAllocation != 10000 ||
		got.Status != experiments.StatusRunning || got.Version != e.Version+1 || !got.StartedAt.Equal(*e.StartedAt) {
		t.Errorf("changed = %+v", got)
	}
}

func TestARunningExperimentRefusesToChangeItsSplit(t *testing.T) {
	e := running(t)
	other := experiments.FlagFacts{ID: flag.ID, Key: "other", Variations: flag.Variations}
	swapped := []experiments.Weight{e.Variations[1], e.Variations[0]}
	for _, c := range []struct {
		what   string
		change experiments.Change
		want   []string
	}{
		{"the flag", experiments.Change{FlagKey: ptr("other"), Flag: &other}, []string{"/flag frozen"}},
		{"the unit type", experiments.Change{UnitType: ptr("anonymous"), DeclaredUnitTypes: ready.DeclaredUnitTypes}, []string{"/unit_type frozen"}},
		{"the weights' order", experiments.Change{Variations: &swapped, Flag: &flag}, []string{"/variations frozen"}},
		{"several, with an undeclared unit type", experiments.Change{UnitType: ptr("device"), Variations: &swapped, Flag: &flag, Name: ptr("New")},
			[]string{"/unit_type frozen", "/variations frozen"}},
	} {
		_, changed, err := e.Change(c.change, later)
		kind, code, got := problem(t, err)
		if changed || kind != apperrors.KindConflict || code != "frozen" || !slices.Equal(got, c.want) {
			t.Errorf("%s: %s %s %v, want conflict frozen %v", c.what, kind, code, got, c.want)
		}
	}
}

func TestARunningExperimentsAllocationNeverShrinks(t *testing.T) {
	e := running(t) // allocated to everyone
	e.TrafficAllocation = 5000
	_, _, err := e.Change(experiments.Change{TrafficAllocation: ptr(4999)}, later)
	if kind, code, _ := problem(t, err); kind != apperrors.KindConflict || code != "traffic_allocation_decrease" {
		t.Errorf("shrinking = %s %s, want conflict traffic_allocation_decrease", kind, code)
	}
	// A value out of range is invalid before it is a decrease.
	_, _, err = e.Change(experiments.Change{TrafficAllocation: ptr(-1)}, later)
	if kind, code, _ := problem(t, err); kind != apperrors.KindInvalid || code != "invalid_experiment" {
		t.Errorf("-1 = %s %s, want invalid_experiment", kind, code)
	}
	if _, changed, err := e.Change(experiments.Change{TrafficAllocation: ptr(5000)}, later); err != nil || changed {
		t.Errorf("the same allocation = changed %v, %v", changed, err)
	}
}

func TestARunningExperimentKeepsAHypothesis(t *testing.T) {
	e := running(t)
	for _, blank := range []string{"", "  "} {
		_, changed, err := e.Change(experiments.Change{Hypothesis: ptr(blank)}, later)
		if got := violations(t, err); changed || !slices.Equal(got, []string{"/hypothesis required"}) {
			t.Errorf("hypothesis %q: violations %v, changed %v", blank, got, changed)
		}
	}
	// A draft may still clear its own: it is required only to start.
	if _, _, err := draft(t).Change(experiments.Change{Hypothesis: ptr("")}, later); err != nil {
		t.Errorf("clearing a draft's hypothesis = %v", err)
	}
}
