package management_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func getConfiguration(t *testing.T, c *management.ClientWithResponses, project, flag, env string) management.FlagConfiguration {
	t.Helper()
	res := do(c.GetFlagConfigurationWithResponse(t.Context(), project, flag, env))
	if res.JSON200 == nil {
		t.Fatalf("GET %s/%s = %d %s", flag, env, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func putConfiguration(t *testing.T, c *management.ClientWithResponses, project, flag, env string, version int64,
	body management.FlagConfigurationPut,
) *management.PutFlagConfigurationResponse {
	t.Helper()
	return do(c.PutFlagConfigurationWithResponse(t.Context(), project, flag, env,
		&management.PutFlagConfigurationParams{IfMatch: ifMatch(version)}, body))
}

// mustPut replaces the configuration, failing unless it is accepted.
func mustPut(t *testing.T, c *management.ClientWithResponses, project, flag, env string, version int64,
	body management.FlagConfigurationPut,
) management.FlagConfiguration {
	t.Helper()
	res := putConfiguration(t, c, project, flag, env, version, body)
	if res.JSON200 == nil {
		t.Fatalf("PUT %s/%s = %d %s", flag, env, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// asPut returns the configuration c as a write sends it back.
func asPut(c management.FlagConfiguration) management.FlagConfigurationPut {
	out := management.FlagConfigurationPut{
		On: c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough, Prerequisites: ptr(slices.Clone(c.Prerequisites)),
		Overrides: ptr([]management.OverrideInput{}), Rules: ptr([]management.RuleInput{}),
	}
	for _, o := range c.Overrides {
		*out.Overrides = append(*out.Overrides, management.OverrideInput{UnitType: o.UnitType, UnitId: o.UnitId, Variation: o.Variation, Note: o.Note})
	}
	for _, r := range c.Rules {
		*out.Rules = append(*out.Rules, management.RuleInput{Id: ptr(r.Id), Description: ptr(r.Description), Conditions: ptr(r.Conditions), Serve: r.Serve})
	}
	return out
}

func serves(v string) management.Serve { return management.Serve{Kind: "variation", Variation: &v} }

func rolloutOf(unitType, v string, percentage int) management.Serve {
	return management.Serve{Kind: "rollout", UnitType: &unitType, Variation: &v, Percentage: &percentage}
}

func requires(flag, v string) management.Condition {
	return management.Condition{Kind: "prerequisite", Flag: &flag, Variation: &v}
}

func inSegment(key string) management.Condition {
	return management.Condition{Kind: "in_segment", Segment: &key}
}

// stringFlag creates a string flag key with variations a, b and c.
func stringFlag(t *testing.T, c *management.ClientWithResponses, project, key string) management.Flag {
	t.Helper()
	return createFlag(t, c, project, management.FlagCreate{Key: key, Type: management.FlagTypeString, Name: key,
		Variations: variations("a", `"A"`, "b", `"B"`, "c", `"C"`)})
}

func referencesOf(t *testing.T, name string, status int, body []byte) []string {
	t.Helper()
	pr := assertProblem(t, name, status, body, http.StatusConflict, "in_use")
	var out []string
	if pr.References != nil {
		for _, r := range *pr.References {
			out = append(out, r.Key+" "+valueOr(r.Environment))
		}
	}
	return out
}

func TestFlagConfigurationWritesNeedTheCurrentVersionInIfMatch(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")

	got := do(admin.GetFlagConfigurationWithResponse(t.Context(), p.Key, "copy", "staging"))
	if got.JSON200 == nil || got.HTTPResponse.Header.Get("ETag") != `"1"` || got.JSON200.Version != 1 || got.JSON200.Environment != "staging" {
		t.Fatalf("GET = %d, ETag %q: %s", got.StatusCode(), got.HTTPResponse.Header.Get("ETag"), got.Body)
	}
	if c := got.JSON200; c.On || c.OffVariation != "a" || c.Fallthrough != "a" || len(c.Rules) != 0 || len(c.Overrides) != 0 ||
		len(c.Prerequisites) != 0 {
		t.Errorf("a new configuration = %+v, want off serving a, with nothing else", c)
	}
	on := management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"}

	missing := do(admin.PutFlagConfigurationWithResponse(t.Context(), p.Key, "copy", "staging", &management.PutFlagConfigurationParams{}, on))
	assertProblem(t, "no If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := putConfiguration(t, admin, p.Key, "copy", "staging", 7, on)
	if pr := assertProblem(t, "stale", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch"); pr.CurrentVersion == nil || *pr.CurrentVersion != 1 {
		t.Errorf("stale: current version %v, want 1", pr.CurrentVersion)
	}
	invalid := putConfiguration(t, admin, p.Key, "copy", "staging", 7, management.FlagConfigurationPut{OffVariation: "z", Fallthrough: "a"})
	violationsOf(t, "invalid and stale", invalid.StatusCode(), invalid.Body, "invalid_configuration")

	res := putConfiguration(t, admin, p.Key, "copy", "staging", 1, on)
	if res.JSON200 == nil || res.JSON200.Version != 2 || res.HTTPResponse.Header.Get("ETag") != `"2"` || !res.JSON200.On {
		t.Fatalf("PUT = %d %s", res.StatusCode(), res.Body)
	}
	if again := putConfiguration(t, admin, p.Key, "copy", "staging", 2, on); again.JSON200 == nil || again.JSON200.Version != 2 {
		t.Errorf("the same configuration again = %d %s, want 200 at version 2", again.StatusCode(), again.Body)
	}
	if prod := getConfiguration(t, admin, p.Key, "copy", "production"); prod.On || prod.Version != 1 {
		t.Errorf("production = %+v, want it untouched", prod)
	}
	if f := getFlag(t, admin, p.Key, "copy"); f.Version != 1 || f.Environments[1].Version != 2 || !f.Environments[1].On {
		t.Errorf("flag = %+v, want its definition at 1 and staging's configuration at 2", f)
	}

	for name, call := range map[string]func() (int, []byte){
		"an unknown environment": func() (int, []byte) {
			r := do(admin.GetFlagConfigurationWithResponse(t.Context(), p.Key, "copy", "qa"))
			return r.StatusCode(), r.Body
		},
		"an unknown flag": func() (int, []byte) {
			r := putConfiguration(t, admin, p.Key, "absent", "staging", 1, on)
			return r.StatusCode(), r.Body
		},
		"writing an unknown environment": func() (int, []byte) {
			r := putConfiguration(t, admin, p.Key, "copy", "qa", 1, on)
			return r.StatusCode(), r.Body
		},
	} {
		status, body := call()
		assertProblem(t, name, status, body, http.StatusNotFound, "not_found")
	}

	archive(t, admin, p.Key, "copy", 1)
	archived := putConfiguration(t, admin, p.Key, "copy", "staging", 2, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a"})
	assertProblem(t, "an archived flag", archived.StatusCode(), archived.Body, http.StatusConflict, "archived")
}

func TestFlagConfigurationsAreValidated(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "gate", Type: management.FlagTypeBoolean, Name: "Gate"})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "old", Type: management.FlagTypeBoolean, Name: "Old"})
	archive(t, admin, p.Key, "old", 1)
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "beta_testers", Name: "Beta testers"})

	res := putConfiguration(t, admin, p.Key, "copy", "production", 1, management.FlagConfigurationPut{
		OffVariation: "z", Fallthrough: "a",
		Prerequisites: &[]management.Prerequisite{{Flag: "gate", Variation: "maybe"}, {Flag: "old", Variation: "true"}},
		Rules: &[]management.RuleInput{
			{Conditions: conditions(attr("shoe_size", "eq", `"42"`), attr("app_version", "in", `["1.2.0","two"]`),
				inSegment("nobody"), requires("absent", "true")), Serve: serves("b")},
			{Serve: management.Serve{Kind: "experiment", Experiment: ptr("nothing")}},
			{Serve: rolloutOf("device", "b", 10001)},
		},
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: strings.Repeat("x", 257), Variation: "z"}},
	})
	want := []string{
		"/off_variation unknown_variation",
		"/prerequisites/0/variation unknown_variation", "/prerequisites/1/flag archived_flag",
		"/overrides/0/unit_id too_long", "/overrides/0/variation unknown_variation",
		"/rules/0/conditions/0/attribute unknown_attribute", "/rules/0/conditions/1/value invalid_value",
		"/rules/0/conditions/2/segment unknown_segment", "/rules/0/conditions/3/flag unknown_flag",
		"/rules/1/serve/experiment unknown_experiment",
		"/rules/2/serve/unit_type unknown_unit_type", "/rules/2/serve/percentage invalid_value",
	}
	if got := violationsOf(t, "invalid", res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got, want) {
		t.Errorf("violations = %v\nwant %v", got, want)
	}

	valid := mustPut(t, admin, p.Key, "copy", "production", 1, management.FlagConfigurationPut{
		On: true, OffVariation: "a", Fallthrough: "c",
		Prerequisites: &[]management.Prerequisite{{Flag: "gate", Variation: "true"}},
		Rules: &[]management.RuleInput{
			{Description: ptr("Beta testers on iOS"), Conditions: conditions(attr("os", "eq", `"ios"`), inSegment("beta_testers")), Serve: serves("b")},
			{Conditions: conditions(attr("user", "in", `["u-1","u-2"]`)), Serve: rolloutOf("user", "b", 2500)},
		},
	})
	if len(valid.Rules) != 2 || valid.Rules[0].Description != "Beta testers on iOS" || valid.Rules[1].Serve.Salt == nil ||
		len(*valid.Rules[1].Serve.Salt) != 16 || *valid.Rules[1].Serve.Percentage != 2500 {
		t.Errorf("configuration = %+v, want both rules, the rollout with a salt of 16 characters", valid)
	}
}

