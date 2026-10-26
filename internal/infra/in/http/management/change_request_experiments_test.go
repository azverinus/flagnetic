package management_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// experimentRequestFixture is a project whose draft experiment checkout_v2
// on checkout_title is held by a rule in staging and in production, which is
// protected, and not in development; with a developer to propose and a
// maintainer to decide.
type experimentRequestFixture struct {
	srv        *testkit.Server
	admin      *management.ClientWithResponses
	project    management.Project
	developer  *testkit.Browser
	maintainer *testkit.Browser
	experiment management.Experiment
}

func heldInProduction(t *testing.T, opts ...testkit.ServerOption) experimentRequestFixture {
	t.Helper()
	srv := testkit.StartServer(t, opts...)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "checkout_title")
	e := createExperiment(t, admin, p.Key, startableExperiment("checkout_v2"))
	for _, env := range []string{"staging", "production"} {
		mustPut(t, admin, p.Key, "checkout_title", env, 1, holding("checkout_v2"))
	}
	mustPut(t, admin, p.Key, "checkout_title", "development", 1, holding())
	setProtected(t, admin, p.Key, "production", true)
	developer, _ := grantedSession(t, srv, p, access.RoleDeveloper)
	maintainer, _ := grantedSession(t, srv, p, access.RoleMaintainer)
	return experimentRequestFixture{srv: srv, admin: admin, project: p, developer: developer, maintainer: maintainer, experiment: e}
}

// runningInProduction is heldInProduction with the experiment started
// before production was protected.
func runningInProduction(t *testing.T, opts ...testkit.ServerOption) experimentRequestFixture {
	t.Helper()
	f := heldInProduction(t, opts...)
	setProtected(t, f.admin, f.project.Key, "production", false)
	f.experiment = mustStart(t, f.admin, f.project.Key, "checkout_v2", f.experiment.Version)
	setProtected(t, f.admin, f.project.Key, "production", true)
	return f
}

func transitionInput(tr management.ExperimentTransition) management.ProposedInput {
	var p management.ProposedInput
	if err := p.FromExperimentTransition(tr); err != nil {
		panic(err)
	}
	return p
}

// experimentRequest is a change request opened for review proposing tr on
// checkout_v2.
func experimentRequest(tr management.ExperimentTransition) management.ChangeRequestCreate {
	return management.ChangeRequestCreate{
		Kind: management.ChangeRequestCreateKindExperiment, Experiment: ptr("checkout_v2"), Title: "Move checkout_v2",
		Proposed: ptr(transitionInput(tr)), Open: ptr(true),
	}
}

var (
	startTransition = management.ExperimentTransition{Action: management.ExperimentTransitionActionStart}
	stopWithLong    = management.ExperimentTransition{Action: management.ExperimentTransitionActionStop,
		Decision: &management.ExperimentStop{Outcome: management.ExperimentOutcomeWinner, WinnerVariation: ptr("b"), Note: ptr("b won.")}}
)

func growTo(allocation int) management.ExperimentTransition {
	return management.ExperimentTransition{
		Action: management.ExperimentTransitionActionSetTrafficAllocation, TrafficAllocation: ptr(allocation),
	}
}

