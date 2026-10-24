package management_test

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// startableExperiment is a draft every start precondition holds for, taking
// seven tenths of the units.
func startableExperiment(key string) management.ExperimentCreate {
	e := validExperiment(key)
	e.Hypothesis = ptr("Short titles raise completed checkouts.")
	e.ExpectedEndAt = &openapi_types.Date{Time: time.Date(2027, time.January, 31, 0, 0, 0, 0, time.UTC)}
	e.TrafficAllocation = ptr(7000)
	return e
}

func startExperiment(t *testing.T, c *management.ClientWithResponses, project, key string, version int64) *management.StartExperimentResponse {
	t.Helper()
	return do(c.StartExperimentWithResponse(t.Context(), project, key, &management.StartExperimentParams{IfMatch: ifMatch(version)}))
}

func mustStart(t *testing.T, c *management.ClientWithResponses, project, key string, version int64) management.Experiment {
	t.Helper()
	res := startExperiment(t, c, project, key, version)
	if res.JSON200 == nil {
		t.Fatalf("start %s = %d %s", key, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// holding returns a configuration put serving the experiments by key, one
// rule each, then the variation rules.
func holding(keys ...string) management.FlagConfigurationPut {
	rules := []management.RuleInput{}
	for _, k := range keys {
		rules = append(rules, management.RuleInput{Serve: serveExperiment(k)})
	}
	return management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "c", Rules: &rules}
}

// unitWithBucket returns a unit ID whose bucket under salt satisfies want,
// by the published formula.
func unitWithBucket(t *testing.T, salt string, want func(bucket int) bool) string {
	t.Helper()
	for i := range 1000 {
		unit := "u_" + strconv.Itoa(i)
		if want(eval.Bucket(salt, unit)) {
			return unit
		}
	}
	t.Fatalf("no unit ID in 1,000 has the bucket the test needs under salt %s", salt)
	return ""
}

// evaluateUnit evaluates checkout_title in staging for unit of unitType.
func evaluateUnit(t *testing.T, c *management.ClientWithResponses, project, unitType, unit string) management.EvaluationResult {
	t.Helper()
	res := evaluate(t, c, project, "staging", management.EvaluationRequest{
		Context: management.EvaluationContext{Units: &map[string]string{unitType: unit}}, Flags: &[]string{"checkout_title"},
	})
	if res.JSON200 == nil {
		t.Fatalf("evaluate = %d %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Flags["checkout_title"]
}

func TestStartingAnExperimentRunsItInEveryEnvironmentHoldingItAtOnce(t *testing.T) {
	f := startExperiments(t)
	created := createExperiment(t, f.admin, f.project.Key, startableExperiment("title_test"))
	createExperiment(t, f.admin, f.project.Key, startableExperiment("unheld"))
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, holding("title_test"))
	mustPut(t, f.admin, f.project.Key, "checkout_title", "development", 1, holding("title_test"))
	before := rulesetVersions(t, f.project)

	res := startExperiment(t, f.admin, f.project.Key, "title_test", 1)
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("start = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	started := *res.JSON200
	if started.Status != management.ExperimentStatusRunning || started.StartedAt == nil || started.Version != 2 ||
		started.InclusionSalt != created.InclusionSalt {
		t.Errorf("started = %+v", started)
	}

	// The holders' rulesets moved, and nothing else did.
	after := rulesetVersions(t, f.project)
	for env, moved := range map[string]bool{"staging": true, "development": true, "production": false} {
		if (after[env] != before[env]) != moved {
			t.Errorf("%s: ruleset version %d -> %d, want moved %v", env, before[env], after[env], moved)
		}
	}
	// What a holder's SDKs receive is the experiment as it froze.
	rules := rulesOfFlag(t, f.admin, f.project.Key, "staging", "checkout_title", after["staging"])
	if len(rules) != 1 {
		t.Fatalf("staging rules = %+v, want the experiment rule", rules)
	}
	s := rules[0].Serve
	if s.Kind != eval.ServeExperiment || s.Experiment != "title_test" || s.UnitType != "user" ||
		s.InclusionSalt != created.InclusionSalt || s.VariationSalt != created.VariationSalt || s.TrafficAllocation != 7000 ||
		len(s.Weights) != 2 || s.Weights[0].Variation != "a" || s.Weights[1].Variation != "b" {
		t.Errorf("staging serve = %+v", s)
	}

	entries := auditOf(t, f.srv, "experiment.started", created.Id.String())
	if len(entries) != 1 || field(entries[0].After, "status") != "running" || field(entries[0].Before, "status") != "draft" {
		t.Errorf("experiment.started entries = %+v", entries)
	}

	// An experiment no environment holds starts all the same, and moves no
	// ruleset.
	if e := mustStart(t, f.admin, f.project.Key, "unheld", 1); e.Status != management.ExperimentStatusRunning {
		t.Errorf("unheld = %+v", e)
	}
	if again := rulesetVersions(t, f.project); !mapsEqual(again, after) {
		t.Errorf("starting an unheld experiment moved %v to %v", after, again)
	}
}

// field returns key of an audit entry's experiment values, nil when there is
// none.
func field(values any, key string) any {
	m, _ := values.(map[string]any)
	return m[key]
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestAStartNamesEveryPreconditionTheDraftFails(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, validExperiment("bare"))
	res := startExperiment(t, f.admin, f.project.Key, "bare", 1)
	if got := violationsOf(t, "a bare draft", res.StatusCode(), res.Body, "experiment_not_ready"); !slices.Equal(got,
		[]string{"/hypothesis required", "/expected_end_at required"}) {
		t.Errorf("violations = %v", got)
	}
	for _, c := range []struct {
		key  string
		drop func(*management.ExperimentCreate)
		want string
	}{
		{"no_hypothesis", func(e *management.ExperimentCreate) { e.Hypothesis = nil }, "/hypothesis required"},
		{"no_end", func(e *management.ExperimentCreate) { e.ExpectedEndAt = nil }, "/expected_end_at required"},
	} {
		body := startableExperiment(c.key)
		c.drop(&body)
		createExperiment(t, f.admin, f.project.Key, body)
		res := startExperiment(t, f.admin, f.project.Key, c.key, 1)
		if got := violationsOf(t, c.key, res.StatusCode(), res.Body, "experiment_not_ready"); !slices.Equal(got, []string{c.want}) {
			t.Errorf("%s: violations = %v", c.key, got)
		}
	}
	// Every refusal left its draft as it was.
	for _, key := range []string{"bare", "no_hypothesis", "no_end"} {
		got := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, key)).JSON200
		if got == nil || got.Status != management.ExperimentStatusDraft || got.StartedAt != nil || got.Version != 1 {
			t.Errorf("%s after a refused start = %+v", key, got)
		}
	}
}

func TestOnlyADraftStartsAndOnlyFromItsCurrentVersion(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, startableExperiment("title_test"))

	missing := do(f.admin.StartExperimentWithResponse(t.Context(), f.project.Key, "title_test", &management.StartExperimentParams{}))
	assertProblem(t, "a start without If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := startExperiment(t, f.admin, f.project.Key, "title_test", 7)
	assertProblem(t, "a start from another version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	unknown := startExperiment(t, f.admin, f.project.Key, "absent", 1)
	assertProblem(t, "a start of an unknown experiment", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")

	mustStart(t, f.admin, f.project.Key, "title_test", 1)
	again := startExperiment(t, f.admin, f.project.Key, "title_test", 2)
	assertProblem(t, "a second start", again.StatusCode(), again.Body, http.StatusConflict, "not_draft")

	for _, status := range []experiments.Status{experiments.StatusStopped, experiments.StatusArchived} {
		key := string(status)
		createExperiment(t, f.admin, f.project.Key, startableExperiment(key))
		putInStatus(t, f, key, status)
		res := startExperiment(t, f.admin, f.project.Key, key, 1)
		assertProblem(t, "a start of a "+key+" experiment", res.StatusCode(), res.Body, http.StatusConflict, "not_draft")
	}
}

func TestASecondReplicaServesAStartedExperiment(t *testing.T) {
	f := startExperiments(t)
	b := testkit.Client(t, testkit.StartServer(t), testkit.InstanceAdminToken(t))
	createExperiment(t, f.admin, f.project.Key, startableExperiment("title_test"))
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, holding("title_test"))

	// b serves the staging ruleset, which leaves the draft's rule out.
	if rules := rulesOfFlag(t, b, f.project.Key, "staging", "checkout_title", rulesetVersions(t, f.project)["staging"]); len(rules) != 0 {
		t.Fatalf("b serves the draft's rule: %+v", rules)
	}
	mustStart(t, f.admin, f.project.Key, "title_test", 1)
	rules := rulesOfFlag(t, b, f.project.Key, "staging", "checkout_title", rulesetVersions(t, f.project)["staging"])
	if len(rules) != 1 || rules[0].Serve.Experiment != "title_test" || rules[0].Serve.TrafficAllocation != 7000 {
		t.Errorf("b serves %+v, want the started experiment", rules)
	}
}

func TestEvaluatingAStartedExperiment(t *testing.T) {
	f := startExperiments(t)
	e := createExperiment(t, f.admin, f.project.Key, startableExperiment("title_test"))
	// The experiment rule first, then a variation rule, so a unit outside
	// the allocation is visibly served by the next one.
	put := holding("title_test")
	*put.Rules = append(*put.Rules, management.RuleInput{Serve: serves("c")})
	c := mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, put)
	mustStart(t, f.admin, f.project.Key, "title_test", 1)

	included := unitWithBucket(t, e.InclusionSalt, func(b int) bool { return b < 7000 })
	excluded := unitWithBucket(t, e.InclusionSalt, func(b int) bool { return b >= 7000 })
	variationBucket := eval.Bucket(e.VariationSalt, included)
	assigned := "a"
	if variationBucket >= 5000 {
		assigned = "b"
	}

	r := evaluateUnit(t, f.admin, f.project.Key, "user", included)
	if r.Reason != "experiment" || valueOr(r.Variation) != assigned || r.RuleId == nil || *r.RuleId != c.Rules[0].Id || r.Experiment == nil {
		t.Fatalf("an included unit: %+v", r)
	}
	if x := *r.Experiment; x.Key != "title_test" || x.Variation != assigned || x.UnitType != "user" || x.UnitId != included ||
		x.InclusionSalt != e.InclusionSalt || x.VariationSalt != e.VariationSalt || x.Override ||
		x.InclusionBucket != eval.Bucket(e.InclusionSalt, included) || x.VariationBucket != variationBucket {
		t.Errorf("the experiment block = %+v", x)
	}
	if out := evaluateUnit(t, f.admin, f.project.Key, "user", excluded); out.Reason != "rule" || valueOr(out.Variation) != "c" || out.Experiment != nil {
		t.Errorf("a unit outside the allocation: %+v", out)
	}
	if other := evaluateUnit(t, f.admin, f.project.Key, "anonymous", included); other.Reason != "rule" || other.Experiment != nil {
		t.Errorf("a context without the experiment's unit type: %+v", other)
	}

	// An override wins and carries the assignment it shadows.
	withOverride := asPut(c)
	withOverride.Overrides = &[]management.OverrideInput{{UnitType: "user", UnitId: included, Variation: "c"}}
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", c.Version, withOverride)
	r = evaluateUnit(t, f.admin, f.project.Key, "user", included)
	if r.Reason != "override" || valueOr(r.Variation) != "c" || r.Experiment == nil || !r.Experiment.Override || r.Experiment.Variation != "c" {
		t.Errorf("an overridden unit: %+v", r)
	}

	// The kill switch is the pause: off, and no experiment at all.
	if res := do(f.admin.KillFlagWithResponse(t.Context(), f.project.Key, "checkout_title", "staging", management.Kill{})); res.JSON200 == nil {
		t.Fatalf("kill = %d %s", res.StatusCode(), res.Body)
	}
	if killed := evaluateUnit(t, f.admin, f.project.Key, "user", included); killed.Reason != "off" || killed.Experiment != nil {
		t.Errorf("after the kill switch: %+v", killed)
	}
}

func TestARunningExperimentEditsItsDescriptionAndOnlyGrowsItsAllocation(t *testing.T) {
	f := startExperiments(t)
	e := createExperiment(t, f.admin, f.project.Key, startableExperiment("title_test"))
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, holding("title_test"))
	mustStart(t, f.admin, f.project.Key, "title_test", 1)
	included := unitWithBucket(t, e.InclusionSalt, func(b int) bool { return b < 7000 })
	first := evaluateUnit(t, f.admin, f.project.Key, "user", included)
	before := rulesetVersions(t, f.project)

	// Its description changes, and no ruleset moves.
	res := patchExperiment(t, f.admin, f.project.Key, "title_test", 2, management.ExperimentPatch{
		Name: ptr("Titles, running"), Hypothesis: ptr("Sharper."), Tags: &[]string{"web"},
		ExpectedEndAt: &openapi_types.Date{Time: time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC)},
		// The frozen fields, named with the values they have.
		Flag: ptr("checkout_title"), UnitType: ptr("user"), Variations: &e.Variations,
	})
	if res.JSON200 == nil || res.JSON200.Version != 3 || res.JSON200.Name != "Titles, running" {
		t.Fatalf("PATCH of the description = %d %s", res.StatusCode(), res.Body)
	}
	if got := rulesetVersions(t, f.project); !mapsEqual(got, before) {
		t.Errorf("editing the description moved %v to %v", before, got)
	}
	if got := auditOf(t, f.srv, "experiment.updated", e.Id.String()); len(got) != 1 {
		t.Errorf("experiment.updated entries = %d, want 1", len(got))
	}

	// Its split is frozen, each field it would change named.
	frozen := patchExperiment(t, f.admin, f.project.Key, "title_test", 3, management.ExperimentPatch{
		UnitType: ptr("anonymous"), Variations: &[]management.ExperimentWeight{e.Variations[1], e.Variations[0]},
	})
	p := assertProblem(t, "changing the split", frozen.StatusCode(), frozen.Body, http.StatusConflict, "frozen")
	var fields []string
	if p.Errors != nil {
		for _, v := range *p.Errors {
			fields = append(fields, v.Pointer+" "+v.Code)
		}
	}
	if !slices.Equal(fields, []string{"/unit_type frozen", "/variations frozen"}) {
		t.Errorf("frozen violations = %v", fields)
	}
	blank := patchExperiment(t, f.admin, f.project.Key, "title_test", 3, management.ExperimentPatch{Hypothesis: ptr("")})
	if got := violationsOf(t, "clearing the hypothesis", blank.StatusCode(), blank.Body, "invalid_experiment"); !slices.Equal(got,
		[]string{"/hypothesis required"}) {
		t.Errorf("violations = %v", got)
	}
	shrink := patchExperiment(t, f.admin, f.project.Key, "title_test", 3, management.ExperimentPatch{TrafficAllocation: ptr(6999)})
	assertProblem(t, "shrinking the allocation", shrink.StatusCode(), shrink.Body, http.StatusConflict, "traffic_allocation_decrease")
	for what, status := range map[string]int{
		"salts":  do(f.admin.RegenerateExperimentSaltsWithResponse(t.Context(), f.project.Key, "title_test", &management.RegenerateExperimentSaltsParams{IfMatch: ifMatch(3)})).StatusCode(),
		"delete": do(f.admin.DeleteExperimentWithResponse(t.Context(), f.project.Key, "title_test", &management.DeleteExperimentParams{IfMatch: ifMatch(3)})).StatusCode(),
	} {
		if status != http.StatusConflict {
			t.Errorf("%s of a running experiment = %d, want 409", what, status)
		}
	}

	// Growing it moves the holders' rulesets, and the unit included stays
	// with its variation.
	grown := patchExperiment(t, f.admin, f.project.Key, "title_test", 3, management.ExperimentPatch{TrafficAllocation: ptr(9000)})
	if grown.JSON200 == nil || grown.JSON200.TrafficAllocation != 9000 || grown.JSON200.Version != 4 {
		t.Fatalf("growing = %d %s", grown.StatusCode(), grown.Body)
	}
	after := rulesetVersions(t, f.project)
	for env, moved := range map[string]bool{"staging": true, "development": false, "production": false} {
		if (after[env] != before[env]) != moved {
			t.Errorf("%s: ruleset version %d -> %d, want moved %v", env, before[env], after[env], moved)
		}
	}
	if rules := rulesOfFlag(t, f.admin, f.project.Key, "staging", "checkout_title", after["staging"]); len(rules) != 1 ||
		rules[0].Serve.TrafficAllocation != 9000 {
		t.Errorf("staging rules after growing = %+v", rules)
	}
	if again := evaluateUnit(t, f.admin, f.project.Key, "user", included); valueOr(again.Variation) != valueOr(first.Variation) ||
		again.Reason != "experiment" {
		t.Errorf("the included unit moved from %+v to %+v", first, again)
	}
	entries := auditOf(t, f.srv, "experiment.traffic_allocation_changed", e.Id.String())
	if len(entries) != 1 || field(entries[0].Before, "traffic_allocation") != float64(7000) ||
		field(entries[0].After, "traffic_allocation") != float64(9000) {
		t.Errorf("experiment.traffic_allocation_changed entries = %+v", entries)
	}
}

