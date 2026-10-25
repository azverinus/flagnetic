package flags_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestTheKillSwitchSwitchesOffAndNothingElse(t *testing.T) {
	in := base()
	in.On = true
	in.Rules = []flags.RuleInput{{Serve: serveVariation("b")}}
	on := mustReplace(t, initial(), in)
	killed, changed := on.Kill(flags.Write{By: &anna, Now: now})
	if killed.On || !changed.Any || !changed.Ruleset || killed.Version != on.Version+1 || len(killed.Rules) != 1 || *killed.UpdatedBy != anna {
		t.Errorf("killed = %+v, %+v, want switched off at the next version, its rules kept", killed, changed)
	}
	again, changed := killed.Kill(flags.Write{By: &bob, Now: now})
	if changed.Any || again.Version != killed.Version || *again.UpdatedBy != anna {
		t.Errorf("killing an off flag = %+v, %+v, want nothing changed", again, changed)
	}

	for name, tc := range map[string]struct {
		reason *string
		want   string
	}{
		"none":        {nil, ""},
		"blank":       {ptr("   "), ""},
		"trimmed":     {ptr("  Payments failing  "), "Payments failing"},
		"2,000 chars": {ptr(strings.Repeat("é", 2000)), strings.Repeat("é", 2000)},
	} {
		if got, err := flags.KillReason(tc.reason); err != nil || got != tc.want {
			t.Errorf("%s: reason %q, %v", name, got, err)
		}
	}
	_, err := flags.KillReason(ptr(strings.Repeat("é", 2001)))
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/reason too_long"}) {
		t.Errorf("violations = %v", got)
	}
}

func TestOneOverrideIsSetOrRemovedWithoutTheRest(t *testing.T) {
	in := base()
	in.Overrides = []flags.OverrideInput{{UnitType: "user", UnitID: "u-1", Variation: "b", Note: ptr("Demo")}}
	c := mustReplace(t, initial(), in)
	write := flags.Write{By: &anna, Now: now}

	same, changed, err := c.SetOverride(copyFlag, flags.OverrideInput{UnitType: "user", UnitID: "u-1", Variation: "b", Note: ptr(" Demo ")}, declarations, write)
	if err != nil || changed.Any || *same.Overrides[0].AddedBy != bob {
		t.Errorf("setting it as it is = %+v, %+v, %v, want nothing changed", same.Overrides, changed, err)
	}
	noted, changed, err := c.SetOverride(copyFlag, flags.OverrideInput{UnitType: "user", UnitID: "u-1", Variation: "b"}, declarations, write)
	if err != nil || !changed.Any || changed.Ruleset || noted.Overrides[0].Note != "" || *noted.Overrides[0].AddedBy != anna {
		t.Errorf("clearing the note = %+v, %+v, %v, want it changed by anna without the ruleset", noted.Overrides, changed, err)
	}
	added, changed, err := noted.SetOverride(copyFlag, flags.OverrideInput{UnitType: "anonymous", UnitID: "u-1", Variation: "c"}, declarations, write)
	if err != nil || !changed.Ruleset || len(added.Overrides) != 2 || added.Version != noted.Version+1 {
		t.Errorf("adding one = %+v, %+v, %v", added.Overrides, changed, err)
	}

	_, _, err = c.SetOverride(copyFlag, flags.OverrideInput{UnitType: "device", UnitID: "", Variation: "z", Note: ptr(strings.Repeat("n", 201))}, declarations, write)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/unit_type unknown_unit_type", "/unit_id invalid_value", "/note too_long", "/variation unknown_variation"}) {
		t.Errorf("violations = %v", got)
	}
	full := c
	full.Overrides = nil
	for i := range 200 {
		full.Overrides = append(full.Overrides, flags.Override{UnitType: "user", UnitID: strings.Repeat("x", i+1), Variation: "b"})
	}
	_, _, err = full.SetOverride(copyFlag, flags.OverrideInput{UnitType: "user", UnitID: "u-1", Variation: "b"}, declarations, write)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{" too_many"}) {
		t.Errorf("a 201st: violations = %v", got)
	}
	if _, _, err = full.SetOverride(copyFlag, flags.OverrideInput{UnitType: "user", UnitID: "x", Variation: "c"}, declarations, write); err != nil {
		t.Errorf("replacing one of 200: %v", err)
	}

	removed, changed, err := added.RemoveOverride("user", "u-1", write)
	if err != nil || !changed.Ruleset || len(removed.Overrides) != 1 || removed.Overrides[0].UnitType != "anonymous" || len(added.Overrides) != 2 {
		t.Errorf("removing one = %+v, %+v, %v", removed.Overrides, changed, err)
	}
	if _, _, err = removed.RemoveOverride("user", "u-1", write); !isNotFound(err) {
		t.Errorf("removing a missing one = %v, want not_found", err)
	}
}

