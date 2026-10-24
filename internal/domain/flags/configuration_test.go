package flags_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// project's flags: copy, the one configured below, with three variations;
// gate and other, which copy may require; and old, archived.
var (
	copyFlag  = mustFlag(input("copy", flags.TypeString, variation("a", `"A"`), variation("b", `"B"`), variation("c", `"C"`)))
	gateFlag  = mustFlag(input("gate", flags.TypeBoolean))
	otherFlag = mustFlag(input("other", flags.TypeBoolean))
	oldFlag   = func() flags.Flag { f, _ := mustFlag(input("old", flags.TypeBoolean)).Archive(now); return f }()
	bob       = id.New[access.Member](ids)
)

func mustFlag(in flags.FlagInput) flags.Flag {
	f, err := flags.NewFlag(in)
	if err != nil {
		panic(err)
	}
	return f
}

// The project's experiments: one of each status on copy, one on another flag.
var experimentRefs = []flags.ExperimentRef{
	{Key: "draft_title", Flag: copyFlag.ID, Status: "draft"},
	{Key: "running_title", Flag: copyFlag.ID, Status: "running"},
	{Key: "stopped_title", Flag: copyFlag.ID, Status: "stopped"},
	{Key: "archived_title", Flag: copyFlag.ID, Status: "archived"},
	{Key: "gate_test", Flag: gateFlag.ID, Status: "draft"},
}

func referents(reqs flags.Requirements) flags.Referents {
	return flags.Referents{
		Flags: []flags.Flag{copyFlag, gateFlag, otherFlag, oldFlag}, Segments: []string{"beta_testers"},
		Declarations: declarations, Requirements: reqs, Experiments: experimentRefs,
	}
}

// salts counts the salts minted, each new one different from every other.
var salts int

// mint draws rule identifiers from ids and numbered salts.
var mint = flags.Mint{
	RuleID: func() flags.RuleID { return id.New[flags.Rule](ids) },
	Salt:   func() string { salts++; return fmt.Sprintf("salt%012d", salts) },
}

func initial() flags.Configuration {
	return flags.InitialConfiguration(copyFlag, id.New[workspace.Environment](ids), &anna, now)
}

func replace(c flags.Configuration, in flags.ConfigurationInput, reqs flags.Requirements) (flags.Configuration, flags.Changed, error) {
	return c.Replace(copyFlag, in, referents(reqs), mint, flags.Write{By: &bob, Now: now.Add(time.Hour)})
}

func base() flags.ConfigurationInput {
	return flags.ConfigurationInput{OffVariation: "a", Fallthrough: "a"}
}

func serveVariation(v string) flags.ServeInput {
	return flags.ServeInput{Kind: "variation", Variation: v}
}

func rollout(unitType, v string, percentage int) flags.ServeInput {
	return flags.ServeInput{Kind: "rollout", UnitType: unitType, Variation: v, Percentage: &percentage}
}

func configurationViolations(t *testing.T, err error) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "invalid_configuration" {
		t.Fatalf("err = %v, want invalid_configuration", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return out
}

func mustReplace(t *testing.T, c flags.Configuration, in flags.ConfigurationInput) flags.Configuration {
	t.Helper()
	out, _, err := replace(c, in, nil)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	return out
}

func TestRulesKeepTheirIdentifiersAcrossWrites(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{{Serve: serveVariation("b")}, {Serve: serveVariation("c")}}
	first := mustReplace(t, initial(), in)
	if len(first.Rules) != 2 || first.Rules[0].ID.IsZero() || first.Rules[0].ID == first.Rules[1].ID {
		t.Fatalf("rules = %+v, want two with identifiers of their own", first.Rules)
	}
	kept, added := first.Rules[1].ID, first.Rules[0].ID
	in.Rules = []flags.RuleInput{{ID: &kept, Serve: serveVariation("c")}, {Serve: serveVariation("b")}}
	second := mustReplace(t, first, in)
	if second.Rules[0].ID != kept || second.Rules[1].ID == added || second.Rules[1].ID.IsZero() {
		t.Errorf("rules = %+v, want the sent one kept and the new one given another identifier", second.Rules)
	}

	stranger := id.New[flags.Rule](ids)
	in.Rules = []flags.RuleInput{{ID: &stranger, Serve: serveVariation("b")}, {ID: &kept, Serve: serveVariation("b")},
		{ID: &kept, Serve: serveVariation("c")}}
	_, _, err := replace(second, in, nil)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/rules/0/id unknown_rule", "/rules/2/id duplicate"}) {
		t.Errorf("violations = %v", got)
	}
	in.Rules = []flags.RuleInput{{ID: &added, Serve: serveVariation("b")}}
	_, _, err = replace(second, in, nil)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/rules/0/id unknown_rule"}) {
		t.Errorf("an identifier of an earlier version: violations = %v", got)
	}
}