func getExperiment(t *testing.T, c *management.ClientWithResponses, project, key string) management.Experiment {
	t.Helper()
	res := do(c.GetExperimentWithResponse(t.Context(), project, key))
	if res.JSON200 == nil {
		t.Fatalf("GET experiment %s = %d: %s", key, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// entryOf returns the entry of action among entries.
func entryOf(t *testing.T, entries []management.AuditEntry, action string) management.AuditEntry {
	t.Helper()
	for _, e := range entries {
		if e.Action == action {
			return e
		}
	}
	t.Fatalf("no %s among %d entries", action, len(entries))
	return management.AuditEntry{}
}

// Starting an experiment production holds is refused directly, since
// production would serve it. A change request starts it: its author may not
// approve it, and applying it runs the experiment, moves on staging's and
// production's rulesets and no other, and records the start carrying the
// change request.
func TestAnExperimentChangeRequestStartsAnExperimentProductionHolds(t *testing.T) {
	f := heldInProduction(t)
	key := f.project.Key
	owner, _ := grantedSession(t, f.srv, f.project, access.RoleOwner)
	versions := rulesetVersions(t, f.project)
	direct := startExperiment(t, f.maintainer.ClientWithResponses, key, "checkout_v2", f.experiment.Version)
	refusedUnchanged(t, "starting directly", f.project, versions, direct.StatusCode(), direct.Body)
	if got := getExperiment(t, f.admin, key, "checkout_v2"); got.Status != management.ExperimentStatusDraft {
		t.Fatalf("after the refused start: %s", got.Status)
	}

	cr := createChangeRequest(t, f.maintainer.ClientWithResponses, key, experimentRequest(startTransition))
	if cr.Kind != management.ChangeRequestKindExperiment || valueOr(cr.Experiment) != "checkout_v2" ||
		valueOr(cr.Flag) != "checkout_title" || cr.Environment != nil || cr.Segment != nil ||
		cr.State != management.ChangeRequestStateOpen || cr.BaseVersion != f.experiment.Version {
		t.Errorf("created = %+v", cr)
	}
	if proposed, err := cr.Proposed.AsProposedExperimentTransition(); err != nil ||
		proposed.Action != management.ExperimentTransitionActionStart || proposed.Decision != nil || proposed.TrafficAllocation != nil {
		t.Errorf("proposed = %+v, %v", proposed, err)
	}
	if got := patchOf(cr.Diff); !slices.Equal(got, []string{`replace /status="running"`}) {
		t.Errorf("diff = %v", got)
	}

	res := approveChangeRequest(t, f.maintainer.ClientWithResponses, key, cr, nil)
	assertProblem(t, "the author approving", res.StatusCode(), res.Body, http.StatusForbidden, "author_cannot_approve")
	approved := mustApprove(t, owner.ClientWithResponses, key, cr)

	applied := mustApply(t, f.maintainer.ClientWithResponses, key, approved)
	if applied.State != management.ChangeRequestStateApplied || applied.AppliedBy == nil ||
		applied.AppliedBy.Kind != management.ChangeRequestApplierKindMember {
		t.Errorf("applied = %+v", applied)
	}
	if got := getExperiment(t, f.admin, key, "checkout_v2"); got.Status != management.ExperimentStatusRunning ||
		got.Version != f.experiment.Version+1 {
		t.Errorf("experiment = %s at %d, want running at %d", got.Status, got.Version, f.experiment.Version+1)
	}
	after := rulesetVersions(t, f.project)
	for env, moved := range map[string]bool{"staging": true, "production": true, "development": false} {
		if (after[env] != versions[env]) != moved {
			t.Errorf("%s: ruleset version %d -> %d, want moved %v", env, versions[env], after[env], moved)
		}
	}
	rules := rulesOfFlag(t, f.admin, key, "production", "checkout_title", after["production"])
	if len(rules) != 1 || rules[0].Serve.Kind != eval.ServeExperiment || rules[0].Serve.TrafficAllocation != 7000 {
		t.Errorf("production rules = %+v, want the running experiment", rules)
	}

	entries := applicationEntries(t, f.srv, cr)
	if len(entries) != 2 {
		t.Fatalf("entries of the application = %d, want the start and the application", len(entries))
	}
	started := entryOf(t, entries, "experiment.started")
	if started.Entity.Key != "checkout_v2" || started.Actor.Name != nameOf(t, f.maintainer) {
		t.Errorf("experiment.started = %+v by %+v", started.Entity, started.Actor)
	}
	entryOf(t, entries, "change_request.applied")
}

// Stopping with a winner an experiment production holds takes a change
// request whose diff shows each rule the stop rewrites; applying it stops
// the experiment and rewrites the rule in both holding environments, each
// a configuration write carrying the change request.
func TestAnExperimentChangeRequestStopsWithAWinnerAndRewritesEveryHoldingRule(t *testing.T) {
	f := runningInProduction(t)
	key := f.project.Key
	versions := rulesetVersions(t, f.project)
	direct := stopExperiment(t, f.admin, key, "checkout_v2", f.experiment.Version, *stopWithLong.Decision)
	refusedUnchanged(t, "stopping directly", f.project, versions, direct.StatusCode(), direct.Body)

	cr := createChangeRequest(t, f.developer.ClientWithResponses, key, experimentRequest(stopWithLong))
	if cr.BaseVersion != f.experiment.Version {
		t.Errorf("base_version = %d, want %d", cr.BaseVersion, f.experiment.Version)
	}
	paths := patchOf(cr.Diff)
	for _, want := range []string{`replace /status="stopped"`, `add /decision={"note":"b won.","outcome":"winner","winner_variation":"b"}`,
		`replace /rules/production/0/serve/kind="variation"`, `add /rules/staging/0/serve/variation="b"`} {
		if !slices.Contains(paths, want) {
			t.Errorf("diff %v lacks %s", paths, want)
		}
	}
	if slices.ContainsFunc(paths, func(p string) bool { return strings.Contains(p, "/rules/development") }) {
		t.Errorf("diff %v touches development, which holds no rule", paths)
	}
	if proposed, err := cr.Proposed.AsProposedExperimentTransition(); err != nil || proposed.Decision == nil ||
		valueOr(proposed.Decision.WinnerVariation) != "b" || proposed.Decision.Outcome != management.ExperimentOutcomeWinner {
		t.Errorf("proposed = %+v, %v", proposed, err)
	}

	approved := mustApprove(t, f.maintainer.ClientWithResponses, key, cr)
	configs := map[string]int64{}
	for _, env := range []string{"staging", "production", "development"} {
		configs[env] = getConfiguration(t, f.admin, key, "checkout_title", env).Version
	}
	mustApply(t, f.developer.ClientWithResponses, key, approved)

	if got := getExperiment(t, f.admin, key, "checkout_v2"); got.Status != management.ExperimentStatusStopped ||
		got.Decision == nil || valueOr(got.Decision.WinnerVariation) != "b" {
		t.Errorf("experiment = %+v", got)
	}
	after := rulesetVersions(t, f.project)
	for _, env := range []string{"staging", "production"} {
		c := getConfiguration(t, f.admin, key, "checkout_title", env)
		if c.Version != configs[env]+1 || len(c.Rules) != 1 || c.Rules[0].Serve.Kind != "variation" || valueOr(c.Rules[0].Serve.Variation) != "b" {
			t.Errorf("%s = %+v, want its rule serving b at version %d", env, c, configs[env]+1)
		}
		if after[env] == versions[env] {
			t.Errorf("%s: ruleset version %d did not move", env, versions[env])
		}
	}
	if c := getConfiguration(t, f.admin, key, "checkout_title", "development"); c.Version != configs["development"] ||
		after["development"] != versions["development"] {
		t.Errorf("development moved: configuration %d, ruleset %d -> %d", c.Version, versions["development"], after["development"])
	}
	entries := applicationEntries(t, f.srv, cr)
	entryOf(t, entries, "experiment.stopped")
	rewritten := 0
	for _, e := range entries {
		if e.Action == "flag.configuration_updated" {
			rewritten++
		}
	}
	if rewritten != 2 {
		t.Errorf("configuration entries of the application = %d, want one per holding environment", rewritten)
	}
}

// Growing a running experiment's traffic allocation production holds takes
// a change request; a decrease is refused when it is proposed, before
// anyone reviews it.
func TestAnExperimentChangeRequestGrowsTrafficAllocationAndRefusesADecrease(t *testing.T) {
	f := runningInProduction(t)
	key := f.project.Key
	versions := rulesetVersions(t, f.project)
	direct := patchExperiment(t, f.admin, key, "checkout_v2", f.experiment.Version,
		management.ExperimentPatch{TrafficAllocation: ptr(9000)})
	refusedUnchanged(t, "growing directly", f.project, versions, direct.StatusCode(), direct.Body)

	for name, tc := range map[string]struct {
		tr     management.ExperimentTransition
		status int
		code   string
	}{
		"a decrease":     {growTo(5000), http.StatusConflict, "traffic_allocation_decrease"},
		"the same":       {growTo(7000), http.StatusUnprocessableEntity, "invalid_experiment"},
		"no value":       {management.ExperimentTransition{Action: management.ExperimentTransitionActionSetTrafficAllocation}, http.StatusUnprocessableEntity, "invalid_change_request"},
		"a start":        {startTransition, http.StatusConflict, "not_draft"},
		"a stop unnamed": {management.ExperimentTransition{Action: management.ExperimentTransitionActionStop}, http.StatusUnprocessableEntity, "invalid_change_request"},
		"an unknown one": {management.ExperimentTransition{Action: "pause"}, http.StatusUnprocessableEntity, "invalid_change_request"},
		"an unknown winner": {management.ExperimentTransition{Action: management.ExperimentTransitionActionStop,
			Decision: &management.ExperimentStop{Outcome: management.ExperimentOutcomeWinner, WinnerVariation: ptr("z")}},
			http.StatusUnprocessableEntity, "invalid_decision"},
	} {
		res := do(f.developer.CreateChangeRequestWithResponse(t.Context(), key, experimentRequest(tc.tr)))
		pr := assertProblem(t, name, res.StatusCode(), res.Body, tc.status, tc.code)
		if tc.code == "invalid_decision" && (pr.Errors == nil || (*pr.Errors)[0].Pointer != "/proposed/decision/winner_variation") {
			t.Errorf("%s: violations = %+v, want one at /proposed/decision/winner_variation", name, pr.Errors)
		}
	}

	cr := createChangeRequest(t, f.developer.ClientWithResponses, key, experimentRequest(growTo(9000)))
	if got := patchOf(cr.Diff); !slices.Equal(got, []string{`replace /traffic_allocation=9000`}) {
		t.Errorf("diff = %v", got)
	}
	mustApply(t, f.developer.ClientWithResponses, key, mustApprove(t, f.maintainer.ClientWithResponses, key, cr))

	if got := getExperiment(t, f.admin, key, "checkout_v2"); got.TrafficAllocation != 9000 {
		t.Errorf("traffic allocation = %d, want 9000", got.TrafficAllocation)
	}
	after := rulesetVersions(t, f.project)
	if after["production"] == versions["production"] {
		t.Errorf("production ruleset version %d did not move", versions["production"])
	}
	if rules := rulesOfFlag(t, f.admin, key, "production", "checkout_title", after["production"]); len(rules) != 1 ||
		rules[0].Serve.TrafficAllocation != 9000 {
		t.Errorf("production rules = %+v, want the allocation grown", rules)
	}
	entryOf(t, applicationEntries(t, f.srv, cr), "experiment.traffic_allocation_changed")
}

// A write to the flag's configuration in an environment holding the
// experiment's rule supersedes its change requests, naming that write; so
// does a write to the experiment itself; a write in an environment holding
// no rule leaves them waiting. A copy proposes the same transition on the
// experiment as it is now.
func TestAnExperimentChangeRequestIsSupersededByAConflictingWrite(t *testing.T) {
	f := heldInProduction(t)
	key := f.project.Key
	byConfiguration := mustApprove(t, f.maintainer.ClientWithResponses, key,
		createChangeRequest(t, f.developer.ClientWithResponses, key, experimentRequest(startTransition)))

	development := getConfiguration(t, f.admin, key, "checkout_title", "development")
	mustPut(t, f.admin, key, "checkout_title", "development", development.Version, management.FlagConfigurationPut{
		On: true, OffVariation: "a", Fallthrough: "b",
	})
	if got := getChangeRequest(t, f.admin, key, byConfiguration); got.State != management.ChangeRequestStateApproved {
		t.Fatalf("after a write in development: %s, want still approved", got.State)
	}
	staging := getConfiguration(t, f.admin, key, "checkout_title", "staging")
	body := holding("checkout_v2")
	body.Fallthrough = "b"
	mustPut(t, f.admin, key, "checkout_title", "staging", staging.Version, body)
	got := getChangeRequest(t, f.admin, key, byConfiguration)
	written, _ := lastConfigurationEntry(t, f.admin, key, getFlag(t, f.admin, key, "checkout_title"))
	if got.State != management.ChangeRequestStateSuperseded || got.SupersededBy == nil || *got.SupersededBy != written.Id {
		t.Fatalf("after a write in staging = %s superseded by %v, want staging's entry %s", got.State, got.SupersededBy, written.Id)
	}

	byExperiment := createChangeRequest(t, f.developer.ClientWithResponses, key, experimentRequest(startTransition))
	if res := patchExperiment(t, f.admin, key, "checkout_v2", f.experiment.Version,
		management.ExperimentPatch{Hypothesis: ptr("The long title raises completed checkouts.")}); res.JSON200 == nil {
		t.Fatalf("edit the experiment = %d: %s", res.StatusCode(), res.Body)
	}
	if got := getChangeRequest(t, f.admin, key, byExperiment); got.State != management.ChangeRequestStateSuperseded || got.SupersededBy == nil {
		t.Errorf("after an edit of the experiment = %s, want superseded", got.State)
	}

	copied := do(f.developer.CopyChangeRequestWithResponse(t.Context(), key, byExperiment.Id))
	if copied.JSON201 == nil {
		t.Fatalf("copy = %d: %s", copied.StatusCode(), copied.Body)
	}
	if c := *copied.JSON201; c.State != management.ChangeRequestStateDraft || valueOr(c.Experiment) != "checkout_v2" ||
		c.BaseVersion != getExperiment(t, f.admin, key, "checkout_v2").Version || len(c.Diff) != 1 {
		t.Errorf("copy = %+v, want a draft on the experiment's version now", c)
	}
}

// An environment that starts holding the experiment's rule after the change
// request was based on it changes what the transition would change without
// writing an environment it was based on: opening a draft finds that and
// asks for a rebase, approving finds it and supersedes the change request.
func TestAnExperimentChangeRequestWhoseHoldersChangedIsFoundAtTheNextStep(t *testing.T) {
	f := heldInProduction(t)
	key := f.project.Key
	open := createChangeRequest(t, f.developer.ClientWithResponses, key, experimentRequest(startTransition))
	body := experimentRequest(startTransition)
	body.Open = ptr(false)
	draft := createChangeRequest(t, f.developer.ClientWithResponses, key, body)

	development := getConfiguration(t, f.admin, key, "checkout_title", "development")
	mustPut(t, f.admin, key, "checkout_title", "development", development.Version, holding("checkout_v2"))
	if got := getChangeRequest(t, f.admin, key, open); got.State != management.ChangeRequestStateOpen {
		t.Fatalf("after development started holding the rule: %s, want still open", got.State)
	}

	res := approveChangeRequest(t, f.maintainer.ClientWithResponses, key, open, nil)
	assertProblem(t, "approving on moved holders", res.StatusCode(), res.Body, http.StatusConflict, "base_moved")
	if got := getChangeRequest(t, f.admin, key, open); got.State != management.ChangeRequestStateSuperseded {
		t.Errorf("after the refused approval: %s, want superseded", got.State)
	}

	opened := openChangeRequest(t, f.developer.ClientWithResponses, key, draft)
	assertProblem(t, "opening on moved holders", opened.StatusCode(), opened.Body, http.StatusConflict, "base_moved")
	rebased := rebaseChangeRequest(t, f.developer.ClientWithResponses, key, draft)
	if rebased.JSON200 == nil {
		t.Fatalf("rebase = %d: %s", rebased.StatusCode(), rebased.Body)
	}
	if opened = openChangeRequest(t, f.developer.ClientWithResponses, key, *rebased.JSON200); opened.JSON200 == nil {
		t.Fatalf("open after the rebase = %d: %s", opened.StatusCode(), opened.Body)
	}
	approved := mustApprove(t, f.maintainer.ClientWithResponses, key, *opened.JSON200)
	mustApply(t, f.developer.ClientWithResponses, key, approved)
	if got := getExperiment(t, f.admin, key, "checkout_v2"); got.Status != management.ExperimentStatusRunning {
		t.Errorf("experiment = %s, want running", got.Status)
	}
}

// Deleting a draft experiment supersedes the change requests proposing to
// start it, naming the deletion, before they go with it.
func TestDeletingAnExperimentSupersedesItsChangeRequests(t *testing.T) {
	f := heldInProduction(t)
	key := f.project.Key
	cr := createChangeRequest(t, f.developer.ClientWithResponses, key, experimentRequest(startTransition))
	setProtected(t, f.admin, key, "production", false)
	for _, env := range []string{"staging", "production"} {
		c := getConfiguration(t, f.admin, key, "checkout_title", env)
		mustPut(t, f.admin, key, "checkout_title", env, c.Version, holding())
	}
	// The configuration writes superseded it already; a fresh one is
	// superseded by the deletion alone.
	cr = createChangeRequest(t, f.developer.ClientWithResponses, key, experimentRequest(startTransition))
	if del := do(f.admin.DeleteExperimentWithResponse(t.Context(), key, "checkout_v2",
		&management.DeleteExperimentParams{IfMatch: ifMatch(f.experiment.Version)})); del.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", del.StatusCode(), del.Body)
	}
	deleted := auditOf(t, f.srv, "experiment.deleted", f.experiment.Id.String())
	superseded := auditOf(t, f.srv, "change_request.superseded", cr.Id.String())
	if len(deleted) != 1 || len(superseded) != 1 || field(superseded[0].After, "superseded_by") != deleted[0].Id.String() {
		t.Errorf("deleted = %+v, superseded = %+v; want the change request superseded by the deletion", deleted, superseded)
	}
	if res := do(f.admin.GetChangeRequestWithResponse(t.Context(), key, cr.Id)); res.StatusCode() != http.StatusNotFound {
		t.Errorf("GET the change request after the deletion = %d, want 404", res.StatusCode())
	}
}

// A scheduled start and a scheduled stop are applied by the scheduler at
// their time as the system, the stop rewriting the rule in every holding
// environment.
func TestTheSchedulerAppliesExperimentTransitions(t *testing.T) {
	c := testkit.Clock()
	f := heldInProduction(t, testkit.WithClock(c))
	key := f.project.Key
	schedule := func(tr management.ExperimentTransition) management.ChangeRequest {
		t.Helper()
		body := experimentRequest(tr)
		at := c.Now().Add(time.Hour)
		body.ApplyAt = &at
		return mustApprove(t, f.maintainer.ClientWithResponses, key, createChangeRequest(t, f.developer.ClientWithResponses, key, body))
	}

	start := schedule(startTransition)
	mustTick(t, f.srv)
	if got := getExperiment(t, f.admin, key, "checkout_v2"); got.Status != management.ExperimentStatusDraft {
		t.Fatalf("before its time the experiment is %s", got.Status)
	}
	c.Advance(time.Hour)
	mustTick(t, f.srv)
	if got := getChangeRequest(t, f.admin, key, start); got.State != management.ChangeRequestStateApplied ||
		got.AppliedBy == nil || got.AppliedBy.Kind != management.ChangeRequestApplierKindSystem {
		t.Fatalf("at its time = %s applied by %+v", got.State, got.AppliedBy)
	}
	started := entryOf(t, applicationEntries(t, f.srv, start), "experiment.started")
	if started.Actor.Kind != management.ActorKindSystem {
		t.Errorf("experiment.started by %+v, want the system", started.Actor)
	}

	stop := schedule(stopWithLong)
	c.Advance(time.Hour)
	mustTick(t, f.srv)
	if got := getChangeRequest(t, f.admin, key, stop); got.State != management.ChangeRequestStateApplied {
		t.Fatalf("the stop at its time = %s", got.State)
	}
	if got := getExperiment(t, f.admin, key, "checkout_v2"); got.Status != management.ExperimentStatusStopped {
		t.Errorf("experiment = %s, want stopped", got.Status)
	}
	for _, env := range []string{"staging", "production"} {
		if cfg := getConfiguration(t, f.admin, key, "checkout_title", env); len(cfg.Rules) != 1 ||
			valueOr(cfg.Rules[0].Serve.Variation) != "b" {
			t.Errorf("%s rules = %+v, want serving b", env, cfg.Rules)
		}
	}
}

// Listing by experiment finds its change requests and no other; a project
// token authors and applies an experiment change request it may not
// approve, and the experiment's entry names the token.
func TestExperimentChangeRequestsAreListedAndAuthoredByAProjectToken(t *testing.T) {
	f := heldInProduction(t)
	key := f.project.Key
	ci := pipeline(t, f.srv, f.project, "maintainer", management.ScopeFlagsWrite)
	other := createChangeRequest(t, f.developer.ClientWithResponses, key, experimentRequest(startTransition))
	res := approveChangeRequest(t, ci, key, other, nil)
	assertProblem(t, "a token approving", res.StatusCode(), res.Body, http.StatusForbidden, "token_cannot_approve")
	configuration := configurationRequest("Elsewhere", proposedOn())
	configuration.Environment = ptr("development")
	createChangeRequest(t, f.developer.ClientWithResponses, key, configuration)

	listed := do(f.admin.ListChangeRequestsWithResponse(t.Context(), key, &management.ListChangeRequestsParams{Experiment: ptr("checkout_v2")}))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 1 || listed.JSON200.Items[0].Id != other.Id {
		t.Errorf("listed by experiment = %d: %s", listed.StatusCode(), listed.Body)
	}
	// The change request names the experiment's flag, so listing by the flag
	// finds it beside the flag's configuration change request.
	byFlag := do(f.admin.ListChangeRequestsWithResponse(t.Context(), key, &management.ListChangeRequestsParams{Flag: ptr("checkout_title")}))
	if byFlag.JSON200 == nil || !slices.ContainsFunc(byFlag.JSON200.Items, func(cr management.ChangeRequest) bool {
		return cr.Id == other.Id && valueOr(cr.Flag) == "checkout_title"
	}) {
		t.Errorf("listed by flag = %d: %s", byFlag.StatusCode(), byFlag.Body)
	}
	cancelChangeRequest(t, f.developer.ClientWithResponses, key, other)

	cr := createChangeRequest(t, ci, key, experimentRequest(startTransition))
	if cr.Author.Kind != management.ChangeRequestAuthorKindToken {
		t.Errorf("author = %+v, want the token", cr.Author)
	}
	applied := mustApply(t, ci, key, mustApprove(t, f.maintainer.ClientWithResponses, key, cr))
	if applied.AppliedBy == nil || applied.AppliedBy.Kind != management.ChangeRequestApplierKindToken {
		t.Errorf("applied by %+v, want the token", applied.AppliedBy)
	}
	if started := entryOf(t, applicationEntries(t, f.srv, cr), "experiment.started"); started.Actor.Kind != management.ActorKindToken {
		t.Errorf("experiment.started by %+v, want the token", started.Actor)
	}
}

// An experiment change request names the experiment alone: its flag is the
// experiment's, and it is applied in every environment holding its rule.
func TestAnExperimentChangeRequestNamesItsExperimentAlone(t *testing.T) {
	f := heldInProduction(t)
	key := f.project.Key
	for name, tc := range map[string]struct {
		mutate  func(*management.ChangeRequestCreate)
		status  int
		code    string
		pointer string
	}{
		"no experiment": {func(b *management.ChangeRequestCreate) { b.Experiment = nil },
			http.StatusUnprocessableEntity, "invalid_change_request", "/experiment"},
		"a flag": {func(b *management.ChangeRequestCreate) { b.Flag = ptr("checkout_title") },
			http.StatusUnprocessableEntity, "invalid_change_request", "/flag"},
		"an environment": {func(b *management.ChangeRequestCreate) { b.Environment = ptr("production") },
			http.StatusUnprocessableEntity, "invalid_change_request", "/environment"},
		"an unknown experiment": {func(b *management.ChangeRequestCreate) { b.Experiment = ptr("nope") },
			http.StatusNotFound, "not_found", ""},
	} {
		body := experimentRequest(startTransition)
		tc.mutate(&body)
		res := do(f.developer.CreateChangeRequestWithResponse(t.Context(), key, body))
		pr := assertProblem(t, name, res.StatusCode(), res.Body, tc.status, tc.code)
		if tc.pointer != "" && (pr.Errors == nil || (*pr.Errors)[0].Pointer != tc.pointer) {
			t.Errorf("%s: violations = %+v, want one at %s", name, pr.Errors, tc.pointer)
		}
	}
	// A configuration or a definition names no experiment.
	body := configurationRequest("With an experiment", proposedOn())
	body.Experiment = ptr("checkout_v2")
	res := do(f.developer.CreateChangeRequestWithResponse(t.Context(), key, body))
	assertProblem(t, "a configuration naming an experiment", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_change_request")
}
