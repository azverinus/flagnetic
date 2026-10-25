package management_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

func stopExperiment(t *testing.T, c *management.ClientWithResponses, project, key string, version int64, body management.ExperimentStop) *management.StopExperimentResponse {
	t.Helper()
	return do(c.StopExperimentWithResponse(t.Context(), project, key, &management.StopExperimentParams{IfMatch: ifMatch(version)}, body))
}

func mustStop(t *testing.T, c *management.ClientWithResponses, project, key string, version int64, body management.ExperimentStop) management.Experiment {
	t.Helper()
	res := stopExperiment(t, c, project, key, version, body)
	if res.JSON200 == nil {
		t.Fatalf("stop %s = %d %s", key, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func archiveExperiment(t *testing.T, c *management.ClientWithResponses, project, key string, version int64) *management.ArchiveExperimentResponse {
	t.Helper()
	return do(c.ArchiveExperimentWithResponse(t.Context(), project, key, &management.ArchiveExperimentParams{IfMatch: ifMatch(version)}))
}

func noteDecision(t *testing.T, c *management.ClientWithResponses, project, key string, version int64, note string) *management.SetExperimentDecisionNoteResponse {
	t.Helper()
	return do(c.SetExperimentDecisionNoteWithResponse(t.Context(), project, key,
		&management.SetExperimentDecisionNoteParams{IfMatch: ifMatch(version)}, management.ExperimentDecisionNote{Note: note}))
}

func winner(variation string) management.ExperimentStop {
	return management.ExperimentStop{Outcome: management.ExperimentOutcomeWinner, WinnerVariation: ptr(variation)}
}

var removed = management.ExperimentStop{Outcome: management.ExperimentOutcomeRemoved}

// runningExperiment creates title_test, held in staging by a rule of its
// own and in development by a rule on an attribute, and starts it.
func runningExperiment(t *testing.T, f experimentFixture) management.Experiment {
	t.Helper()
	createExperiment(t, f.admin, f.project.Key, startableExperiment("title_test"))
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, holding("title_test"))
	dev := holding("title_test")
	(*dev.Rules)[0].Description = ptr("Linux first")
	(*dev.Rules)[0].Conditions = conditions(attr("os", "eq", `"linux"`))
	mustPut(t, f.admin, f.project.Key, "checkout_title", "development", 1, dev)
	return mustStart(t, f.admin, f.project.Key, "title_test", 1)
}

func TestStoppingWithAWinnerServesItInPlaceOfEveryRuleServingTheExperiment(t *testing.T) {
	f := startExperiments(t)
	e := runningExperiment(t, f)
	devBefore := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "development")
	before := rulesetVersions(t, f.project)

	res := stopExperiment(t, f.admin, f.project.Key, "title_test", e.Version,
		management.ExperimentStop{Outcome: management.ExperimentOutcomeWinner, WinnerVariation: ptr("b"), Note: ptr(" Short titles won. ")})
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"3"` {
		t.Fatalf("stop = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	stopped := *res.JSON200
	if stopped.Status != management.ExperimentStatusStopped || stopped.StoppedAt == nil || stopped.Decision == nil ||
		stopped.Decision.Outcome != management.ExperimentOutcomeWinner || valueOr(stopped.Decision.WinnerVariation) != "b" ||
		stopped.Decision.Note != "Short titles won." {
		t.Errorf("stopped = %+v, decision %+v", stopped, stopped.Decision)
	}
	if got := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, "title_test")); got.JSON200 == nil ||
		!reflect.DeepEqual(got.JSON200.Decision, stopped.Decision) {
		t.Errorf("GET after the stop = %s", got.Body)
	}

	after := rulesetVersions(t, f.project)
	for env, moved := range map[string]bool{"staging": true, "development": true, "production": false} {
		if (after[env] != before[env]) != moved {
			t.Errorf("%s: ruleset version %d -> %d, want moved %v", env, before[env], after[env], moved)
		}
	}
	rules := rulesOfFlag(t, f.admin, f.project.Key, "staging", "checkout_title", after["staging"])
	if len(rules) != 1 || rules[0].Serve.Kind != eval.ServeVariation || rules[0].Serve.Variation != "b" {
		t.Errorf("staging rules = %+v, want the winner served", rules)
	}
	dev := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "development")
	r, was := dev.Rules[0], devBefore.Rules[0]
	if len(dev.Rules) != 1 || r.Id != was.Id || !reflect.DeepEqual(r.Conditions, was.Conditions) || r.Description != "Linux first" ||
		r.Serve.Kind != "variation" || valueOr(r.Serve.Variation) != "b" || dev.Version != devBefore.Version+1 {
		t.Errorf("development = %+v, want the rule serving b with its identifier, description and conditions", dev)
	}

	// Every unit the rule matches gets the winner now, with no experiment.
	excluded := unitWithBucket(t, e.InclusionSalt, func(b int) bool { return b >= 7000 })
	if out := evaluateUnit(t, f.admin, f.project.Key, "user", excluded); valueOr(out.Variation) != "b" || out.Experiment != nil {
		t.Errorf("a unit once outside the allocation: %+v", out)
	}

	// Each configuration rewritten is a configuration write of its own.
	flag := getFlag(t, f.admin, f.project.Key, "checkout_title")
	entry, values := lastConfigurationEntry(t, f.admin, f.project.Key, flag)
	if entry.Action != "flag.configuration_updated" || values["experiment_stopped"] != "title_test" || values["version"] != float64(dev.Version) {
		t.Errorf("last configuration entry = %s %v", entry.Action, values)
	}
	n := 0
	for _, e := range configurationEntries(t, f.admin, f.project.Key, flag, management.ListProjectAuditEntriesParams{}) {
		if field(e.After, "experiment_stopped") == "title_test" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("configuration entries of the stop = %d, want one per holding environment", n)
	}
	entries := auditOf(t, f.srv, "experiment.stopped", e.Id.String())
	if len(entries) != 1 || field(entries[0].Before, "status") != "running" || field(entries[0].After, "status") != "stopped" ||
		field(field(entries[0].After, "decision"), "winner_variation") != "b" {
		t.Errorf("experiment.stopped entries = %+v", entries)
	}
}

func TestStoppingWithRemovalDeletesTheRulesAndTheFlagFallsThrough(t *testing.T) {
	f := startExperiments(t)
	e := runningExperiment(t, f)
	put := holding("title_test")
	*put.Rules = append(*put.Rules, management.RuleInput{Serve: serves("a")})
	c := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "staging")
	put.Rules = &[]management.RuleInput{{Id: &c.Rules[0].Id, Serve: serveExperiment("title_test")}, {Serve: serves("a")}}
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", c.Version, put)

	stopped := mustStop(t, f.admin, f.project.Key, "title_test", e.Version, removed)
	if stopped.Decision == nil || stopped.Decision.Outcome != management.ExperimentOutcomeRemoved || stopped.Decision.WinnerVariation != nil ||
		stopped.Decision.Note != "" {
		t.Errorf("decision = %+v", stopped.Decision)
	}
	staging := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "staging")
	if len(staging.Rules) != 1 || valueOr(staging.Rules[0].Serve.Variation) != "a" {
		t.Errorf("staging rules = %+v, want the next rule alone", staging.Rules)
	}
	included := unitWithBucket(t, e.InclusionSalt, func(b int) bool { return b < 7000 })
	if out := evaluateUnit(t, f.admin, f.project.Key, "user", included); valueOr(out.Variation) != "a" || out.Experiment != nil {
		t.Errorf("a unit once in the experiment: %+v", out)
	}
	if dev := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "development"); len(dev.Rules) != 0 {
		t.Errorf("development rules = %+v, want none", dev.Rules)
	}
}

func TestAStopIsRefusedAWinnerTheExperimentDoesNotCompare(t *testing.T) {
	f := startExperiments(t)
	e := runningExperiment(t, f)
	for what, c := range map[string]struct {
		body    management.ExperimentStop
		pointer string
		code    string
	}{
		"a winner naming none":         {management.ExperimentStop{Outcome: management.ExperimentOutcomeWinner}, "/winner_variation", "required"},
		"c, which the flag has":        {winner("c"), "/winner_variation", "unknown_variation"},
		"a variation the flag lacks":   {winner("z"), "/winner_variation", "unknown_variation"},
		"a removal naming a winner":    {management.ExperimentStop{Outcome: management.ExperimentOutcomeRemoved, WinnerVariation: ptr("a")}, "/winner_variation", "invalid_value"},
		"a note over 2,000 characters": {management.ExperimentStop{Outcome: management.ExperimentOutcomeRemoved, Note: ptr(strings.Repeat("n", 2001))}, "/note", "too_long"},
	} {
		res := stopExperiment(t, f.admin, f.project.Key, "title_test", e.Version, c.body)
		pr := assertProblem(t, what, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_decision")
		if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != c.pointer || (*pr.Errors)[0].Code != c.code {
			t.Errorf("%s: violations = %+v, want %s %s", what, pr.Errors, c.pointer, c.code)
		}
	}
	unknown := stopExperiment(t, f.admin, f.project.Key, "title_test", e.Version, management.ExperimentStop{Outcome: "draw"})
	assertProblem(t, "an unknown outcome", unknown.StatusCode(), unknown.Body, http.StatusBadRequest, "invalid_request")

	got := do(f.admin.GetExperimentWithResponse(t.Context(), f.project.Key, "title_test"))
	if got.JSON200 == nil || got.JSON200.Status != management.ExperimentStatusRunning || got.JSON200.Version != e.Version {
		t.Errorf("after refused stops = %s", got.Body)
	}
	if c := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "staging"); c.Rules[0].Serve.Kind != "experiment" {
		t.Errorf("staging rule = %+v, want the experiment still served", c.Rules[0])
	}
}

func TestOnlyARunningExperimentStopsAndOnlyFromItsCurrentVersion(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, startableExperiment("draft"))
	res := stopExperiment(t, f.admin, f.project.Key, "draft", 1, removed)
	assertProblem(t, "stopping a draft", res.StatusCode(), res.Body, http.StatusConflict, "not_running")

	e := runningExperiment(t, f)
	none := do(f.admin.StopExperimentWithResponse(t.Context(), f.project.Key, "title_test", &management.StopExperimentParams{}, removed))
	assertProblem(t, "a stop without If-Match", none.StatusCode(), none.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := stopExperiment(t, f.admin, f.project.Key, "title_test", e.Version-1, removed)
	assertProblem(t, "a stop at a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")

	stopped := mustStop(t, f.admin, f.project.Key, "title_test", e.Version, removed)
	again := stopExperiment(t, f.admin, f.project.Key, "title_test", stopped.Version, winner("a"))
	assertProblem(t, "stopping a stopped experiment", again.StatusCode(), again.Body, http.StatusConflict, "not_running")
	unknown := stopExperiment(t, f.admin, f.project.Key, "missing", 1, removed)
	assertProblem(t, "stopping an unknown experiment", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "not_found")
}

func TestAStoppedExperimentChangesItsDescriptionAndItsDecisionsNote(t *testing.T) {
	f := startExperiments(t)
	e := runningExperiment(t, f)
	early := noteDecision(t, f.admin, f.project.Key, "title_test", e.Version, "Too early")
	assertProblem(t, "a note while running", early.StatusCode(), early.Body, http.StatusConflict, "not_stopped")
	s := mustStop(t, f.admin, f.project.Key, "title_test", e.Version, winner("b"))

	patched := patchExperiment(t, f.admin, f.project.Key, "title_test", s.Version, management.ExperimentPatch{
		Name: ptr("Titles, decided"), Hypothesis: ptr("Short titles won."), Tags: &[]string{"done"},
		TrafficAllocation: ptr(s.TrafficAllocation), Variations: &s.Variations,
	})
	if patched.JSON200 == nil || patched.JSON200.Name != "Titles, decided" || patched.JSON200.Version != s.Version+1 {
		t.Fatalf("PATCH of a stopped experiment's description = %d %s", patched.StatusCode(), patched.Body)
	}
	s = *patched.JSON200
	frozen := patchExperiment(t, f.admin, f.project.Key, "title_test", s.Version, management.ExperimentPatch{
		TrafficAllocation: ptr(10000), ExpectedEndAt: ptr(*s.ExpectedEndAt),
	})
	pr := assertProblem(t, "growing a stopped experiment's allocation", frozen.StatusCode(), frozen.Body, http.StatusConflict, "frozen")
	if pr.Errors == nil || len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != "/traffic_allocation" {
		t.Errorf("violations = %+v, want /traffic_allocation alone", pr.Errors)
	}

	noted := noteDecision(t, f.admin, f.project.Key, "title_test", s.Version, "By 3 % of checkouts.")
	if noted.JSON200 == nil || noted.JSON200.Decision.Note != "By 3 % of checkouts." || valueOr(noted.JSON200.Decision.WinnerVariation) != "b" ||
		noted.JSON200.Version != s.Version+1 || noted.HTTPResponse.Header.Get("ETag") != etagOf(s.Version+1) {
		t.Fatalf("PUT decision = %d %s", noted.StatusCode(), noted.Body)
	}
	entries := auditOf(t, f.srv, "experiment.updated", e.Id.String())
	if n := len(entries); n != 2 || field(field(entries[0].After, "decision"), "note") != "By 3 % of checkouts." {
		t.Errorf("experiment.updated entries = %+v, want the description and then the note", entries)
	}
	same := noteDecision(t, f.admin, f.project.Key, "title_test", noted.JSON200.Version, "By 3 % of checkouts.")
	if same.JSON200 == nil || same.JSON200.Version != noted.JSON200.Version {
		t.Errorf("the same note = %d %s, want nothing changed", same.StatusCode(), same.Body)
	}
	stale := noteDecision(t, f.admin, f.project.Key, "title_test", s.Version, "Stale")
	assertProblem(t, "a note at a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")

	del := do(f.admin.DeleteExperimentWithResponse(t.Context(), f.project.Key, "title_test", &management.DeleteExperimentParams{IfMatch: ifMatch(noted.JSON200.Version)}))
	assertProblem(t, "deleting a stopped experiment", del.StatusCode(), del.Body, http.StatusConflict, "not_draft")

	createExperiment(t, f.admin, f.project.Key, validExperiment("draft"))
	draft := noteDecision(t, f.admin, f.project.Key, "draft", 1, "Nothing yet")
	assertProblem(t, "a note on a draft", draft.StatusCode(), draft.Body, http.StatusConflict, "not_stopped")
}

func etagOf(version int64) string { return *ifMatch(version) }

func TestADraftOrStoppedExperimentIsArchivedAndThenRefusesEveryWrite(t *testing.T) {
	f := startExperiments(t)
	e := runningExperiment(t, f)
	running := archiveExperiment(t, f.admin, f.project.Key, "title_test", e.Version)
	assertProblem(t, "archiving a running experiment", running.StatusCode(), running.Body, http.StatusConflict, "running")
	s := mustStop(t, f.admin, f.project.Key, "title_test", e.Version, winner("a"))
	res := archiveExperiment(t, f.admin, f.project.Key, "title_test", s.Version)
	if res.JSON200 == nil || res.JSON200.Status != management.ExperimentStatusArchived || res.JSON200.ArchivedAt == nil ||
		res.JSON200.Version != s.Version+1 || res.JSON200.Decision == nil {
		t.Fatalf("archiving a stopped experiment = %d %s", res.StatusCode(), res.Body)
	}
	if entries := auditOf(t, f.srv, "experiment.archived", e.Id.String()); len(entries) != 1 {
		t.Errorf("experiment.archived entries = %d, want 1", len(entries))
	}

	createExperiment(t, f.admin, f.project.Key, validExperiment("draft"))
	before := rulesetVersions(t, f.project)
	if d := archiveExperiment(t, f.admin, f.project.Key, "draft", 1); d.JSON200 == nil || d.JSON200.Decision != nil {
		t.Fatalf("archiving a draft = %d %s", d.StatusCode(), d.Body)
	}
	if after := rulesetVersions(t, f.project); !mapsEqual(after, before) {
		t.Errorf("archiving moved rulesets %v to %v", before, after)
	}

	for _, key := range []string{"title_test", "draft"} {
		v := int64(2)
		if key == "title_test" {
			v = res.JSON200.Version
		}
		p := f.project.Key
		patch := patchExperiment(t, f.admin, p, key, v, management.ExperimentPatch{Name: ptr("New")})
		del := do(f.admin.DeleteExperimentWithResponse(t.Context(), p, key, &management.DeleteExperimentParams{IfMatch: ifMatch(v)}))
		salts := do(f.admin.RegenerateExperimentSaltsWithResponse(t.Context(), p, key, &management.RegenerateExperimentSaltsParams{IfMatch: ifMatch(v)}))
		start := startExperiment(t, f.admin, p, key, v)
		stop := stopExperiment(t, f.admin, p, key, v, removed)
		note := noteDecision(t, f.admin, p, key, v, "Late")
		again := archiveExperiment(t, f.admin, p, key, v)
		for what, r := range map[string]answer{
			"patch": {patch.StatusCode(), patch.Body}, "delete": {del.StatusCode(), del.Body},
			"salts": {salts.StatusCode(), salts.Body}, "start": {start.StatusCode(), start.Body},
			"stop": {stop.StatusCode(), stop.Body}, "note": {note.StatusCode(), note.Body},
			"archive": {again.StatusCode(), again.Body},
		} {
			assertProblem(t, what+" of archived "+key, r.status, r.body, http.StatusConflict, "archived")
		}
		got := do(f.admin.GetExperimentWithResponse(t.Context(), p, key))
		if got.JSON200 == nil || got.JSON200.Version != v {
			t.Errorf("archived %s after refused writes = %s", key, got.Body)
		}
	}
	if keys := experimentKeys(t, f.admin, f.project.Key); len(keys) != 0 {
		t.Errorf("listed = %v, want the archived hidden", keys)
	}

	// An archived experiment is still the source of a rerun.
	clone := do(f.admin.CloneExperimentWithResponse(t.Context(), f.project.Key, "title_test", management.ExperimentClone{Key: "title_rerun"}))
	if clone.JSON201 == nil || clone.JSON201.Status != management.ExperimentStatusDraft || clone.JSON201.Decision != nil {
		t.Errorf("clone of an archived experiment = %d %s", clone.StatusCode(), clone.Body)
	}
}

// answer is the status and body of a response.
type answer struct {
	status int
	body   []byte
}

func TestStoppingAnExperimentLetsItsFlagBeArchivedButKeepsItsVariations(t *testing.T) {
	f := startExperiments(t)
	e := runningExperiment(t, f)
	flag := getFlag(t, f.admin, f.project.Key, "checkout_title")
	if res := archive(t, f.admin, f.project.Key, "checkout_title", flag.Version); res.StatusCode() != http.StatusConflict {
		t.Fatalf("archiving the flag of a running experiment = %d %s", res.StatusCode(), res.Body)
	}
	mustStop(t, f.admin, f.project.Key, "title_test", e.Version, removed)

	// The decision is about a and b, so neither leaves the flag, though no
	// configuration serves b any more.
	listed := updateFlag(t, f.admin, f.project.Key, "checkout_title", flag.Version, management.FlagPatch{Variations: variations("a", `"A"`, "c", `"C"`)})
	if got := referencesOf(t, "removing a stopped experiment's variation", listed.StatusCode(), listed.Body); !slices.Equal(got, []string{"title_test "}) {
		t.Errorf("variation references = %v, want the experiment", got)
	}
	if res := archive(t, f.admin, f.project.Key, "checkout_title", flag.Version); res.JSON200 == nil {
		t.Errorf("archiving the flag of a stopped experiment = %d %s", res.StatusCode(), res.Body)
	}
}

func TestCloningAStoppedExperimentMakesADraftWithNewSaltsAndNoDecision(t *testing.T) {
	f := startExperiments(t)
	e := runningExperiment(t, f)
	mustStop(t, f.admin, f.project.Key, "title_test", e.Version, winner("b"))
	res := do(f.admin.CloneExperimentWithResponse(t.Context(), f.project.Key, "title_test", management.ExperimentClone{Key: "title_rerun"}))
	if res.JSON201 == nil {
		t.Fatalf("clone = %d %s", res.StatusCode(), res.Body)
	}
	c := *res.JSON201
	if c.Status != management.ExperimentStatusDraft || c.Decision != nil || c.StartedAt != nil || c.StoppedAt != nil ||
		c.InclusionSalt == e.InclusionSalt || c.VariationSalt == e.VariationSalt || !slices.Equal(c.Variations, e.Variations) {
		t.Errorf("clone = %+v", c)
	}
}

func TestARevertOrCopyIsRefusedWhenItWouldChangeWhichRulesServeAnExperiment(t *testing.T) {
	f := startExperiments(t)
	e := runningExperiment(t, f)
	p := f.project.Key

	// Copying staging's experiment rule into production would put the
	// experiment there.
	copied := copyFrom(t, f.admin, p, "checkout_title", "production", ifMatch(1), "staging", nil)
	assertProblem(t, "copying an experiment rule in", copied.StatusCode(), copied.Body, http.StatusConflict, "experiment_rules_changed")
	// Copying production over staging would take it out.
	stagingVersion := getConfiguration(t, f.admin, p, "checkout_title", "staging").Version
	out := copyFrom(t, f.admin, p, "checkout_title", "staging", ifMatch(stagingVersion), "production", nil)
	assertProblem(t, "copying an experiment rule out", out.StatusCode(), out.Body, http.StatusConflict, "experiment_rules_changed")
	// Reverting staging to before its experiment rule would starve it.
	starved := revert(t, f.admin, p, "checkout_title", "staging", ifMatch(stagingVersion), 1)
	assertProblem(t, "reverting across the rule's arrival", starved.StatusCode(), starved.Body, http.StatusConflict, "experiment_rules_changed")

	// Reverting across the stop would serve an experiment that no longer
	// runs in place of the decision.
	mustStop(t, f.admin, p, "title_test", e.Version, winner("b"))
	decided := getConfiguration(t, f.admin, p, "checkout_title", "staging")
	resurrect := revert(t, f.admin, p, "checkout_title", "staging", ifMatch(decided.Version), decided.Version-1)
	assertProblem(t, "reverting across the stop", resurrect.StatusCode(), resurrect.Body, http.StatusConflict, "experiment_rules_changed")
	if now := getConfiguration(t, f.admin, p, "checkout_title", "staging"); now.Version != decided.Version {
		t.Errorf("staging after refusals = %+v", now)
	}

	// With no experiment rule on either side, a copy goes through.
	if res := copyFrom(t, f.admin, p, "checkout_title", "production", ifMatch(1), "staging", nil); res.JSON200 == nil {
		t.Errorf("copying the decided staging = %d %s", res.StatusCode(), res.Body)
	}
}
