package delivery_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var now = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

var (
	ids     = id.NewDeterministicSource(fixedClock{})
	project = id.New[workspace.Project](ids)
	env     = id.New[workspace.Environment](ids)
	author  = id.New[access.Member](ids)
)

func unitTypes(keys ...string) []workspace.UnitType {
	out := make([]workspace.UnitType, len(keys))
	for i, k := range keys {
		out[i] = workspace.UnitType{Key: k, Name: k}
	}
	return out
}

func attributes(kv ...string) []workspace.Attribute {
	var out []workspace.Attribute
	for i := 0; i < len(kv); i += 2 {
		out = append(out, workspace.Attribute{Key: kv[i], Name: kv[i], Type: workspace.AttributeType(kv[i+1])})
	}
	return out
}

func newFlag(key string, typ flags.Type, kv ...string) flags.Flag {
	f := flags.Flag{
		ID: id.New[flags.Flag](ids), Project: project, Key: key, Type: typ, Name: "Name of " + key,
		Description: "Described", Owner: author, Lifetime: flags.Permanent, CreatedAt: now, UpdatedAt: now, Version: 3,
	}
	for i := 0; i < len(kv); i += 2 {
		f.Variations = append(f.Variations, flags.Variation{Key: kv[i], Value: json.RawMessage(kv[i+1]), Name: "Named " + kv[i]})
	}
	return f
}