func TestTwoExperimentRulesServeByFirstMatch(t *testing.T) {
	f := startExperiments(t)
	// The first takes a tenth of the units, the second all of them, so most
	// units fall through the first into the second.
	firstBody := startableExperiment("first")
	firstBody.TrafficAllocation = ptr(1000)
	secondBody := startableExperiment("second")
	secondBody.TrafficAllocation = ptr(10000)
	first := createExperiment(t, f.admin, f.project.Key, firstBody)
	createExperiment(t, f.admin, f.project.Key, secondBody)
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, holding("first", "second"))
	mustStart(t, f.admin, f.project.Key, "first", 1)
	mustStart(t, f.admin, f.project.Key, "second", 1)

	inside := unitWithBucket(t, first.InclusionSalt, func(b int) bool { return b < 1000 })
	if r := evaluateUnit(t, f.admin, f.project.Key, "user", inside); r.Experiment == nil || r.Experiment.Key != "first" {
		t.Errorf("a unit inside the first experiment: %+v", r)
	}
	outside := unitWithBucket(t, first.InclusionSalt, func(b int) bool { return b >= 1000 })
	if r := evaluateUnit(t, f.admin, f.project.Key, "user", outside); r.Experiment == nil || r.Experiment.Key != "second" {
		t.Errorf("a unit outside the first experiment: %+v", r)
	}
}