func TestRulesKeepTheirIdentifiersAndSaltsAcrossWrites(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")
	first := mustPut(t, admin, p.Key, "copy", "staging", 1, management.FlagConfigurationPut{
		OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: rolloutOf("user", "b", 1000)}, {Serve: serves("c")}},
	})
	body := asPut(first)
	(*body.Rules)[0].Serve.Percentage = ptr(5000)
	*body.Rules = append(*body.Rules, management.RuleInput{Serve: serves("b")})
	second := mustPut(t, admin, p.Key, "copy", "staging", 2, body)
	if second.Rules[0].Id != first.Rules[0].Id || second.Rules[1].Id != first.Rules[1].Id || *second.Rules[0].Serve.Salt != *first.Rules[0].Serve.Salt {
		t.Errorf("rules = %+v, want the first two kept with their salt", second.Rules)
	}
	if second.Rules[2].Id == first.Rules[0].Id || second.Rules[2].Id == first.Rules[1].Id {
		t.Errorf("a new rule has identifier %s of another", second.Rules[2].Id)
	}

	body = asPut(second)
	(*body.Rules)[0].Serve.Salt = ptr("abcdefghijklmnop")
	(*body.Rules)[2].Id = (*body.Rules)[1].Id
	res := putConfiguration(t, admin, p.Key, "copy", "staging", 3, body)
	if got := violationsOf(t, "a changed salt and a repeated rule", res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got,
		[]string{"/rules/0/serve/salt immutable", "/rules/2/id duplicate"}) {
		t.Errorf("violations = %v", got)
	}
	other := mustPut(t, admin, p.Key, "copy", "production", 1, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: serves("b")}}})
	body = asPut(second)
	(*body.Rules)[0].Id = ptr(other.Rules[0].Id)
	res = putConfiguration(t, admin, p.Key, "copy", "staging", 3, body)
	if got := violationsOf(t, "another environment's rule", res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got,
		[]string{"/rules/0/id unknown_rule"}) {
		t.Errorf("violations = %v", got)
	}
}

func TestPrerequisitesThatCloseACycleAreRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	for _, k := range []string{"f1", "f2", "f3", "f4", "f5", "f6", "f7"} {
		createFlag(t, admin, p.Key, management.FlagCreate{Key: k, Type: management.FlagTypeBoolean, Name: k})
	}
	require := func(flag, env, on string, version int64) *management.PutFlagConfigurationResponse {
		return putConfiguration(t, admin, p.Key, flag, env, version, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "true",
			Prerequisites: &[]management.Prerequisite{{Flag: on, Variation: "true"}}})
	}
	paths := func(name string, res *management.PutFlagConfigurationResponse, code string) []string {
		pr := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_configuration")
		var out []string
		for _, v := range *pr.Errors {
			if v.Code == code && v.Params != nil {
				b, _ := json.Marshal((*v.Params)["path"])
				out = append(out, v.Pointer+" "+string(b))
			}
		}
		return out
	}
	for i, pair := range [][2]string{{"f1", "f2"}, {"f2", "f3"}, {"f3", "f4"}, {"f4", "f5"}, {"f5", "f6"}} {
		if res := require(pair[0], "production", pair[1], 1); res.JSON200 == nil {
			t.Fatalf("link %d = %d %s", i, res.StatusCode(), res.Body)
		}
	}
	if got := paths("a cycle", require("f6", "production", "f1", 1), "prerequisite_cycle"); !slices.Equal(got,
		[]string{`/prerequisites/0/flag ["f6","f1","f2","f3","f4","f5","f6"]`}) {
		t.Errorf("cycle = %v", got)
	}
	if got := paths("six steps", require("f6", "production", "f7", 1), "prerequisite_too_deep"); !slices.Equal(got,
		[]string{`/prerequisites/0/flag ["f1","f2","f3","f4","f5","f6","f7"]`}) {
		t.Errorf("too deep = %v", got)
	}
	if res := require("f6", "staging", "f1", 1); res.JSON200 == nil {
		t.Errorf("the same requirement in another environment = %d %s, want it accepted", res.StatusCode(), res.Body)
	}
}

func TestRemovingAReferencedVariationIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "banner", Type: management.FlagTypeBoolean, Name: "Banner"})
	mustPut(t, admin, p.Key, "copy", "staging", 1, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules:     &[]management.RuleInput{{Serve: rolloutOf("user", "b", 100)}},
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: "u-1", Variation: "c"}}})
	mustPut(t, admin, p.Key, "banner", "production", 1, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "true",
		Rules: &[]management.RuleInput{{Conditions: conditions(requires("copy", "a")), Serve: serves("true")}}})
	before := rulesetVersions(t, p)

	for name, tc := range map[string]struct {
		vs   *[]management.VariationInput
		want []string
	}{
		"b, which a rollout serves": {variations("a", `"A"`, "c", `"C"`), []string{"copy staging"}},
		"c, which an override pins": {variations("a", `"A"`, "b", `"B"`), []string{"copy staging"}},
		"a, which another flag requires": {variations("A", `"A"`, "b", `"B"`, "c", `"C"`),
			[]string{"copy development", "copy staging", "copy production", "banner production"}},
	} {
		res := updateFlag(t, admin, p.Key, "copy", 1, management.FlagPatch{Variations: tc.vs})
		if got := referencesOf(t, name, res.StatusCode(), res.Body); !slices.Equal(got, tc.want) {
			t.Errorf("%s: references = %v, want %v", name, got, tc.want)
		}
	}
	if got := bumped(t, "refusals", before, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("refusals bumped %v", got)
	}
}