func TestARolloutKeepsItsSaltUntilItStopsBeingOne(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{{Serve: rollout("user", "b", 1000)}}
	first := mustReplace(t, initial(), in)
	salt := first.Rules[0].Serve.Salt
	if salt == "" {
		t.Fatalf("serve = %+v, want a salt", first.Rules[0].Serve)
	}
	ruleID := first.Rules[0].ID
	grown := rollout("user", "b", 5000)
	in.Rules = []flags.RuleInput{{ID: &ruleID, Serve: grown}}
	second := mustReplace(t, first, in)
	if s := second.Rules[0].Serve; s.Salt != salt || s.Percentage != 5000 {
		t.Errorf("grown rollout = %+v, want the salt kept", s)
	}
	echoed := grown
	echoed.Salt = salt
	in.Rules = []flags.RuleInput{{ID: &ruleID, Serve: echoed}}
	if got := mustReplace(t, second, in); got.Rules[0].Serve.Salt != salt {
		t.Errorf("echoing the salt changed it to %q", got.Rules[0].Serve.Salt)
	}

	other := grown
	other.Salt = "abcdefghijklmnop"
	in.Rules = []flags.RuleInput{{ID: &ruleID, Serve: other}, {Serve: other}}
	_, _, err := replace(second, in, nil)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/rules/0/serve/salt immutable", "/rules/1/serve/salt immutable"}) {
		t.Errorf("violations = %v", got)
	}

	in.Rules = []flags.RuleInput{{ID: &ruleID, Serve: serveVariation("b")}}
	third := mustReplace(t, second, in)
	in.Rules = []flags.RuleInput{{ID: &ruleID, Serve: rollout("user", "b", 5000)}}
	if got := mustReplace(t, third, in); got.Rules[0].Serve.Salt == "" || got.Rules[0].Serve.Salt == salt {
		t.Errorf("a rule becoming a rollout again kept salt %q, want a new one", got.Rules[0].Serve.Salt)
	}
}

func TestRuleServesAreValidated(t *testing.T) {
	in := base()
	extra := serveVariation("b")
	extra.UnitType = "user"
	in.Rules = []flags.RuleInput{
		{Serve: serveVariation("z")},
		{Serve: extra},
		{Serve: rollout("device", "z", 10001)},
		{Serve: flags.ServeInput{Kind: "rollout", UnitType: "user", Variation: "b"}},
		{Serve: rollout("user", "b", -1)},
		{Serve: flags.ServeInput{Kind: "experiment", Experiment: "nothing"}},
		{Serve: flags.ServeInput{Kind: "split"}},
		{Serve: rollout("user", "b", 0)},
		{Serve: rollout("anonymous", "c", 10000)},
	}
	_, _, err := replace(initial(), in, nil)
	want := []string{
		"/rules/0/serve/variation unknown_variation",
		"/rules/1/serve invalid_value",
		"/rules/2/serve/unit_type unknown_unit_type", "/rules/2/serve/variation unknown_variation",
		"/rules/2/serve/percentage invalid_value",
		"/rules/3/serve/percentage invalid_value",
		"/rules/4/serve/percentage invalid_value",
		"/rules/5/serve/experiment unknown_experiment",
		"/rules/6/serve/kind invalid_value",
	}
	if got := configurationViolations(t, err); !slices.Equal(got, want) {
		t.Errorf("violations = %v\nwant %v", got, want)
	}
}

