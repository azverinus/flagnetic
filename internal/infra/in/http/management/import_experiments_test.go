package management_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// experimentsToTransfer builds, on the flag checkout_title, one experiment in
// every status: the draft a_draft; the running b_running, whose rule
// development and staging hold; c_stopped, stopped with the winner a, which
// rewrote its rule in development; and d_archived, archived from a draft,
// whose rule development keeps.
func experimentsToTransfer(t *testing.T) experimentFixture {
	t.Helper()
	f := startExperiments(t)
	k := f.project.Key
	draft := validExperiment("a_draft")
	draft.Tags, draft.Hypothesis, draft.TrafficAllocation = &[]string{"checkout", "mobile"}, ptr("A shorter title helps."), ptr(2500)
	createExperiment(t, f.admin, k, draft)
	createExperiment(t, f.admin, k, startableExperiment("b_running"))
	createExperiment(t, f.admin, k, startableExperiment("c_stopped"))
	archived := createExperiment(t, f.admin, k, validExperiment("d_archived"))
	mustPut(t, f.admin, k, "checkout_title", "development", 1, holding("b_running", "c_stopped", "d_archived"))
	mustPut(t, f.admin, k, "checkout_title", "staging", 1, holding("b_running"))
	mustStart(t, f.admin, k, "b_running", 1)
	started := mustStart(t, f.admin, k, "c_stopped", 1)
	mustStop(t, f.admin, k, "c_stopped", started.Version, management.ExperimentStop{
		Outcome: management.ExperimentOutcomeWinner, WinnerVariation: ptr("a"), Note: ptr("A wins.")})
	if res := archiveExperiment(t, f.admin, k, "d_archived", archived.Version); res.JSON200 == nil {
		t.Fatalf("archive d_archived = %d %s", res.StatusCode(), res.Body)
	}
	return f
}

// transferExperimentKeys are the experiments of the transfer project.
var transferExperimentKeys = []string{"a_draft", "b_running", "c_stopped", "d_archived"}

func withoutExperiments(t *testing.T, d map[string]any, keys ...string) {
	t.Helper()
	var kept []any
	for _, e := range d["experiments"].([]any) {
		if !slices.Contains(keys, e.(map[string]any)["key"].(string)) {
			kept = append(kept, e)
		}
	}
	if kept == nil {
		kept = []any{}
	}
	d["experiments"] = kept
}

// withoutRulesServing removes, from every configuration of every flag of d,
// the rules serving the experiment key.
func withoutRulesServing(d map[string]any, key string) {
	for _, f := range d["flags"].([]any) {
		for _, c := range f.(map[string]any)["configurations"].(map[string]any) {
			c := c.(map[string]any)
			rules := []any{}
			for _, r := range c["rules"].([]any) {
				if r.(map[string]any)["serve"].(map[string]any)["experiment"] != key {
					rules = append(rules, r)
				}
			}
			c["rules"] = rules
		}
	}
}

