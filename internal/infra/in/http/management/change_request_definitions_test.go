package management_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// definitionFixture is a project whose protected production switches
// checkout on for the members of the segment employees (user u1) and
// serves false to everyone else, with a developer to propose and a
// maintainer to decide.
type definitionFixture struct {
	srv        *testkit.Server
	admin      *management.ClientWithResponses
	project    management.Project
	developer  *testkit.Browser
	maintainer *testkit.Browser
	checkout   management.Flag
}

func startDefinitions(t *testing.T) definitionFixture {
	t.Helper()
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	createSegment(t, admin, p.Key, management.SegmentCreate{Key: "employees", Name: "Employees",
		Conditions: conditions(attr("user", "in", `["u1"]`))})
	checkout := createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Type: management.FlagTypeBoolean, Name: "Checkout"})
	mustPut(t, admin, p.Key, "checkout", "production", 1, management.FlagConfigurationPut{On: true, OffVariation: "false",
		Fallthrough: "false", Rules: &[]management.RuleInput{{Conditions: conditions(inSegment("employees")), Serve: serves("true")}}})
	setProtected(t, admin, p.Key, "production", true)
	developer, _ := grantedSession(t, srv, p, access.RoleDeveloper)
	maintainer, _ := grantedSession(t, srv, p, access.RoleMaintainer)
	return definitionFixture{srv: srv, admin: admin, project: p, developer: developer, maintainer: maintainer, checkout: checkout}
}

// changeRequests returns the fixture as the change request helpers take it,
// the developer proposing.
func (f definitionFixture) changeRequests() changeRequestFixture {
	return changeRequestFixture{srv: f.srv, admin: f.admin, project: f.project, dev: f.developer}
}

func flagDefinitionInput(d management.FlagDefinitionPut) management.ProposedInput {
	var p management.ProposedInput
	if err := p.FromFlagDefinitionPut(d); err != nil {
		panic(err)
	}
	return p
}

func segmentDefinitionInput(d management.SegmentDefinitionPut) management.ProposedInput {
	var p management.ProposedInput
	if err := p.FromSegmentDefinitionPut(d); err != nil {
		panic(err)
	}
	return p
}

// flagDefinitionRequest is a change request opened for review proposing
// flag's definition as d.
func flagDefinitionRequest(flag string, d management.FlagDefinitionPut) management.ChangeRequestCreate {
	return management.ChangeRequestCreate{
		Kind: management.ChangeRequestCreateKindDefinition, Flag: ptr(flag), Title: "Change " + flag,
		Proposed: ptr(flagDefinitionInput(d)), Open: ptr(true),
	}
}

// segmentDefinitionRequest is a change request opened for review proposing
// that segment's members are the users users, a JSON list.
func segmentDefinitionRequest(segment, users string) management.ChangeRequestCreate {
	return management.ChangeRequestCreate{
		Kind: management.ChangeRequestCreateKindDefinition, Segment: ptr(segment), Title: "Change " + segment,
		Proposed: ptr(segmentDefinitionInput(management.SegmentDefinitionPut{
			Conditions: []management.Condition{attr("user", "in", users)},
		})), Open: ptr(true),
	}
}

// booleanDefinition is a boolean flag's definition with automatic exposure.
var booleanDefinition = management.FlagDefinitionPut{AutomaticExposure: true, Variations: *variations("true", "true", "false", "false")}

func evaluateCheckout(t *testing.T, f definitionFixture, user string) string {
	t.Helper()
	got := mustEvaluate(t, f.admin, f.project.Key, "production", management.EvaluationRequest{
		Context: contextOf(map[string]string{"user": user}, ""), Flags: &[]string{"checkout"},
	}).Flags["checkout"]
	return valueOr(got.Variation)
}