func TestRuleConditionsValidateAgainstTheProject(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{{Serve: serveVariation("b"), Conditions: []flags.Condition{
		cond("os", "eq", `"ios"`),
		cond("country", "eq", `"NL"`),
		cond("app_version", "in", `["1.2.0", "two"]`),
		cond("user", "in", `["u-1", "u-2"]`),
		{Kind: "in_segment", Segment: "beta_testers"},
		{Kind: "not_in_segment", Segment: "nobody"},
		{Kind: "in_segment", Segment: "beta_testers", Attribute: "os"},
		{Kind: "prerequisite", Flag: "gate", Variation: "true"},
		{Kind: "prerequisite", Flag: "gate", Variation: "maybe"},
		{Kind: "prerequisite", Flag: "missing", Variation: "true"},
		{Kind: "prerequisite", Flag: "old", Variation: "true"},
		{Kind: "prerequisite", Flag: "other", Variation: "true", Segment: "beta_testers"},
		{Kind: "nested"},
	}}}
	_, _, err := replace(initial(), in, nil)
	want := []string{
		"/rules/0/conditions/1/attribute unknown_attribute",
		"/rules/0/conditions/2/value invalid_value",
		"/rules/0/conditions/5/segment unknown_segment",
		"/rules/0/conditions/6 invalid_value",
		"/rules/0/conditions/8/variation unknown_variation",
		"/rules/0/conditions/9/flag unknown_flag",
		"/rules/0/conditions/10/flag archived_flag",
		"/rules/0/conditions/11 invalid_value",
		"/rules/0/conditions/12/kind invalid_value",
	}
	if got := configurationViolations(t, err); !slices.Equal(got, want) {
		t.Errorf("violations = %v\nwant %v", got, want)
	}
}

func TestPrerequisitesAndTheOffAndFallthroughVariationsAreValidated(t *testing.T) {
	in := flags.ConfigurationInput{OffVariation: "z", Fallthrough: "y", Prerequisites: []flags.Prerequisite{
		{Flag: "gate", Variation: "true"}, {Flag: "gate", Variation: "false"}, {Flag: "other", Variation: "maybe"},
		{Flag: "missing", Variation: "true"}, {Flag: "old", Variation: "true"},
	}}
	_, _, err := replace(initial(), in, nil)
	want := []string{
		"/off_variation unknown_variation", "/fallthrough unknown_variation",
		"/prerequisites/1/flag duplicate", "/prerequisites/2/variation unknown_variation",
		"/prerequisites/3/flag unknown_flag", "/prerequisites/4/flag archived_flag",
	}
	if got := configurationViolations(t, err); !slices.Equal(got, want) {
		t.Errorf("violations = %v\nwant %v", got, want)
	}
}

func TestOverridesAreValidatedInAFullWrite(t *testing.T) {
	in := base()
	in.Overrides = []flags.OverrideInput{
		{UnitType: "user", UnitID: "u-1", Variation: "b", Note: ptr("  Pinned for a demo  ")},
		{UnitType: "user", UnitID: "u-1", Variation: "c"},
		{UnitType: "anonymous", UnitID: "u-1", Variation: "c", Note: ptr("   ")},
		{UnitType: "device", UnitID: "", Variation: "z"},
		{UnitType: "user", UnitID: strings.Repeat("x", 257), Variation: "b", Note: ptr(strings.Repeat("é", 201))},
	}
	_, _, err := replace(initial(), in, nil)
	want := []string{
		"/overrides/1/unit_id duplicate",
		"/overrides/3/unit_type unknown_unit_type", "/overrides/3/unit_id invalid_value", "/overrides/3/variation unknown_variation",
		"/overrides/4/unit_id too_long", "/overrides/4/note too_long",
	}
	if got := configurationViolations(t, err); !slices.Equal(got, want) {
		t.Errorf("violations = %v\nwant %v", got, want)
	}

	in.Overrides = []flags.OverrideInput{
		{UnitType: "user", UnitID: strings.Repeat("x", 256), Variation: "b", Note: ptr("  Pinned for a demo  ")},
		{UnitType: "anonymous", UnitID: "u-1", Variation: "c", Note: ptr("   ")},
	}
	out := mustReplace(t, initial(), in)
	if o := out.Overrides[0]; o.Note != "Pinned for a demo" || o.AddedBy == nil || *o.AddedBy != bob {
		t.Errorf("override = %+v, want its note trimmed and written by the writer", o)
	}
	if o := out.Overrides[1]; o.Note != "" {
		t.Errorf("a blank note = %q, want none", o.Note)
	}
}

