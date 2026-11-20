package eval_test

import (
	"fmt"
	"testing"

	"github.com/azverinus/flagnetic/eval"
)

const boolVariations = `"variations":[{"key":"true","value":true},{"key":"false","value":false}],"off_variation":"false"`

// TestInvalidRolloutServeIsInconsistent: a rollout serve without a unit
// type or salt, or with a percentage outside 0 to 10000, is found when the
// ruleset is decoded and is an inconsistent_ruleset only where evaluation
// reaches it; a rule matching before it still serves.
func TestInvalidRolloutServeIsInconsistent(t *testing.T) {
	for name, serve := range map[string]string{
		"missing salt":      `{"kind":"rollout","unit_type":"user","variation":"true","percentage":5000}`,
		"missing unit type": `{"kind":"rollout","salt":"s","variation":"true","percentage":5000}`,
		"percentage above":  `{"kind":"rollout","unit_type":"user","salt":"s","variation":"true","percentage":10001}`,
		"percentage below":  `{"kind":"rollout","unit_type":"user","salt":"s","variation":"true","percentage":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			rs := decode(t, []byte(fmt.Sprintf(`{"schema_version":1,"version":1,"scope":"server","unit_types":["user"],"attributes":[{"key":"os","type":"string"}],
				"flags":[{"key":"invalid","type":"boolean","on":true,%s,"fallthrough":"false",
				"rules":[
					{"id":"r1","conditions":[{"kind":"attribute","attribute":"os","op":"eq","value":"ios"}],"serve":{"kind":"variation","variation":"true"}},
					{"id":"rx","conditions":[],"serve":%s}]}]}`, boolVariations, serve)))
			reached := rs.Evaluate(eval.Context{Units: map[string]string{"user": "u1"}, Attributes: map[string]any{"os": "linux"}}, "invalid")
			if reached.Reason != eval.ReasonError || reached.ErrorCode != eval.ErrorInconsistentRuleset {
				t.Errorf("reaching the invalid serve: %+v", reached)
			}
			before := rs.Evaluate(eval.Context{Units: map[string]string{"user": "u1"}, Attributes: map[string]any{"os": "ios"}}, "invalid")
			if before.Reason != eval.ReasonRule || before.RuleID != "r1" {
				t.Errorf("a rule matching before the invalid serve: %+v", before)
			}
		})
	}
}

// TestOverrideShadowLeavesNothingInTheMemo: while an override's shadow body
// runs, the flag is still being visited, so a prerequisite cycle closing
// through it must not leave a cycle result behind for the flags evaluated
// after it, in the same call or the same evaluation.
func TestOverrideShadowLeavesNothingInTheMemo(t *testing.T) {
	flag := func(key, fallthrough_, prereqs, overrides string) string {
		return fmt.Sprintf(`{"key":%q,"type":"boolean","on":true,%s,"fallthrough":%q,"prerequisites":[%s],"overrides":[%s]}`,
			key, boolVariations, fallthrough_, prereqs, overrides)
	}
	rs := decode(t, []byte(`{"schema_version":1,"version":1,"scope":"server","unit_types":["user"],"flags":[`+
		flag("a", "false", `{"flag":"b","variation":"true"}`, `{"unit_type":"user","unit_id":"u1","variation":"true"}`)+`,`+
		flag("b", "true", `{"flag":"a","variation":"true"}`, ``)+`,`+
		flag("c", "true", `{"flag":"a","variation":"true"},{"flag":"b","variation":"true"}`, ``)+`]}`))
	ctx := eval.Context{Units: map[string]string{"user": "u1"}}

	for _, key := range []string{"b", "c"} {
		if r := rs.Evaluate(ctx, key); r.Reason != eval.ReasonFallthrough || string(r.Value) != "true" || !r.OverrideInfluenced {
			t.Errorf("%s: %+v", key, r)
		}
	}
	all := rs.EvaluateAll(ctx)
	for _, key := range []string{"a", "b", "c"} {
		if all[key].Reason == eval.ReasonError {
			t.Errorf("EvaluateAll %s: %+v", key, all[key])
		}
	}
	if r := rs.Evaluate(eval.Context{Units: map[string]string{"user": "u2"}}, "b"); r.ErrorCode != eval.ErrorPrerequisiteCycle {
		t.Errorf("without the override the cycle is an error: %+v", r)
	}
}