func TestARoundTripOfExperimentsKeepsEveryAssignment(t *testing.T) {
	f := experimentsToTransfer(t)
	source := f.project.Key
	exportedYAML := export(t, f.admin, source, "yaml")

	copied := projectKey()
	res := importNew(t, f.admin, management.ImportNewProjectParams{Key: &copied}, "application/yaml", exportedYAML.Body)
	if res.JSON201 == nil {
		t.Fatalf("import create = %d: %s", res.StatusCode(), res.Body)
	}
	assertJSON(t, "experiment changes", res.JSON201.Changes.Experiments,
		`{"created": ["a_draft", "b_running", "c_stopped", "d_archived"], "updated": [], "deleted": []}`)
	assertJSON(t, "warnings", res.JSON201.Warnings, `[]`)

	// The copy is the original but for its key: status, both salts, every
	// timestamp and the decision included.
	want := exported(t, f.admin, source)
	want["project"].(map[string]any)["key"] = copied
	assertJSON(t, "export of the copy", exported(t, f.admin, copied), asJSON(t, want))

	// A unit gets the same assignment in both, through the assignment
	// endpoint and through evaluation, in the environments holding the rules.
	included := 0
	for i := range 150 {
		unit := fmt.Sprintf("unit-%d", i)
		for _, key := range transferExperimentKeys {
			a, b := assignment(t, f.admin, source, key, unit), assignment(t, f.admin, copied, key, unit)
			if a.JSON200 == nil || b.JSON200 == nil {
				t.Fatalf("assignment of %s to %s = %d, %d", unit, key, a.StatusCode(), b.StatusCode())
			}
			assertJSON(t, unit+" in "+key, b.JSON200, asJSON(t, a.JSON200))
			if key == "b_running" && a.JSON200.Included {
				included++
			}
		}
		for _, env := range []string{"development", "staging", "production"} {
			ctx := contextOf(map[string]string{"user": unit}, "")
			a := mustEvaluate(t, f.admin, source, env, management.EvaluationRequest{Context: ctx})
			b := mustEvaluate(t, f.admin, copied, env, management.EvaluationRequest{Context: ctx})
			if asJSON(t, a.Flags) != asJSON(t, b.Flags) {
				t.Fatalf("%s in %s: the copy evaluates %s where the original evaluated %s", unit, env, asJSON(t, b.Flags), asJSON(t, a.Flags))
			}
		}
	}
	if included < 50 {
		t.Errorf("only %d units were included in b_running; the round trip was checked on too few assignments", included)
	}

	// Each experiment is recorded as created, then the import once.
	actions := auditActions(t, f.admin, copied)
	slices.Sort(actions)
	counts := map[string]int{}
	for _, a := range actions {
		counts[a]++
	}
	if counts["experiment.created"] != 4 || counts["project.imported"] != 1 {
		t.Errorf("audit actions = %v, want four experiment.created and one project.imported", actions)
	}
}

func TestAnExperimentDocumentedWithoutSaltsGetsThemAndIsThenLeftAlone(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	doc := map[string]any{"schema_version": 1, "experiments": []any{map[string]any{
		"key": "e_new", "name": "New", "flag": "checkout_title", "unit_type": "user", "status": "draft",
		"variations": []any{map[string]any{"variation": "a", "weight": 5000}, map[string]any{"variation": "b", "weight": 5000}},
	}}}
	r := mustMerge(t, f.admin, k, doc)
	assertJSON(t, "experiment changes", r.Changes.Experiments, `{"created": ["e_new"], "updated": [], "deleted": []}`)
	e := entry(t, exported(t, f.admin, k), "experiments", "e_new")
	if !experimentSalt.MatchString(e["inclusion_salt"].(string)) || !experimentSalt.MatchString(e["variation_salt"].(string)) ||
		e["inclusion_salt"] == e["variation_salt"] || e["traffic_allocation"] != float64(0) && e["traffic_allocation"] != float64(10000) {
		t.Errorf("e_new = %v, want salts minted and the document's allocation", e)
	}
	// A document that still lacks the salts is the same experiment.
	assertNothingChanged(t, mustMerge(t, f.admin, k, doc))

	// A draft that names one salt is given the other, apart from it.
	one := doc["experiments"].([]any)[0].(map[string]any)
	one["key"], one["inclusion_salt"] = "e_half", "00000000000000cc"
	mustMerge(t, f.admin, k, doc)
	half := entry(t, exported(t, f.admin, k), "experiments", "e_half")
	if half["inclusion_salt"] != "00000000000000cc" || !experimentSalt.MatchString(half["variation_salt"].(string)) ||
		half["variation_salt"] == "00000000000000cc" {
		t.Errorf("e_half = %v, want its salt kept and the other drawn", half)
	}
}