func TestAnOverrideKeepsItsAuthorUntilItIsChanged(t *testing.T) {
	c := initial()
	c.Overrides = []flags.Override{
		{UnitType: "user", UnitID: "u-1", Variation: "b", Note: "Demo", AddedBy: &anna},
		{UnitType: "user", UnitID: "u-2", Variation: "b", Note: "Demo", AddedBy: &anna},
		{UnitType: "user", UnitID: "u-3", Variation: "b", AddedBy: &anna},
	}
	in := base()
	in.Overrides = []flags.OverrideInput{
		{UnitType: "user", UnitID: "u-1", Variation: "b", Note: ptr("Demo")},
		{UnitType: "user", UnitID: "u-2", Variation: "b", Note: ptr("Demo for sales")},
		{UnitType: "user", UnitID: "u-3", Variation: "c"},
		{UnitType: "user", UnitID: "u-4", Variation: "c"},
	}
	out := mustReplace(t, c, in)
	var authors []string
	for _, o := range out.Overrides {
		who := "anna"
		if *o.AddedBy == bob {
			who = "bob"
		}
		authors = append(authors, o.UnitID+" "+who)
	}
	if want := []string{"u-1 anna", "u-2 bob", "u-3 bob", "u-4 bob"}; !slices.Equal(authors, want) {
		t.Errorf("authors = %v, want %v", authors, want)
	}
}

func TestConfigurationLimits(t *testing.T) {
	in := base()
	for range 50 {
		in.Rules = append(in.Rules, flags.RuleInput{Serve: serveVariation("b")})
	}
	for i := range 200 {
		in.Overrides = append(in.Overrides, flags.OverrideInput{UnitType: "user", UnitID: fmt.Sprint(i), Variation: "b"})
	}
	twenty := make([]flags.Condition, 20)
	for i := range twenty {
		twenty[i] = cond("os", "exists", "")
	}
	in.Rules[0].Conditions = twenty
	full := mustReplace(t, initial(), in)
	if len(full.Rules) != 50 || len(full.Overrides) != 200 {
		t.Fatalf("at the limits: %d rules, %d overrides", len(full.Rules), len(full.Overrides))
	}

	in.Rules = append(in.Rules, flags.RuleInput{Serve: serveVariation("b")})
	in.Overrides = append(in.Overrides, flags.OverrideInput{UnitType: "user", UnitID: "one more", Variation: "b"})
	_, _, err := replace(initial(), in, nil)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/overrides too_many", "/rules too_many"}) {
		t.Errorf("violations = %v", got)
	}
	in.Rules, in.Overrides = in.Rules[:1], nil
	in.Rules[0].Conditions = append(twenty, cond("os", "exists", ""))
	_, _, err = replace(initial(), in, nil)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/rules/0/conditions too_many"}) {
		t.Errorf("violations = %v", got)
	}
	e, _ := apperrors.From(err)
	if limit := e.Violations[0].Params["max"]; limit != 20 {
		t.Errorf("params = %v, want max 20", e.Violations[0].Params)
	}
}