func getSegment(t *testing.T, c *management.ClientWithResponses, project, key string) management.Segment {
	t.Helper()
	res := do(c.GetSegmentWithResponse(t.Context(), project, key))
	if res.JSON200 == nil {
		t.Fatalf("GET segment %s = %d: %s", key, res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// A variation's value is part of every environment's ruleset, so with
// production protected it is not edited directly, while the flag's name
// is. Through a definition change request it is: the diff names the value,
// the author may not approve it, and applying it moves the flag's version
// and every environment's ruleset, records the flag's entry carrying
// the change request, and production serves the new value.
func TestADefinitionChangeRequestChangesAVariationValueInProduction(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProjectAsCreated(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "banner")
	author, authorMember := grantedSession(t, srv, p, access.RoleMaintainer)
	owner, _ := grantedSession(t, srv, p, access.RoleOwner)
	welcome := *variations("a", `"Welcome"`, "b", `"B"`, "c", `"C"`)

	direct := updateFlag(t, author.ClientWithResponses, p.Key, "banner", 1, management.FlagPatch{Variations: &welcome})
	assertProblem(t, "editing a variation's value directly", direct.StatusCode(), direct.Body, http.StatusForbidden,
		"change_request_required")
	renamed := updateFlag(t, author.ClientWithResponses, p.Key, "banner", 1, management.FlagPatch{Name: ptr("Banner text")})
	if renamed.JSON200 == nil {
		t.Fatalf("renaming the flag = %d: %s", renamed.StatusCode(), renamed.Body)
	}

	cr := createChangeRequest(t, author.ClientWithResponses, p.Key,
		flagDefinitionRequest("banner", management.FlagDefinitionPut{Variations: welcome}))
	if cr.Kind != management.ChangeRequestKindDefinition || valueOr(cr.Flag) != "banner" || cr.Segment != nil ||
		cr.Environment != nil || cr.State != management.ChangeRequestStateOpen || cr.BaseVersion != renamed.JSON200.Version {
		t.Errorf("created = %s, want an open definition of banner, no environment, on version %d", raw(cr), renamed.JSON200.Version)
	}
	if got := raw(cr.Diff); got != `[{"op":"replace","path":"/variations/0/value","value":"Welcome"}]` {
		t.Errorf("diff = %s", got)
	}
	if got := raw(proposedAs[management.ProposedFlagDefinition](t, cr)); got != `{"automatic_exposure":false,"variations":[`+
		`{"description":"","key":"a","name":"a","value":"Welcome"},{"description":"","key":"b","name":"b","value":"B"},`+
		`{"description":"","key":"c","name":"c","value":"C"}]}` {
		t.Errorf("proposed = %s", got)
	}

	own := approveChangeRequest(t, author.ClientWithResponses, p.Key, cr, nil)
	assertProblem(t, "the author approving", own.StatusCode(), own.Body, http.StatusForbidden, "author_cannot_approve")
	approved := mustApprove(t, owner.ClientWithResponses, p.Key, cr)
	before := rulesetVersions(t, p)

	applied := mustApply(t, author.ClientWithResponses, p.Key, approved)
	if applied.State != management.ChangeRequestStateApplied || applied.AppliedBy == nil ||
		*applied.AppliedBy.Id != authorMember.ID.Bytes() {
		t.Errorf("applied = %s, want applied by the author", raw(applied))
	}
	f := getFlag(t, admin, p.Key, "banner")
	if f.Version != cr.BaseVersion+1 || string(f.Variations[0].Value) != `"Welcome"` || f.Name != "Banner text" {
		t.Errorf("flag = %s, want version %d with a = Welcome and its name kept", raw(f), cr.BaseVersion+1)
	}
	if got := bumped(t, "the application", before, rulesetVersions(t, p)); !slices.Equal(got, everyEnvironment) {
		t.Errorf("rulesets moved = %v, want every environment", got)
	}
	got := mustEvaluate(t, admin, p.Key, "production", management.EvaluationRequest{
		Context: contextOf(map[string]string{"user": "u-1"}, ""), Flags: &[]string{"banner"},
	}).Flags["banner"]
	if got.Value == nil || string(*got.Value) != `"Welcome"` {
		t.Errorf("banner in production = %s, want the off variation's new value", raw(got))
	}

	entries := applicationEntries(t, srv, cr)
	if got := actionsOf(entries); !slices.Equal(got, []string{"change_request.applied", "flag.updated"}) {
		t.Fatalf("entries of the application = %v", got)
	}
	written := entries[1]
	if written.Entity.Key != "banner" || written.EnvironmentId != nil || written.Actor.Name != nameOf(t, author) {
		t.Errorf("flag entry = %s, want banner, no environment, by the author", raw(written))
	}
}

// A segment production names is not edited directly; through a definition
// change request applied by its developer author, production evaluates the
// new conditions and the segment's entry carries the change request.
func TestADefinitionChangeRequestEditsAReferencedSegmentsConditions(t *testing.T) {
	f := startDefinitions(t)
	wider := conditions(attr("user", "in", `["u1","u2"]`))
	segment := getSegment(t, f.admin, f.project.Key, "employees")

	direct := updateSegment(t, f.maintainer.ClientWithResponses, f.project.Key, "employees", segment.Version,
		management.SegmentPatch{Conditions: wider})
	assertProblem(t, "editing the segment's conditions directly", direct.StatusCode(), direct.Body, http.StatusForbidden,
		"change_request_required")
	if got := evaluateCheckout(t, f, "u2"); got != "false" {
		t.Fatalf("u2 before = %s, want false", got)
	}

	cr := createChangeRequest(t, f.developer.ClientWithResponses, f.project.Key, segmentDefinitionRequest("employees", `["u1","u2"]`))
	if valueOr(cr.Segment) != "employees" || cr.Flag != nil || cr.Environment != nil || cr.BaseVersion != segment.Version {
		t.Errorf("created = %s, want the definition of employees on its version %d", raw(cr), segment.Version)
	}
	if got := raw(cr.Diff); got != `[{"op":"add","path":"/conditions/0/value/1","value":"u2"}]` {
		t.Errorf("diff = %s", got)
	}
	approved := mustApprove(t, f.maintainer.ClientWithResponses, f.project.Key, cr)
	before := rulesetVersions(t, f.project)
	mustApply(t, f.developer.ClientWithResponses, f.project.Key, approved)

	if got := bumped(t, "the application", before, rulesetVersions(t, f.project)); !slices.Equal(got, everyEnvironment) {
		t.Errorf("rulesets moved = %v, want every environment", got)
	}
	if s := getSegment(t, f.admin, f.project.Key, "employees"); s.Version != cr.BaseVersion+1 {
		t.Errorf("segment version = %d, want %d", s.Version, cr.BaseVersion+1)
	}
	if got := evaluateCheckout(t, f, "u2"); got != "true" {
		t.Errorf("u2 after = %s, want true", got)
	}
	entries := applicationEntries(t, f.srv, cr)
	if got := actionsOf(entries); !slices.Equal(got, []string{"change_request.applied", "segment.updated"}) {
		t.Fatalf("entries of the application = %v", got)
	}
	if written := entries[1]; written.Entity.Key != "employees" || written.Actor.Name != nameOf(t, f.developer) {
		t.Errorf("segment entry = %s, want employees, by the developer", raw(written))
	}
}

// Switching a segment production names private changes what production's
// ruleset carries for client SDKs, so it too takes a change request.
func TestADefinitionChangeRequestSwitchesASegmentPrivate(t *testing.T) {
	f := startDefinitions(t)
	segment := getSegment(t, f.admin, f.project.Key, "employees")

	direct := updateSegment(t, f.maintainer.ClientWithResponses, f.project.Key, "employees", segment.Version,
		management.SegmentPatch{Private: ptr(true)})
	assertProblem(t, "switching the segment private directly", direct.StatusCode(), direct.Body, http.StatusForbidden,
		"change_request_required")

	body := segmentDefinitionRequest("employees", `["u1"]`)
	body.Proposed = ptr(segmentDefinitionInput(management.SegmentDefinitionPut{Conditions: segment.Conditions, Private: true}))
	cr := createChangeRequest(t, f.developer.ClientWithResponses, f.project.Key, body)
	if got := raw(cr.Diff); got != `[{"op":"replace","path":"/private","value":true}]` {
		t.Errorf("diff = %s", got)
	}
	mustApply(t, f.developer.ClientWithResponses, f.project.Key, mustApprove(t, f.maintainer.ClientWithResponses, f.project.Key, cr))
	if s := getSegment(t, f.admin, f.project.Key, "employees"); !s.Private {
		t.Errorf("segment = %s, want private", raw(s))
	}
}

// Definition edits that change no protected ruleset stay direct: a
// variation's name, a flag's description, and a segment no flag names.
func TestDefinitionEditsThatChangeNoProtectedRulesetStayDirect(t *testing.T) {
	f := startDefinitions(t)
	stringFlag(t, f.admin, f.project.Key, "banner")
	createSegment(t, f.admin, f.project.Key, management.SegmentCreate{Key: "unused", Name: "Unused"})
	c := f.developer.ClientWithResponses
	named := []management.VariationInput{
		{Key: "a", Value: value(`"A"`), Name: ptr("Formal")}, {Key: "b", Value: value(`"B"`)}, {Key: "c", Value: value(`"C"`)},
	}

	for name, call := range map[string]func() (int, []byte){
		"a variation's name": func() (int, []byte) {
			res := updateFlag(t, c, f.project.Key, "banner", 1, management.FlagPatch{Variations: &named})
			return res.StatusCode(), res.Body
		},
		"a flag's description": func() (int, []byte) {
			res := updateFlag(t, c, f.project.Key, "checkout", f.checkout.Version, management.FlagPatch{Description: ptr("The new checkout")})
			return res.StatusCode(), res.Body
		},
		"a segment no flag names": func() (int, []byte) {
			res := updateSegment(t, c, f.project.Key, "unused", 1, management.SegmentPatch{
				Private: ptr(true), Conditions: conditions(attr("user", "in", `["u9"]`))})
			return res.StatusCode(), res.Body
		},
	} {
		if status, body := call(); status != http.StatusOK {
			t.Errorf("%s = %d: %s, want 200", name, status, body)
		}
	}
}

// A definition change request is superseded by a write to the
// configuration of its flag, or of a flag naming its segment, in any
// environment, by that write's entry; a write to a flag naming neither
// supersedes nothing. A flag that named the segment when the change request
// was based on it counts once the write stopped naming it, and the kill
// switch, direct in protected production, supersedes both kinds.
func TestADefinitionChangeRequestIsSupersededByAConfigurationWriteInAnotherEnvironment(t *testing.T) {
	f := startDefinitions(t)
	c := f.developer.ClientWithResponses
	createFlag(t, f.admin, f.project.Key, management.FlagCreate{Key: "quiet", Type: management.FlagTypeBoolean, Name: "Quiet"})
	beta := createFlag(t, f.admin, f.project.Key, management.FlagCreate{Key: "beta", Type: management.FlagTypeBoolean, Name: "Beta"})
	mustPut(t, f.admin, f.project.Key, "beta", "staging", 1, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "false",
		Rules: &[]management.RuleInput{{Conditions: conditions(inSegment("employees")), Serve: serves("true")}}})

	onFlag := mustApprove(t, f.maintainer.ClientWithResponses, f.project.Key,
		createChangeRequest(t, c, f.project.Key, flagDefinitionRequest("checkout", booleanDefinition)))
	onSegment := createChangeRequest(t, c, f.project.Key, segmentDefinitionRequest("employees", `["u1","u5"]`))

	mustPut(t, f.admin, f.project.Key, "quiet", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	for _, cr := range []management.ChangeRequest{onFlag, onSegment} {
		if got := getChangeRequest(t, c, f.project.Key, cr); got.State == management.ChangeRequestStateSuperseded {
			t.Errorf("%s superseded by a write to a flag it does not depend on", cr.Title)
		}
	}

	mustPut(t, f.admin, f.project.Key, "checkout", "staging", 1, management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "true"})
	written := lastEntryOf(t, f.srv, f.checkout.Id)
	assertSupersededBy(t, f.changeRequests(), onFlag, written)
	assertSupersededBy(t, f.changeRequests(), onSegment, written)

	// beta's only reference to the segment goes with the write that
	// supersedes.
	again := createChangeRequest(t, c, f.project.Key, segmentDefinitionRequest("employees", `["u1","u6"]`))
	mustPut(t, f.admin, f.project.Key, "beta", "staging", 2, management.FlagConfigurationPut{OffVariation: "false", Fallthrough: "false"})
	assertSupersededBy(t, f.changeRequests(), again, lastEntryOf(t, f.srv, beta.Id))

	onFlag = createChangeRequest(t, c, f.project.Key, flagDefinitionRequest("checkout", booleanDefinition))
	onSegment = createChangeRequest(t, c, f.project.Key, segmentDefinitionRequest("employees", `["u1","u7"]`))
	if res := kill(t, f.maintainer.ClientWithResponses, f.project.Key, "checkout", "production", ptr("errors")); res.JSON200 == nil {
		t.Fatalf("kill = %d: %s", res.StatusCode(), res.Body)
	}
	killed := lastEntryOf(t, f.srv, f.checkout.Id)
	assertSupersededBy(t, f.changeRequests(), onFlag, killed)
	assertSupersededBy(t, f.changeRequests(), onSegment, killed)
}

// Any write to the entity itself moves a definition's base: a flag's rename
// supersedes by its entry, a draft opens only once rebased, and a copy
// lands on the current version; a segment's description does the same to
// the segment's definition.
func TestADefinitionChangeRequestIsSupersededWhenItsEntityChanges(t *testing.T) {
	f := startDefinitions(t)
	c := f.developer.ClientWithResponses

	open := createChangeRequest(t, c, f.project.Key, flagDefinitionRequest("checkout", booleanDefinition))
	draftBody := flagDefinitionRequest("checkout", booleanDefinition)
	draftBody.Open = nil
	draft := createChangeRequest(t, c, f.project.Key, draftBody)
	onSegment := mustApprove(t, f.maintainer.ClientWithResponses, f.project.Key,
		createChangeRequest(t, c, f.project.Key, segmentDefinitionRequest("employees", `["u1","u5"]`)))

	renamed := updateFlag(t, f.maintainer.ClientWithResponses, f.project.Key, "checkout", f.checkout.Version,
		management.FlagPatch{Name: ptr("Checkout v2")})
	if renamed.JSON200 == nil {
		t.Fatalf("rename = %d: %s", renamed.StatusCode(), renamed.Body)
	}
	assertSupersededBy(t, f.changeRequests(), open, lastEntryOf(t, f.srv, f.checkout.Id))
	if got := getChangeRequest(t, c, f.project.Key, onSegment); got.State != management.ChangeRequestStateApproved {
		t.Errorf("the segment's change request is %s after renaming a flag, want approved", got.State)
	}

	opening := do(c.OpenChangeRequestWithResponse(t.Context(), f.project.Key, draft.Id,
		&management.OpenChangeRequestParams{IfMatch: ifMatch(draft.Version)}))
	assertProblem(t, "opening a draft on a moved flag", opening.StatusCode(), opening.Body, http.StatusConflict, "base_moved")
	rebased := do(c.RebaseChangeRequestWithResponse(t.Context(), f.project.Key, draft.Id,
		&management.RebaseChangeRequestParams{IfMatch: ifMatch(draft.Version)}))
	if rebased.JSON200 == nil || rebased.JSON200.BaseVersion != renamed.JSON200.Version {
		t.Fatalf("rebase = %d: %s, want based on version %d", rebased.StatusCode(), rebased.Body, renamed.JSON200.Version)
	}
	if opened := do(c.OpenChangeRequestWithResponse(t.Context(), f.project.Key, draft.Id,
		&management.OpenChangeRequestParams{IfMatch: ifMatch(rebased.JSON200.Version)})); opened.JSON200 == nil {
		t.Fatalf("open after the rebase = %d: %s", opened.StatusCode(), opened.Body)
	}

	copied := do(c.CopyChangeRequestWithResponse(t.Context(), f.project.Key, open.Id))
	if copied.JSON201 == nil {
		t.Fatalf("copy = %d: %s", copied.StatusCode(), copied.Body)
	}
	if cp := *copied.JSON201; cp.Kind != management.ChangeRequestKindDefinition || valueOr(cp.Flag) != "checkout" ||
		cp.State != management.ChangeRequestStateDraft || cp.BaseVersion != renamed.JSON200.Version ||
		!proposedAs[management.ProposedFlagDefinition](t, cp).AutomaticExposure {
		t.Errorf("copy = %s, want a draft of checkout's definition on version %d", raw(cp), renamed.JSON200.Version)
	}

	segment := getSegment(t, f.admin, f.project.Key, "employees")
	if res := updateSegment(t, f.maintainer.ClientWithResponses, f.project.Key, "employees", segment.Version,
		management.SegmentPatch{Description: ptr("Staff")}); res.JSON200 == nil {
		t.Fatalf("describe the segment = %d: %s", res.StatusCode(), res.Body)
	}
	assertSupersededBy(t, f.changeRequests(), onSegment, lastEntryOf(t, f.srv, segment.Id))
}

// Across kinds on one flag: applying a definition leaves a configuration
// change request on the flag waiting, since no configuration moved;
// applying a configuration change request supersedes the flag's definition
// change requests, since one of its configurations did.
func TestChangeRequestsOfBothKindsOnOneFlag(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProjectAsCreated(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "banner")
	developer, _ := grantedSession(t, srv, p, access.RoleDeveloper)
	maintainer, _ := grantedSession(t, srv, p, access.RoleMaintainer)
	c := developer.ClientWithResponses

	configuration := mustApprove(t, maintainer.ClientWithResponses, p.Key, openedOn(t, c, p.Key, "banner", "production",
		management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b"}))
	friendlier := management.FlagDefinitionPut{Variations: *variations("a", `"Hello"`, "b", `"Hey"`, "c", `"C"`)}
	first := mustApprove(t, maintainer.ClientWithResponses, p.Key,
		createChangeRequest(t, c, p.Key, flagDefinitionRequest("banner", friendlier)))
	mustApply(t, c, p.Key, first)
	if got := getChangeRequest(t, c, p.Key, configuration); got.State != management.ChangeRequestStateApproved {
		t.Fatalf("the configuration's change request once the definition is applied = %s, want approved", got.State)
	}

	friendlier.AutomaticExposure = true
	second := createChangeRequest(t, c, p.Key, flagDefinitionRequest("banner", friendlier))
	mustApply(t, c, p.Key, getChangeRequest(t, c, p.Key, configuration))
	entries := applicationEntries(t, srv, configuration)
	written := entries[len(entries)-1]
	if written.Entity.Key != "banner" || written.EnvironmentId == nil {
		t.Fatalf("the configuration's entry = %s", raw(written))
	}
	assertSupersededBy(t, changeRequestFixture{srv: srv, project: p, dev: developer}, second, written)
	got := mustEvaluate(t, admin, p.Key, "production", management.EvaluationRequest{
		Context: contextOf(map[string]string{"user": "u-1"}, ""), Flags: &[]string{"banner"},
	}).Flags["banner"]
	if got.Value == nil || string(*got.Value) != `"Hey"` {
		t.Errorf("banner in production = %s, want the fallthrough b with its new value", raw(got))
	}
}

// What is proposed is checked as writing it directly would be, with the
// same refusals, on creation and on every edit; a definition names one
// entity and no environment.
func TestADefinitionChangeRequestIsValidatedAsTheDirectWrite(t *testing.T) {
	f := startDefinitions(t)
	stringFlag(t, f.admin, f.project.Key, "banner")
	c := f.developer.ClientWithResponses
	mustPut(t, f.admin, f.project.Key, "banner", "staging", 1, management.FlagConfigurationPut{OffVariation: "c", Fallthrough: "c"})

	for _, tc := range []struct {
		name   string
		body   management.ChangeRequestCreate
		status int
		code   string
	}{
		{"a boolean flag with a third variation", flagDefinitionRequest("checkout", management.FlagDefinitionPut{
			Variations: *variations("true", "true", "false", "false", "maybe", "true")}), http.StatusUnprocessableEntity, "invalid_flag"},
		{"a value of another type", flagDefinitionRequest("banner", management.FlagDefinitionPut{
			Variations: *variations("a", "1", "b", `"B"`, "c", `"C"`)}), http.StatusUnprocessableEntity, "invalid_flag"},
		{"removing a variation a configuration serves", flagDefinitionRequest("banner", management.FlagDefinitionPut{
			Variations: *variations("a", `"A"`, "b", `"B"`)}), http.StatusConflict, "in_use"},
		{"a segment condition in a segment", management.ChangeRequestCreate{
			Kind: management.ChangeRequestCreateKindDefinition, Segment: ptr("employees"), Title: "Nested",
			Proposed: ptr(segmentDefinitionInput(management.SegmentDefinitionPut{Conditions: []management.Condition{inSegment("employees")}})),
		}, http.StatusUnprocessableEntity, "invalid_segment"},
		{"a condition on an undeclared attribute", management.ChangeRequestCreate{
			Kind: management.ChangeRequestCreateKindDefinition, Segment: ptr("employees"), Title: "Shoes",
			Proposed: ptr(segmentDefinitionInput(management.SegmentDefinitionPut{Conditions: []management.Condition{attr("shoe_size", "eq", "42")}})),
		}, http.StatusUnprocessableEntity, "invalid_segment"},
		{"both a flag and a segment", management.ChangeRequestCreate{
			Kind: management.ChangeRequestCreateKindDefinition, Flag: ptr("banner"), Segment: ptr("employees"), Title: "Both",
			Proposed: ptr(segmentDefinitionInput(management.SegmentDefinitionPut{Conditions: []management.Condition{}})),
		}, http.StatusUnprocessableEntity, "invalid_change_request"},
		{"an environment", management.ChangeRequestCreate{
			Kind: management.ChangeRequestCreateKindDefinition, Flag: ptr("banner"), Environment: ptr("production"), Title: "Where",
			Proposed: ptr(flagDefinitionInput(management.FlagDefinitionPut{Variations: *variations("a", `"x"`)})),
		}, http.StatusUnprocessableEntity, "invalid_change_request"},
		{"neither a flag nor a segment", management.ChangeRequestCreate{
			Kind: management.ChangeRequestCreateKindDefinition, Title: "What",
			Proposed: ptr(flagDefinitionInput(booleanDefinition)),
		}, http.StatusUnprocessableEntity, "invalid_change_request"},
		{"a configuration without an environment", management.ChangeRequestCreate{
			Kind: management.ChangeRequestCreateKindFlagConfiguration, Flag: ptr("banner"), Title: "Where",
			Proposed: ptr(configurationInput(management.FlagConfigurationPut{OffVariation: "a", Fallthrough: "a"})),
		}, http.StatusUnprocessableEntity, "invalid_change_request"},
	} {
		res := do(c.CreateChangeRequestWithResponse(t.Context(), f.project.Key, tc.body))
		pr := assertProblem(t, tc.name, res.StatusCode(), res.Body, tc.status, tc.code)
		if tc.code == "invalid_flag" || tc.code == "invalid_segment" {
			if pr.Errors == nil || len(*pr.Errors) == 0 || (*pr.Errors)[0].Pointer[:len("/proposed/")] != "/proposed/" {
				t.Errorf("%s: errors = %+v, want them under /proposed", tc.name, pr.Errors)
			}
		}
	}

	cr := createChangeRequest(t, c, f.project.Key, segmentDefinitionRequest("employees", `["u1","u5"]`))
	nested := segmentDefinitionInput(management.SegmentDefinitionPut{Conditions: []management.Condition{{
		Kind: "prerequisite", Flag: ptr("banner"), Variation: ptr("a")}}})
	edit := updateChangeRequest(t, c, f.project.Key, cr, management.ChangeRequestPatch{Proposed: &nested})
	assertProblem(t, "editing proposed into a prerequisite", edit.StatusCode(), edit.Body, http.StatusUnprocessableEntity, "invalid_segment")
	private := segmentDefinitionInput(management.SegmentDefinitionPut{Conditions: []management.Condition{attr("user", "in", `["u7"]`)}, Private: true})
	edited := updateChangeRequest(t, c, f.project.Key, cr, management.ChangeRequestPatch{Proposed: &private})
	if edited.JSON200 == nil {
		t.Fatalf("edit = %d: %s", edited.StatusCode(), edited.Body)
	}
	if got := raw(edited.JSON200.Diff); got != `[{"op":"replace","path":"/conditions/0/value/0","value":"u7"},`+
		`{"op":"replace","path":"/private","value":true}]` {
		t.Errorf("diff after the edit = %s", got)
	}
}

// The approval covers the environments protected when it was given. A
// definition whose application would change an environment protected
// since is refused and the change request superseded; nothing moves.
func TestADefinitionIsNotAppliedOverAnEnvironmentProtectedSinceItsApproval(t *testing.T) {
	f := startDefinitions(t)
	stringFlag(t, f.admin, f.project.Key, "banner")
	c := f.developer.ClientWithResponses
	cr := mustApprove(t, f.maintainer.ClientWithResponses, f.project.Key, createChangeRequest(t, c, f.project.Key,
		flagDefinitionRequest("banner", management.FlagDefinitionPut{Variations: *variations("a", `"Welcome"`, "b", `"B"`, "c", `"C"`)})))
	setProtected(t, f.admin, f.project.Key, "staging", true)
	before := rulesetVersions(t, f.project)

	res := applyChangeRequest(t, c, f.project.Key, cr)
	assertProblem(t, "applying once staging is protected", res.StatusCode(), res.Body, http.StatusConflict, "base_moved")
	if got := getChangeRequest(t, c, f.project.Key, cr); got.State != management.ChangeRequestStateSuperseded {
		t.Errorf("state = %s, want superseded", got.State)
	}
	if got := bumped(t, "the refusal", before, rulesetVersions(t, f.project)); len(got) != 0 {
		t.Errorf("rulesets moved = %v, want none", got)
	}
	if v := getFlag(t, f.admin, f.project.Key, "banner").Variations[0].Value; string(v) != `"A"` {
		t.Errorf("variation a = %s, want A", v)
	}

	// An environment unprotected since needs no cover.
	cr = mustApprove(t, f.maintainer.ClientWithResponses, f.project.Key, createChangeRequest(t, c, f.project.Key,
		flagDefinitionRequest("banner", management.FlagDefinitionPut{Variations: *variations("a", `"Welcome"`, "b", `"B"`, "c", `"C"`)})))
	setProtected(t, f.admin, f.project.Key, "staging", false)
	if applied := mustApply(t, c, f.project.Key, cr); applied.State != management.ChangeRequestStateApplied {
		t.Errorf("applied once staging is unprotected = %s", applied.State)
	}
}

// Deleting a segment supersedes the change requests to its definition with
// an entry of their own, written before the cascade removes them; the list
// finds a segment's change requests by its key.
func TestDeletingASegmentSupersedesItsDefinitionChangeRequests(t *testing.T) {
	f := startDefinitions(t)
	c := f.developer.ClientWithResponses
	unused := createSegment(t, f.admin, f.project.Key, management.SegmentCreate{Key: "unused", Name: "Unused"})
	cr := createChangeRequest(t, c, f.project.Key, segmentDefinitionRequest("unused", `["u9"]`))
	onEmployees := createChangeRequest(t, c, f.project.Key, segmentDefinitionRequest("employees", `["u1","u9"]`))

	listed := do(c.ListChangeRequestsWithResponse(t.Context(), f.project.Key, &management.ListChangeRequestsParams{Segment: ptr("unused")}))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 1 || listed.JSON200.Items[0].Id != cr.Id {
		t.Fatalf("change requests of unused = %d: %s", listed.StatusCode(), listed.Body)
	}

	if res := deleteSegment(t, f.admin, f.project.Key, "unused", unused.Version); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", res.StatusCode(), res.Body)
	}
	deleted := lastEntryOf(t, f.srv, unused.Id)
	if deleted.Action != "segment.deleted" {
		t.Fatalf("the segment's last entry = %s", deleted.Action)
	}
	assertSupersededEntry(t, f.srv, cr.Id, deleted)
	if got := getChangeRequest(t, c, f.project.Key, onEmployees); got.State != management.ChangeRequestStateOpen {
		t.Errorf("another segment's change request = %s, want open", got.State)
	}
}