func TestMergingAnExperimentChangesItOnlyWithinWhatItsStatusAllows(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key

	// Merging the project's own export changes nothing and records nothing.
	actions := auditActions(t, f.admin, k)
	assertNothingChanged(t, mustMerge(t, f.admin, k, exported(t, f.admin, k)))
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Fatalf("a merge changing nothing recorded %v", got)
	}

	// A draft takes every field, its salts included; a running experiment its
	// description and a larger traffic allocation; a stopped one its
	// description.
	d := exported(t, f.admin, k)
	draft := entry(t, d, "experiments", "a_draft")
	draft["unit_type"] = "anonymous"
	draft["variations"] = []any{map[string]any{"variation": "b", "weight": 3000}, map[string]any{"variation": "c", "weight": 7000}}
	draft["inclusion_salt"], draft["variation_salt"] = "0123456789abcdef", "fedcba9876543210"
	draft["expected_end_at"] = "2027-06-30"
	running := entry(t, d, "experiments", "b_running")
	running["name"], running["traffic_allocation"] = "Checkout, larger", 9000
	stopped := entry(t, d, "experiments", "c_stopped")
	stopped["tags"], stopped["hypothesis"] = []any{"done"}, "Restated."
	before := rulesetVersions(t, f.project)
	units := map[string]string{}
	for i := range 300 {
		unit := fmt.Sprintf("unit-%d", i)
		if a := assignment(t, f.admin, k, "b_running", unit).JSON200; a != nil && a.Included {
			units[unit] = *a.Variation
		}
	}

	r := mustMerge(t, f.admin, k, d)
	assertJSON(t, "experiment changes", []any{r.Changes.Experiments.Created, keysOfUpdates(r.Changes.Experiments.Updated), r.Changes.Experiments.Deleted},
		`[[], ["a_draft", "b_running", "c_stopped"], []]`)
	assertJSON(t, "the patch of the running experiment", updateOf(t, r.Changes.Experiments.Updated, "b_running").Patch,
		`[{"op":"replace","path":"/name","value":"Checkout, larger"},{"op":"replace","path":"/traffic_allocation","value":9000}]`)
	assertJSON(t, "export after the merge", exported(t, f.admin, k), asJSON(t, d))

	// Growing the allocation moved on the rulesets holding the rule, and only
	// them, and no unit already in b_running moved.
	if got := bumped(t, "growing b_running", before, rulesetVersions(t, f.project)); !slices.Equal(got, []string{"development", "staging"}) {
		t.Errorf("rulesets moved on = %v, want development and staging", got)
	}
	grown := 0
	for i := range 300 {
		unit := fmt.Sprintf("unit-%d", i)
		a := assignment(t, f.admin, k, "b_running", unit).JSON200
		if was, ok := units[unit]; ok && (!a.Included || *a.Variation != was) {
			t.Errorf("%s moved out of %s when the allocation grew", unit, was)
		}
		if a.Included {
			grown++
		}
	}
	if grown <= len(units) {
		t.Errorf("b_running takes %d of 300 units at 9000, as many as the %d it took at 7000", grown, len(units))
	}
}

func TestGrowingARunningExperimentsAllocationIsRecordedAsTheAllocationChangeItIs(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	d := exported(t, f.admin, k)
	entry(t, d, "experiments", "b_running")["traffic_allocation"] = 8000
	entry(t, d, "experiments", "a_draft")["name"] = "Renamed draft"
	mustMerge(t, f.admin, k, d)

	res := do(f.admin.ListProjectAuditEntriesWithResponse(t.Context(), k, &management.ListProjectAuditEntriesParams{Limit: ptr(5)}))
	if res.JSON200 == nil {
		t.Fatalf("GET audit = %d %s", res.StatusCode(), res.Body)
	}
	var got []string
	for _, e := range res.JSON200.Items {
		got = append(got, e.Action+" "+e.Entity.Key)
	}
	slices.Sort(got[:3])
	want := []string{"experiment.traffic_allocation_changed b_running", "experiment.updated a_draft", "project.imported " + k}
	if !slices.Equal(got[:3], want) {
		t.Errorf("newest entries = %v, want %v", got[:3], want)
	}
	for _, e := range res.JSON200.Items {
		if e.Action == "experiment.traffic_allocation_changed" {
			before, after := e.Before.(map[string]any), e.After.(map[string]any)
			if before["traffic_allocation"] != float64(7000) || after["traffic_allocation"] != float64(8000) || after["status"] != "running" {
				t.Errorf("the entry holds %v → %v, want 7000 → 8000 while running", before["traffic_allocation"], after["traffic_allocation"])
			}
		}
	}
}