// chain returns the requirements of flags each requiring the next, the
// last requiring nothing.
func chain(keys ...string) flags.Requirements {
	out := flags.Requirements{}
	for i, k := range keys {
		out[k] = nil
		if i+1 < len(keys) {
			out[k] = []string{keys[i+1]}
		}
	}
	return out
}

func merged(rs ...flags.Requirements) flags.Requirements {
	out := flags.Requirements{}
	for _, r := range rs {
		for k, v := range r {
			out[k] = append(out[k], v...)
		}
	}
	return out
}

// requiring returns a configuration input requiring each flag in keys, the
// first as a prerequisite and the others in a rule's conditions.
func requiring(keys ...string) flags.ConfigurationInput {
	in := base()
	in.Prerequisites = []flags.Prerequisite{{Flag: keys[0], Variation: "true"}}
	if len(keys) > 1 {
		rule := flags.RuleInput{Serve: serveVariation("b")}
		for _, k := range keys[1:] {
			rule.Conditions = append(rule.Conditions, flags.Condition{Kind: "prerequisite", Flag: k, Variation: "true"})
		}
		in.Rules = []flags.RuleInput{rule}
	}
	return in
}

// chainViolations returns "pointer code path" for each violation of err.
func chainViolations(t *testing.T, err error) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Code != "invalid_configuration" {
		t.Fatalf("err = %v, want invalid_configuration", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, fmt.Sprint(v.Pointer, " ", v.Code, " ", v.Params["path"]))
	}
	return out
}

