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

var later = now.Add(time.Hour)

func draft(t *testing.T) experiments.Experiment {
	t.Helper()
	e, err := experiments.New(input())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestADraftIsEditedInEveryField(t *testing.T) {
	e := draft(t)
	bella := id.New[access.Member](ids)
	other := experiments.FlagFacts{ID: flag.ID, Key: "checkout_title", Variations: flag.Variations}

	got, changed, err := e.Change(experiments.Change{
		Name: ptr(" Checkout v3 "), Hypothesis: ptr("Huge titles win."), Owner: &experiments.Owner{Member: bella, Eligible: true},
		Tags: &[]string{"web"}, FlagKey: ptr("checkout_title"), Flag: &other, UnitType: ptr("anonymous"),
		DeclaredUnitTypes: []string{"user", "anonymous"},
		Variations:        &[]experiments.Weight{{Variation: "huge", Weight: 2500}, {Variation: "short", Weight: 7500}},
		TrafficAllocation: ptr(2000), ExpectedEndAt: ptr(time.Date(2026, time.May, 1, 9, 0, 0, 0, time.UTC)),
	}, later)
	if err != nil || !changed {
		t.Fatalf("Change = %v, changed %v", err, changed)
	}
	if got.Name != "Checkout v3" || got.Hypothesis != "Huge titles win." || got.Owner != bella || !slices.Equal(got.Tags, []string{"web"}) ||
		got.UnitType != "anonymous" || got.TrafficAllocation != 2000 || got.ExpectedEndAt == nil || got.ExpectedEndAt.Day() != 1 ||
		len(got.Variations) != 2 || got.Variations[0].Variation != "huge" {
		t.Errorf("changed experiment = %+v", got)
	}
	if got.Version != 2 || !got.UpdatedAt.Equal(later) || !got.CreatedAt.Equal(now) || got.Key != e.Key ||
		got.InclusionSalt != e.InclusionSalt || got.Status != experiments.StatusDraft {
		t.Errorf("version %d, updated %v, key %q, salt %q", got.Version, got.UpdatedAt, got.Key, got.InclusionSalt)
	}
}

func TestAnEditToValuesTheDraftHasChangesNothing(t *testing.T) {
	e := draft(t)
	got, changed, err := e.Change(experiments.Change{
		Name: ptr(e.Name), Tags: &e.Tags, Owner: &experiments.Owner{Member: e.Owner}, // not eligible, but not changing
		FlagKey: ptr(e.FlagKey), Flag: &flag, Variations: &e.Variations, UnitType: ptr(e.UnitType),
	}, later)
	if err != nil || changed || got.Version != 1 || !got.UpdatedAt.Equal(now) {
		t.Errorf("Change = version %d, changed %v, err %v", got.Version, changed, err)
	}
}

func TestAnEditIsRefusedWithEveryFieldToFix(t *testing.T) {
	e := draft(t)
	archived := experiments.FlagFacts{ID: flag.ID, Key: "old", Archived: true}
	for _, c := range []struct {
		what   string
		change experiments.Change
		want   []string
	}{
		{"the key", experiments.Change{Key: ptr("renamed")}, []string{"/key immutable"}},
		{"a blank name", experiments.Change{Name: ptr(" ")}, []string{"/name invalid_name"}},
		{"an ineligible new owner", experiments.Change{Owner: &experiments.Owner{Member: id.New[access.Member](ids)}}, []string{"/owner invalid_owner"}},
		{"an allocation over the total", experiments.Change{TrafficAllocation: ptr(10001)}, []string{"/traffic_allocation out_of_range"}},
		{"an undeclared unit type", experiments.Change{UnitType: ptr("device"), DeclaredUnitTypes: []string{"user"}}, []string{"/unit_type undeclared_unit_type"}},
		{"an unknown flag", experiments.Change{FlagKey: ptr("absent")}, []string{"/flag unknown_flag"}},
		{"an archived flag", experiments.Change{FlagKey: ptr("old"), Flag: &archived}, []string{"/flag flag_archived"}},
		{"weights that sum short", experiments.Change{Flag: &flag, Variations: &[]experiments.Weight{{Variation: "long", Weight: 1}, {Variation: "short", Weight: 1}}},
			[]string{"/variations invalid_weights_sum"}},
		{"a variation the new flag lacks", experiments.Change{FlagKey: ptr("other"), Flag: &experiments.FlagFacts{ID: flag.ID, Key: "other", Variations: []string{"long"}}},
			[]string{"/variations/1/variation unknown_variation"}},
		{"several at once", experiments.Change{Name: ptr(""), Key: ptr("x")}, []string{"/key immutable", "/name invalid_name"}},
	} {
		_, changed, err := e.Change(c.change, later)
		if got := violations(t, err); changed || !slices.Equal(got, c.want) {
			t.Errorf("%s: violations %v, changed %v, want %v", c.what, got, changed, c.want)
		}
	}
}

func TestOnlyADraftIsDeletedOrGivenNewSalts(t *testing.T) {
	for _, s := range []experiments.Status{experiments.StatusRunning, experiments.StatusStopped} {
		e := draft(t)
		e.Status = s
		_, salts := e.RegenerateSalts(experiments.Salts{Inclusion: "0123456789abcdef", Variation: "fedcba9876543210"}, later)
		refusals := map[string]error{"delete": e.CanDelete(), "salts": salts}
		for what, err := range refusals {
			if got, ok := apperrors.From(err); !ok || got.Kind != apperrors.KindConflict || got.Code != "not_draft" {
				t.Errorf("%s of a %s experiment = %v, want conflict not_draft", what, s, err)
			}
		}
	}
	if err := draft(t).CanDelete(); err != nil {
		t.Errorf("deleting a draft = %v", err)
	}
}

func TestRegeneratingSaltsReplacesBothAndBumpsTheVersion(t *testing.T) {
	e := draft(t)
	got, err := e.RegenerateSalts(experiments.Salts{Inclusion: "0123456789abcdef", Variation: "fedcba9876543210"}, later)
	if err != nil || got.InclusionSalt != "0123456789abcdef" || got.VariationSalt != "fedcba9876543210" || got.Version != 2 || !got.UpdatedAt.Equal(later) {
		t.Errorf("RegenerateSalts = %+v, %v", got, err)
	}
}