func isNotFound(err error) bool {
	e, ok := apperrors.From(err)
	return ok && e.Kind == apperrors.KindNotFound && e.Code == "not_found"
}

func TestRolloutsCarryAGeneratedSaltOnlyRegenerationChanges(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{{Serve: rollout("user", "b", 5000)}, {Serve: serveVariation("c")}}
	c := mustReplace(t, initial(), in)
	salt := c.Rules[0].Serve.Salt
	regenerated, changed, err := c.RegenerateSalt(c.Rules[0].ID, mint, flags.Write{By: &anna, Now: now})
	if err != nil || !changed.Ruleset || regenerated.Rules[0].Serve.Salt == salt || regenerated.Rules[0].Serve.Salt == "" ||
		c.Rules[0].Serve.Salt != salt || regenerated.Version != c.Version+1 {
		t.Errorf("regenerated = %+v, %+v, %v, want a new salt at the next version", regenerated.Rules[0], changed, err)
	}
	if _, _, err = c.RegenerateSalt(c.Rules[1].ID, mint, flags.Write{Now: now}); err == nil {
		t.Error("a variation serve's salt regenerated")
	} else if e, _ := apperrors.From(err); e.Kind != apperrors.KindConflict || e.Code != "not_a_rollout" {
		t.Errorf("a variation serve: %v, want not_a_rollout", err)
	}
	if _, _, err = c.RegenerateSalt(id.New[flags.Rule](ids), mint, flags.Write{Now: now}); !isNotFound(err) {
		t.Errorf("an unknown rule: %v, want not_found", err)
	}
}

func TestApplyingADecisionRewritesOrDeletesTheRulesServingIt(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{
		{Description: "Staff", Conditions: []flags.Condition{cond("os", "eq", `"linux"`)}, Serve: experimentServe("running_title")},
		{Description: "Others", Serve: experimentServe("draft_title")},
		{Description: "Everyone", Serve: experimentServe("running_title")},
		{Description: "Rest", Serve: serveVariation("a")},
	}
	c := mustReplace(t, initial(), in)
	w := flags.Write{By: &bob, Now: now.Add(3 * time.Hour)}

	won, changed := c.ApplyDecision("running_title", "b", w)
	if !changed.Any || !changed.Ruleset || won.Version != c.Version+1 || won.UpdatedAt != w.Now || len(won.Rules) != 4 {
		t.Fatalf("won = %+v, %+v", won, changed)
	}
	for _, i := range []int{0, 2} {
		r := won.Rules[i]
		if r.ID != c.Rules[i].ID || r.Description != c.Rules[i].Description || !reflect.DeepEqual(r.Conditions, c.Rules[i].Conditions) ||
			r.Serve != (flags.Serve{Kind: "variation", Variation: "b"}) {
			t.Errorf("rule %d = %+v, want the winner served in its place", i, r)
		}
	}
	if !reflect.DeepEqual(won.Rules[1], c.Rules[1]) || !reflect.DeepEqual(won.Rules[3], c.Rules[3]) {
		t.Errorf("rules = %+v, want the others as they were", won.Rules)
	}

	removed, changed := c.ApplyDecision("running_title", "", w)
	if !changed.Ruleset || len(removed.Rules) != 2 || removed.Rules[0].ID != c.Rules[1].ID || removed.Rules[1].ID != c.Rules[3].ID {
		t.Errorf("removed = %+v, want the experiment's rules gone", removed.Rules)
	}

	same, changed := c.ApplyDecision("stopped_title", "b", w)
	if changed.Any || same.Version != c.Version {
		t.Errorf("applying a decision on an experiment no rule serves = %+v, want nothing changed", changed)
	}
}