// TestMaterializeCarriesWhatSDKsEvaluateAndNothingElse pins the document:
// ordered by key, archived flags left out, rule descriptions, override notes
// and authors, names and versions never in it.
func TestMaterializeCarriesWhatSDKsEvaluateAndNothingElse(t *testing.T) {
	checkout := newFlag("checkout.newFlow", flags.TypeBoolean, "true", "true", "false", "false")
	banner := newFlag("banner", flags.TypeString, "a", `"A"`, "b", `"B"`)
	retired := newFlag("retired", flags.TypeBoolean, "true", "true", "false", "false")
	archivedAt := now
	retired.ArchivedAt = &archivedAt
	rule := id.New[flags.Rule](ids)
	rollout := id.New[flags.Rule](ids)
	configured := flags.InitialConfiguration(checkout, env, &author, now)
	configured.On = true
	configured.Prerequisites = []flags.Prerequisite{{Flag: "banner", Variation: "b"}}
	configured.Overrides = []flags.Override{{UnitType: "user", UnitID: "u-7", Variation: "true", Note: "Demo account", AddedBy: &author}}
	configured.Rules = []flags.Rule{
		{ID: rule, Description: "Internal staff first", Conditions: []flags.Condition{{Kind: flags.ConditionInSegment, Segment: "staff"}},
			Serve: flags.Serve{Kind: flags.ServeVariation, Variation: "true"}},
		{ID: rollout, Description: "Ten percent", Conditions: []flags.Condition{
			{Kind: flags.ConditionAttribute, Attribute: "country", Op: "in", Value: []any{"NL", "SE"}},
		}, Serve: flags.Serve{Kind: flags.ServeRollout, Variation: "true", UnitType: "user", Percentage: 1000, Salt: "k3f9s0d2m1x8q7wz"}},
	}
	m := delivery.Materials{
		Environment: "production", Version: 12,
		UnitTypes:  unitTypes("user", "anonymous"),
		Attributes: attributes("os", "string", "country", "string"),
		Segments: []flags.Segment{
			{Key: "staff", Name: "Staff", Description: "Employees", Private: true, Version: 2,
				Conditions: []flags.Condition{{Kind: flags.ConditionAttribute, Attribute: "os", Op: "eq", Value: "linux"}}},
			{Key: "beta", Name: "Beta", Conditions: []flags.Condition{}},
		},
		Flags: []flags.Flag{checkout, banner, retired},
		Configurations: []flags.Configuration{
			configured, flags.InitialConfiguration(banner, env, nil, now), flags.InitialConfiguration(retired, env, nil, now),
		},
	}
	doc, err := delivery.Materialize(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":1,"version":12,"scope":"server","environment":{"key":"production","protected":false},
		"unit_types":["anonymous","user"],
		"attributes":[{"key":"country","type":"string"},{"key":"os","type":"string"}],
		"segments":[{"key":"beta","private":false,"conditions":[]},
			{"key":"staff","private":true,"conditions":[{"kind":"attribute","attribute":"os","op":"eq","value":"linux"}]}],
		"flags":[
			{"key":"banner","type":"string","automatic_exposure":false,"variations":[{"key":"a","value":"A"},{"key":"b","value":"B"}],
				"on":false,"off_variation":"a","fallthrough":"a"},
			{"key":"checkout.newFlow","type":"boolean","automatic_exposure":false,
				"variations":[{"key":"true","value":true},{"key":"false","value":false}],
				"on":true,"off_variation":"false","fallthrough":"true",
				"prerequisites":[{"flag":"banner","variation":"b"}],
				"overrides":[{"unit_type":"user","unit_id":"u-7","variation":"true"}],
				"rules":[
					{"id":"` + rule.String() + `","conditions":[{"kind":"in_segment","segment":"staff"}],"serve":{"kind":"variation","variation":"true"}},
					{"id":"` + rollout.String() + `","conditions":[{"kind":"attribute","attribute":"country","op":"in","value":["NL","SE"]}],
						"serve":{"kind":"rollout","variation":"true","unit_type":"user","percentage":1000,"salt":"k3f9s0d2m1x8q7wz"}}]}]}`
	var compact bytes.Buffer
	if err = json.Compact(&compact, []byte(want)); err != nil {
		t.Fatal(err)
	}
	if string(doc) != compact.String() {
		t.Errorf("document\n%s\nwant\n%s", doc, compact.String())
	}

	rs, err := eval.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Evaluate(eval.Context{Units: map[string]string{"user": "u-7"}}, "checkout.newFlow"); got.Reason != eval.ReasonOverride {
		t.Errorf("u-7 = %+v, want its override", got)
	}
	if got := rs.Evaluate(eval.Context{}, "retired"); got.ErrorCode != eval.ErrorFlagNotFound {
		t.Errorf("archived flag = %+v, want flag_not_found", got)
	}
}

func TestMaterializeMarksAProtectedEnvironment(t *testing.T) {
	doc, err := delivery.Materialize(delivery.Materials{Environment: "production", Protected: true, Version: 3})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := eval.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !rs.Protected() || !bytes.Contains(doc, []byte(`"environment":{"key":"production","protected":true}`)) {
		t.Errorf("document = %s, want production marked protected", doc)
	}
}

func TestADraftTakesThePlaceOfItsFlagsConfiguration(t *testing.T) {
	f := newFlag("banner", flags.TypeString, "a", `"A"`, "b", `"B"`)
	other := newFlag("other", flags.TypeString, "a", `"A"`, "b", `"B"`)
	m := delivery.Materials{
		Environment: "staging", Version: 1, Flags: []flags.Flag{f, other},
		Configurations: []flags.Configuration{flags.InitialConfiguration(f, env, nil, now), flags.InitialConfiguration(other, env, nil, now)},
	}
	draft := flags.InitialConfiguration(f, env, nil, now)
	draft.On, draft.Fallthrough = true, "b"
	doc, err := delivery.Materialize(m.With(draft))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := eval.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Evaluate(eval.Context{}, "banner"); got.Variation != "b" || got.Reason != eval.ReasonFallthrough {
		t.Errorf("banner = %+v, want the draft's fallthrough", got)
	}
	if got := rs.Evaluate(eval.Context{}, "other"); got.Reason != eval.ReasonOff {
		t.Errorf("other = %+v, want it as saved", got)
	}
	if m.Configurations[0].On {
		t.Error("With changed the materials it was called on")
	}
}

func TestEvaluateExplainsARolloutWithItsSalt(t *testing.T) {
	f := newFlag("banner", flags.TypeString, "a", `"A"`, "b", `"B"`)
	c := flags.InitialConfiguration(f, env, nil, now)
	c.On = true
	rule := id.New[flags.Rule](ids)
	c.Rules = []flags.Rule{{ID: rule, Conditions: []flags.Condition{},
		Serve: flags.Serve{Kind: flags.ServeRollout, Variation: "b", UnitType: "user", Percentage: 10_000, Salt: "salt0123456789ab"}}}
	doc, err := delivery.Materialize(delivery.Materials{
		Environment: "staging", Version: 4, UnitTypes: unitTypes("user"), Flags: []flags.Flag{f}, Configurations: []flags.Configuration{c},
	})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := eval.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	all := delivery.Evaluate(rs, eval.Context{Units: map[string]string{"user": "u-1"}}, nil)
	got := all["banner"]
	if len(all) != 1 || got.Reason != eval.ReasonRollout || got.RuleID != rule.String() || got.Salt != "salt0123456789ab" ||
		got.Bucket == nil || *got.Bucket != eval.Bucket("salt0123456789ab", "u-1") {
		t.Errorf("results = %+v", all)
	}
	named := delivery.Evaluate(rs, eval.Context{}, []string{"banner", "missing"})
	if named["banner"].Reason != eval.ReasonFallthrough || named["banner"].Salt != "" ||
		named["missing"].ErrorCode != eval.ErrorFlagNotFound || len(named) != 2 {
		t.Errorf("named results = %+v", named)
	}
}

func TestAContextWithAnOverlongUnitIDIsInvalid(t *testing.T) {
	long := string(bytes.Repeat([]byte("x"), flags.MaxUnitIDBytes+1))
	multibyte := string(bytes.Repeat([]byte("я"), flags.MaxUnitIDBytes/2))
	if err := delivery.CheckContext(eval.Context{Units: map[string]string{"user": multibyte, "anonymous": ""}}); err != nil {
		t.Errorf("256 bytes and an empty unit ID: %v", err)
	}
	err := delivery.CheckContext(eval.Context{Units: map[string]string{"user": long, "a/b": long, "ok": "u-1"}})
	if got := violationsOf(t, err, "invalid_context"); len(got) != 2 || got[0] != "/units/a~1b too_long" || got[1] != "/units/user too_long" {
		t.Errorf("violations = %v", got)
	}
}

// violationsOf returns "pointer code" for each violation of err, failing
// unless it is invalid with code.
func violationsOf(t *testing.T, err error, code string) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return out
}

// TestMaterializeLeavesOutRulesServingAnExperimentThatIsNotRunning pins that
// preparing an experiment changes nothing any application is served, and
// that a running one is embedded with the fields SDKs assign units by.
func TestMaterializeLeavesOutRulesServingAnExperimentThatIsNotRunning(t *testing.T) {
	title := newFlag("title", flags.TypeString, "a", `"A"`, "b", `"B"`)
	configured := flags.InitialConfiguration(title, env, &author, now)
	configured.On = true
	rule := func(experiment string) flags.Rule {
		return flags.Rule{
			ID: id.New[flags.Rule](ids), Conditions: []flags.Condition{},
			Serve: flags.Serve{Kind: flags.ServeExperiment, Experiment: experiment},
		}
	}
	configured.Rules = []flags.Rule{rule("preparing"), rule("live"), rule("finished")}
	weights := []flags.ExperimentWeight{{Variation: "b", Weight: 3000}, {Variation: "a", Weight: 7000}}
	experiments := []flags.ExperimentRef{
		{Key: "preparing", Flag: title.ID, Status: "draft", UnitType: "user", Weights: weights},
		{Key: "live", Flag: title.ID, Status: "running", UnitType: "user", InclusionSalt: "0123456789abcdef",
			VariationSalt: "fedcba9876543210", TrafficAllocation: 2500, Weights: weights},
		{Key: "finished", Flag: title.ID, Status: "stopped", UnitType: "user", Weights: weights},
	}
	doc, err := delivery.Materialize(delivery.Materials{
		Environment: "staging", Version: 1, UnitTypes: unitTypes("user"), Flags: []flags.Flag{title},
		Configurations: []flags.Configuration{configured}, Experiments: experiments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eval.Decode(doc); err != nil {
		t.Fatal(err)
	}
	var got eval.Ruleset
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatal(err)
	}
	rules := got.Flags[0].Rules
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want only the running experiment's", rules)
	}
	want := eval.Serve{
		Kind: "experiment", Experiment: "live", UnitType: "user", InclusionSalt: "0123456789abcdef",
		VariationSalt: "fedcba9876543210", TrafficAllocation: 2500,
		Weights: []eval.Weight{{Variation: "b", Weight: 3000}, {Variation: "a", Weight: 7000}},
	}
	if s := rules[0].Serve; s.Kind != want.Kind || s.Experiment != want.Experiment || s.UnitType != want.UnitType ||
		s.InclusionSalt != want.InclusionSalt || s.VariationSalt != want.VariationSalt ||
		s.TrafficAllocation != want.TrafficAllocation || len(s.Weights) != 2 || s.Weights[0] != want.Weights[0] || s.Weights[1] != want.Weights[1] {
		t.Errorf("serve = %+v, want %+v", s, want)
	}
}
