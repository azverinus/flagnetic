package firebase_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/transfer/firebase"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// standard are the attributes every project has, and units its unit types.
func project() firebase.Current {
	std := func(key string, typ workspace.AttributeType) workspace.Attribute {
		return workspace.Attribute{Key: key, Type: typ, Standard: true}
	}
	return firebase.Current{
		UnitTypes: []workspace.UnitType{{Key: "user"}, {Key: "anonymous"}},
		Attributes: []workspace.Attribute{
			std("platform", workspace.TypeString), std("os", workspace.TypeString), std("app_id", workspace.TypeString),
			std("locale", workspace.TypeString), std("country", workspace.TypeString), std("os_version", workspace.TypeSemver),
			std("app_version", workspace.TypeSemver), std("build_number", workspace.TypeNumber),
		},
	}
}

var anonymous = firebase.Target{UnitType: "anonymous"}

func decode(t *testing.T, s string) firebase.Template {
	t.Helper()
	tpl, err := firebase.Decode([]byte(s))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return tpl
}

func translate(t *testing.T, tpl firebase.Template, current firebase.Current) firebase.Result {
	t.Helper()
	r, err := firebase.Translate(tpl, current, anonymous)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	return r
}

func fixture(t *testing.T) firebase.Template {
	t.Helper()
	b, err := os.ReadFile("testdata/firebase-template.json")
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, string(b))
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func attr(name, op string, value any) flags.Condition {
	return flags.Condition{Kind: flags.ConditionAttribute, Attribute: name, Op: op, Value: value}
}

type warning struct{ code, pointer string }

func warnings(r firebase.Result) []warning {
	out := []warning{}
	for _, w := range r.Warnings {
		out = append(out, warning{w.Code, w.Pointer})
	}
	return out
}