func TestAMergeRefusesToChangeAFrozenExperimentFieldNamingEachOne(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	before := exported(t, f.admin, k)
	versions, actions := rulesetVersions(t, f.project), auditActions(t, f.admin, k)

	for name, c := range map[string]struct {
		key    string
		edit   map[string]any
		code   string
		fields []string
	}{
		"running split": {"b_running", map[string]any{
			"unit_type":  "anonymous",
			"variations": []any{map[string]any{"variation": "b", "weight": 4000}, map[string]any{"variation": "a", "weight": 6000}}},
			"frozen", []string{"/experiments/1/unit_type", "/experiments/1/variations"}},
		"running salt":       {"b_running", map[string]any{"inclusion_salt": "0123456789abcdef"}, "frozen", []string{"/experiments/1/inclusion_salt"}},
		"running started_at": {"b_running", map[string]any{"started_at": "2020-01-01T00:00:00Z"}, "frozen", []string{"/experiments/1/started_at"}},
		"running to stopped": {"b_running", map[string]any{"status": "stopped", "stopped_at": "2026-02-01T00:00:00Z",
			"decision": map[string]any{"outcome": "removed", "winner_variation": "", "note": ""}},
			"frozen", []string{"/experiments/1/status", "/experiments/1/stopped_at", "/experiments/1/decision"}},
		"draft to running": {"a_draft", map[string]any{"status": "running", "started_at": "2026-02-01T00:00:00Z", "expected_end_at": "2027-12-01"},
			"frozen", []string{"/experiments/0/status", "/experiments/0/started_at"}},
		"allocation decrease": {"b_running", map[string]any{"traffic_allocation": 6000}, "traffic_allocation_decrease", nil},
		"stopped allocation":  {"c_stopped", map[string]any{"traffic_allocation": 9000}, "frozen", []string{"/experiments/2/traffic_allocation"}},
		"stopped decision": {"c_stopped", map[string]any{"decision": map[string]any{"outcome": "winner", "winner_variation": "a", "note": "Changed."}},
			"frozen", []string{"/experiments/2/decision"}},
		"archived name": {"d_archived", map[string]any{"name": "Renamed"}, "frozen", []string{"/experiments/3/name"}},
	} {
		d := exported(t, f.admin, k)
		for field, value := range c.edit {
			entry(t, d, "experiments", c.key)[field] = value
		}
		for _, dryRun := range []bool{true, false} {
			res := mergeInto(t, f.admin, k, d, mergeOptions{dryRun: dryRun})
			p := assertProblem(t, name, res.StatusCode(), res.Body, http.StatusConflict, c.code)
			if c.code != "frozen" {
				continue
			}
			var fields []string
			if p.Errors != nil {
				for _, v := range *p.Errors {
					if v.Code != "frozen" {
						t.Errorf("%s: violation %s %s, want frozen", name, v.Pointer, v.Code)
					}
					fields = append(fields, v.Pointer)
				}
			}
			if !slices.Equal(fields, c.fields) {
				t.Errorf("%s: fields = %v, want %v", name, fields, c.fields)
			}
			if p.Detail == nil || !strings.Contains(*p.Detail, c.key) {
				t.Errorf("%s: detail %v does not name %s", name, p.Detail, c.key)
			}
		}
	}
	assertJSON(t, "the project after the refusals", exported(t, f.admin, k), asJSON(t, before))
	if got := bumped(t, "refusals", versions, rulesetVersions(t, f.project)); len(got) != 0 {
		t.Errorf("a refused merge moved %v on", got)
	}
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("a refused merge recorded %v", got)
	}
}

func TestADryRunReportsTheExperimentsToCreateAndUpdateAndWritesNothing(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	before := exported(t, f.admin, k)
	versions, actions := rulesetVersions(t, f.project), auditActions(t, f.admin, k)

	d := exported(t, f.admin, k)
	entry(t, d, "experiments", "b_running")["hypothesis"] = "Restated."
	d["experiments"] = append(d["experiments"].([]any), map[string]any{
		"key": "e_new", "name": "New", "hypothesis": "", "owner": "", "tags": []any{}, "flag": "checkout_title", "unit_type": "user",
		"variations":         []any{map[string]any{"variation": "a", "weight": 5000}, map[string]any{"variation": "b", "weight": 5000}},
		"traffic_allocation": 10000, "inclusion_salt": "00000000000000aa", "variation_salt": "00000000000000bb", "status": "draft",
		"started_at": nil, "stopped_at": nil, "archived_at": nil, "expected_end_at": nil, "decision": nil,
	})
	r := mustMerge(t, f.admin, k, d, mergeOptions{dryRun: true})
	if !r.DryRun {
		t.Error("dry_run = false")
	}
	assertJSON(t, "dry run", []any{r.Changes.Experiments.Created, keysOfUpdates(r.Changes.Experiments.Updated)}, `[["e_new"], ["b_running"]]`)
	assertJSON(t, "the project after the dry run", exported(t, f.admin, k), asJSON(t, before))
	if got := bumped(t, "dry run", versions, rulesetVersions(t, f.project)); len(got) != 0 {
		t.Errorf("a dry run moved %v on", got)
	}
	if got := auditActions(t, f.admin, k); !slices.Equal(got, actions) {
		t.Errorf("a dry run recorded %v", got)
	}

	// Applied, the new draft keeps the salts the document gives it, and the
	// next merge of the same document changes nothing.
	mustMerge(t, f.admin, k, d)
	e := entry(t, exported(t, f.admin, k), "experiments", "e_new")
	if e["inclusion_salt"] != "00000000000000aa" || e["variation_salt"] != "00000000000000bb" {
		t.Errorf("e_new = %v, want the document's salts", e)
	}
	assertNothingChanged(t, mustMerge(t, f.admin, k, d))
}

