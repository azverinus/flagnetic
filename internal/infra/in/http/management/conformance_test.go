package management_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// conformanceDir holds the fixtures every evaluator passes.
const conformanceDir = "../../../../../conformance/fixtures"

// fixturesNotRulesets are the fixture files that hold no ruleset, and
// scope/client the one ruleset of client scope, which the management API
// does not write: it writes configurations, and client scope is a view of
// them SDK keys receive.
var fixturesNotRulesets = []string{"murmur3", "regex", "scope/client"}

// refusedByTheAPI are the fixture flags and segments the management API
// refuses to write: experiment serves, which a configuration cannot hold
// yet, and documents broken on purpose to pin how an evaluator copes with
// what no valid write produces (dangling references, unknown constructs,
// cycles, unparsable values). The cases of a flag reaching one of them are
// not run. Every refusal must be listed here and every entry refused, so a
// new fixture is never skipped silently.
var refusedByTheAPI = []string{
	// Values no write accepts: a date and semantic versions that do not
	// parse.
	"dates/formats bad_condition_value",
	"semver/parsing case_34",
	"semver/parsing case_35",
	"semver/unparsable-list-items in_list_with_unparsable_item",
	"semver/unparsable-list-items not_in_list_with_unparsable_item",
	"semver/unparsable-list-items not_in_only_unparsable",
	// Documents broken on purpose.
	"errors/codes broken_condition_after_false",
	"errors/codes broken_rule_not_reached",
	"errors/codes broken_weights",
	"errors/codes invalid_regex",
	"errors/codes missing_condition_flag",
	"errors/codes missing_fallthrough",
	"errors/codes missing_off_variation",
	"errors/codes missing_override_variation",
	"errors/codes missing_prerequisite_flag",
	"errors/codes missing_prerequisite_variation",
	"errors/codes missing_rule_variation",
	"errors/codes missing_segment",
	"errors/codes operator_not_for_type",
	"errors/codes undeclared_attribute",
	"errors/codes wrong_value_type",
	// Experiment serves.
	"experiments/allocation-growth alloc_10000",
	"experiments/allocation-growth alloc_2000",
	"experiments/allocation-growth alloc_5000",
	"experiments/automatic-exposure auto_off",
	"experiments/automatic-exposure auto_on",
	"experiments/boundaries alloc_0",
	"experiments/boundaries alloc_10000",
	"experiments/boundaries alloc_7000",
	"experiments/boundaries conditions_fail",
	"experiments/boundaries keyed_on_user",
	"experiments/boundaries two_experiments",
	"experiments/boundaries weights_235",
	"overrides/influence checkout",
	"overrides/influence checkout_not_included",
	"overrides/influence checkout_off",
	"overrides/influence checkout_shadow_prerequisite_fails",
	"overrides/influence checkout_shadow_rule_first",
	"overrides/influence gated_experiment",
	// The writes closing each cycle.
	"prerequisites/cycle b",
	"prerequisites/cycle back",
	"prerequisites/cycle self",
	// Constructs no evaluator knows yet, and what names them.
	"unsupported/rules segment future_segment",
	"unsupported/rules segment segment_of_a_future_kind",
	"unsupported/rules several_unknown_constructs",
	"unsupported/rules unknown_condition_kind",
	"unsupported/rules unknown_condition_kind_in_a_segment",
	"unsupported/rules unknown_operator",
	"unsupported/rules unknown_operator_in_a_segment",
	"unsupported/rules unknown_operator_not_reached",
	"unsupported/rules unknown_operator_while_off",
	"unsupported/rules unknown_serve_kind",
}

type conformanceFixture struct {
	Name    string       `json:"name"`
	Ruleset eval.Ruleset `json:"ruleset"`
	Cases   []struct {
		Name    string          `json:"name"`
		Context json.RawMessage `json:"context"`
		Flag    string          `json:"flag"`
		Expect  json.RawMessage `json:"expect"`
	} `json:"cases"`
}