func flagOf(t *testing.T, r firebase.Result, key string) firebase.Flag {
	t.Helper()
	for _, f := range r.Flags {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("no flag %q in %v", key, r.Flags)
	return firebase.Flag{}
}

func TestTheFixtureBecomesFlagsRulesAttributesAndWarnings(t *testing.T) {
	r := translate(t, fixture(t), project())

	keys := []string{}
	for _, f := range r.Flags {
		keys = append(keys, f.Key)
	}
	if got := strings.Join(keys, ","); got != "banner_text,legacy_limits,new_matching,onboarding_steps" {
		t.Errorf("flags = %s, want them in key order", got)
	}
	if got := asJSON(t, r.Attributes); got !=
		`[{"Key":"plan_type","Type":"string","Name":"Plan Type","Description":"The Firebase user property \"Plan Type\"."}]` {
		t.Errorf("attributes = %s", got)
	}

	want := []warning{
		{firebase.WarnAttributeDeclared, "/conditions/4/expression"},
		{firebase.WarnConditionSkipped, "/parameters/banner_text/conditionalValues/pro_or_early"},
		{firebase.WarnInAppDefault, "/parameterGroups/Onboarding/parameters/legacy_limits/defaultValue"},
		{firebase.WarnRolloutReshuffled, "/parameters/new_matching/conditionalValues/ten_percent"},
		{firebase.WarnConditionSkipped, "/parameters/new_matching/conditionalValues/launch_day"},
	}
	if got := warnings(r); !reflect.DeepEqual(got, want) {
		t.Errorf("warnings = %v\nwant %v", got, want)
	}

	android := []flags.Condition{attr("platform", "eq", "android"), attr("app_version", "lt", "1.41.0")}

	matching := flagOf(t, r, "new_matching")
	if matching.Type != flags.TypeBoolean || matching.Name != "new_matching" || matching.Description != "The new matching screen." ||
		len(matching.Tags) != 0 || matching.Existing || !matching.On || matching.OffVariation != "false" || matching.Fallthrough != "false" {
		t.Errorf("new_matching = %+v", matching)
	}
	if got := asJSON(t, matching.Variations); got !=
		`[{"Key":"true","Value":true,"Name":"true","Description":""},{"Key":"false","Value":false,"Name":"false","Description":""}]` {
		t.Errorf("new_matching variations = %s", got)
	}
	ten := 1000
	wantRules := []flags.Rule{
		{Description: `Firebase condition "android_old"`, Conditions: android, Serve: flags.Serve{Kind: flags.ServeVariation, Variation: "true"}},
		{Description: `Firebase condition "ten_percent"`, Conditions: []flags.Condition{},
			Serve: flags.Serve{Kind: flags.ServeRollout, Variation: "true", UnitType: "anonymous", Percentage: ten}},
	}
	if !reflect.DeepEqual(matching.Rules, wantRules) {
		t.Errorf("new_matching rules = %+v\nwant %+v", matching.Rules, wantRules)
	}

	banner := flagOf(t, r, "banner_text")
	if got := asJSON(t, banner.Variations); got !=
		`[{"Key":"v1","Value":"Hello","Name":"defaultValue","Description":""},{"Key":"v2","Value":"Hi","Name":"ios_or_beta_app","Description":""},`+
			`{"Key":"v3","Value":"Welcome, pro","Name":"pro_or_early","Description":""}]` {
		t.Errorf("banner_text variations = %s", got)
	}
	if banner.OffVariation != "v1" || banner.Fallthrough != "v1" || len(banner.Rules) != 4 {
		t.Fatalf("banner_text = %+v", banner)
	}
	wantBanner := []struct {
		description, variation string
		conditions             []flags.Condition
	}{
		{`Firebase condition "android_old"`, "v1", android},
		{`Firebase condition "ios_or_beta_app"`, "v2", []flags.Condition{attr("platform", "eq", "ios")}},
		{`Firebase condition "ios_or_beta_app"`, "v2", []flags.Condition{attr("app_id", "eq", "1:1234:android:beta")}},
		{`Firebase condition "pro_or_early"`, "v3", []flags.Condition{attr("plan_type", "in", []any{"pro"})}},
	}
	for i, w := range wantBanner {
		got := banner.Rules[i]
		if got.Description != w.description || got.Serve.Variation != w.variation || !reflect.DeepEqual(got.Conditions, w.conditions) {
			t.Errorf("banner_text rule %d = %+v, want %+v", i, got, w)
		}
	}

	steps := flagOf(t, r, "onboarding_steps")
	if !reflect.DeepEqual(steps.Tags, []string{"Onboarding"}) || steps.Type != flags.TypeNumber || len(steps.Rules) != 1 ||
		steps.Rules[0].Serve.Variation != "v2" || steps.Fallthrough != "v1" {
		t.Errorf("onboarding_steps = %+v", steps)
	}
	if got := asJSON(t, steps.Variations); !strings.Contains(got, `"Value":3`) || !strings.Contains(got, `"Value":5`) {
		t.Errorf("onboarding_steps variations = %s", got)
	}
	limits := flagOf(t, r, "legacy_limits")
	if limits.Type != flags.TypeJSON || string(limits.Variations[0].Value) != `{}` || len(limits.Rules) != 0 {
		t.Errorf("legacy_limits = %+v", limits)
	}
}

func TestTheTermTable(t *testing.T) {
	type rule struct {
		conditions []flags.Condition
		serve      flags.Serve
	}
	v2 := flags.Serve{Kind: flags.ServeVariation, Variation: "v2"}
	basis := func(n int) flags.Serve {
		return flags.Serve{Kind: flags.ServeRollout, Variation: "v2", UnitType: "anonymous", Percentage: n}
	}
	rollout := func(share string) firebase.Warning {
		return firebase.Warning{Code: firebase.WarnRolloutReshuffled, Detail: "p, condition c: percent becomes a rollout of " + share +
			" on unit type anonymous with a fresh salt; Firebase's hashing is unpublished, so which units are inside changes"}
	}
	rangeStart := func(from string) firebase.Warning {
		return firebase.Warning{Code: firebase.WarnRolloutReshuffled, Detail: "p, condition c: Firebase's percent range from " + from +
			" keeps only its width: a rollout starts at bucket 0 of its own salt, so ranges Firebase kept apart on one seed now overlap and an earlier rule takes part of this one's share"}
	}
	skipped := func(term, reason string) firebase.Warning {
		return firebase.Warning{Code: firebase.WarnConditionSkipped,
			Detail: fmt.Sprintf(`p, condition c: the rule for "%s" is skipped: %s`, term, reason)}
	}
	for name, tc := range map[string]struct {
		expression string
		rules      []rule
		warnings   []firebase.Warning
	}{
		"build, language and country": {`app.build.>=(['100']) && device.language in ['en-US', 'ru-RU'] && device.country in ['us', 'Gb']`,
			[]rule{{[]flags.Condition{attr("build_number", "gte", 100.0), attr("locale", "in", []any{"en-US", "ru-RU"}), attr("country", "in", []any{"US", "GB"})}, v2}}, nil},
		"build exactly matches": {`app.build.exactlyMatches(['7', '9'])`,
			[]rule{{[]flags.Condition{attr("build_number", "in", []any{7.0, 9.0})}, v2}}, nil},
		"numeric custom signal": {`app.customSignal['tier'].>([2])`,
			[]rule{{[]flags.Condition{attr("tier", "gt", 2.0)}, v2}}, nil},
		"version user property": {`version(app.userProperty['build_tag']).<(['3.1'])`,
			[]rule{{[]flags.Condition{attr("build_tag", "lt", "3.1")}, v2}}, nil},
		"version custom signal": {`version(app.customSignal['sdk_level']).>=(['2.0'])`,
			[]rule{{[]flags.Condition{attr("sdk_level", "gte", "2.0")}, v2}}, nil},
		"string equality on a user property": {`app.userProperty['tier'] == 'gold'`,
			[]rule{{[]flags.Condition{attr("tier", "eq", "gold")}, v2}}, nil},
		"contains any: a rule each": {`app.userProperty['plan'].contains(['pro', 'max'])`,
			[]rule{{[]flags.Condition{attr("plan", "contains", "pro")}, v2}, {[]flags.Condition{attr("plan", "contains", "max")}, v2}}, nil},
		"matches any: a rule each": {`app.userProperty['plan'].matches(['^pro', 'x$'])`,
			[]rule{{[]flags.Condition{attr("plan", "matches", "^pro")}, v2}, {[]flags.Condition{attr("plan", "matches", "x$")}, v2}}, nil},
		"contains none: one rule": {`app.userProperty['plan'].notContains(['free', 'trial'])`,
			[]rule{{[]flags.Condition{attr("plan", "not_contains", "free"), attr("plan", "not_contains", "trial")}, v2}}, nil},
		"app id exactly matches": {`app.id.exactlyMatches(['a', 'b'])`,
			[]rule{{[]flags.Condition{attr("app_id", "in", []any{"a", "b"})}, v2}}, nil},
		"version exactly matches": {`app.version.exactlyMatches(['1.2.3', '1.3'])`,
			[]rule{{[]flags.Condition{attr("app_version", "in", []any{"1.2.3", "1.3"})}, v2}}, nil},
		"version infix": {`app.version >= '2.0.0'`, []rule{{[]flags.Condition{attr("app_version", "gte", "2.0.0")}, v2}}, nil},
		"parentheses and ||": {`(device.os == 'ios' || device.os != "android") && app.id == 'x'`,
			[]rule{{[]flags.Condition{attr("platform", "eq", "ios"), attr("app_id", "eq", "x")}, v2},
				{[]flags.Condition{attr("platform", "neq", "android"), attr("app_id", "eq", "x")}, v2}}, nil},
		"escaped quote": {`app.id == 'it\'s'`, []rule{{[]flags.Condition{attr("app_id", "eq", "it's")}, v2}}, nil},
		"true":          {`true`, []rule{{[]flags.Condition{}, v2}}, nil},
		"false":         {`false`, nil, nil},
		"percent between": {`percent between 20 and 60`, []rule{{[]flags.Condition{}, basis(4000)}},
			[]firebase.Warning{rollout("40%"), rangeStart("20")}},
		"percent above, seeded": {`percent('seed') > 87.5 && device.os == 'ios'`,
			[]rule{{[]flags.Condition{attr("platform", "eq", "ios")}, basis(1250)}}, []firebase.Warning{rollout("12.5%"), rangeStart("87.5")}},
		"percent below": {`percent < 5`, []rule{{[]flags.Condition{}, basis(500)}}, []firebase.Warning{rollout("5%")}},
		"percent rounded": {`percent <= 0.001`, []rule{{[]flags.Condition{}, basis(0)}},
			[]firebase.Warning{rollout("0% (0.001% in Firebase, rounded to basis points)")}},
		"percent over 100": {`percent <= 250`, []rule{{[]flags.Condition{}, basis(10000)}},
			[]firebase.Warning{rollout("100%")}},
		"two percents": {`percent <= 10 && percent('x') > 50`, nil,
			[]firebase.Warning{skipped("percent <= 10 && percent('x') > 50", "a rule holds one percent term at most")}},
		"percent equals":       {`percent == 5`, nil, []firebase.Warning{skipped("percent == 5", "percent == is not supported")}},
		"percent not a number": {`percent <= 'a'`, nil, []firebase.Warning{skipped("percent <= 'a'", `percent: "a" is not a number`)}},
		"version contains": {`app.version.contains(['1.2'])`, nil,
			[]firebase.Warning{skipped("app.version.contains(['1.2'])", "app.version.contains is not supported")}},
		"build matches": {`app.build.matches(['^1'])`, nil,
			[]firebase.Warning{skipped("app.build.matches(['^1'])", `app.build compares as build_number, a number: "^1" is not a number`)}},
		"os contains":     {`device.os.contains(['a'])`, nil, []firebase.Warning{skipped("device.os.contains(['a'])", "device.os.contains is not supported")}},
		"language equals": {`device.language == 'en'`, nil, []firebase.Warning{skipped("device.language == 'en'", "device.language.== is not supported")}},
		"version of a standard element": {`version(app.id) == '1.0'`, nil,
			[]firebase.Warning{skipped("version(app.id) == '1.0'", "version(app.id) is not supported")}},
		"audiences": {`app.audiences.inAtLeastOne(['A']) || device.os == 'ios'`,
			[]rule{{[]flags.Condition{attr("platform", "eq", "ios")}, v2}},
			[]firebase.Warning{skipped("app.audiences.inAtLeastOne(['A'])", "app.audiences is not supported")}},
		"date time": {`dateTime >= dateTime('2026-10-01T00:00:00')`, nil, []firebase.Warning{skipped("dateTime >= dateTime('2026-10-01T00:00:00')", "dateTime is not supported")}},
		"unparseable version": {`app.version.<(['garbage'])`, nil,
			[]firebase.Warning{skipped("app.version.<(['garbage'])", `"garbage" is not a semantic version`)}},
		"unparseable version in a list": {`app.version.exactlyMatches(['1.0', 'x'])`, nil,
			[]firebase.Warning{skipped("app.version.exactlyMatches(['1.0', 'x'])", `"x" is not a semantic version`)}},
		"retyped attribute": {`app.userProperty['app_version'] > 3`, nil,
			[]firebase.Warning{skipped("app.userProperty['app_version'] > 3", "the attribute app_version is a semver, and the comparison needs a number")}},
		"unit type key": {`app.userProperty['user'].exactlyMatches(['u1'])`, nil,
			[]firebase.Warning{skipped("app.userProperty['user'].exactlyMatches(['u1'])", `user property "user" maps to user, which is a unit type`)}},
		"reserved key": {`app.userProperty['Discovered'] == 'x'`, nil,
			[]firebase.Warning{skipped("app.userProperty['Discovered'] == 'x'", `user property "Discovered" maps to discovered, which no attribute can be named`)}},
		"empty key": {`app.userProperty['***'] == 'x'`, nil,
			[]firebase.Warning{skipped("app.userProperty['***'] == 'x'", `user property "***" has no usable key`)}},
		"not a number": {`app.customSignal['tier'] > 'NaN'`, nil,
			[]firebase.Warning{skipped("app.customSignal['tier'] > 'NaN'", `app.customSignal > compares numbers, but "NaN" is not a number`)}},
		"custom signal operator": {`version(app.customSignal['v']).contains(['1'])`, nil,
			[]firebase.Warning{skipped("version(app.customSignal['v']).contains(['1'])", "version(app.customSignal).contains is not supported")}},
		"custom comparison with two values": {`app.customSignal['v'].>([1, 2])`, nil,
			[]firebase.Warning{skipped("app.customSignal['v'].>([1, 2])", "app.customSignal.> takes one value")}},
		"custom unknown operator": {`app.customSignal['v'].startsWith(['1'])`, nil,
			[]firebase.Warning{skipped("app.customSignal['v'].startsWith(['1'])", "app.customSignal.startsWith is not supported")}},
		"malformed term":             {`device.os ==`, nil, []firebase.Warning{skipped("device.os ==", "cannot read it: expected a value")}},
		"unknown operator character": {`device.os ! 'a'`, nil, []firebase.Warning{skipped("device.os ! 'a'", `cannot read it: unknown operator "!"`)}},
		"stray character":            {`device.os == 'a' @`, nil, []firebase.Warning{skipped("device.os == 'a' @", "cannot read it: unexpected '@'")}},
	} {
		t.Run(name, func(t *testing.T) {
			tpl := decode(t, `{"conditions": [{"name": "c", "expression": `+asJSON(t, tc.expression)+`}],
				"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"c": {"value": "b"}}}}}`)
			r := translate(t, tpl, project())
			var got []rule
			for _, x := range r.Flags[0].Rules {
				x.Serve.Salt = ""
				got = append(got, rule{x.Conditions, x.Serve})
			}
			if !reflect.DeepEqual(got, tc.rules) {
				t.Errorf("rules = %+v\nwant %+v", got, tc.rules)
			}
			var gotW []firebase.Warning
			for _, w := range r.Warnings {
				gotW = append(gotW, firebase.Warning{Code: w.Code, Detail: w.Detail})
			}
			if !reflect.DeepEqual(gotW, tc.warnings) {
				t.Errorf("warnings = %+v\nwant %+v", gotW, tc.warnings)
			}
		})
	}
}

func TestAnExpressionThatCannotBeReadSkipsItsRules(t *testing.T) {
	for name, expression := range map[string]string{
		"term missing":          `device.os == 'ios' &&`,
		"unclosed parenthesis":  `(device.os == 'ios'`,
		"unclosed bracket":      `app.version.<(['1.0']`,
		"unclosed string":       `app.id == 'x`,
		"trailing text":         `device.os == 'ios') x`,
		"empty":                 ``,
		"too many rules":        strings.Repeat("(device.os == 'a' || device.os == 'b') && ", 6) + "true",
		"too many alternatives": strings.Repeat("device.os == 'a' || ", 50) + "device.os == 'b'",
	} {
		t.Run(name, func(t *testing.T) {
			tpl := decode(t, `{"conditions": [{"name": "c", "expression": `+asJSON(t, expression)+`}],
				"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"c": {"value": "b"}}}}}`)
			r := translate(t, tpl, project())
			if len(r.Flags[0].Rules) != 0 || len(r.Warnings) != 1 || r.Warnings[0].Code != firebase.WarnConditionSkipped ||
				!strings.Contains(r.Warnings[0].Detail, "the rules are skipped: the expression cannot be read") {
				t.Errorf("rules = %v, warnings = %+v; want the rules skipped with one warning", r.Flags[0].Rules, r.Warnings)
			}
		})
	}
}

func TestFiftyAlternativesAreExpanded(t *testing.T) {
	// 50 alternatives is the most a configuration holds, and the most an
	// expression may expand to.
	tpl := decode(t, `{"conditions": [{"name": "c", "expression": `+asJSON(t, strings.Repeat("device.os == 'a' || ", 49)+"device.os == 'b'")+`}],
		"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"c": {"value": "b"}}}}}`)
	if r := translate(t, tpl, project()); len(r.Flags[0].Rules) != 50 || len(r.Warnings) != 0 {
		t.Errorf("rules = %d, warnings = %+v; want 50 rules", len(r.Flags[0].Rules), r.Warnings)
	}
}

func TestRulesPastTheLimitAreSkippedAndTheOthersStay(t *testing.T) {
	values := make([]string, flags.MaxRules)
	for i := range values {
		values[i] = fmt.Sprintf("'v%d'", i)
	}
	// A null personalization value is no personalization value.
	tpl := decode(t, `{"conditions": [
			{"name": "many", "expression": `+asJSON(t, "app.userProperty['plan'].contains(["+strings.Join(values, ", ")+"])")+`},
			{"name": "ios", "expression": "device.os == 'ios'"}],
		"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {
			"many": {"value": "b", "personalizationValue": null}, "ios": {"value": "c"}}}}}`)
	r := translate(t, tpl, project())
	if n := len(r.Flags[0].Rules); n != flags.MaxRules {
		t.Errorf("rules = %d, want %d", n, flags.MaxRules)
	}
	if len(r.Warnings) != 1 || r.Warnings[0].Code != firebase.WarnRuleLimit ||
		r.Warnings[0].Pointer != "/parameters/p/conditionalValues/ios" || r.Warnings[0].Params["max"] != flags.MaxRules ||
		r.Warnings[0].Detail != `p, condition ios: the rule for "device.os == 'ios'" is skipped: a configuration holds at most 50 rules` {
		t.Errorf("warnings = %+v, want rule_limit", r.Warnings)
	}
}

func TestARulePastTheLimitDeclaresNothingAndWarnsOfNoRollout(t *testing.T) {
	tpl := decode(t, `{"conditions": [
			{"name": "many", "expression": `+asJSON(t, strings.Repeat("device.os == 'a' || ", flags.MaxRules-1)+"device.os == 'b'")+`},
			{"name": "late", "expression": "app.userProperty['Plan'] == 'x' && percent <= 10"}],
		"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"many": {"value": "b"}, "late": {"value": "c"}}}}}`)
	r := translate(t, tpl, project())
	if len(r.Attributes) != 0 {
		t.Errorf("attributes = %+v, want none for a rule left out", r.Attributes)
	}
	if got := warnings(r); !reflect.DeepEqual(got, []warning{{firebase.WarnRuleLimit, "/parameters/p/conditionalValues/late"}}) {
		t.Errorf("warnings = %+v, want rule_limit only", got)
	}
}

func TestOneTermExpandingPastTheLimitCannotBeRead(t *testing.T) {
	values := make([]string, flags.MaxRules+1)
	for i := range values {
		values[i] = fmt.Sprintf("'v%d'", i)
	}
	tpl := decode(t, `{"conditions": [{"name": "c", "expression": `+asJSON(t, "app.userProperty['plan'].contains(["+strings.Join(values, ", ")+"])")+`}],
		"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"c": {"value": "b"}}}}}`)
	r := translate(t, tpl, project())
	if len(r.Flags[0].Rules) != 0 || len(r.Attributes) != 0 || len(r.Warnings) != 1 ||
		!strings.Contains(r.Warnings[0].Detail, "the expression cannot be read: it makes more than 50 rules") {
		t.Errorf("rules = %d, attributes = %+v, warnings = %+v; want the rules skipped", len(r.Flags[0].Rules), r.Attributes, r.Warnings)
	}
}

func TestPersonalizationAndRolloutValuesSkipTheirRules(t *testing.T) {
	tpl := decode(t, `{"conditions": [{"name": "a", "expression": "true"}, {"name": "b", "expression": "true"}],
		"parameters": {"p": {"defaultValue": {"value": "x"}, "conditionalValues": {
			"a": {"personalizationValue": {"personalizationId": "1"}}, "b": {"rolloutValue": {"rolloutId": "2", "percent": 5}}}}}}`)
	r := translate(t, tpl, project())
	if len(r.Flags[0].Rules) != 0 || len(r.Warnings) != 2 {
		t.Fatalf("rules = %v, warnings = %+v", r.Flags[0].Rules, r.Warnings)
	}
	w := r.Warnings[0]
	if w.Code != firebase.WarnConditionSkipped || w.Pointer != "/parameters/p/conditionalValues/a" ||
		!reflect.DeepEqual(w.Params, map[string]any{"parameter": "p", "condition": "a", "reason": "personalization and rollout values are not supported"}) {
		t.Errorf("warning = %+v", w)
	}
}

func TestAMissingValueServesTheEmptyValueOfItsType(t *testing.T) {
	for typ, want := range map[string]string{"BOOLEAN": "false", "STRING": `""`, "": `""`, "NUMBER": "0", "JSON": "{}"} {
		tpl := decode(t, `{"conditions": [{"name": "c", "expression": "true"}],
			"parameters": {"p": {"valueType": `+asJSON(t, typ)+`, "conditionalValues": {"c": {"useInAppDefault": true}}}}}`)
		r := translate(t, tpl, project())
		f := r.Flags[0]
		if f.Rules[0].Serve.Variation != f.Fallthrough || !strings.Contains(asJSON(t, f.Variations), `"Value":`+want) || len(f.Variations) > 2 && typ != "BOOLEAN" {
			t.Errorf("%q: flag = %+v, want one variation serving %s", typ, f, want)
		}
		if len(r.Warnings) != 2 || r.Warnings[0].Pointer != "/parameters/p/defaultValue" || r.Warnings[1].Pointer != "/parameters/p/conditionalValues/c" ||
			r.Warnings[1].Params["condition"] != "c" {
			t.Errorf("%q: warnings = %+v, want in_app_default for the default and the conditional value", typ, r.Warnings)
		}
	}
}

func TestValuesAreComparedByJSONMeaning(t *testing.T) {
	tpl := decode(t, `{"conditions": [{"name": "c", "expression": "true"}, {"name": "d", "expression": "true"}],
		"parameters": {
			"n": {"valueType": "NUMBER", "defaultValue": {"value": "3"}, "conditionalValues": {"c": {"value": "3.0"}, "d": {"value": " 4 "}}},
			"j": {"valueType": "JSON", "defaultValue": {"value": "{\"a\": 1, \"b\": 2}"}, "conditionalValues": {"c": {"value": "{\"b\":2,\"a\":1.0}"}, "d": {"value": "[1]"}}},
			"b": {"valueType": "BOOLEAN", "defaultValue": {"value": "TRUE"}, "conditionalValues": {"c": {"value": " false "}}}}}`)
	r := translate(t, tpl, project())
	if f := flagOf(t, r, "n"); len(f.Variations) != 2 || f.Rules[0].Serve.Variation != "v1" || f.Rules[1].Serve.Variation != "v2" {
		t.Errorf("n = %+v", f)
	}
	if f := flagOf(t, r, "j"); len(f.Variations) != 2 || f.Rules[0].Serve.Variation != "v1" || f.Rules[1].Serve.Variation != "v2" {
		t.Errorf("j = %+v", f)
	}
	if f := flagOf(t, r, "b"); f.Fallthrough != "true" || f.Rules[0].Serve.Variation != "false" || len(f.Variations) != 2 {
		t.Errorf("b = %+v", f)
	}
}

func TestSlugging(t *testing.T) {
	for name, tc := range map[string]struct{ key, attribute string }{
		"spaces and capitals": {"Plan Type", "plan_type"},
		"already a key":       {"plan-type_2", "plan-type_2"},
		"leading digit":       {"2fa enabled", "a_2fa_enabled"},
		"edge punctuation":    {"..tier..", "tier"},
		"unicode":             {"план", ""},
		"long":                {strings.Repeat("x", 63) + "-yz", strings.Repeat("x", 63)},
	} {
		t.Run(name, func(t *testing.T) {
			tpl := decode(t, `{"conditions": [{"name": "c", "expression": `+asJSON(t, "app.userProperty["+asJSON(t, tc.key)+"] == 'v'")+`}],
				"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"c": {"value": "b"}}}}}`)
			r := translate(t, tpl, project())
			if tc.attribute == "" {
				if len(r.Attributes) != 0 || len(r.Flags[0].Rules) != 0 {
					t.Errorf("attributes = %+v, rules = %v, want a skipped rule", r.Attributes, r.Flags[0].Rules)
				}
				return
			}
			if len(r.Attributes) != 1 || r.Attributes[0].Key != tc.attribute || r.Flags[0].Rules[0].Conditions[0].Attribute != tc.attribute {
				t.Errorf("attributes = %+v, rules = %+v; want %s", r.Attributes, r.Flags[0].Rules, tc.attribute)
			}
		})
	}
}

func TestAnAttributeIsDeclaredOnceAndOnlyForARuleThatIsKept(t *testing.T) {
	tpl := decode(t, `{"conditions": [
			{"name": "a", "expression": "app.userProperty['Plan'] == 'x' && app.userProperty['Plan'] == 'y'"},
			{"name": "b", "expression": "app.userProperty['Plan'] == 'z' || app.userProperty['Skipped'] == 'q' && percent <= 1 && percent <= 2"},
			{"name": "c", "expression": "app.userProperty['Plan'] > 3"}],
		"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"a": {"value": "1"}, "b": {"value": "2"}, "c": {"value": "3"}}}}}`)
	r := translate(t, tpl, project())
	if len(r.Attributes) != 1 || r.Attributes[0].Key != "plan" || r.Attributes[0].Type != workspace.TypeString {
		t.Errorf("attributes = %+v, want plan, once, and not skipped", r.Attributes)
	}
	attributeDeclared := 0
	for _, w := range r.Warnings {
		if w.Code == firebase.WarnAttributeDeclared {
			attributeDeclared++
		}
	}
	if attributeDeclared != 1 {
		t.Errorf("warnings = %+v, want one attribute_declared", r.Warnings)
	}
	// The third condition compares a string attribute declared by an
	// earlier rule as a number.
	last := r.Warnings[len(r.Warnings)-1]
	if last.Code != firebase.WarnConditionSkipped || last.Params["reason"] != "the attribute plan is a string, and the comparison needs a number" {
		t.Errorf("last warning = %+v", last)
	}
}

func TestADeclaredAttributeIsUsedAsItIs(t *testing.T) {
	current := project()
	current.Attributes = append(current.Attributes, workspace.Attribute{Key: "plan", Type: workspace.TypeString})
	tpl := decode(t, `{"conditions": [{"name": "c", "expression": "app.userProperty['Plan'] == 'x'"}],
		"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"c": {"value": "b"}}}}}`)
	r := translate(t, tpl, current)
	if len(r.Attributes) != 0 || len(r.Warnings) != 0 || r.Flags[0].Rules[0].Conditions[0].Attribute != "plan" {
		t.Errorf("attributes = %+v, warnings = %+v, rules = %+v", r.Attributes, r.Warnings, r.Flags[0].Rules)
	}
}

func TestARuleHoldsAtMostTwentyConditions(t *testing.T) {
	terms := make([]string, 21)
	for i := range terms {
		terms[i] = fmt.Sprintf("app.customSignal['s%d'] == 'x'", i)
	}
	tpl := decode(t, `{"conditions": [{"name": "c", "expression": `+asJSON(t, strings.Join(terms, " && "))+`}],
		"parameters": {"p": {"defaultValue": {"value": "a"}, "conditionalValues": {"c": {"value": "b"}}}}}`)
	r := translate(t, tpl, project())
	if len(r.Flags[0].Rules) != 0 || len(r.Attributes) != 0 || len(r.Warnings) != 1 || r.Warnings[0].Params["reason"] != "a rule holds at most 20 conditions" {
		t.Errorf("rules = %v, attributes = %v, warnings = %+v", r.Flags[0].Rules, r.Attributes, r.Warnings)
	}
}

func existingFlag(key string, typ flags.Type, rules ...flags.Rule) firebase.ExistingFlag {
	return firebase.ExistingFlag{Flag: flags.Flag{Key: key, Type: typ, Name: "Kept name", Description: "Kept.", Tags: []string{"web"},
		Variations: []flags.Variation{{Key: "hello", Value: json.RawMessage(`"Hello"`), Name: "hello"}, {Key: "other", Value: json.RawMessage(`"Other"`)}}}, Rules: rules}
}

func TestAnExistingFlagKeepsWhatTheTemplateDoesNotSay(t *testing.T) {
	current := project()
	current.Flags = []firebase.ExistingFlag{existingFlag("banner_text", flags.TypeString)}
	r := translate(t, fixture(t), current)
	f := flagOf(t, r, "banner_text")
	if !f.Existing || f.Name != "Kept name" || f.Description != "Kept." || !reflect.DeepEqual(f.Tags, []string{"web"}) {
		t.Errorf("banner_text = %+v, want its name, description and tags kept", f)
	}
	if got := asJSON(t, f.Variations); got !=
		`[{"Key":"hello","Value":"Hello","Name":"hello","Description":""},{"Key":"other","Value":"Other","Name":"","Description":""},`+
			`{"Key":"v1","Value":"Hi","Name":"ios_or_beta_app","Description":""},{"Key":"v2","Value":"Welcome, pro","Name":"pro_or_early","Description":""}]` {
		t.Errorf("variations = %s, want the flag's own, then the new values", got)
	}
	if f.OffVariation != "hello" || f.Fallthrough != "hello" || !f.On || f.Rules[0].Serve.Variation != "hello" || f.Rules[1].Serve.Variation != "v1" {
		t.Errorf("configuration = %+v", f)
	}
}

func TestAnExistingFlagTakesANonEmptyDescriptionAndTheGroupAsATag(t *testing.T) {
	current := project()
	steps := existingFlag("onboarding_steps", flags.TypeNumber)
	steps.Flag.Tags, steps.Flag.Variations = nil, nil
	current.Flags = []firebase.ExistingFlag{steps, existingFlag("new_matching", flags.TypeBoolean)}
	current.Flags[1].Flag.Variations = []flags.Variation{{Key: "true", Value: json.RawMessage(`true`)}, {Key: "false", Value: json.RawMessage(`false`)}}
	r := translate(t, fixture(t), current)
	if f := flagOf(t, r, "onboarding_steps"); !reflect.DeepEqual(f.Tags, []string{"Onboarding"}) || f.Description != "Kept." {
		t.Errorf("onboarding_steps = %+v, want the group as a tag and the old description", f)
	}
	if f := flagOf(t, r, "new_matching"); f.Description != "The new matching screen." || !reflect.DeepEqual(f.Tags, []string{"web"}) {
		t.Errorf("new_matching = %+v, want the template's description", f)
	}
}

func TestReImportingKeepsRuleIdentities(t *testing.T) {
	first := translate(t, fixture(t), project())
	// The project now holds what the first translation produced, with the
	// identities a write gives.
	current := project()
	n := byte(0)
	for _, f := range first.Flags {
		stored := firebase.ExistingFlag{Flag: flags.Flag{Key: f.Key, Type: f.Type, Name: f.Name, Description: f.Description, Tags: f.Tags, Variations: f.Variations},
			Rules: []flags.Rule{}}
		for _, r := range f.Rules {
			n++
			r.ID = id.FromBytes[flags.Rule]([16]byte{15: n})
			if r.Serve.Kind == flags.ServeRollout {
				r.Serve.Salt = "0123456789abcdef"
			}
			stored.Rules = append(stored.Rules, r)
		}
		current.Flags = append(current.Flags, stored)
	}
	current.Attributes = append(current.Attributes, workspace.Attribute{Key: "plan_type", Type: workspace.TypeString})

	again := translate(t, fixture(t), current)
	for i, f := range again.Flags {
		if !f.Existing || !reflect.DeepEqual(f.Variations, current.Flags[i].Flag.Variations) || !reflect.DeepEqual(f.Rules, current.Flags[i].Rules) {
			t.Errorf("%s: flag = %+v, want what the project holds", f.Key, f)
		}
	}
	if len(again.Attributes) != 0 {
		t.Errorf("attributes = %+v, want none declared again", again.Attributes)
	}
	for _, f := range again.Flags {
		for _, r := range f.Rules {
			if r.ID.IsZero() {
				t.Errorf("%s: a rule lost its identity: %+v", f.Key, r)
			}
		}
	}
	if m := flagOf(t, again, "new_matching"); m.Rules[1].Serve.Salt != "0123456789abcdef" {
		t.Errorf("the rollout lost its salt: %+v", m.Rules[1])
	}
}

func TestAnEditedRuleLosesItsIdentityAndOneIdentityIsClaimedOnce(t *testing.T) {
	rule := func(n byte, variation string) flags.Rule {
		return flags.Rule{ID: id.FromBytes[flags.Rule]([16]byte{15: n}), Description: `Firebase condition "c"`,
			Conditions: []flags.Condition{attr("platform", "eq", "ios")}, Serve: flags.Serve{Kind: flags.ServeVariation, Variation: variation}}
	}
	current := project()
	current.Flags = []firebase.ExistingFlag{existingFlag("p", flags.TypeString, rule(1, "other"), rule(2, "hello"))}
	tpl := decode(t, `{"conditions": [{"name": "c", "expression": "device.os == 'ios' || device.os == 'ios'"}],
		"parameters": {"p": {"defaultValue": {"value": "Hello"}, "conditionalValues": {"c": {"value": "Hello"}}}}}`)
	got := flagOf(t, translate(t, tpl, current), "p").Rules
	if len(got) != 2 || got[0].ID != rule(2, "").ID || !got[1].ID.IsZero() {
		t.Errorf("rules = %+v, want the first to claim the equal rule and the second to be new", got)
	}
}

func TestAnArchivedFlagIsConfiguredAndWarned(t *testing.T) {
	archived := existingFlag("p", flags.TypeString)
	now := archived.Flag.CreatedAt
	archived.Flag.ArchivedAt = &now
	current := project()
	current.Flags = []firebase.ExistingFlag{archived}
	tpl := decode(t, `{"parameters": {"p": {"defaultValue": {"value": "Hello"}}}}`)
	r := translate(t, tpl, current)
	if len(r.Warnings) != 1 || r.Warnings[0].Code != firebase.WarnArchivedFlag || r.Warnings[0].Pointer != "/parameters/p" ||
		r.Warnings[0].Params["parameter"] != "p" || len(r.Flags) != 1 {
		t.Errorf("flags = %+v, warnings = %+v", r.Flags, r.Warnings)
	}
}

func violations(t *testing.T, err error) []apperrors.Violation {
	t.Helper()
	var e *apperrors.Error
	if !asError(err, &e) || e.Code != "invalid_document" {
		t.Fatalf("error = %v, want invalid_document", err)
	}
	return e.Violations
}

func asError(err error, target **apperrors.Error) bool {
	e, ok := err.(*apperrors.Error)
	*target = e
	return ok
}

func TestTranslateRefusesWhatCannotBeTranslated(t *testing.T) {
	retyped := project()
	retyped.Flags = []firebase.ExistingFlag{existingFlag("a", flags.TypeBoolean)}
	for name, tc := range map[string]struct {
		template string
		current  firebase.Current
		pointer  string
		code     string
	}{
		"not a boolean":      {`{"parameters": {"a": {"valueType": "BOOLEAN", "defaultValue": {"value": "maybe"}}}}`, project(), "/parameters/a/defaultValue/value", "invalid_value"},
		"not a number":       {`{"parameters": {"a": {"valueType": "NUMBER", "defaultValue": {"value": "many"}}}}`, project(), "/parameters/a/defaultValue/value", "invalid_value"},
		"number is infinite": {`{"parameters": {"a": {"valueType": "NUMBER", "defaultValue": {"value": "1e999"}}}}`, project(), "/parameters/a/defaultValue/value", "invalid_value"},
		"not JSON":           {`{"parameterGroups": {"g": {"parameters": {"a": {"valueType": "JSON", "defaultValue": {"value": "{"}}}}}}`, project(), "/parameterGroups/g/parameters/a/defaultValue/value", "invalid_value"},
		"unknown type":       {`{"parameters": {"a": {"valueType": "DATE"}}}`, project(), "/parameters/a/valueType", "invalid_value_type"},
		"retyped flag":       {`{"parameters": {"a": {"defaultValue": {"value": "x"}}}}`, retyped, "/parameters/a/valueType", "type_immutable"},
		"conditional value": {`{"conditions": [{"name": "c", "expression": "true"}], "parameters": {"a": {"valueType": "NUMBER", "defaultValue": {"value": "1"}, "conditionalValues": {"c": {"value": "x"}}}}}`,
			project(), "/parameters/a/conditionalValues/c/value", "invalid_value"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := firebase.Translate(decode(t, tc.template), tc.current, anonymous)
			v := violations(t, err)
			if len(v) != 1 || v[0].Pointer != tc.pointer || v[0].Code != tc.code {
				t.Errorf("violations = %+v, want %s %s", v, tc.pointer, tc.code)
			}
		})
	}

	// Every fault is reported at once, and a refused template warns nothing.
	_, err := firebase.Translate(decode(t, `{"parameters": {"a": {"valueType": "BOOLEAN", "defaultValue": {"value": "x"}}, "b": {"valueType": "NUMBER", "defaultValue": {"value": "y"}}}}`), project(), anonymous)
	if v := violations(t, err); len(v) != 2 {
		t.Errorf("violations = %+v, want both", v)
	}
	// A template that was not decoded is checked all the same.
	_, err = firebase.Translate(firebase.Template{Parameters: map[string]firebase.Parameter{"a": {ConditionalValues: map[string]firebase.Value{"nope": {}}}}}, project(), anonymous)
	if v := violations(t, err); len(v) != 1 || v[0].Code != "unknown_condition" {
		t.Errorf("violations = %+v", v)
	}
}