func TestAnInvalidExperimentRefusesTheDocumentWholeAtItsPointer(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	draftSalt := entry(t, exported(t, f.admin, k), "experiments", "a_draft")["inclusion_salt"].(string)

	for name, c := range map[string]struct {
		key    string
		edit   func(e map[string]any, d map[string]any)
		pointe string
	}{
		"an unknown flag":         {"a_draft", func(e, _ map[string]any) { e["flag"] = "nope" }, "/flag unknown_flag"},
		"an undeclared unit type": {"a_draft", func(e, _ map[string]any) { e["unit_type"] = "company" }, "/unit_type undeclared_unit_type"},
		"an unknown variation": {"a_draft", func(e, _ map[string]any) {
			e["variations"] = []any{map[string]any{"variation": "a", "weight": 5000}, map[string]any{"variation": "huge", "weight": 5000}}
		}, "/variations/1/variation unknown_variation"},
		"weights off 10000": {"a_draft", func(e, _ map[string]any) {
			e["variations"] = []any{map[string]any{"variation": "a", "weight": 5000}, map[string]any{"variation": "b", "weight": 4999}}
		}, "/variations invalid_weights_sum"},
		"a salt two experiments share": {"b_running", func(e, d map[string]any) {
			e["variation_salt"] = entry(t, d, "experiments", "a_draft")["inclusion_salt"]
		}, "/variation_salt duplicate"},
		"a salt a rollout has": {"a_draft", func(_, d map[string]any) {
			production := entry(t, d, "flags", "checkout_title")["configurations"].(map[string]any)["production"].(map[string]any)
			production["rules"] = []any{map[string]any{"conditions": []any{}, "serve": map[string]any{
				"kind": "rollout", "unit_type": "user", "variation": "a", "percentage": 1000, "salt": draftSalt}}}
		}, "/inclusion_salt duplicate"},
		"a malformed salt":  {"a_draft", func(e, _ map[string]any) { e["inclusion_salt"] = "NOT-HEX" }, "/inclusion_salt invalid_value"},
		"an unknown status": {"a_draft", func(e, _ map[string]any) { e["status"] = "paused" }, "/status invalid_value"},
	} {
		d := exported(t, f.admin, k)
		e := entry(t, d, "experiments", c.key)
		c.edit(e, d)
		index := slices.Index(keysOf(d, "experiments"), c.key)
		want := fmt.Sprintf("/experiments/%d%s", index, c.pointe)

		// Merge and create both refuse it, naming the experiment's field. A
		// status the project's experiment is not in is frozen before it is
		// invalid, so only create judges it.
		if c.pointe != "/status invalid_value" {
			merged := mergeInto(t, f.admin, k, d)
			got := violationsOf(t, name+", merged", merged.StatusCode(), merged.Body, "invalid_document")
			if !slices.Contains(got, want) {
				t.Errorf("%s, merged: violations = %v, want %q among them", name, got, want)
			}
		}
		target := projectKey()
		res := importJSON(t, f.admin, management.ImportNewProjectParams{Key: &target}, d)
		got := violationsOf(t, name+", created", res.StatusCode(), res.Body, "invalid_document")
		if !slices.Contains(got, want) {
			t.Errorf("%s, created: violations = %v, want %q among them", name, got, want)
		}
		if hasProject(t, f.admin, target) {
			t.Errorf("%s: the refused import created the project", name)
		}
	}
}

