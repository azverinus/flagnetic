package delivery_test

import (
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The project the property test writes configurations in: every attribute
// type, two unit types, a public and a private segment, flags of three
// types to require, and an archived one.
var (
	propUnits      = unitTypes("user", "anonymous")
	propAttributes = attributes("s", "string", "n", "number", "b", "boolean", "v", "semver", "d", "date", "l", "list")
	propSubject    = newFlag("subject", flags.TypeString, "a", `"A"`, "b", `"B"`, "c", `"C"`)
	propGate       = newFlag("gate", flags.TypeBoolean, "true", "true", "false", "false")
	propLevel      = newFlag("level", flags.TypeNumber, "one", "1", "two", "2")
	propOld        = func() flags.Flag {
		f := newFlag("old", flags.TypeBoolean, "true", "true", "false", "false")
		at := now
		f.ArchivedAt = &at
		return f
	}()
	propSegments = []flags.Segment{
		{Key: "beta", Conditions: []flags.Condition{{Kind: flags.ConditionAttribute, Attribute: "s", Op: "starts_with", Value: "be"}}},
		{Key: "staff", Private: true, Conditions: []flags.Condition{{Kind: flags.ConditionAttribute, Attribute: "n", Op: "gt", Value: 3.0}}},
	}
)

// pick returns one of options.
func pick[T any](r *rand.Rand, options ...T) T { return options[r.IntN(len(options))] }

// attributeCondition returns an attribute condition that the validation
// mostly accepts and sometimes refuses: an operator fitting the attribute's
// type with a value fitting the operator, now and then mixed up.
func attributeCondition(r *rand.Rand) flags.Condition {
	strs := func() []any { return []any{pick(r, "a", "be", "x.y"), pick(r, "z", "")} }
	values := map[string]func() (string, any){
		"s": func() (string, any) {
			op := pick(r, "eq", "neq", "contains", "not_contains", "starts_with", "ends_with", "in", "not_in", "matches", "exists", "not_exists")
			switch op {
			case "in", "not_in":
				return op, strs()
			case "matches":
				return op, pick[any](r, "^a.*z$", "(?i)beta", "[", "a{2,}")
			case "exists", "not_exists":
				return op, nil
			}
			return op, pick[any](r, "a", "beta", "")
		},
		"n": func() (string, any) {
			op := pick(r, "eq", "neq", "lt", "lte", "gt", "gte", "in", "not_in")
			if op == "in" || op == "not_in" {
				return op, []any{1.0, 2.5, -3.0}
			}
			return op, pick[any](r, 0.0, 3.0, 1e9, -0.5)
		},
		"b": func() (string, any) { return pick(r, "eq", "neq"), pick[any](r, true, false) },
		"v": func() (string, any) {
			op := pick(r, "eq", "neq", "lt", "lte", "gt", "gte", "in", "not_in")
			if op == "in" || op == "not_in" {
				return op, []any{"1.2.3", pick(r, "2.0.0-beta.1", "1.x")}
			}
			return op, pick[any](r, "1.41.0", "2.0.0-rc.1", "1.2", "not a version")
		},
		"d": func() (string, any) {
			return pick(r, "before", "after"), pick[any](r, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00.5+02:00", "yesterday")
		},
		"l": func() (string, any) {
			op := pick(r, "contains", "not_contains", "contains_any", "contains_all", "is_empty")
			switch op {
			case "contains_any", "contains_all":
				return op, strs()
			case "is_empty":
				return op, nil
			}
			return op, pick[any](r, "a", "be")
		},
		"user": func() (string, any) { return pick(r, "in", "eq"), pick[any](r, []any{"u-1", "u-2"}, "u-3") },
	}
	attribute := pick(r, "s", "n", "b", "v", "d", "l", "user")
	op, value := values[attribute]()
	if r.IntN(10) == 0 {
		// A value of another type, or for another operator.
		value = pick[any](r, 1.0, "x", true, []any{1.0})
	}
	return flags.Condition{Kind: flags.ConditionAttribute, Attribute: attribute, Op: op, Value: value}
}

func ruleCondition(r *rand.Rand) flags.Condition {
	switch r.IntN(4) {
	case 0:
		return flags.Condition{Kind: pick(r, flags.ConditionInSegment, flags.ConditionNotInSegment), Segment: pick(r, "beta", "staff", "gone")}
	case 1:
		p := pick(r, flags.Prerequisite{Flag: "gate", Variation: "true"}, flags.Prerequisite{Flag: "level", Variation: "two"},
			flags.Prerequisite{Flag: "old", Variation: "true"}, flags.Prerequisite{Flag: "subject", Variation: "a"})
		return flags.Condition{Kind: flags.ConditionPrerequisite, Flag: p.Flag, Variation: p.Variation}
	default:
		return attributeCondition(r)
	}
}

func configurationInput(r *rand.Rand) flags.ConfigurationInput {
	variation := func() string { return pick(r, "a", "b", "c", "a", "b", "c", "z") }
	in := flags.ConfigurationInput{On: r.IntN(4) > 0, OffVariation: variation(), Fallthrough: variation()}
	for range r.IntN(3) {
		in.Prerequisites = append(in.Prerequisites, pick(r, flags.Prerequisite{Flag: "gate", Variation: "true"},
			flags.Prerequisite{Flag: "level", Variation: pick(r, "one", "three")}))
	}
	for i := range r.IntN(4) {
		in.Overrides = append(in.Overrides, flags.OverrideInput{
			UnitType: pick(r, "user", "anonymous"), UnitID: "u-" + strconv.Itoa(i), Variation: variation(), Note: pick(r, nil, ptr("Demo")),
		})
	}
	for range r.IntN(6) {
		rule := flags.RuleInput{Description: "Described"}
		for range r.IntN(4) {
			rule.Conditions = append(rule.Conditions, ruleCondition(r))
		}
		if r.IntN(2) == 0 {
			rule.Serve = flags.ServeInput{Kind: flags.ServeVariation, Variation: variation()}
		} else {
			rule.Serve = flags.ServeInput{Kind: flags.ServeRollout, Variation: variation(), UnitType: pick(r, "user", "anonymous"),
				Percentage: ptr(pick(r, 0, 1, 5000, 9999, 10_000))}
		}
		in.Rules = append(in.Rules, rule)
	}
	return in
}

func randomContext(r *rand.Rand) eval.Context {
	ctx := eval.Context{Units: map[string]string{}, Attributes: map[string]any{}}
	if r.IntN(3) > 0 {
		ctx.Units["user"] = pick(r, "u-0", "u-1", "u-3", "")
	}
	if r.IntN(2) > 0 {
		ctx.Units["anonymous"] = pick(r, "d-1", "d-2")
	}
	for key, values := range map[string][]any{
		"s": {"a", "beta", "abz", 7.0},
		"n": {0.0, 3.0, 4.0, "4"},
		"b": {true, false},
		"v": {"1.41.0", "2.0.0", "1.2", "x"},
		"d": {"2025-12-31T23:00:00Z", 1767225600000.0, "never"},
		"l": {[]any{"a", "be"}, []any{}, "a"},
	} {
		if r.IntN(3) > 0 {
			ctx.Attributes[key] = pick(r, values...)
		}
	}
	return ctx
}

func ptr[T any](v T) *T { return &v }

// TestEveryAcceptedConfigurationMaterializesIntoAnEvaluableRuleset is the
// promise the validation makes SDKs: a configuration it accepts holds
// nothing an evaluator does not know and nothing it cannot satisfy, so no
// context ever meets inconsistent_ruleset.
func TestEveryAcceptedConfigurationMaterializesIntoAnEvaluableRuleset(t *testing.T) {
	r := rand.New(rand.NewPCG(4, 2))
	decls := flags.NewDeclarations(propUnits, propAttributes)
	everyFlag := []flags.Flag{propSubject, propGate, propLevel, propOld}
	referents := flags.Referents{
		Flags: everyFlag, Segments: []string{"beta", "staff"}, Declarations: decls,
		Requirements: flags.Requirements{"gate": {"level"}},
	}
	mint := flags.Mint{RuleID: func() flags.RuleID { return newRuleID() }, Salt: func() string { return "s" + strconv.Itoa(r.IntN(1e9)) }}
	gate := flags.InitialConfiguration(propGate, env, nil, now)
	gate.Prerequisites = []flags.Prerequisite{{Flag: "level", Variation: "one"}}
	others := []flags.Configuration{
		gate, flags.InitialConfiguration(propLevel, env, nil, now), flags.InitialConfiguration(propOld, env, nil, now),
	}

	const runs = 20_000
	accepted := 0
	for run := range runs {
		before := flags.InitialConfiguration(propSubject, env, nil, now)
		after, _, err := before.Replace(propSubject, configurationInput(r), referents, mint, flags.Write{Now: now})
		if err != nil {
			continue
		}
		accepted++
		doc, err := delivery.Materialize(delivery.Materials{
			Environment: "production", Version: int64(run + 1), UnitTypes: propUnits, Attributes: propAttributes,
			Segments: propSegments, Flags: everyFlag, Configurations: append([]flags.Configuration{after}, others...),
		})
		if err != nil {
			t.Fatal(err)
		}
		rs, err := eval.Decode(doc)
		if err != nil {
			t.Fatalf("run %d: %v\n%s", run, err, doc)
		}
		if u := rs.UnsupportedRules(); len(u) > 0 {
			t.Fatalf("run %d: unsupported %v\n%s", run, u, doc)
		}
		for range 5 {
			ctx := randomContext(r)
			for key, res := range delivery.Evaluate(rs, ctx, nil) {
				if res.ErrorCode == eval.ErrorInconsistentRuleset {
					t.Fatalf("run %d: %s for %+v is inconsistent_ruleset\n%s", run, key, ctx, doc)
				}
			}
		}
	}
	// The property says nothing unless the validation accepts a fair share
	// of what is generated.
	if accepted < runs/10 {
		t.Errorf("only %d of %d configurations accepted", accepted, runs)
	}
	t.Logf("%d of %d configurations accepted", accepted, runs)
}

func newRuleID() flags.RuleID { return id.New[flags.Rule](ids) }