func TestRulesAndOverridesKeepWhatTheyReferenceInPlace(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "staff", Name: "Staff"})
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "beta_testers", Name: "Beta testers"})
	stringFlag(t, admin, p.Key, "copy")
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "banner", Type: management.FlagTypeBoolean, Name: "Banner"})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "retired", Type: management.FlagTypeBoolean, Name: "Retired"})
	for _, env := range []string{"production", "staging"} {
		mustPut(t, admin, p.Key, "banner", env, 1, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "true",
			Rules: &[]management.RuleInput{{
				Conditions: conditions(management.Condition{Kind: "not_in_segment", Segment: ptr("staff")}, requires("copy", "b")),
				Serve:      serves("false"),
			}}})
	}
	mustPut(t, admin, p.Key, "retired", "development", 1, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "true",
		Rules: &[]management.RuleInput{{Conditions: conditions(inSegment("beta_testers")), Serve: serves("true")}}})
	archive(t, admin, p.Key, "retired", 1)
	before := rulesetVersions(t, p)

	cases := []struct {
		name string
		call func() (int, []byte)
		want []string
	}{
		{"deleting a segment a rule names", func() (int, []byte) {
			r := deleteSegment(t, admin, p.Key, "staff", 1)
			return r.StatusCode(), r.Body
		}, []string{"banner staging", "banner production"}},
		{"deleting a segment an archived flag's rule names", func() (int, []byte) {
			r := deleteSegment(t, admin, p.Key, "beta_testers", 1)
			return r.StatusCode(), r.Body
		}, []string{"retired development"}},
		{"archiving a flag a condition requires", func() (int, []byte) {
			r := archive(t, admin, p.Key, "copy", 1)
			return r.StatusCode(), r.Body
		}, []string{"banner staging", "banner production"}},
		{"removing the variation a condition requires", func() (int, []byte) {
			r := updateFlag(t, admin, p.Key, "copy", 1, management.FlagPatch{Variations: variations("a", `"A"`, "c", `"C"`)})
			return r.StatusCode(), r.Body
		}, []string{"banner staging", "banner production"}},
	}
	for _, tc := range cases {
		status, body := tc.call()
		if got := referencesOf(t, tc.name, status, body); !slices.Equal(got, tc.want) {
			t.Errorf("%s: references = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := bumped(t, "refusals", before, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("refusals bumped %v", got)
	}

	for _, env := range []string{"production", "staging"} {
		mustPut(t, admin, p.Key, "banner", env, 2, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "true"})
	}
	if r := deleteSegment(t, admin, p.Key, "staff", 1); r.StatusCode() != http.StatusNoContent {
		t.Errorf("deleting a segment nothing names = %d %s", r.StatusCode(), r.Body)
	}
	if r := archive(t, admin, p.Key, "copy", 1); r.JSON200 == nil {
		t.Errorf("archiving a flag nothing requires = %d %s", r.StatusCode(), r.Body)
	}
}

