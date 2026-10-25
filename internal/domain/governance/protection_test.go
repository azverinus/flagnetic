package governance_test

import (
	"encoding/json"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// served is the ruleset of a protected production holding checkout on with
// a rule on the segment employees, banner off, and the unused segment
// beta.
func served() *eval.Ruleset {
	return &eval.Ruleset{
		SchemaVersion: 1, Version: 7, Scope: "server",
		Environment: &eval.Environment{Key: "production", Protected: true},
		UnitTypes:   []string{"user"},
		Attributes:  []eval.Attribute{{Key: "country", Type: "string"}},
		Segments: []eval.Segment{
			{Key: "beta", Conditions: []eval.Condition{attribute("country", `"fr"`)}},
			{Key: "employees", Conditions: []eval.Condition{attribute("country", `"de"`)}},
		},
		Flags: []eval.Flag{
			{Key: "banner", Type: "string", OffVariation: "a", Fallthrough: "a",
				Variations: []eval.Variation{{Key: "a", Value: json.RawMessage(`"Hello"`)}, {Key: "b", Value: json.RawMessage(`"Hi"`)}}},
			{Key: "checkout", Type: "boolean", On: true, OffVariation: "false", Fallthrough: "false",
				Variations: []eval.Variation{{Key: "false", Value: json.RawMessage(`false`)}, {Key: "true", Value: json.RawMessage(`true`)}},
				Rules: []eval.Rule{{ID: "r1", Conditions: []eval.Condition{{Kind: eval.ConditionInSegment, Segment: "employees"}},
					Serve: eval.Serve{Kind: eval.ServeVariation, Variation: "true"}}}},
		},
	}
}

func attribute(key, value string) eval.Condition {
	return eval.Condition{Kind: eval.ConditionAttribute, Attribute: key, Op: "eq", Value: json.RawMessage(value)}
}

func flag(rs *eval.Ruleset, key string) *eval.Flag {
	for i := range rs.Flags {
		if rs.Flags[i].Key == key {
			return &rs.Flags[i]
		}
	}
	panic("no flag " + key)
}

func segment(rs *eval.Ruleset, key string) *eval.Segment {
	for i := range rs.Segments {
		if rs.Segments[i].Key == key {
			return &rs.Segments[i]
		}
	}
	panic("no segment " + key)
}

func TestWhatNoEvaluationDependsOnLeavesAProtectedRulesetUnchanged(t *testing.T) {
	for name, change := range map[string]func(rs *eval.Ruleset){
		"the version":           func(rs *eval.Ruleset) { rs.Version = 8 },
		"the protection marker": func(rs *eval.Ruleset) { rs.Environment.Protected = false },
		"the unit types":        func(rs *eval.Ruleset) { rs.UnitTypes = append(rs.UnitTypes, "account") },
		"the attributes": func(rs *eval.Ruleset) {
			rs.Attributes = append(rs.Attributes, eval.Attribute{Key: "plan", Type: eval.AttributeType("string")})
		},
		"an unreferenced segment's conditions": func(rs *eval.Ruleset) {
			segment(rs, "beta").Conditions = []eval.Condition{attribute("country", `"it"`)}
		},
		"an unreferenced segment's private switch": func(rs *eval.Ruleset) { segment(rs, "beta").Private = true },
		"a new unreferenced segment": func(rs *eval.Ruleset) {
			rs.Segments = append(rs.Segments, eval.Segment{Key: "gamma"})
		},
		"an unreferenced segment removed": func(rs *eval.Ruleset) { rs.Segments = rs.Segments[1:] },
		"a new flag off": func(rs *eval.Ruleset) {
			rs.Flags = append(rs.Flags, eval.Flag{Key: "fresh", Type: "boolean", OffVariation: "false"})
		},
		"a flag off removed": func(rs *eval.Ruleset) { rs.Flags = rs.Flags[1:] },
	} {
		after := served()
		change(after)
		if governance.RulesetChanged(served(), after) {
			t.Errorf("%s: changed, want the ruleset judged unchanged", name)
		}
	}
}

func TestWhatAnEvaluationDependsOnChangesAProtectedRuleset(t *testing.T) {
	for name, change := range map[string]func(rs *eval.Ruleset){
		"a flag switched off": func(rs *eval.Ruleset) { flag(rs, "checkout").On = false },
		"a flag switched on":  func(rs *eval.Ruleset) { flag(rs, "banner").On = true },
		"an off flag's off variation's value": func(rs *eval.Ruleset) {
			flag(rs, "banner").Variations[0].Value = json.RawMessage(`"Welcome"`)
		},
		"automatic exposure": func(rs *eval.Ruleset) { flag(rs, "checkout").AutomaticExposure = true },
		"a rule": func(rs *eval.Ruleset) {
			flag(rs, "checkout").Rules[0].Serve.Variation = "false"
		},
		"an override": func(rs *eval.Ruleset) {
			flag(rs, "checkout").Overrides = []eval.Override{{UnitType: "user", UnitID: "qa-7", Variation: "true"}}
		},
		"a referenced segment's conditions": func(rs *eval.Ruleset) {
			segment(rs, "employees").Conditions = []eval.Condition{attribute("country", `"at"`)}
		},
		"a referenced segment's private switch": func(rs *eval.Ruleset) { segment(rs, "employees").Private = true },
		"a new flag on": func(rs *eval.Ruleset) {
			rs.Flags = append(rs.Flags, eval.Flag{Key: "fresh", Type: "boolean", On: true, OffVariation: "false", Fallthrough: "false"})
		},
		"a flag on removed": func(rs *eval.Ruleset) { rs.Flags = rs.Flags[:1] },
	} {
		after := served()
		change(after)
		if !governance.RulesetChanged(served(), after) {
			t.Errorf("%s: unchanged, want the ruleset judged changed", name)
		}
	}
}

// A segment referenced on one side only is referenced by a flag whose rules
// changed, which the flag's own comparison already judges: the segment
// itself does not count.
func TestASegmentCountsOnlyWhenReferencedOnBothSides(t *testing.T) {
	after := served()
	flag(after, "checkout").Rules[0].Conditions[0].Segment = "beta"
	segment(after, "beta").Conditions = []eval.Condition{attribute("country", `"it"`)}
	if !governance.RulesetChanged(served(), after) {
		t.Error("a rule moved to another segment: unchanged, want changed by the rule")
	}

	// A flag off on both sides names a segment in a rule nobody is served;
	// the segment still counts, since switching the flag on would serve it.
	before, after := served(), served()
	for _, rs := range []*eval.Ruleset{before, after} {
		flag(rs, "banner").Rules = []eval.Rule{{ID: "r2", Conditions: []eval.Condition{{Kind: eval.ConditionNotInSegment, Segment: "beta"}},
			Serve: eval.Serve{Kind: eval.ServeVariation, Variation: "b"}}}
	}
	segment(after, "beta").Private = true
	if !governance.RulesetChanged(before, after) {
		t.Error("a segment an off flag's rule names on both sides: unchanged, want changed")
	}
}

func TestARulesetChangeIsRefusedNamingTheProtectedEnvironments(t *testing.T) {
	err := governance.ErrChangeRequestRequired([]string{"staging", "production"})
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindForbidden || e.Code != "change_request_required" {
		t.Fatalf("err = %v, want forbidden change_request_required", err)
	}
	if e.Detail != "this write would change the ruleset of the protected environments staging, production; propose it as a change request" {
		t.Errorf("detail = %q", e.Detail)
	}
	if one, _ := apperrors.From(governance.ErrChangeRequestRequired([]string{"production"})); one.Detail !=
		"this write would change the ruleset of the protected environment production; propose it as a change request" {
		t.Errorf("detail = %q", one.Detail)
	}
}