func TestARuleServingAnArchivedExperimentDoesNotFreezeTheConfiguration(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, startableExperiment("title_test"))
	c := mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, holding("title_test"))
	held := c.Rules[0].Id
	putInStatus(t, f, "title_test", experiments.StatusArchived)

	// Another rule joins, and the archived experiment's rule keeps its place
	// and its identifier.
	c = mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", c.Version, management.FlagConfigurationPut{
		On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{{Id: &held, Serve: serveExperiment("title_test")}, {Serve: serves("b")}},
	})
	if len(c.Rules) != 2 || c.Rules[0].Id != held {
		t.Fatalf("rules = %+v", c.Rules)
	}
	// SDKs never receive it: the next rule serves.
	rules := rulesOfFlag(t, f.admin, f.project.Key, "staging", "checkout_title", rulesetVersions(t, f.project)["staging"])
	if len(rules) != 1 || rules[0].Serve.Kind != eval.ServeVariation {
		t.Errorf("ruleset rules = %+v", rules)
	}
	// A new rule serving it is refused, and the kept one is removed like
	// any other.
	refused := putConfiguration(t, f.admin, f.project.Key, "checkout_title", "staging", c.Version, holding("title_test"))
	if got := violationsOf(t, "a new rule", refused.StatusCode(), refused.Body, "invalid_configuration"); !slices.Equal(got,
		[]string{"/rules/0/serve/experiment experiment_not_servable"}) {
		t.Errorf("violations = %v", got)
	}
	c = mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", c.Version, management.FlagConfigurationPut{
		OffVariation: "a", Fallthrough: "a", Rules: &[]management.RuleInput{{Serve: serves("b")}},
	})
	if len(c.Rules) != 1 {
		t.Errorf("rules after removing it = %+v", c.Rules)
	}
}