func TestOverridesAreValidatedInAFullWriteAndKeepTheirNotesOnTheServer(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")
	developer := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleDeveloper))

	written := mustPut(t, admin, p.Key, "copy", "staging", 1, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Overrides: &[]management.OverrideInput{
			{UnitType: "user", UnitId: "u-1", Variation: "b", Note: ptr("  Pinned for the demo  ")},
			{UnitType: "user", UnitId: "u-2", Variation: "c"},
		}})
	if o := written.Overrides[0]; valueOr(o.Note) != "Pinned for the demo" || o.AddedBy == nil || *o.AddedBy != adminID {
		t.Errorf("override = %+v, want the note trimmed and the writer as its author", o)
	}
	if written.Overrides[1].Note != nil {
		t.Errorf("an override without a note = %v, want null", *written.Overrides[1].Note)
	}
	if written.UpdatedBy == nil || *written.UpdatedBy != adminID {
		t.Errorf("updated by %v, want the writer", written.UpdatedBy)
	}

	body := asPut(written)
	(*body.Overrides)[1].Note = ptr("Sales asked")
	replaced := mustPut(t, developer, p.Key, "copy", "staging", 2, body)
	if *replaced.Overrides[0].AddedBy != adminID || *replaced.Overrides[1].AddedBy == adminID || valueOr(replaced.Overrides[1].Note) != "Sales asked" {
		t.Errorf("overrides = %+v, want the untouched one kept by its author and the other by the new writer", replaced.Overrides)
	}
	body = asPut(replaced)
	(*body.Overrides)[0].Note = ptr("   ")
	cleared := mustPut(t, admin, p.Key, "copy", "staging", 3, body)
	if cleared.Overrides[0].Note != nil {
		t.Errorf("a blank note = %v, want it cleared", *cleared.Overrides[0].Note)
	}

	(*body.Overrides)[0].Note = ptr(strings.Repeat("n", 201))
	*body.Overrides = append(*body.Overrides, management.OverrideInput{UnitType: "user", UnitId: "u-2", Variation: "a"},
		management.OverrideInput{UnitType: "organization", UnitId: "o-1", Variation: "a"})
	res := putConfiguration(t, admin, p.Key, "copy", "staging", 4, body)
	if got := violationsOf(t, "invalid overrides", res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got,
		[]string{"/overrides/0/note too_long", "/overrides/2/unit_id duplicate", "/overrides/3/unit_type unknown_unit_type"}) {
		t.Errorf("violations = %v", got)
	}

	token := pipeline(t, srv, p, "developer", management.ScopeFlagsWrite)
	byToken := mustPut(t, token, p.Key, "copy", "staging", 4, management.FlagConfigurationPut{OffVariation: "b", Fallthrough: "a"})
	if byToken.UpdatedBy != nil {
		t.Errorf("updated by %v, want null for a project token", *byToken.UpdatedBy)
	}
}

func TestConfigurationLimits(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "copy")
	rules := make([]management.RuleInput, 51)
	for i := range rules {
		rules[i] = management.RuleInput{Serve: serves("b")}
	}
	overrides := make([]management.OverrideInput, 201)
	for i := range overrides {
		overrides[i] = management.OverrideInput{UnitType: "user", UnitId: "u-" + strconv.Itoa(i), Variation: "b"}
	}
	twentyOne := make([]management.Condition, 21)
	for i := range twentyOne {
		twentyOne[i] = attr("os", "exists", "")
	}
	rules[0].Conditions = &twentyOne
	res := putConfiguration(t, admin, p.Key, "copy", "staging", 1, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules: ptr(rules[:50]), Overrides: &overrides})
	pr := assertProblem(t, "over the limits", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_configuration")
	var got []string
	for _, v := range *pr.Errors {
		got = append(got, v.Pointer+" "+v.Code+" "+raw((*v.Params)["max"]))
	}
	if want := []string{"/overrides too_many 200", "/rules/0/conditions too_many 20"}; !slices.Equal(got, want) {
		t.Errorf("violations = %v, want %v", got, want)
	}
	rules[0].Conditions = nil
	res = putConfiguration(t, admin, p.Key, "copy", "staging", 1, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a", Rules: &rules})
	if got := violationsOf(t, "51 rules", res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got, []string{"/rules too_many"}) {
		t.Errorf("violations = %v", got)
	}
	mustPut(t, admin, p.Key, "copy", "staging", 1, management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules: ptr(rules[:50]), Overrides: ptr(overrides[:200])})
}

