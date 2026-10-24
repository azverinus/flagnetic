package flags_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// targeted returns a configuration of copy switched on with an override
// noted by anna, a prerequisite, a rule and a rollout.
func targeted(t *testing.T) flags.Configuration {
	t.Helper()
	in := base()
	in.On, in.Fallthrough = true, "b"
	in.Prerequisites = []flags.Prerequisite{{Flag: "gate", Variation: "true"}}
	in.Overrides = []flags.OverrideInput{{UnitType: "user", UnitID: "u-1", Variation: "c", Note: ptr("Demo account")}}
	in.Rules = []flags.RuleInput{
		{Description: "Staff first", Conditions: []flags.Condition{cond("os", "eq", `"linux"`)}, Serve: serveVariation("c")},
		{Description: "A tenth", Serve: rollout("user", "b", 1000)},
	}
	c, _, err := initial().Replace(copyFlag, in, referents(nil), mint, flags.Write{By: &anna, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// sameServed reports whether a and b are the same document, what stays on
// the server included, whatever their versions and writers.
func sameServed(a, b flags.Configuration) bool {
	a.UpdatedAt, a.UpdatedBy, a.Version = b.UpdatedAt, b.UpdatedBy, b.Version
	a.Environment = b.Environment
	return reflect.DeepEqual(a, b)
}

func TestARevertWritesTheRecordedDocumentWholeIdentifiersAndSaltsIncluded(t *testing.T) {
	recorded := targeted(t)
	cleared := mustReplace(t, recorded, base())
	later := flags.Write{By: &bob, Now: now.Add(2 * time.Hour)}

	reverted, changed, err := cleared.Revert(copyFlag, recorded, referents(nil), later)
	if err != nil {
		t.Fatal(err)
	}
	if !sameServed(reverted, recorded) || reverted.Version != cleared.Version+1 || *reverted.UpdatedBy != bob ||
		!changed.Any || !changed.Ruleset {
		t.Errorf("reverted = %+v, %+v, want the recorded document at the next version", reverted, changed)
	}
	if *reverted.Overrides[0].AddedBy != anna || reverted.Overrides[0].Note != "Demo account" {
		t.Errorf("override = %+v, want its note and author as recorded", reverted.Overrides[0])
	}
	if reverted.Rules[1].ID != recorded.Rules[1].ID || reverted.Rules[1].Serve.Salt != recorded.Rules[1].Serve.Salt {
		t.Errorf("rollout = %+v, want its identifier and salt as recorded", reverted.Rules[1])
	}

	same, changed, err := reverted.Revert(copyFlag, recorded, referents(nil), later)
	if err != nil || changed.Any || same.Version != reverted.Version {
		t.Errorf("reverting to the current content = %+v, %+v, %v, want nothing changed", same, changed, err)
	}
}

func TestARevertIsRefusedLikeAnyWriteWhenWhatItNamesIsGone(t *testing.T) {
	recorded := targeted(t)
	cleared := mustReplace(t, recorded, base())
	withoutC := copyFlag
	withoutC.Variations = slices.DeleteFunc(slices.Clone(copyFlag.Variations), func(v flags.Variation) bool { return v.Key == "c" })
	gone := referents(nil)
	gone.Flags = []flags.Flag{withoutC, otherFlag, oldFlag}
	gone.Declarations = flags.NewDeclarations([]workspace.UnitType{{Key: "anonymous"}}, nil)

	_, _, err := cleared.Revert(withoutC, recorded, gone, flags.Write{By: &bob, Now: now})
	want := []string{
		"/prerequisites/0/flag unknown_flag", "/overrides/0/unit_type unknown_unit_type", "/overrides/0/variation unknown_variation",
		"/rules/0/conditions/0/attribute unknown_attribute", "/rules/0/serve/variation unknown_variation",
		"/rules/1/serve/unit_type unknown_unit_type",
	}
	if got := configurationViolations(t, err); !slices.Equal(got, want) {
		t.Errorf("violations = %v\nwant %v", got, want)
	}
	_, _, err = cleared.Revert(copyFlag, recorded, referents(flags.Requirements{"gate": {"copy"}}), flags.Write{By: &bob, Now: now})
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/prerequisites/0/flag prerequisite_cycle"}) {
		t.Errorf("a revert closing a cycle: violations = %v", got)
	}
}

func TestACopyServesTheSourcesTargetingAndKeepsTheTargetsOnOffAndOverrides(t *testing.T) {
	source := targeted(t)
	in := base()
	in.Overrides = []flags.OverrideInput{{UnitType: "anonymous", UnitID: "d-9", Variation: "a"}}
	in.Rules = []flags.RuleInput{{Serve: serveVariation("a")}}
	target, _, err := flags.InitialConfiguration(copyFlag, id.New[workspace.Environment](ids), &anna, now).
		Replace(copyFlag, in, referents(nil), mint, flags.Write{By: &anna, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	later := flags.Write{By: &bob, Now: now.Add(time.Hour)}

	copied, changed, err := target.Copy(copyFlag, source, false, referents(nil), later)
	if err != nil {
		t.Fatal(err)
	}
	if copied.On || copied.Environment != target.Environment || copied.Fallthrough != "b" ||
		!slices.Equal(copied.Prerequisites, source.Prerequisites) || !reflect.DeepEqual(copied.Rules, source.Rules) ||
		!reflect.DeepEqual(copied.Overrides, target.Overrides) || copied.Version != target.Version+1 || !changed.Ruleset {
		t.Errorf("copied = %+v, want the source's targeting, the target's on/off and overrides", copied)
	}
	if !copied.SameRuleset(withOn(source, false, target.Overrides)) {
		t.Error("the copy differs from the source in more than on/off and overrides")
	}

	withOverrides, _, err := target.Copy(copyFlag, source, true, referents(nil), later)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withOverrides.Overrides, source.Overrides) || withOverrides.On {
		t.Errorf("overrides = %+v, want the source's with their notes and authors", withOverrides.Overrides)
	}
	if _, _, err = target.Copy(copyFlag, source, false, referents(flags.Requirements{"gate": {"copy"}}), later); err == nil {
		t.Error("a copy closing a cycle in the target's environment was accepted")
	}
	_, _, err = target.Copy(copyFlag, target, false, referents(nil), later)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/from invalid_value"}) {
		t.Errorf("copying from its own environment: violations = %v", got)
	}
	again, changed, err := copied.Copy(copyFlag, source, false, referents(nil), later)
	if err != nil || changed.Any || again.Version != copied.Version {
		t.Errorf("copying again = %+v, %v, want nothing changed", changed, err)
	}
}

func withOn(c flags.Configuration, on bool, overrides []flags.Override) flags.Configuration {
	c.On, c.Overrides = on, overrides
	return c
}

// TestARevertOrCopyKeepsTheExperimentARuleServes pins that a rule serving an
// experiment is written back naming the same experiment, not refused as
// naming none.
func TestARevertOrCopyKeepsTheExperimentARuleServes(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{{Description: "Titles under test", Serve: experimentServe("draft_title")}}
	recorded := mustReplace(t, initial(), in)
	edited := in
	edited.Rules = []flags.RuleInput{{ID: &recorded.Rules[0].ID, Description: "Titles, again", Serve: experimentServe("draft_title")}}
	current := mustReplace(t, recorded, edited)
	later := flags.Write{By: &bob, Now: now.Add(time.Hour)}

	reverted, _, err := current.Revert(copyFlag, recorded, referents(nil), later)
	if err != nil {
		t.Fatal(err)
	}
	if !sameServed(reverted, recorded) {
		t.Errorf("reverted rules = %+v, want %+v", reverted.Rules, recorded.Rules)
	}

	target := flags.InitialConfiguration(copyFlag, id.New[workspace.Environment](ids), &anna, now)
	copied, _, err := target.Copy(copyFlag, recorded, false, referents(nil), later)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copied.Rules, recorded.Rules) {
		t.Errorf("copied rules = %+v, want %+v", copied.Rules, recorded.Rules)
	}
}
