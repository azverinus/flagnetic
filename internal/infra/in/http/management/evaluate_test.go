package management_test

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func evaluate(t *testing.T, c *management.ClientWithResponses, project, env string, body management.EvaluationRequest) *management.EvaluateResponse {
	t.Helper()
	return do(c.EvaluateWithResponse(t.Context(), project, env, body))
}

// mustEvaluate evaluates, failing unless the evaluation is answered.
func mustEvaluate(t *testing.T, c *management.ClientWithResponses, project, env string, body management.EvaluationRequest) management.Evaluation {
	t.Helper()
	res := evaluate(t, c, project, env, body)
	if res.JSON200 == nil {
		t.Fatalf("evaluate in %s = %d %s", env, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// contextOf returns a context with units by type and attributes as JSON.
func contextOf(units map[string]string, attributes string) management.EvaluationContext {
	c := management.EvaluationContext{Units: &units}
	if attributes != "" {
		var a map[string]any
		if err := json.Unmarshal([]byte(attributes), &a); err != nil {
			panic(err)
		}
		c.Attributes = &a
	}
	return c
}

func user(id string) map[string]string { return map[string]string{"user": id} }

// assertResult fails unless got is the JSON want, member for member.
func assertResult(t *testing.T, name string, got management.EvaluationResult, want string) {
	t.Helper()
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("%s: want %s: %v", name, want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s\nwant %s", name, data, want)
	}
}

func TestEvaluateReturnsEveryFlagWithItsFullReason(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "linux_staff", Name: "Linux staff", Private: ptr(true),
		Conditions: conditions(attr("os", "eq", `"linux"`))})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "gate", Type: management.FlagTypeBoolean, Name: "Gate"})
	for _, key := range []string{"plain", "ruled", "rolled", "pinned", "gated", "private", "retired"} {
		stringFlag(t, admin, p.Key, key)
	}
	archive(t, admin, p.Key, "retired", 1)
	ruled := mustPut(t, admin, p.Key, "ruled", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Description: ptr("Android first"), Conditions: conditions(attr("os", "eq", `"android"`)), Serve: serves("b")}}})
	rolled := mustPut(t, admin, p.Key, "rolled", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: rolloutOf("user", "c", 10_000)}}})
	mustPut(t, admin, p.Key, "pinned", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: "u1", Variation: "b", Note: ptr("Demo account")}}})
	mustPut(t, admin, p.Key, "gated", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "c", Fallthrough: "a",
		Prerequisites: &[]management.Prerequisite{{Flag: "gate", Variation: "true"}}})
	private := mustPut(t, admin, p.Key, "private", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: conditions(inSegment("linux_staff")), Serve: serves("c")}}})
	mustPut(t, admin, p.Key, "plain", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"})

	got := mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(user("u1"), `{"os":"android"}`)})
	if want := rulesetVersions(t, p)["staging"]; got.RulesetVersion != want {
		t.Errorf("ruleset_version = %d, want staging's %d", got.RulesetVersion, want)
	}
	if keys := mapKeys(got.Flags); !slices.Equal(keys, []string{"gate", "gated", "pinned", "plain", "private", "rolled", "ruled"}) {
		t.Errorf("flags evaluated = %v, want every flag but the archived one", keys)
	}
	salt := *rolled.Rules[0].Serve.Salt
	results := map[string]string{
		"gate": `{"type":"boolean","value":false,"variation":"false","reason":"off","automatic_exposure":false,"override_influenced":false}`,
		"gated": `{"type":"string","value":"C","variation":"c","reason":"prerequisite_failed","prerequisite":"gate",
			"automatic_exposure":false,"override_influenced":false}`,
		"pinned": `{"type":"string","value":"B","variation":"b","reason":"override","automatic_exposure":false,"override_influenced":true}`,
		"plain":  `{"type":"string","value":"B","variation":"b","reason":"fallthrough","automatic_exposure":false,"override_influenced":false}`,
		"private": `{"type":"string","value":"A","variation":"a","reason":"fallthrough","automatic_exposure":false,
			"override_influenced":false}`,
		"rolled": `{"type":"string","value":"C","variation":"c","reason":"rollout","rule_id":"` + rolled.Rules[0].Id.String() + `",
			"bucket":` + strconv.Itoa(eval.Bucket(salt, "u1")) + `,"salt":"` + salt + `","automatic_exposure":false,"override_influenced":false}`,
		"ruled": `{"type":"string","value":"B","variation":"b","reason":"rule","rule_id":"` + ruled.Rules[0].Id.String() + `",
			"automatic_exposure":false,"override_influenced":false}`,
	}
	for key, want := range results {
		assertResult(t, key, got.Flags[key], want)
	}
	onLinux := mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(user("u2"), `{"os":"linux"}`)})
	assertResult(t, "private on linux", onLinux.Flags["private"], `{"type":"string","value":"C","variation":"c","reason":"rule",
		"rule_id":"`+private.Rules[0].Id.String()+`","automatic_exposure":false,"override_influenced":false}`)

	named := mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(user("u1"), `{"os":"android"}`),
		Flags: &[]string{"ruled", "nope", "retired", "ruled"}})
	if keys := mapKeys(named.Flags); !slices.Equal(keys, []string{"nope", "retired", "ruled"}) {
		t.Errorf("named flags evaluated = %v", keys)
	}
	for _, key := range []string{"nope", "retired"} {
		assertResult(t, key, named.Flags[key], `{"reason":"error","error_code":"flag_not_found","override_influenced":false}`)
	}
	assertResult(t, "ruled, named", named.Flags["ruled"], results["ruled"])

	empty := mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: management.EvaluationContext{}})
	for _, key := range []string{"pinned", "rolled", "ruled"} {
		if r := empty.Flags[key]; r.Reason != "fallthrough" || valueOr(r.Variation) != "a" {
			t.Errorf("%s for an empty context = %+v, want the fallthrough", key, r)
		}
	}
	production := mustEvaluate(t, admin, p.Key, "production", management.EvaluationRequest{Context: contextOf(user("u1"), `{"os":"android"}`)})
	if production.RulesetVersion != rulesetVersions(t, p)["production"] || production.Flags["ruled"].Reason != "off" ||
		production.Flags["pinned"].Reason != "off" {
		t.Errorf("production = %+v, want its own configurations, all off", production)
	}

	viewer := testkit.Client(t, srv, testkit.GrantedToken(t, testkit.Pool(t), projectIDOf(p), access.RoleViewer))
	if res := evaluate(t, viewer, p.Key, "staging", management.EvaluationRequest{Context: contextOf(user("u1"), "")}); res.JSON200 == nil {
		t.Errorf("a viewer evaluating = %d %s, want the read it is", res.StatusCode(), res.Body)
	}
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestEvaluateUsesADraftWithoutSavingIt(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	subjectFlag := stringFlag(t, admin, p.Key, "subject")
	stringFlag(t, admin, p.Key, "dependent")
	stringFlag(t, admin, p.Key, "retired")
	archive(t, admin, p.Key, "retired", 1)
	saved := mustPut(t, admin, p.Key, "subject", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: conditions(attr("os", "eq", `"ios"`)), Serve: serves("c")}}})
	mustPut(t, admin, p.Key, "dependent", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: conditions(requires("subject", "b")), Serve: serves("b")}}})
	versions := rulesetVersions(t, p)
	entries := len(auditActions(t, admin, p.Key))
	ctx := contextOf(user("u1"), `{"os":"ios"}`)

	draft := asPut(saved)
	*draft.Rules = append(*draft.Rules, management.RuleInput{Serve: rolloutOf("user", "b", 10_000)})
	draft.Fallthrough = "b"
	(*draft.Rules)[0].Conditions = conditions(attr("os", "eq", `"android"`))
	got := mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: ctx,
		Draft: &management.EvaluationDraft{Flag: "subject", Configuration: draft}})
	subject := got.Flags["subject"]
	if subject.Reason != "rollout" || subject.RuleId == nil || *subject.RuleId == saved.Rules[0].Id || subject.Bucket == nil ||
		subject.Salt == nil || *subject.Bucket != eval.Bucket(*subject.Salt, "u1") {
		t.Errorf("subject under the draft = %+v, want its new rollout", subject)
	}
	if d := got.Flags["dependent"]; d.Reason != "rule" || valueOr(d.Variation) != "b" {
		t.Errorf("dependent under the draft = %+v, want it to see the drafted subject", d)
	}
	if got.RulesetVersion != versions["staging"] {
		t.Errorf("ruleset_version = %d, want the saved %d", got.RulesetVersion, versions["staging"])
	}
	kept := asPut(saved)
	(*kept.Rules)[0].Serve = serves("b")
	if r := mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: ctx,
		Draft: &management.EvaluationDraft{Flag: "subject", Configuration: kept}}).Flags["subject"]; r.RuleId == nil ||
		*r.RuleId != saved.Rules[0].Id || valueOr(r.Variation) != "b" {
		t.Errorf("an existing rule under a draft = %+v, want it to keep its identifier", r)
	}

	refused := func(name string, body management.FlagConfigurationPut, want ...string) {
		t.Helper()
		res := evaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: ctx,
			Draft: &management.EvaluationDraft{Flag: "subject", Configuration: body}})
		if got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got, want) {
			t.Errorf("%s: violations = %v, want %v", name, got, want)
		}
	}
	refused("an unknown variation", management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "z"},
		"/draft/configuration/fallthrough unknown_variation")
	refused("an unknown rule", management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Id: ptr(subjectFlag.Id), Serve: serves("a")}}}, "/draft/configuration/rules/0/id unknown_rule")
	refused("an undeclared attribute", management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Conditions: conditions(attr("shoe_size", "eq", `"9"`)), Serve: serves("a")}}},
		"/draft/configuration/rules/0/conditions/0/attribute unknown_attribute")
	refused("an experiment", management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: management.Serve{Kind: "experiment"}}}},
		"/draft/configuration/rules/0/serve/kind experiment_not_available")
	refused("a cycle", management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a",
		Prerequisites: &[]management.Prerequisite{{Flag: "dependent", Variation: "a"}}},
		"/draft/configuration/prerequisites/0/flag prerequisite_cycle")
	unknown := evaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: ctx,
		Draft: &management.EvaluationDraft{Flag: "nope", Configuration: draft}})
	assertProblem(t, "a draft of an unknown flag", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
	archived := evaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: ctx,
		Draft: &management.EvaluationDraft{Flag: "retired", Configuration: management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a"}}})
	assertProblem(t, "a draft of an archived flag", archived.StatusCode(), archived.Body, http.StatusConflict, "archived")

	if c := getConfiguration(t, admin, p.Key, "subject", "staging"); c.Version != saved.Version || c.Fallthrough != "a" || len(c.Rules) != 1 {
		t.Errorf("subject after drafts = %+v, want it as saved", c)
	}
	if got := bumped(t, "drafts", versions, rulesetVersions(t, p)); len(got) != 0 {
		t.Errorf("drafts bumped %v, want nothing", got)
	}
	if n := len(auditActions(t, admin, p.Key)); n != entries {
		t.Errorf("drafts recorded %d audit entries, want none", n-entries)
	}
}

// auditActions returns the actions of the project's audit log, newest
// first.
func auditActions(t *testing.T, c *management.ClientWithResponses, project string) []string {
	t.Helper()
	res := do(c.ListProjectAuditEntriesWithResponse(t.Context(), project, &management.ListProjectAuditEntriesParams{Limit: ptr(200)}))
	if res.JSON200 == nil {
		t.Fatalf("GET audit = %d %s", res.StatusCode(), res.Body)
	}
	return actionsOf(res.JSON200.Items)
}

func TestEvaluateRefusesInvalidContexts(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "subject")

	for name, id := range map[string]string{
		"257 bytes": strings.Repeat("x", 257), "129 two-byte characters": strings.Repeat("я", 129),
	} {
		res := evaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(map[string]string{"user": id, "anonymous": "d-1"}, "")})
		if got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_context"); !slices.Equal(got, []string{"/context/units/user too_long"}) {
			t.Errorf("%s: violations = %v", name, got)
		}
	}
	for name, id := range map[string]string{"256 bytes": strings.Repeat("я", 128), "empty": ""} {
		if res := evaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(user(id), "")}); res.JSON200 == nil {
			t.Errorf("%s = %d %s, want it evaluated", name, res.StatusCode(), res.Body)
		}
	}
	unknownEnv := evaluate(t, admin, p.Key, "qa", management.EvaluationRequest{Context: contextOf(user("u1"), "")})
	assertProblem(t, "an unknown environment", unknownEnv.StatusCode(), unknownEnv.Body, http.StatusNotFound, "not_found")
	unknownProject := evaluate(t, admin, projectKey(), "staging", management.EvaluationRequest{Context: contextOf(user("u1"), "")})
	assertProblem(t, "an unknown project", unknownProject.StatusCode(), unknownProject.Body, http.StatusNotFound, "not_found")
}