func TestDecode(t *testing.T) {
	for name, tc := range map[string]struct {
		template string
		faults   []apperrors.Violation
	}{
		"not JSON":                 {`conditions:`, []apperrors.Violation{{Pointer: "", Code: "invalid_json"}}},
		"data after":               {`{} {}`, []apperrors.Violation{{Pointer: "", Code: "invalid_json"}}},
		"wrong shape":              {`{"conditions": 3}`, []apperrors.Violation{{Pointer: "", Code: "invalid_json"}}},
		"condition without a name": {`{"conditions": [{"expression": "true"}]}`, []apperrors.Violation{{Pointer: "/conditions/0/name", Code: "required"}}},
		"duplicate condition":      {`{"conditions": [{"name": "a"}, {"name": "b"}, {"name": "a"}]}`, []apperrors.Violation{{Pointer: "/conditions/2/name", Code: "duplicate_condition"}}},
		"unknown condition": {`{"conditions": [{"name": "a"}], "parameters": {"p": {"conditionalValues": {"a": {}, "x/y": {}}}}}`,
			[]apperrors.Violation{{Pointer: "/parameters/p/conditionalValues/x~1y", Code: "unknown_condition"}}},
		"parameter in two places": {`{"parameters": {"p": {}}, "parameterGroups": {"g": {"parameters": {"p": {}}}}}`,
			[]apperrors.Violation{{Pointer: "/parameterGroups/g/parameters/p", Code: "duplicate_parameter"}}},
		"parameter in two groups": {`{"parameterGroups": {"g": {"parameters": {"p~": {}}}, "h": {"parameters": {"p~": {}}}}}`,
			[]apperrors.Violation{{Pointer: "/parameterGroups/h/parameters/p~0", Code: "duplicate_parameter"}}},
		"every fault": {`{"conditions": [{}, {"name": "a"}, {"name": "a"}], "parameters": {"p": {"conditionalValues": {"b": {}}}}}`,
			[]apperrors.Violation{{Pointer: "/conditions/0/name", Code: "required"}, {Pointer: "/conditions/2/name", Code: "duplicate_condition"},
				{Pointer: "/parameters/p/conditionalValues/b", Code: "unknown_condition"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := firebase.Decode([]byte(tc.template))
			got := violations(t, err)
			if len(got) != len(tc.faults) {
				t.Fatalf("violations = %+v, want %+v", got, tc.faults)
			}
			for i := range got {
				if got[i].Pointer != tc.faults[i].Pointer || got[i].Code != tc.faults[i].Code {
					t.Errorf("violation %d = %s %s, want %s %s", i, got[i].Pointer, got[i].Code, tc.faults[i].Pointer, tc.faults[i].Code)
				}
			}
		})
	}
}

func TestDecodeIgnoresFieldsItDoesNotKnow(t *testing.T) {
	tpl := decode(t, `{"etag": "x", "version": {"versionNumber": "3"}, "conditions": [{"name": "a", "expression": "true", "tagColor": "RED"}],
		"parameters": {"p": {"defaultValue": {"value": "v"}, "valueType": "STRING", "description": "d", "extra": [1]}},
		"parameterGroups": {"g": {"description": "d", "parameters": {"q": {}}}}}`)
	if len(tpl.Conditions) != 1 || len(tpl.Parameters) != 1 || len(tpl.ParameterGroups["g"].Parameters) != 1 {
		t.Errorf("template = %+v", tpl)
	}
}