func TestTheHistoryOfAnImportedExperimentMatchesItsStatus(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	for name, c := range map[string]struct {
		key  string
		edit func(e map[string]any)
		want string
	}{
		"a running experiment without started_at": {"b_running", func(e map[string]any) { e["started_at"] = nil }, "/started_at required"},
		"a stopped experiment without a decision": {"c_stopped", func(e map[string]any) { e["decision"] = nil }, "/decision required"},
		"a winner that was not compared": {"c_stopped", func(e map[string]any) {
			e["decision"] = map[string]any{"outcome": "winner", "winner_variation": "huge", "note": ""}
		}, "/decision/winner_variation unknown_variation"},
		"a draft that started": {"a_draft", func(e map[string]any) { e["started_at"] = "2026-01-01T00:00:00Z" }, "/started_at invalid_value"},
	} {
		d := exported(t, f.admin, k)
		c.edit(entry(t, d, "experiments", c.key))
		target := projectKey()
		res := importJSON(t, f.admin, management.ImportNewProjectParams{Key: &target}, d)
		got := violationsOf(t, name, res.StatusCode(), res.Body, "invalid_document")
		want := fmt.Sprintf("/experiments/%d%s", slices.Index(keysOf(d, "experiments"), c.key), c.want)
		if !slices.Contains(got, want) {
			t.Errorf("%s: violations = %v, want %q among them", name, got, want)
		}
	}
}

func TestAStoppedOrArchivedExperimentOutlivesItsFlagAndADraftDoesNot(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key

	// c_stopped and d_archived no longer hold their flag: it may be archived.
	d := exported(t, f.admin, k)
	withoutExperiments(t, d, "a_draft", "b_running")
	withoutRulesServing(d, "b_running")
	entry(t, d, "flags", "checkout_title")["archived"] = true
	target := projectKey()
	res := importJSON(t, f.admin, management.ImportNewProjectParams{Key: &target}, d)
	if res.JSON201 == nil {
		t.Fatalf("import create = %d: %s", res.StatusCode(), res.Body)
	}
	d["project"].(map[string]any)["key"] = target
	assertJSON(t, "export of the copy", exported(t, f.admin, target), asJSON(t, d))
	// The rule serving d_archived, which an archived experiment leaves out of
	// the ruleset, is kept.
	if rules := documentConfigurationOf(exported(t, f.admin, target), "checkout_title", "development")["rules"].([]any); len(rules) == 0 {
		t.Error("the rule serving the archived experiment was not imported")
	}

	// A draft still holds it.
	d = exported(t, f.admin, k)
	withoutExperiments(t, d, "b_running", "c_stopped")
	withoutRulesServing(d, "b_running")
	entry(t, d, "flags", "checkout_title")["archived"] = true
	res = importJSON(t, f.admin, management.ImportNewProjectParams{Key: ptr(projectKey())}, d)
	got := violationsOf(t, "a draft on an archived flag", res.StatusCode(), res.Body, "invalid_document")
	if !slices.Contains(got, "/experiments/0/flag flag_archived") {
		t.Errorf("violations = %v, want /experiments/0/flag flag_archived", got)
	}
}

func TestAnExperimentTheDocumentAddsToAProjectIsCreatedAsWrittenAndItsRuleIsServedFromThen(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	other := projectKey()
	srcDoc := exported(t, f.admin, k)
	// Copy only the flag and the running experiment into another project that
	// already has the flag, as a merge into a project that was set up apart.
	res := importJSON(t, f.admin, management.ImportNewProjectParams{Key: &other}, mutate(srcDoc, func(d map[string]any) {
		withoutExperiments(t, d, "a_draft", "b_running", "c_stopped", "d_archived")
		withoutRulesServing(d, "b_running")
		withoutRulesServing(d, "c_stopped")
		withoutRulesServing(d, "d_archived")
	}))
	if res.JSON201 == nil {
		t.Fatalf("import create = %d: %s", res.StatusCode(), res.Body)
	}
	p := do(f.admin.GetProjectWithResponse(t.Context(), other))
	if p.JSON200 == nil {
		t.Fatalf("GET project = %d", p.StatusCode())
	}
	before := rulesetVersions(t, *p.JSON200)

	r := mustMerge(t, f.admin, other, srcDoc)
	assertJSON(t, "experiments created", r.Changes.Experiments.Created, `["a_draft", "b_running", "c_stopped", "d_archived"]`)
	assertJSON(t, "the copy after the merge", exported(t, f.admin, other)["experiments"], asJSON(t, srcDoc["experiments"]))
	if got := bumped(t, "merge", before, rulesetVersions(t, *p.JSON200)); !slices.Equal(got, []string{"development", "staging"}) {
		t.Errorf("rulesets moved on = %v, want development and staging, where the rules are", got)
	}
	for _, unit := range []string{"unit-1", "unit-2", "unit-3", "unit-4", "unit-5", "unit-6"} {
		a, b := assignment(t, f.admin, k, "b_running", unit), assignment(t, f.admin, other, "b_running", unit)
		assertJSON(t, unit, b.JSON200, asJSON(t, a.JSON200))
	}
}