// loadedFixture is a fixture's ruleset as the API wrote it.
type loadedFixture struct {
	// refused are the flags and segments the API refused, by key.
	refused map[string]string
	// ruleIDs and salts are what the server gave each written flag's
	// rules, by flag key in the rules' order.
	ruleIDs, salts map[string][]string
	// fixtureIDs maps each identifier the server gave a rule to the
	// fixture's, which may be empty.
	fixtureIDs map[string]string
}

func TestEvaluateAgreesWithTheEvaluatorOnConformanceRulesets(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	var refused []string
	ran := 0
	for _, path := range conformanceFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var f conformanceFixture
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		t.Run(f.Name, func(t *testing.T) {
			p := createProject(t, admin, projectKey())
			loaded := loadFixture(t, admin, p.Key, f.Ruleset)
			for _, key := range mapKeys(loaded.refused) {
				refused = append(refused, f.Name+" "+key)
				t.Logf("refused %s: %s", key, loaded.refused[key])
			}
			reference := withServerRules(t, f.Ruleset, loaded)
			for _, c := range f.Cases {
				reached, rollouts := reach(f.Ruleset, c.Flag)
				if slices.ContainsFunc(reached, func(key string) bool { return loaded.refused[key] != "" }) {
					continue
				}
				ran++
				var ctx eval.Context
				if err := json.Unmarshal(c.Context, &ctx); err != nil {
					t.Fatal(err)
				}
				var body management.EvaluationContext
				if err := json.Unmarshal(c.Context, &body); err != nil {
					t.Fatal(err)
				}
				res := evaluate(t, admin, p.Key, "production", management.EvaluationRequest{Context: body, Flags: &[]string{c.Flag}})
				if res.JSON200 == nil {
					t.Fatalf("%s: evaluate = %d %s", c.Name, res.StatusCode(), res.Body)
				}
				got := res.JSON200.Flags[c.Flag]
				want := delivery.Evaluate(reference, ctx, []string{c.Flag})[c.Flag]
				if server, reference := resultJSON(t, got, nil), referenceJSON(t, want); !sameJSON(t, server, reference) {
					t.Errorf("%s: server %s, reference evaluator %s", c.Name, server, reference)
				}
				// Without a rollout on the way, the salts the server gave
				// change nothing, so the fixture's own expectation holds.
				if server := resultJSON(t, got, loaded.fixtureIDs); !rollouts && !sameJSON(t, server, c.Expect) {
					t.Errorf("%s: server %s, fixture expects %s", c.Name, server, c.Expect)
				}
			}
		})
	}
	slices.Sort(refused)
	if want := slices.Sorted(slices.Values(refusedByTheAPI)); !slices.Equal(refused, want) {
		t.Errorf("refused by the API:\n%s\nwant:\n%s", strings.Join(refused, "\n"), strings.Join(refusedByTheAPI, "\n"))
	}
	if ran < 300 {
		t.Errorf("ran %d cases, want the fixtures' cases the API can express", ran)
	}
}

func conformanceFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(conformanceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		name := strings.TrimSuffix(filepath.ToSlash(strings.TrimPrefix(path, conformanceDir+"/")), ".json")
		if !slices.Contains(fixturesNotRulesets, name) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// loadFixture writes rs through the API into project's production
// environment: its unit types and attributes, segments, flags and their
// configurations, recording what the API refuses.
func loadFixture(t *testing.T, c *management.ClientWithResponses, project string, rs eval.Ruleset) loadedFixture {
	t.Helper()
	l := loadedFixture{refused: map[string]string{}, ruleIDs: map[string][]string{}, salts: map[string][]string{}, fixtureIDs: map[string]string{}}
	units := map[string]bool{}
	for _, u := range do(c.ListUnitTypesWithResponse(t.Context(), project)).JSON200.Items {
		units[u.Key] = true
	}
	for _, u := range rs.UnitTypes {
		if !units[u] {
			declareUnitType(t, c, project, u)
		}
	}
	types := map[string]string{}
	for _, a := range do(c.ListAttributesWithResponse(t.Context(), project)).JSON200.Items {
		types[a.Key] = string(a.Type)
	}
	for _, a := range rs.Attributes {
		switch types[a.Key] {
		case string(a.Type):
		case "":
			res := do(c.CreateAttributeWithResponse(t.Context(), project, management.AttributeCreate{
				Key: a.Key, Name: a.Key, Type: management.AttributeType(a.Type)}))
			if res.JSON201 == nil {
				t.Fatalf("attribute %s = %d %s", a.Key, res.StatusCode(), res.Body)
			}
		default:
			t.Fatalf("attribute %s is a %s, the fixture declares a %s", a.Key, types[a.Key], a.Type)
		}
	}
	for _, s := range rs.Segments {
		res := do(c.CreateSegmentWithResponse(t.Context(), project, management.SegmentCreate{
			Key: s.Key, Name: s.Key, Private: &s.Private, Conditions: conditionsOf(s.Conditions)}))
		if res.JSON201 == nil {
			l.refused["segment "+s.Key] = string(res.Body)
		}
	}
	for _, f := range rs.Flags {
		body := management.FlagCreate{Key: f.Key, Name: f.Key, Type: management.FlagType(f.Type), AutomaticExposure: &f.AutomaticExposure}
		if f.Type != "boolean" {
			vs := make([]management.VariationInput, len(f.Variations))
			for i, v := range f.Variations {
				vs[i] = management.VariationInput{Key: v.Key, Value: v.Value}
			}
			body.Variations = &vs
		}
		res := do(c.CreateFlagWithResponse(t.Context(), project, body))
		switch {
		case res.JSON201 == nil:
			l.refused[f.Key] = string(res.Body)
		case !sameVariations(res.JSON201.Variations, f.Variations):
			l.refused[f.Key] = "a boolean flag has exactly the variations true and false"
		}
	}
	for _, f := range rs.Flags {
		if l.refused[f.Key] != "" {
			continue
		}
		res := putConfiguration(t, c, project, f.Key, "production", 1, configurationOf(f))
		if res.JSON200 == nil {
			l.refused[f.Key] = string(res.Body)
			continue
		}
		for j, r := range res.JSON200.Rules {
			l.ruleIDs[f.Key] = append(l.ruleIDs[f.Key], r.Id.String())
			l.salts[f.Key] = append(l.salts[f.Key], valueOr(r.Serve.Salt))
			l.fixtureIDs[r.Id.String()] = f.Rules[j].ID
		}
	}
	return l
}

func sameVariations(got []management.Variation, want []eval.Variation) bool {
	return slices.EqualFunc(got, want, func(g management.Variation, w eval.Variation) bool {
		var a, b any
		return g.Key == w.Key && json.Unmarshal(g.Value, &a) == nil && json.Unmarshal(w.Value, &b) == nil && reflect.DeepEqual(a, b)
	})
}

func conditionsOf(in []eval.Condition) *[]management.Condition {
	out := make([]management.Condition, len(in))
	for i, c := range in {
		out[i] = management.Condition{Kind: c.Kind, Value: c.Value, Attribute: nonEmpty(c.Attribute), Op: nonEmpty(c.Op),
			Segment: nonEmpty(c.Segment), Flag: nonEmpty(c.Flag), Variation: nonEmpty(c.Variation)}
	}
	return &out
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// configurationOf returns f's configuration as a write sends it: rules
// without identifiers or salts, which the server gives.
func configurationOf(f eval.Flag) management.FlagConfigurationPut {
	out := management.FlagConfigurationPut{On: f.On, OffVariation: f.OffVariation, Fallthrough: f.Fallthrough,
		Prerequisites: &[]management.Prerequisite{}, Overrides: &[]management.OverrideInput{}, Rules: &[]management.RuleInput{}}
	for _, p := range f.Prerequisites {
		*out.Prerequisites = append(*out.Prerequisites, management.Prerequisite{Flag: p.Flag, Variation: p.Variation})
	}
	for _, o := range f.Overrides {
		*out.Overrides = append(*out.Overrides, management.OverrideInput{UnitType: o.UnitType, UnitId: o.UnitID, Variation: o.Variation})
	}
	for _, r := range f.Rules {
		serve := management.Serve{Kind: r.Serve.Kind, Variation: nonEmpty(r.Serve.Variation), UnitType: nonEmpty(r.Serve.UnitType)}
		if r.Serve.Kind == eval.ServeRollout {
			serve.Percentage = &r.Serve.Percentage
		}
		*out.Rules = append(*out.Rules, management.RuleInput{Conditions: conditionsOf(r.Conditions), Serve: serve})
	}
	return out
}

// withServerRules returns rs decoded with the rule identifiers and salts
// the server gave the flags it wrote, which is the ruleset the server
// evaluates for them.
func withServerRules(t *testing.T, rs eval.Ruleset, l loadedFixture) *eval.Ruleset {
	t.Helper()
	rs.Flags = slices.Clone(rs.Flags)
	for i, f := range rs.Flags {
		ids, ok := l.ruleIDs[f.Key]
		if !ok {
			continue
		}
		rules := slices.Clone(f.Rules)
		for j := range rules {
			rules[j].ID = ids[j]
			if rules[j].Serve.Kind == eval.ServeRollout {
				rules[j].Serve.Salt = l.salts[f.Key][j]
			}
		}
		rs.Flags[i].Rules = rules
	}
	doc, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := eval.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

// reach returns the keys of the flags and segments evaluating flag may
// consult in rs, itself included, and whether any of those flags serves a
// rollout, whose bucket depends on the salt the server gave it.
func reach(rs eval.Ruleset, flag string) ([]string, bool) {
	var (
		out      []string
		rollouts bool
	)
	var walk func(key string)
	walk = func(key string) {
		if slices.Contains(out, key) {
			return
		}
		out = append(out, key)
		i := slices.IndexFunc(rs.Flags, func(f eval.Flag) bool { return f.Key == key })
		if i < 0 {
			return
		}
		f := rs.Flags[i]
		for _, p := range f.Prerequisites {
			walk(p.Flag)
		}
		for _, r := range f.Rules {
			rollouts = rollouts || r.Serve.Kind == eval.ServeRollout
			for _, c := range r.Conditions {
				switch {
				case c.Flag != "":
					walk(c.Flag)
				case c.Segment != "":
					out = append(out, "segment "+c.Segment)
				}
			}
		}
	}
	walk(flag)
	return out, rollouts
}

// resultJSON returns r as JSON. With fixtureIDs, its rule identifier is the
// fixture's, absent when the fixture's is empty, and it carries no salt,
// which no fixture result has.
func resultJSON(t *testing.T, r management.EvaluationResult, fixtureIDs map[string]string) []byte {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if fixtureIDs == nil {
		return data
	}
	var m map[string]any
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "salt")
	if ruleID, ok := m["rule_id"].(string); ok {
		m["rule_id"] = fixtureIDs[ruleID]
		if fixtureIDs[ruleID] == "" {
			delete(m, "rule_id")
		}
	}
	data, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// referenceJSON returns what the reference evaluator gave, as the server
// answers it: with the rollout's salt.
func referenceJSON(t *testing.T, r delivery.Result) []byte {
	t.Helper()
	data, err := json.Marshal(r.Result)
	if err != nil {
		t.Fatal(err)
	}
	if r.Salt == "" {
		return data
	}
	var m map[string]any
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["salt"] = r.Salt
	data, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// sameJSON reports whether a and b are the same JSON value.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}