func TestPrerequisitesFormAnAcyclicGraphAtMostFiveDeep(t *testing.T) {
	flagsOf := func(keys ...string) flags.Referents {
		r := referents(nil)
		for _, k := range keys {
			r.Flags = append(r.Flags, mustFlag(input(k, flags.TypeBoolean)))
		}
		return r
	}
	write := func(r flags.Referents, reqs flags.Requirements, in flags.ConfigurationInput) error {
		r.Requirements = reqs
		_, _, err := initial().Replace(copyFlag, in, r, mint, flags.Write{By: &bob, Now: now})
		return err
	}
	r := flagsOf("f2", "f3", "f4", "f5", "f6", "f7")

	for name, tc := range map[string]struct {
		reqs flags.Requirements
		in   flags.ConfigurationInput
		want []string
	}{
		"itself": {nil, flags.ConfigurationInput{OffVariation: "a", Fallthrough: "a",
			Prerequisites: []flags.Prerequisite{{Flag: "copy", Variation: "a"}}},
			[]string{"/prerequisites/0/flag prerequisite_cycle [copy copy]"}},
		"a flag requiring it": {chain("gate", "copy"), requiring("gate"),
			[]string{"/prerequisites/0/flag prerequisite_cycle [copy gate copy]"}},
		"through a rule, two steps away": {merged(chain("other", "gate", "copy")), requiring("f2", "other"),
			[]string{"/rules/0/conditions/0/flag prerequisite_cycle [copy other gate copy]"}},
		"five steps down": {chain("gate", "f2", "f3", "f4", "f5"), requiring("gate"), nil},
		"six steps down": {chain("gate", "f2", "f3", "f4", "f5", "f6"), requiring("gate"),
			[]string{"/prerequisites/0/flag prerequisite_too_deep [copy gate f2 f3 f4 f5 f6]"}},
		"joining two chains into six steps": {merged(chain("f2", "f3", "copy"), chain("gate", "f4", "f5", "f6")), requiring("gate"),
			[]string{"/prerequisites/0/flag prerequisite_too_deep [f2 f3 copy gate f4 f5 f6]"}},
		"joining two chains into five steps": {merged(chain("f2", "f3", "copy"), chain("gate", "f4", "f5")), requiring("gate"), nil},
		"six steps through a rule only": {chain("other", "f2", "f3", "f4", "f5", "f6"), requiring("gate", "other"),
			[]string{"/rules/0/conditions/0/flag prerequisite_too_deep [copy other f2 f3 f4 f5 f6]"}},
		"an archived flag counts": {chain("gate", "f2", "f3", "f4", "old", "f7"), requiring("gate"),
			[]string{"/prerequisites/0/flag prerequisite_too_deep [copy gate f2 f3 f4 old f7]"}},
		"its own former requirements are replaced": {merged(chain("copy", "f2", "f3", "f4", "f5", "f6"), chain("gate")),
			requiring("gate"), nil},
	} {
		err := write(r, tc.reqs, tc.in)
		if tc.want == nil {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if got := chainViolations(t, err); !slices.Equal(got, tc.want) {
			t.Errorf("%s: violations = %v\nwant %v", name, got, tc.want)
		}
	}
}

func TestAReplaceSaysWhatItChanged(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{{Description: "Testers", Serve: serveVariation("b")}}
	in.Overrides = []flags.OverrideInput{{UnitType: "user", UnitID: "u-1", Variation: "b", Note: ptr("Demo")}}
	c := mustReplace(t, initial(), in)
	ruleID := c.Rules[0].ID
	in.Rules[0].ID = &ruleID
	same := func() flags.ConfigurationInput {
		out := in
		out.Rules = slices.Clone(in.Rules)
		out.Overrides = slices.Clone(in.Overrides)
		return out
	}

	for name, tc := range map[string]struct {
		change func(*flags.ConfigurationInput)
		want   flags.Changed
	}{
		"nothing":            {func(*flags.ConfigurationInput) {}, flags.Changed{}},
		"a rule description": {func(in *flags.ConfigurationInput) { in.Rules[0].Description = "Beta" }, flags.Changed{Any: true}},
		"an override note": {func(in *flags.ConfigurationInput) { in.Overrides[0].Note = ptr("Sales") },
			flags.Changed{Any: true}},
		"on": {func(in *flags.ConfigurationInput) { in.On = true }, flags.Changed{Any: true, Ruleset: true, Switched: true}},
		"on and a description": {func(in *flags.ConfigurationInput) { in.On = true; in.Rules[0].Description = "Beta" },
			flags.Changed{Any: true, Ruleset: true, Switched: true}},
		"on and a serve": {func(in *flags.ConfigurationInput) { in.On = true; in.Rules[0].Serve = serveVariation("c") },
			flags.Changed{Any: true, Ruleset: true}},
		"the fallthrough": {func(in *flags.ConfigurationInput) { in.Fallthrough = "c" }, flags.Changed{Any: true, Ruleset: true}},
	} {
		next := same()
		tc.change(&next)
		out, changed, err := replace(c, next, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.Equal(changed.RemovedVariations, nil) || changed.Any != tc.want.Any || changed.Ruleset != tc.want.Ruleset ||
			changed.Switched != tc.want.Switched {
			t.Errorf("%s: changed = %+v, want %+v", name, changed, tc.want)
		}
		wantVersion := c.Version
		if tc.want.Any {
			wantVersion++
		}
		if out.Version != wantVersion || (tc.want.Any && (out.UpdatedBy == nil || *out.UpdatedBy != bob)) {
			t.Errorf("%s: version %d by %v, want %d", name, out.Version, out.UpdatedBy, wantVersion)
		}
	}
}

func TestAConfigurationOfAnArchivedFlagIsNotWritten(t *testing.T) {
	if err := copyFlag.CanConfigure(); err != nil {
		t.Errorf("an active flag: %v", err)
	}
	if e, ok := apperrors.From(oldFlag.CanConfigure()); !ok || e.Kind != apperrors.KindConflict || e.Code != "archived" {
		t.Errorf("an archived flag: %v, want archived", oldFlag.CanConfigure())
	}
}

func TestRemovingAVariationIsRefusedWhileServesOverridesOrOtherFlagsNameIt(t *testing.T) {
	served := initial()
	served.Rules = []flags.Rule{{ID: id.New[flags.Rule](ids), Serve: flags.Serve{Kind: "rollout", Variation: "b", UnitType: "user"}}}
	overridden := initial()
	overridden.Overrides = []flags.Override{{UnitType: "user", UnitID: "u-1", Variation: "c"}}
	configs := []flags.EnvironmentConfiguration{
		{Environment: "production", Configuration: served}, {Environment: "staging", Configuration: overridden},
	}
	dependents := []flags.Dependent{
		{Flag: "gate", Environment: "production", Variation: "b"}, {Flag: "gate", Environment: "production", Variation: "c"},
		{Flag: "gate", Environment: "staging", Variation: "a"}, {Flag: "other", Environment: "staging", Variation: "c"},
	}
	refs := func(rs []flags.Reference) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Key+" "+r.Environment)
		}
		return out
	}
	if got := refs(flags.VariationReferences(copyFlag, []string{"b"}, configs, dependents)); !slices.Equal(got, []string{"copy production", "gate production"}) {
		t.Errorf("b: %v", got)
	}
	if got := refs(flags.VariationReferences(copyFlag, []string{"b", "c"}, configs, dependents)); !slices.Equal(got,
		[]string{"copy production", "copy staging", "gate production", "other staging"}) {
		t.Errorf("b and c: %v", got)
	}
	if got := refs(flags.DependentReferences(dependents)); !slices.Equal(got, []string{"gate production", "gate staging", "other staging"}) {
		t.Errorf("archiving: %v", got)
	}
}