func TestOwnersOfExperimentsAreSettledAsOwnersOfFlagsAre(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	d := exported(t, f.admin, k)
	existing := entry(t, d, "experiments", "b_running")
	owner := existing["owner"].(string)
	existing["owner"] = "stranger@example.com"
	withoutExperiments(t, d, "a_draft", "c_stopped", "d_archived")
	d["experiments"] = append(d["experiments"].([]any), mutate(map[string]any{"e": entry(t, exported(t, f.admin, k), "experiments", "a_draft")},
		func(m map[string]any) {
			e := m["e"].(map[string]any)
			e["key"], e["owner"], e["inclusion_salt"], e["variation_salt"] = "e_new", "ghost@example.com", "", ""
		})["e"])

	r := mustMerge(t, f.admin, k, d)
	var got []string
	for _, w := range r.Warnings {
		got = append(got, w.Code+" "+w.Pointer)
	}
	slices.Sort(got)
	want := []string{"owner_replaced /experiments/0/owner", "owner_replaced /experiments/1/owner"}
	if !slices.Equal(got, want) {
		t.Errorf("warnings = %v, want %v", got, want)
	}
	after := exported(t, f.admin, k)
	if entry(t, after, "experiments", "b_running")["owner"] != owner || entry(t, after, "experiments", "e_new")["owner"] != owner {
		t.Errorf("owners = %v and %v, want the existing owner kept and the importer owning the new one",
			entry(t, after, "experiments", "b_running")["owner"], entry(t, after, "experiments", "e_new")["owner"])
	}
	assertJSON(t, "experiment changes", []any{r.Changes.Experiments.Created, r.Changes.Experiments.Updated}, `[["e_new"], []]`)
}

func TestAnOwnerWhoMayOwnTakesTheExperimentsTheDocumentGivesThem(t *testing.T) {
	f := experimentsToTransfer(t)
	k := f.project.Key
	pool := testkit.Pool(t)
	bella := testkit.NewMember(t, pool, testkit.WithEmail("bella@example.com"))
	testkit.Grant(t, pool, projectIDOf(f.project), bella.ID, access.RoleMaintainer)

	d := exported(t, f.admin, k)
	entry(t, d, "experiments", "b_running")["owner"] = "BELLA@example.com"
	entry(t, d, "experiments", "c_stopped")["owner"] = "bella@example.com"
	d["experiments"] = append(d["experiments"].([]any), mutate(map[string]any{"e": entry(t, exported(t, f.admin, k), "experiments", "a_draft")},
		func(m map[string]any) {
			e := m["e"].(map[string]any)
			e["key"], e["owner"], e["inclusion_salt"], e["variation_salt"] = "e_new", "bella@example.com", "", ""
		})["e"])

	r := mustMerge(t, f.admin, k, d)
	assertJSON(t, "warnings", r.Warnings, `[]`)
	assertJSON(t, "experiment changes", []any{r.Changes.Experiments.Created, keysOfUpdates(r.Changes.Experiments.Updated)},
		`[["e_new"], ["b_running", "c_stopped"]]`)
	after := exported(t, f.admin, k)
	for _, key := range []string{"b_running", "c_stopped", "e_new"} {
		if got := entry(t, after, "experiments", key)["owner"]; got != "bella@example.com" {
			t.Errorf("%s owner = %v, want bella@example.com", key, got)
		}
	}
	// Merging the same document again changes nothing, the address matching in
	// whatever case it is written.
	assertNothingChanged(t, mustMerge(t, f.admin, k, d))
}
