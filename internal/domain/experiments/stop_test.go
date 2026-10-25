package experiments_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func stopped(t *testing.T) experiments.Experiment {
	t.Helper()
	e, err := running(t).Stop(experiments.Decision{Outcome: experiments.OutcomeWinner, WinnerVariation: "short"}, later)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func archived(t *testing.T) experiments.Experiment {
	t.Helper()
	e, err := stopped(t).Archive(later)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestStoppingARunningExperimentRecordsItsDecision(t *testing.T) {
	e := running(t)
	for _, d := range []experiments.Decision{
		{Outcome: experiments.OutcomeWinner, WinnerVariation: "short", Note: "  Short titles won.  "},
		{Outcome: experiments.OutcomeRemoved},
	} {
		got, err := e.Stop(d, later)
		if err != nil {
			t.Fatalf("stop with %+v: %v", d, err)
		}
		d.Note = strings.TrimSpace(d.Note)
		if got.Status != experiments.StatusStopped || got.StoppedAt == nil || !got.StoppedAt.Equal(later) ||
			got.Decision == nil || *got.Decision != d || got.Version != e.Version+1 || !got.UpdatedAt.Equal(later) {
			t.Errorf("stopped = %+v, decision %+v, want %+v", got, got.Decision, d)
		}
		if !slices.Equal(got.Variations, e.Variations) || got.InclusionSalt != e.InclusionSalt {
			t.Errorf("stop changed the split: %+v", got)
		}
	}
}

func TestAStopIsRefusedADecisionThatIsNotValid(t *testing.T) {
	e := running(t)
	for _, c := range []struct {
		what string
		d    experiments.Decision
		want []string
	}{
		{"a winner naming none", experiments.Decision{Outcome: experiments.OutcomeWinner}, []string{"/winner_variation required"}},
		// The flag has huge, but the experiment does not compare it.
		{"a winner the experiment does not compare", experiments.Decision{Outcome: experiments.OutcomeWinner, WinnerVariation: "huge"},
			[]string{"/winner_variation unknown_variation"}},
		{"a removal naming a winner", experiments.Decision{Outcome: experiments.OutcomeRemoved, WinnerVariation: "short"},
			[]string{"/winner_variation invalid_value"}},
		{"an unknown outcome and a long note", experiments.Decision{Outcome: "draw", Note: strings.Repeat("é", 2001)},
			[]string{"/note too_long", "/outcome invalid_value"}},
	} {
		got, err := e.Stop(c.d, later)
		kind, code, v := problem(t, err)
		if kind != apperrors.KindInvalid || code != "invalid_decision" || !slices.Equal(v, c.want) || got.Status != experiments.StatusRunning {
			t.Errorf("%s: %s %s %v, want invalid_decision %v", c.what, kind, code, v, c.want)
		}
	}
	if _, err := e.Stop(experiments.Decision{Outcome: experiments.OutcomeRemoved, Note: strings.Repeat("é", 2000)}, later); err != nil {
		t.Errorf("a note of 2,000 characters = %v", err)
	}
}

func TestOnlyARunningExperimentStops(t *testing.T) {
	d := experiments.Decision{Outcome: experiments.OutcomeRemoved}
	for e, want := range map[*experiments.Experiment]string{
		ptr(startable(t)): "not_running", ptr(stopped(t)): "not_running", ptr(archived(t)): "archived",
	} {
		// The status is told before the decision is judged.
		_, err := e.Stop(experiments.Decision{Outcome: "draw"}, later)
		if kind, code, _ := problem(t, err); kind != apperrors.KindConflict || code != want {
			t.Errorf("stop of a %s experiment = %s %s, want conflict %s", e.Status, kind, code, want)
		}
		if _, err = e.Stop(d, later); err == nil {
			t.Errorf("a %s experiment stopped", e.Status)
		}
	}
}

func TestAStoppedExperimentChangesItsDescriptionAndItsNoteAlone(t *testing.T) {
	e := stopped(t)
	got, changed, err := e.Change(experiments.Change{
		Name: ptr("Checkout v2, decided"), Hypothesis: ptr("Short titles won."), Tags: &[]string{"done"},
		// The frozen fields, named with the values they have, change
		// nothing and are not refused.
		FlagKey: ptr(e.FlagKey), Flag: &flag, UnitType: ptr(e.UnitType), Variations: &e.Variations,
		TrafficAllocation: ptr(e.TrafficAllocation), ExpectedEndAt: ptr(e.ExpectedEndAt.Add(5 * time.Hour)),
	}, later)
	if err != nil || !changed || got.Name != "Checkout v2, decided" || got.Status != experiments.StatusStopped || got.Version != e.Version+1 {
		t.Fatalf("Change = %+v, changed %v, %v", got, changed, err)
	}

	swapped := []experiments.Weight{e.Variations[1], e.Variations[0]}
	_, changed, err = e.Change(experiments.Change{
		Name: ptr("New"), FlagKey: ptr("other"), UnitType: ptr("anonymous"), Variations: &swapped, Flag: &flag,
		TrafficAllocation: ptr(10), ExpectedEndAt: ptr(e.ExpectedEndAt.AddDate(0, 0, 1)),
	}, later)
	kind, code, v := problem(t, err)
	want := []string{"/flag frozen", "/unit_type frozen", "/variations frozen", "/traffic_allocation frozen", "/expected_end_at frozen"}
	if changed || kind != apperrors.KindConflict || code != "frozen" || !slices.Equal(v, want) {
		t.Errorf("changing the split of a stopped experiment = %s %s %v, want conflict frozen %v", kind, code, v, want)
	}

	noted, changed, err := e.SetDecisionNote(" Short titles won by 3 %. ", later)
	if err != nil || !changed || noted.Decision.Note != "Short titles won by 3 %." || noted.Decision.WinnerVariation != "short" ||
		noted.Version != e.Version+1 {
		t.Errorf("SetDecisionNote = %+v, %v, %v", noted.Decision, changed, err)
	}
	if again, changed, err := noted.SetDecisionNote("Short titles won by 3 %.", later); err != nil || changed || again.Version != noted.Version {
		t.Errorf("the same note = changed %v, %v", changed, err)
	}
	if cleared, _, err := noted.SetDecisionNote("", later); err != nil || cleared.Decision.Note != "" {
		t.Errorf("clearing the note = %+v, %v", cleared.Decision, err)
	}
	_, _, err = noted.SetDecisionNote(strings.Repeat("x", 2001), later)
	if kind, code, v := problem(t, err); kind != apperrors.KindInvalid || code != "invalid_decision" || !slices.Equal(v, []string{"/note too_long"}) {
		t.Errorf("a long note = %s %s %v", kind, code, v)
	}
}

func TestADecisionsNoteIsWrittenOnlyWhileStopped(t *testing.T) {
	for e, want := range map[*experiments.Experiment]string{
		ptr(startable(t)): "not_stopped", ptr(running(t)): "not_stopped", ptr(archived(t)): "archived",
	} {
		_, _, err := e.SetDecisionNote("Why", later)
		if kind, code, _ := problem(t, err); kind != apperrors.KindConflict || code != want {
			t.Errorf("a note on a %s experiment = %s %s, want conflict %s", e.Status, kind, code, want)
		}
	}
}

func TestADraftOrAStoppedExperimentIsArchived(t *testing.T) {
	for _, e := range []experiments.Experiment{startable(t), stopped(t)} {
		got, err := e.Archive(later)
		if err != nil || got.Status != experiments.StatusArchived || got.ArchivedAt == nil || !got.ArchivedAt.Equal(later) ||
			got.Version != e.Version+1 {
			t.Errorf("archiving a %s experiment = %+v, %v", e.Status, got, err)
		}
	}
	for e, want := range map[*experiments.Experiment]string{ptr(running(t)): "running", ptr(archived(t)): "archived"} {
		_, err := e.Archive(later)
		if kind, code, _ := problem(t, err); kind != apperrors.KindConflict || code != want {
			t.Errorf("archiving a %s experiment = %s %s, want conflict %s", e.Status, kind, code, want)
		}
	}
}

func TestAnArchivedExperimentRefusesEveryWrite(t *testing.T) {
	e := archived(t)
	_, _, edit := e.Change(experiments.Change{Name: ptr("New")}, later)
	_, salts := e.RegenerateSalts(experiments.Salts{Inclusion: "0123456789abcdef", Variation: "fedcba9876543210"}, later)
	_, start := e.Start(ready, later)
	for what, err := range map[string]error{"edit": edit, "delete": e.CanDelete(), "salts": salts, "start": start} {
		if got, ok := apperrors.From(err); !ok || got.Kind != apperrors.KindConflict || got.Code != "archived" {
			t.Errorf("%s of an archived experiment = %v, want conflict archived", what, err)
		}
	}
}
