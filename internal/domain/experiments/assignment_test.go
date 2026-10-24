package experiments_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
)

func assignable() experiments.Experiment {
	return experiments.Experiment{
		Key: "checkout_v2", UnitType: "user", Status: experiments.StatusRunning,
		Variations: []experiments.Weight{
			{Variation: "long", Weight: 3000}, {Variation: "huge", Weight: 0}, {Variation: "short", Weight: 7000},
		},
		TrafficAllocation: 6000, InclusionSalt: "a3f9c2e17b4d0e58", VariationSalt: "7e1d4c90b2a6f3d1",
	}
}

// experimentRuleset is the ruleset an SDK gets for a flag whose only rule
// serves e.
func experimentRuleset(t *testing.T, e experiments.Experiment) *eval.Ruleset {
	t.Helper()
	weights := make([]map[string]any, 0, len(e.Variations))
	variations := []map[string]any{{"key": "off", "value": "off"}}
	for _, w := range e.Variations {
		weights = append(weights, map[string]any{"variation": w.Variation, "weight": w.Weight})
		variations = append(variations, map[string]any{"key": w.Variation, "value": w.Variation})
	}
	doc, err := json.Marshal(map[string]any{
		"schema_version": 1, "version": 1, "scope": "server", "unit_types": []string{e.UnitType},
		"flags": []map[string]any{{
			"key": "checkout_title", "type": "string", "variations": variations, "on": true,
			"off_variation": "off", "fallthrough": "off",
			"rules": []map[string]any{{"id": "r1", "conditions": []any{}, "serve": map[string]any{
				"kind": "experiment", "experiment": e.Key, "unit_type": e.UnitType,
				"inclusion_salt": e.InclusionSalt, "variation_salt": e.VariationSalt,
				"traffic_allocation": e.TrafficAllocation, "weights": weights,
			}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := eval.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// An assignment is what the evaluator serves: the same buckets, the same
// inclusion and the same variation, for every unit.
func TestAssignmentEqualsTheEvaluators(t *testing.T) {
	e := assignable()
	rs := experimentRuleset(t, e)
	included := 0
	for i := range 5000 {
		unit := fmt.Sprintf("user-%d", i)
		a := e.Assign(unit)
		r := rs.Evaluate(eval.Context{Units: map[string]string{"user": unit}}, "checkout_title")
		if a.InclusionBucket != eval.Bucket(e.InclusionSalt, unit) || a.VariationBucket != eval.Bucket(e.VariationSalt, unit) {
			t.Fatalf("%s: buckets %d and %d differ from the hash's", unit, a.InclusionBucket, a.VariationBucket)
		}
		if r.Experiment == nil {
			if a.Included || a.Variation != nil {
				t.Fatalf("%s: assigned %+v, the evaluator left it out", unit, a)
			}
			continue
		}
		included++
		x := r.Experiment
		if !a.Included || a.Variation == nil || *a.Variation != x.Variation ||
			a.InclusionBucket != x.InclusionBucket || a.VariationBucket != x.VariationBucket {
			t.Fatalf("%s: assigned %+v, the evaluator served %+v", unit, a, x)
		}
	}
	if included < 2800 || included > 3200 {
		t.Errorf("%d of 5000 included at an allocation of 60 %%", included)
	}
}

func TestAssignmentCarriesWhatRecomputesIt(t *testing.T) {
	e := assignable()
	a := e.Assign("user-1")
	if a.UnitType != "user" || a.UnitID != "user-1" || a.InclusionSalt != e.InclusionSalt || a.VariationSalt != e.VariationSalt ||
		a.TrafficAllocation != 6000 {
		t.Errorf("assignment = %+v", a)
	}
	want := `SELECT
    unit_id,
    murmurHash3_32(concat('a3f9c2e17b4d0e58', '.', unit_id)) % 10000 AS inclusion_bucket,
    murmurHash3_32(concat('7e1d4c90b2a6f3d1', '.', unit_id)) % 10000 AS variation_bucket,
    inclusion_bucket < 6000 AS included,
    if(included, multiIf(variation_bucket < 3000, 'long', variation_bucket < 10000, 'short', NULL), NULL) AS variation
FROM (SELECT 'user-1' AS unit_id)`
	if a.SQL != want {
		t.Errorf("SQL =\n%s\nwant\n%s", a.SQL, want)
	}
}

// The SQL quotes what it embeds as ClickHouse string literals, so any unit
// ID or variation key reads back as itself.
func TestAssignmentSQLQuotesLiterals(t *testing.T) {
	e := assignable()
	e.Variations[0].Variation = `it's`
	a := e.Assign(`o'brien\x`)
	for _, want := range []string{`'it\'s'`, `SELECT 'o\'brien\\x' AS unit_id`} {
		if !strings.Contains(a.SQL, want) {
			t.Errorf("SQL lacks %s:\n%s", want, a.SQL)
		}
	}
}

// Assignment reads the experiment's own fields whatever its status: a draft
// is assigned as it would be once started, and an allocation of 0 includes
// no one.
func TestAssignmentInAnyStatus(t *testing.T) {
	e := assignable()
	e.Status, e.TrafficAllocation = experiments.StatusDraft, 0
	if a := e.Assign("user-1"); a.Included || a.Variation != nil {
		t.Errorf("allocation 0: %+v", a)
	}
}