// TestBucketMatchesTheMurmur3Vectors checks evaluate's bucket against the
// published formula, MurmurHash3 of "{salt}.{unit_id}" modulo 10,000, as the
// conformance vectors pin it: for the unit ID of every vector, under the
// salt the server gave the rollout.
func TestBucketMatchesTheMurmur3Vectors(t *testing.T) {
	data, err := os.ReadFile("../../../../../conformance/fixtures/murmur3.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Vectors []struct {
			Input  string `json:"input"`
			Hash   uint32 `json:"hash"`
			Bucket int    `json:"bucket"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "rolled")
	c := mustPut(t, admin, p.Key, "rolled", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Serve: rolloutOf("user", "b", 10_000)}}})
	salt := *c.Rules[0].Serve.Salt

	checked := 0
	for _, v := range vectors.Vectors {
		if eval.Murmur3([]byte(v.Input)) != v.Hash || int(v.Hash%eval.Buckets) != v.Bucket {
			t.Fatalf("vector %q: the formula checked against it does not reproduce it", v.Input)
		}
		_, unitID, ok := strings.Cut(v.Input, ".")
		if !ok || unitID == "" || len(unitID) > 256 {
			continue
		}
		r := mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: contextOf(user(unitID), "")}).Flags["rolled"]
		want := int(eval.Murmur3([]byte(salt+"."+unitID)) % eval.Buckets)
		if r.Bucket == nil || *r.Bucket != want || valueOr(r.Salt) != salt {
			t.Errorf("unit %q: bucket %v salt %v, want %d under %s", unitID, r.Bucket, r.Salt, want, salt)
		}
		checked++
	}
	if checked < 10 {
		t.Errorf("checked %d vectors, want at least 10", checked)
	}
}

func TestKillSwitchServesTheOffVariationToEveryContext(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "checkout")
	stringFlag(t, admin, p.Key, "other")
	mustPut(t, admin, p.Key, "checkout", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b",
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: "qa-7", Variation: "c"}},
		Rules:     &[]management.RuleInput{{Conditions: conditions(attr("os", "eq", `"android"`)), Serve: serves("c")}}})
	mustPut(t, admin, p.Key, "other", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"})
	android := contextOf(user("u1"), `{"os":"android"}`)
	if r := mustEvaluate(t, admin, p.Key, "production", management.EvaluationRequest{Context: android}).Flags["checkout"]; r.Reason != "rule" {
		t.Fatalf("before the kill = %+v, want the rule", r)
	}

	kill(t, admin, p.Key, "checkout", "production", ptr("Checkout failing"))
	for name, ctx := range map[string]management.EvaluationContext{
		"android": android, "the override": contextOf(user("qa-7"), ""), "empty": {},
	} {
		got := mustEvaluate(t, admin, p.Key, "production", management.EvaluationRequest{Context: ctx})
		if r := got.Flags["checkout"]; r.Reason != "off" || valueOr(r.Variation) != "a" {
			t.Errorf("%s after the kill = %+v, want the off variation", name, r)
		}
		if r := got.Flags["other"]; r.Reason != "fallthrough" {
			t.Errorf("other for %s = %+v, want it untouched", name, r)
		}
	}
}

// A ruleset version is the greater of the previous plus one and the
// application clock in milliseconds, so a version lost with a failover's
// commits is never handed out again with other content.
func TestRulesetVersionsComeFromTheClock(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	version := func() int64 {
		t.Helper()
		return mustEvaluate(t, admin, p.Key, "staging", management.EvaluationRequest{Context: management.EvaluationContext{}}).RulesetVersion
	}
	if got := version(); got != 1 {
		t.Errorf("a new environment's ruleset_version = %d, want 1", got)
	}

	c.Advance(time.Minute)
	stringFlag(t, admin, p.Key, "checkout")
	if got, want := version(), c.Now().UnixMilli(); got != want {
		t.Errorf("after a write = %d, want the clock's milliseconds %d", got, want)
	}
	mustPut(t, admin, p.Key, "checkout", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"})
	if got, want := version(), c.Now().UnixMilli()+1; got != want {
		t.Errorf("after a second write in the same millisecond = %d, want the next one %d", got, want)
	}

	c.Advance(time.Second)
	mustPut(t, admin, p.Key, "checkout", "staging", 2, management.FlagConfigurationPut{On: false, OffVariation: "a", Fallthrough: "b"})
	if got, want := version(), c.Now().UnixMilli(); got != want {
		t.Errorf("after the clock moved on = %d, want the clock's milliseconds %d", got, want)
	}
	before := version()
	c.Set(c.Now().Add(-time.Hour))
	mustPut(t, admin, p.Key, "checkout", "staging", 3, management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "c"})
	if got := version(); got != before+1 {
		t.Errorf("after the clock moved back = %d, want one above %d", got, before)
	}
}