func TestEveryAcceptedConfigurationWriteBumpsItsEnvironmentOnceAndRecordsOneAuditEntry(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	f := stringFlag(t, admin, p.Key, "copy")
	rule := []management.RuleInput{{Description: ptr("Testers"), Serve: serves("b")}}

	steps := []struct {
		name   string
		body   management.FlagConfigurationPut
		bumped []string
	}{
		{"switched on", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a"}, []string{"staging"}},
		{"a rule added", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a", Rules: &rule}, []string{"staging"}},
		{"the same again", management.FlagConfigurationPut{}, nil},
		{"a description changed", management.FlagConfigurationPut{}, nil},
		{"switched off", management.FlagConfigurationPut{}, []string{"staging"}},
		{"a stale write", management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"}, nil},
		{"an invalid write", management.FlagConfigurationPut{On: true, OffVariation: "z", Fallthrough: "b"}, nil},
	}
	version := int64(1)
	for _, s := range steps {
		body := s.body
		current := getConfiguration(t, admin, p.Key, "copy", "staging")
		switch s.name {
		case "the same again":
			body = asPut(current)
		case "a description changed":
			body = asPut(current)
			(*body.Rules)[0].Description = ptr("Beta testers")
		case "switched off":
			body = asPut(current)
			body.On = false
			(*body.Rules)[0].Description = ptr("Beta testers, again")
		}
		before := rulesetVersions(t, p)
		v := version
		if s.name == "a stale write" {
			v = 1
		}
		res := putConfiguration(t, admin, p.Key, "copy", "staging", v, body)
		if res.JSON200 != nil {
			version = res.JSON200.Version
		}
		if got := bumped(t, s.name, before, rulesetVersions(t, p)); !slices.Equal(got, s.bumped) {
			t.Errorf("%s bumped %v, want %v", s.name, got, s.bumped)
		}
	}

	res := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ListProjectAuditEntriesParams{EntityId: ptr(f.Id.String())}))
	if res.JSON200 == nil {
		t.Fatalf("GET audit = %d %s", res.StatusCode(), res.Body)
	}
	entries := res.JSON200.Items
	slices.Reverse(entries)
	var got []string
	for _, e := range entries[1:] {
		got = append(got, e.Action)
		if e.EnvironmentId == nil {
			t.Errorf("%s addresses no environment", e.Action)
		}
	}
	want := []string{"flag.switched_on", "flag.configuration_updated", "flag.configuration_updated", "flag.switched_off"}
	if !slices.Equal(got, want) {
		t.Fatalf("actions = %v, want %v: no-ops and refusals record nothing", got, want)
	}
	updated := entries[2]
	before, _ := json.Marshal(updated.Before)
	after, _ := json.Marshal(updated.After)
	if !strings.Contains(string(before), `"version":2`) || !strings.Contains(string(after), `"version":3`) ||
		!strings.Contains(string(after), `"description":"Testers"`) || !strings.Contains(string(after), `"rules":[{`) {
		t.Errorf("flag.configuration_updated before %s after %s, want the whole document and version around the change", before, after)
	}
}

func TestAConfigurationCarriesTheMemberWhoseWriteMadeIt(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	adminID := me(t, srv, testkit.InstanceAdminToken(t)).Member.Id
	p := createProject(t, admin, projectKey())
	f := stringFlag(t, admin, p.Key, "copy")
	for _, c := range f.Environments {
		if c.UpdatedBy == nil || *c.UpdatedBy != adminID {
			t.Errorf("%s configured with the flag: updated by %v, want its creator", c.Environment, c.UpdatedBy)
		}
	}
	if res := do(admin.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: "qa", Name: "QA"})); res.JSON201 == nil {
		t.Fatalf("POST environment = %d %s", res.StatusCode(), res.Body)
	}
	if c := getConfiguration(t, admin, p.Key, "copy", "qa"); c.UpdatedBy == nil || *c.UpdatedBy != adminID {
		t.Errorf("configured with the environment: updated by %v, want its creator", c.UpdatedBy)
	}
	token := pipeline(t, srv, p, "owner", management.ScopeFlagsWrite, management.ScopeProjectAdmin)
	createFlag(t, token, p.Key, management.FlagCreate{Key: "banner", Type: management.FlagTypeBoolean, Name: "Banner", Owner: uuidOf(id.FromBytes[access.Member](adminID))})
	if c := getConfiguration(t, admin, p.Key, "banner", "qa"); c.UpdatedBy != nil {
		t.Errorf("configured by a project token: updated by %v, want null", *c.UpdatedBy)
	}
}