func experimentServe(key string) flags.ServeInput {
	return flags.ServeInput{Kind: "experiment", Experiment: key}
}

func TestARuleServesADraftOrRunningExperimentOfItsFlag(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{
		{Serve: experimentServe("draft_title")},
		{Serve: experimentServe("running_title")},
		{Serve: experimentServe("draft_title")},
	}
	got, changed, err := replace(initial(), in, nil)
	if err != nil || !changed.Ruleset {
		t.Fatalf("replace = %v, %+v, want accepted, changing the ruleset", err, changed)
	}
	for i, want := range []string{"draft_title", "running_title", "draft_title"} {
		if s := got.Rules[i].Serve; s.Kind != flags.ServeExperiment || s.Experiment != want || s.Variation != "" || s.Salt != "" {
			t.Errorf("rule %d serves %+v, want the experiment %s alone", i, s, want)
		}
	}
}

func TestARuleServingAnExperimentIsRefusedWhenTheExperimentCannotBeServed(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{
		{Serve: experimentServe("missing")},
		{Serve: experimentServe("gate_test")},
		{Serve: experimentServe("stopped_title")},
		{Serve: experimentServe("archived_title")},
		{Serve: flags.ServeInput{Kind: "experiment", Experiment: "draft_title", Variation: "a"}},
		{Serve: flags.ServeInput{Kind: "variation", Variation: "a", Experiment: "draft_title"}},
	}
	_, _, err := replace(initial(), in, nil)
	want := []string{
		"/rules/0/serve/experiment unknown_experiment",
		"/rules/1/serve/experiment experiment_of_another_flag",
		"/rules/2/serve/experiment experiment_not_servable",
		"/rules/3/serve/experiment experiment_not_servable",
		"/rules/4/serve invalid_value",
		"/rules/5/serve invalid_value",
	}
	if got := configurationViolations(t, err); !slices.Equal(got, want) {
		t.Errorf("violations = %v\nwant %v", got, want)
	}
}

func TestARuleAlreadyServingAnExperimentKeepsServingItWhateverItsStatus(t *testing.T) {
	c := initial()
	held := mint.RuleID()
	c.Rules = []flags.Rule{{ID: held, Serve: flags.Serve{Kind: flags.ServeExperiment, Experiment: "stopped_title"}}}

	// Another rule joins, and the stopped experiment's rule keeps its place:
	// stopping an experiment never blocks the configuration's other edits.
	in := base()
	in.Rules = []flags.RuleInput{{ID: &held, Serve: experimentServe("stopped_title")}, {Serve: serveVariation("b")}}
	got, _, err := replace(c, in, nil)
	if err != nil || len(got.Rules) != 2 || got.Rules[0].ID != held || got.Rules[0].Serve.Experiment != "stopped_title" {
		t.Fatalf("keeping the rule = %+v, %v", got.Rules, err)
	}

	// The same rule turned to another experiment that cannot be served is
	// refused as a new rule would be.
	in.Rules = []flags.RuleInput{{ID: &held, Serve: experimentServe("archived_title")}}
	_, _, err = replace(c, in, nil)
	if got := configurationViolations(t, err); !slices.Equal(got, []string{"/rules/0/serve/experiment experiment_not_servable"}) {
		t.Errorf("violations = %v", got)
	}
}

func TestExperimentsHoldingAFlagAreReferencesToArchiveOrRemoveFromIt(t *testing.T) {
	experiments := []flags.ExperimentRef{
		{Key: "draft_title", Status: "draft", Weights: []flags.ExperimentWeight{{Variation: "a", Weight: 5000}, {Variation: "b", Weight: 5000}}},
		{Key: "stopped_title", Status: "stopped", Weights: []flags.ExperimentWeight{{Variation: "a", Weight: 5000}, {Variation: "c", Weight: 5000}}},
		{Key: "archived_title", Status: "archived"},
		{Key: "running_title", Status: "running"},
	}
	keys := func(refs []flags.Reference) []string {
		var out []string
		for _, r := range refs {
			out = append(out, r.Kind+" "+r.Key+" "+r.Environment)
		}
		return out
	}
	if got := keys(flags.ExperimentArchiveReferences(experiments)); !slices.Equal(got, []string{"experiment draft_title ", "experiment running_title "}) {
		t.Errorf("archive references = %v, want the draft and the running experiment", got)
	}
	if got := keys(flags.ExperimentVariationReferences(experiments, []string{"c"})); !slices.Equal(got, []string{"experiment stopped_title "}) {
		t.Errorf("references of c = %v, want the stopped experiment listing it", got)
	}
	if got := flags.ExperimentVariationReferences(experiments, []string{"z"}); len(got) != 0 {
		t.Errorf("references of z = %v, want none", got)
	}
}

func TestAProposalKeepsOnlyTheRuleIdentifiersAndSaltsTheConfigurationHolds(t *testing.T) {
	in := base()
	in.Rules = []flags.RuleInput{{Serve: rollout("user", "b", 1000)}, {Serve: serveVariation("c")}}
	live := mustReplace(t, initial(), in)
	rolloutID, variationID, stranger := live.Rules[0].ID, live.Rules[1].ID, id.New[flags.Rule](ids)
	salt := live.Rules[0].Serve.Salt

	withSalt := func(s flags.ServeInput, salt string) flags.ServeInput { s.Salt = salt; return s }
	in.Rules = []flags.RuleInput{
		{ID: &rolloutID, Serve: withSalt(rollout("user", "b", 2000), salt)},
		{ID: &rolloutID, Serve: withSalt(rollout("user", "b", 2000), "elsewhere0000000")},
		{ID: &variationID, Serve: withSalt(rollout("user", "b", 2000), salt)},
		{ID: &stranger, Serve: withSalt(rollout("user", "b", 2000), salt)},
		{Serve: withSalt(rollout("user", "b", 2000), salt)},
	}
	got := live.Proposable(in)
	type kept struct {
		id   *flags.RuleID
		salt string
	}
	want := []kept{{&rolloutID, salt}, {&rolloutID, ""}, {&variationID, ""}, {nil, ""}, {nil, ""}}
	for i, r := range got.Rules {
		if (r.ID == nil) != (want[i].id == nil) || (r.ID != nil && *r.ID != *want[i].id) || r.Serve.Salt != want[i].salt {
			t.Errorf("rule %d = id %v, salt %q; want id %v, salt %q", i, r.ID, r.Serve.Salt, want[i].id, want[i].salt)
		}
	}
	if in.Rules[3].ID == nil || in.Rules[3].Serve.Salt == "" {
		t.Error("the input given was changed")
	}
}
