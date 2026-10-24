package management_test

import (
	"net/http"
	"slices"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// changeRequestFixture is a project with a string flag, and a developer
// signed in to propose changes to it.
type changeRequestFixture struct {
	srv     *testkit.Server
	admin   *management.ClientWithResponses
	project management.Project
	dev     *testkit.Browser
	devID   access.MemberID
}

func startChangeRequests(t *testing.T) changeRequestFixture {
	t.Helper()
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "checkout_title")
	dev, m := grantedSession(t, srv, p, access.RoleDeveloper)
	return changeRequestFixture{srv: srv, admin: admin, project: p, dev: dev, devID: m.ID}
}

// proposedOn is the flag's initial configuration switched on.
func proposedOn() management.FlagConfigurationPut {
	return management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "a"}
}

func configurationRequest(title string, proposed management.FlagConfigurationPut) management.ChangeRequestCreate {
	return management.ChangeRequestCreate{
		Kind: management.ChangeRequestCreateKindFlagConfiguration, Flag: "checkout_title", Environment: "production", Title: title,
		Proposed: proposed,
	}
}

func createChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, body management.ChangeRequestCreate) management.ChangeRequest {
	t.Helper()
	res := do(c.CreateChangeRequestWithResponse(t.Context(), project, body))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST change-requests = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	return *res.JSON201
}

func updateChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest,
	body management.ChangeRequestPatch,
) *management.UpdateChangeRequestResponse {
	t.Helper()
	return do(c.UpdateChangeRequestWithResponse(t.Context(), project, cr.Id,
		&management.UpdateChangeRequestParams{IfMatch: ifMatch(cr.Version)}, body))
}

func openChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest) *management.OpenChangeRequestResponse {
	t.Helper()
	return do(c.OpenChangeRequestWithResponse(t.Context(), project, cr.Id, &management.OpenChangeRequestParams{IfMatch: ifMatch(cr.Version)}))
}

func rebaseChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest) *management.RebaseChangeRequestResponse {
	t.Helper()
	return do(c.RebaseChangeRequestWithResponse(t.Context(), project, cr.Id, &management.RebaseChangeRequestParams{IfMatch: ifMatch(cr.Version)}))
}

func cancelChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest) *management.CancelChangeRequestResponse {
	t.Helper()
	return do(c.CancelChangeRequestWithResponse(t.Context(), project, cr.Id, &management.CancelChangeRequestParams{IfMatch: ifMatch(cr.Version)}))
}

// patchOf returns the diff as op path=value strings, values as compact JSON.
func patchOf(d []management.PatchOperation) []string {
	out := make([]string, len(d))
	for i, op := range d {
		out[i] = string(op.Op) + " " + op.Path
		if op.Value != nil {
			out[i] += "=" + string(*op.Value)
		}
	}
	return out
}

// changeRequestActions returns the actions of the audit entries of the
// change request, newest first.
func changeRequestActions(t *testing.T, srv *testkit.Server, cr management.ChangeRequest) []string {
	t.Helper()
	entityID := cr.Id.String()
	res := do(testkit.Client(t, srv, testkit.InstanceAdminToken(t)).ListAuditEntriesWithResponse(t.Context(),
		&management.ListAuditEntriesParams{EntityId: &entityID}))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	var out []string
	for _, e := range res.JSON200.Items {
		out = append(out, e.Action)
	}
	return out
}

func TestAChangeRequestIsDraftedEditedAndOpenedWithoutChangingTheConfiguration(t *testing.T) {
	f := startChangeRequests(t)
	devName := do(f.dev.GetMeWithResponse(t.Context())).JSON200.Member.DisplayName
	before := rulesetVersions(t, f.project)

	body := configurationRequest("  Show the checkout title  ", proposedOn())
	body.Description = ptr("Ready for everyone.")
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, body)
	if cr.Kind != management.ChangeRequestKindFlagConfiguration || cr.State != management.ChangeRequestStateDraft ||
		cr.Title != "Show the checkout title" || cr.Description != "Ready for everyone." || valueOr(cr.Flag) != "checkout_title" ||
		valueOr(cr.Environment) != "production" || cr.BaseVersion != 1 || cr.Version != 1 || !cr.CreatedAt.Equal(cr.UpdatedAt) {
		t.Errorf("created = %+v", cr)
	}
	if cr.Author != (management.ChangeRequestAuthor{Kind: management.ChangeRequestAuthorKindMember, Id: f.devID.Bytes(), Name: devName}) {
		t.Errorf("author = %+v, want the developer", cr.Author)
	}
	if got := patchOf(cr.Diff); !slices.Equal(got, []string{"replace /on=true"}) {
		t.Errorf("diff = %v", got)
	}
	if !cr.Proposed.On || cr.Proposed.Rules == nil || cr.Proposed.Overrides == nil || cr.Proposed.Prerequisites == nil {
		t.Errorf("proposed = %+v", cr.Proposed)
	}
	if c := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "production"); c.On || c.Version != 1 {
		t.Errorf("the configuration changed: %+v", c)
	}
	if moved := bumped(t, "a change request", before, rulesetVersions(t, f.project)); len(moved) != 0 {
		t.Errorf("rulesets moved in %v", moved)
	}

	proposed := proposedOn()
	proposed.Overrides = &[]management.OverrideInput{{UnitType: "user", UnitId: "u-1", Variation: "b", Note: ptr("QA")}}
	res := updateChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr, management.ChangeRequestPatch{
		Title: ptr("Show the title, pin QA"), Proposed: &proposed,
	})
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("PATCH = %d: %s", res.StatusCode(), res.Body)
	}
	edited := *res.JSON200
	want := []string{"replace /on=true", `add /overrides/0={"note":"QA","unit_id":"u-1","unit_type":"user","variation":"b"}`}
	if got := patchOf(edited.Diff); !slices.Equal(got, want) || edited.Title != "Show the title, pin QA" ||
		edited.Description != "Ready for everyone." || edited.Version != 2 || edited.BaseVersion != 1 {
		t.Errorf("edited = %+v, diff %v", edited, got)
	}

	// An edit that changes nothing writes nothing.
	same := updateChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, edited, management.ChangeRequestPatch{
		Title: ptr("Show the title, pin QA"), Proposed: &proposed,
	})
	if same.JSON200 == nil || same.JSON200.Version != 2 || !same.JSON200.UpdatedAt.Equal(edited.UpdatedAt) {
		t.Errorf("the same edit = %d: %s", same.StatusCode(), same.Body)
	}

	opened := openChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, edited)
	if opened.JSON200 == nil || opened.JSON200.State != management.ChangeRequestStateOpen || opened.JSON200.Version != 3 {
		t.Fatalf("open = %d: %s", opened.StatusCode(), opened.Body)
	}
	// An open change request is still edited by its author.
	if res := updateChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, *opened.JSON200,
		management.ChangeRequestPatch{Description: ptr("Ready.")}); res.JSON200 == nil || res.JSON200.State != management.ChangeRequestStateOpen {
		t.Errorf("editing an open change request = %d: %s", res.StatusCode(), res.Body)
	}
	got := do(f.dev.GetChangeRequestWithResponse(t.Context(), f.project.Key, cr.Id))
	if got.JSON200 == nil || got.JSON200.Version != 4 || got.HTTPResponse.Header.Get("ETag") != `"4"` {
		t.Errorf("GET = %d: %s", got.StatusCode(), got.Body)
	}
	if actions := changeRequestActions(t, f.srv, cr); !slices.Equal(actions,
		[]string{"change_request.updated", "change_request.opened", "change_request.updated", "change_request.drafted"}) {
		t.Errorf("audit = %v", actions)
	}
}

func TestAChangeRequestCreatedOpenIsAuditedAsOpened(t *testing.T) {
	f := startChangeRequests(t)
	body := configurationRequest("Turn it on", proposedOn())
	body.Open = ptr(true)
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, body)
	if cr.State != management.ChangeRequestStateOpen {
		t.Errorf("state = %s", cr.State)
	}
	if actions := changeRequestActions(t, f.srv, cr); !slices.Equal(actions, []string{"change_request.opened"}) {
		t.Errorf("audit = %v", actions)
	}
}

func TestAChangeRequestProposesAnOverridesNoteButNotItsAuthor(t *testing.T) {
	f := startChangeRequests(t)
	putOverride(t, f.admin, f.project.Key, "checkout_title", "production", "user", "u-1", management.OverridePut{Variation: "b", Note: ptr("first")})
	current := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "production")

	echoed := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Nothing", asPut(current)))
	if len(echoed.Diff) != 0 || echoed.BaseVersion != current.Version {
		t.Errorf("echoing the configuration: diff %v, base %d", patchOf(echoed.Diff), echoed.BaseVersion)
	}
	noted := asPut(current)
	(*noted.Overrides)[0].Note = ptr("second")
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Note", noted))
	if got := patchOf(cr.Diff); !slices.Equal(got, []string{`replace /overrides/0/note="second"`}) {
		t.Errorf("diff = %v", got)
	}
}

func TestOpeningOnAMovedBaseIsRefusedUntilTheChangeRequestIsRebased(t *testing.T) {
	f := startChangeRequests(t)
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Turn it on", proposedOn()))

	moved := proposedOn()
	moved.On, moved.Fallthrough = false, "b"
	mustPut(t, f.admin, f.project.Key, "checkout_title", "production", 1, moved)

	res := openChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr)
	assertProblem(t, "opening on a moved base", res.StatusCode(), res.Body, http.StatusConflict, "base_moved")

	rebased := rebaseChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr)
	if rebased.JSON200 == nil {
		t.Fatalf("rebase = %d: %s", rebased.StatusCode(), rebased.Body)
	}
	r := *rebased.JSON200
	if got := patchOf(r.Diff); r.BaseVersion != 2 || r.Version != 2 || r.State != management.ChangeRequestStateDraft ||
		!slices.Equal(got, []string{`replace /fallthrough="a"`, "replace /on=true"}) {
		t.Errorf("rebased = %+v, diff %v", r, got)
	}
	// Rebasing on the base it has changes nothing.
	if again := rebaseChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, r); again.JSON200 == nil || again.JSON200.Version != 2 {
		t.Errorf("rebasing again = %d: %s", again.StatusCode(), again.Body)
	}
	if opened := openChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, r); opened.JSON200 == nil {
		t.Errorf("open after the rebase = %d: %s", opened.StatusCode(), opened.Body)
	}
	if actions := changeRequestActions(t, f.srv, cr); !slices.Equal(actions,
		[]string{"change_request.opened", "change_request.rebased", "change_request.drafted"}) {
		t.Errorf("audit = %v", actions)
	}
	// Only a draft is rebased.
	opened := do(f.dev.GetChangeRequestWithResponse(t.Context(), f.project.Key, cr.Id)).JSON200
	again := rebaseChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, *opened)
	assertProblem(t, "rebasing an open change request", again.StatusCode(), again.Body, http.StatusConflict, "invalid_state")
}

func TestACopyIsANewDraftOfTheCallersOnTheCurrentBase(t *testing.T) {
	f := startChangeRequests(t)
	body := configurationRequest("Turn it on", proposedOn())
	body.Description = ptr("Why.")
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, body)
	cancelled := cancelChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr)
	if cancelled.JSON200 == nil || cancelled.JSON200.State != management.ChangeRequestStateCancelled {
		t.Fatalf("cancel = %d: %s", cancelled.StatusCode(), cancelled.Body)
	}
	moved := proposedOn()
	moved.On, moved.Fallthrough = false, "c"
	mustPut(t, f.admin, f.project.Key, "checkout_title", "production", 1, moved)

	other, otherMember := grantedSession(t, f.srv, f.project, access.RoleDeveloper)
	res := do(other.CopyChangeRequestWithResponse(t.Context(), f.project.Key, cr.Id))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("copy = %d: %s", res.StatusCode(), res.Body)
	}
	cp := *res.JSON201
	if cp.Id == cr.Id || cp.State != management.ChangeRequestStateDraft || cp.Title != "Turn it on" || cp.Description != "Why." ||
		cp.BaseVersion != 2 || cp.Author.Id != otherMember.ID.Bytes() || !cp.Proposed.On || cp.Proposed.Fallthrough != "a" {
		t.Errorf("copy = %+v", cp)
	}
	if got := patchOf(cp.Diff); !slices.Equal(got, []string{`replace /fallthrough="a"`, "replace /on=true"}) {
		t.Errorf("copy's diff = %v", got)
	}
	entries := auditOf(t, f.srv, "change_request.copied", cp.Id.String())
	if len(entries) != 1 {
		t.Fatalf("copied entries = %+v", entries)
	}
	if after, ok := entries[0].After.(map[string]any); !ok || after["copied_from"] != cr.Id.String() {
		t.Errorf("copied entry after = %v", entries[0].After)
	}
}

func TestOnlyTheAuthorEditsOpensRebasesAndCancels(t *testing.T) {
	f := startChangeRequests(t)
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Turn it on", proposedOn()))
	other, _ := grantedSession(t, f.srv, f.project, access.RoleDeveloper)
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)

	for name, c := range map[string]*management.ClientWithResponses{"another developer": other.ClientWithResponses, "a maintainer": maintainer.ClientWithResponses} {
		res := updateChangeRequest(t, c, f.project.Key, cr, management.ChangeRequestPatch{Title: ptr("Mine now")})
		assertProblem(t, name+" editing", res.StatusCode(), res.Body, http.StatusForbidden, "not_author")
		opened := openChangeRequest(t, c, f.project.Key, cr)
		assertProblem(t, name+" opening", opened.StatusCode(), opened.Body, http.StatusForbidden, "not_author")
		rebased := rebaseChangeRequest(t, c, f.project.Key, cr)
		assertProblem(t, name+" rebasing", rebased.StatusCode(), rebased.Body, http.StatusForbidden, "not_author")
		cancelled := cancelChangeRequest(t, c, f.project.Key, cr)
		assertProblem(t, name+" cancelling", cancelled.StatusCode(), cancelled.Body, http.StatusForbidden, "not_author")
	}

	cancelled := cancelChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr)
	if cancelled.JSON200 == nil {
		t.Fatalf("cancel = %d: %s", cancelled.StatusCode(), cancelled.Body)
	}
	done := *cancelled.JSON200
	again := cancelChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, done)
	assertProblem(t, "cancelling again", again.StatusCode(), again.Body, http.StatusConflict, "invalid_state")
	edit := updateChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, done, management.ChangeRequestPatch{Title: ptr("Again")})
	assertProblem(t, "editing a cancelled change request", edit.StatusCode(), edit.Body, http.StatusConflict, "invalid_state")
	opened := openChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, done)
	assertProblem(t, "opening a cancelled change request", opened.StatusCode(), opened.Body, http.StatusConflict, "invalid_state")
	if actions := changeRequestActions(t, f.srv, cr); !slices.Equal(actions, []string{"change_request.cancelled", "change_request.drafted"}) {
		t.Errorf("audit = %v", actions)
	}
}

func TestEveryChangeRequestWriteButCreateAndCopyNeedsTheVersion(t *testing.T) {
	f := startChangeRequests(t)
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Turn it on", proposedOn()))
	ctx, key := t.Context(), f.project.Key
	for name, call := range map[string]func(*string) (int, []byte){
		"edit": func(v *string) (int, []byte) {
			r := do(f.dev.UpdateChangeRequestWithResponse(ctx, key, cr.Id, &management.UpdateChangeRequestParams{IfMatch: v},
				management.ChangeRequestPatch{Title: ptr("Other")}))
			return r.StatusCode(), r.Body
		},
		"open": func(v *string) (int, []byte) {
			r := do(f.dev.OpenChangeRequestWithResponse(ctx, key, cr.Id, &management.OpenChangeRequestParams{IfMatch: v}))
			return r.StatusCode(), r.Body
		},
		"rebase": func(v *string) (int, []byte) {
			r := do(f.dev.RebaseChangeRequestWithResponse(ctx, key, cr.Id, &management.RebaseChangeRequestParams{IfMatch: v}))
			return r.StatusCode(), r.Body
		},
		"cancel": func(v *string) (int, []byte) {
			r := do(f.dev.CancelChangeRequestWithResponse(ctx, key, cr.Id, &management.CancelChangeRequestParams{IfMatch: v}))
			return r.StatusCode(), r.Body
		},
	} {
		status, body := call(nil)
		assertProblem(t, name+" without If-Match", status, body, http.StatusPreconditionRequired, "precondition_required")
		status, body = call(ifMatch(2))
		assertProblem(t, name+" on another version", status, body, http.StatusPreconditionFailed, "version_mismatch")
	}
}

func TestAProposalIsCheckedAsTheWriteWouldBe(t *testing.T) {
	f := startChangeRequests(t)
	bad := proposedOn()
	bad.OffVariation = "z"
	res := do(f.dev.CreateChangeRequestWithResponse(t.Context(), f.project.Key, configurationRequest("Bad", bad)))
	p := assertProblem(t, "an invalid proposal", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_configuration")
	if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != "/proposed/off_variation" {
		t.Errorf("errors = %+v, want /proposed/off_variation", p.Errors)
	}

	res = do(f.dev.CreateChangeRequestWithResponse(t.Context(), f.project.Key, configurationRequest(" ", proposedOn())))
	p = assertProblem(t, "a blank title", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_change_request")
	if p.Errors == nil || len(*p.Errors) != 1 || (*p.Errors)[0].Pointer != "/title" {
		t.Errorf("errors = %+v, want /title", p.Errors)
	}

	unknown := configurationRequest("Unknown", proposedOn())
	unknown.Flag = "no_such_flag"
	res = do(f.dev.CreateChangeRequestWithResponse(t.Context(), f.project.Key, unknown))
	assertProblem(t, "an unknown flag", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")

	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Good", proposedOn()))
	edit := updateChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr, management.ChangeRequestPatch{Proposed: &bad})
	assertProblem(t, "an invalid edit", edit.StatusCode(), edit.Body, http.StatusUnprocessableEntity, "invalid_configuration")

	missing := do(f.dev.GetChangeRequestWithResponse(t.Context(), f.project.Key, openapi_types.UUID(projectIDOf(f.project).Bytes())))
	assertProblem(t, "an unknown change request", missing.StatusCode(), missing.Body, http.StatusNotFound, "not_found")
}

func TestRuleIdentifiersAndSaltsTheConfigurationDoesNotHoldAreLeftOut(t *testing.T) {
	f := startChangeRequests(t)
	stranger := openapi_types.UUID(projectIDOf(f.project).Bytes())
	salt := "0123456789abcdef"
	rollout := rolloutOf("user", "b", 2500)
	rollout.Salt = &salt
	proposed := proposedOn()
	proposed.Rules = &[]management.RuleInput{{Id: &stranger, Serve: rollout}}

	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Roll out", proposed))
	rules := cr.Proposed.Rules
	if len(rules) != 1 || rules[0].Id == stranger || rules[0].Id == (openapi_types.UUID{}) || rules[0].Serve.Salt == nil ||
		*rules[0].Serve.Salt == salt || *rules[0].Serve.Salt == "" {
		t.Errorf("proposed rules = %+v, want a new rule with a salt of its own", rules)
	}
}

func TestAProposalsNewRulesKeepTheirIdentifiersAndSaltsWhileItIsEdited(t *testing.T) {
	f := startChangeRequests(t)
	proposed := proposedOn()
	proposed.Rules = &[]management.RuleInput{{Serve: rolloutOf("user", "b", 2500)}}
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Roll out", proposed))
	given := cr.Proposed.Rules[0]

	// Sending back the proposal as it was answered changes nothing.
	echoed := proposedOn()
	echoed.Rules = &[]management.RuleInput{{Id: &given.Id, Serve: given.Serve}}
	same := updateChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr, management.ChangeRequestPatch{Proposed: &echoed})
	if same.JSON200 == nil || same.JSON200.Version != 1 || !same.JSON200.UpdatedAt.Equal(cr.UpdatedAt) {
		t.Fatalf("sending the proposal back = %d: %s", same.StatusCode(), same.Body)
	}

	// Growing the rollout keeps the rule and its salt, so no unit moves out.
	grown := proposedOn()
	serve := rolloutOf("user", "b", 5000)
	grown.Rules = &[]management.RuleInput{{Id: &given.Id, Serve: serve}}
	res := updateChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr, management.ChangeRequestPatch{Proposed: &grown})
	if res.JSON200 == nil {
		t.Fatalf("growing the rollout = %d: %s", res.StatusCode(), res.Body)
	}
	rules := res.JSON200.Proposed.Rules
	if len(rules) != 1 || rules[0].Id != given.Id || valueOr(rules[0].Serve.Salt) != valueOr(given.Serve.Salt) ||
		rules[0].Serve.Percentage == nil || *rules[0].Serve.Percentage != 5000 {
		t.Errorf("proposed rules = %+v, want rule %v with salt %q at 5000", rules, given.Id, valueOr(given.Serve.Salt))
	}
	if actions := changeRequestActions(t, f.srv, cr); !slices.Equal(actions, []string{"change_request.updated", "change_request.drafted"}) {
		t.Errorf("audit = %v", actions)
	}
}

func TestChangeRequestsAreReadByAnyMemberAndProposedWithChangeRequestPropose(t *testing.T) {
	f := startChangeRequests(t)
	cr := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("Turn it on", proposedOn()))
	viewer, _ := grantedSession(t, f.srv, f.project, access.RoleViewer)

	if got := do(viewer.GetChangeRequestWithResponse(t.Context(), f.project.Key, cr.Id)); got.JSON200 == nil {
		t.Errorf("a viewer reading = %d: %s", got.StatusCode(), got.Body)
	}
	if got := do(viewer.ListChangeRequestsWithResponse(t.Context(), f.project.Key, nil)); got.JSON200 == nil || len(got.JSON200.Items) != 1 {
		t.Errorf("a viewer listing = %d: %s", got.StatusCode(), got.Body)
	}
	res := do(viewer.CreateChangeRequestWithResponse(t.Context(), f.project.Key, configurationRequest("Mine", proposedOn())))
	p := assertProblem(t, "a viewer proposing", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
	if p.Permission == nil || *p.Permission != "change_request.propose" {
		t.Errorf("problem = %+v, want the permission change_request.propose named", p)
	}
	copied := do(viewer.CopyChangeRequestWithResponse(t.Context(), f.project.Key, cr.Id))
	assertProblem(t, "a viewer copying", copied.StatusCode(), copied.Body, http.StatusForbidden, "forbidden")

	reader := pipeline(t, f.srv, f.project, "developer")
	res = do(reader.CreateChangeRequestWithResponse(t.Context(), f.project.Key, configurationRequest("Mine", proposedOn())))
	assertProblem(t, "a read token proposing", res.StatusCode(), res.Body, http.StatusForbidden, "insufficient_scope")
}

func TestAProjectTokenAuthorsItsOwnChangeRequests(t *testing.T) {
	f := startChangeRequests(t)
	ci := pipeline(t, f.srv, f.project, "developer", management.ScopeFlagsWrite)
	cr := createChangeRequest(t, ci, f.project.Key, configurationRequest("From CI", proposedOn()))
	if cr.Author.Kind != management.ChangeRequestAuthorKindToken || cr.Author.Name != "ci" {
		t.Errorf("author = %+v, want the token ci", cr.Author)
	}
	entries := auditOf(t, f.srv, "change_request.drafted", cr.Id.String())
	if len(entries) != 1 || entries[0].Actor.Kind != management.ActorKindToken || entries[0].Actor.Name != "ci" {
		t.Errorf("drafted entries = %+v, want the token as actor", entries)
	}

	res := updateChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr, management.ChangeRequestPatch{Title: ptr("Mine")})
	assertProblem(t, "a member editing the token's", res.StatusCode(), res.Body, http.StatusForbidden, "not_author")

	edited := updateChangeRequest(t, ci, f.project.Key, cr, management.ChangeRequestPatch{Title: ptr("From CI, edited")})
	if edited.JSON200 == nil {
		t.Fatalf("the token editing = %d: %s", edited.StatusCode(), edited.Body)
	}
	opened := openChangeRequest(t, ci, f.project.Key, *edited.JSON200)
	if opened.JSON200 == nil {
		t.Fatalf("the token opening = %d: %s", opened.StatusCode(), opened.Body)
	}
	if c := cancelChangeRequest(t, ci, f.project.Key, *opened.JSON200); c.JSON200 == nil || c.JSON200.State != management.ChangeRequestStateCancelled {
		t.Errorf("the token cancelling = %d: %s", c.StatusCode(), c.Body)
	}
	author := cr.Author.Id
	listed := do(f.dev.ListChangeRequestsWithResponse(t.Context(), f.project.Key, &management.ListChangeRequestsParams{Author: &author}))
	if listed.JSON200 == nil || len(listed.JSON200.Items) != 1 || listed.JSON200.Items[0].Id != cr.Id {
		t.Errorf("listed by the token = %d: %s", listed.StatusCode(), listed.Body)
	}
}

func TestChangeRequestsAreListedNewestFirstByFilter(t *testing.T) {
	f := startChangeRequests(t)
	stringFlag(t, f.admin, f.project.Key, "banner")
	other, otherMember := grantedSession(t, f.srv, f.project, access.RoleDeveloper)

	first := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, configurationRequest("First", proposedOn()))
	staging := configurationRequest("Staging", proposedOn())
	staging.Environment = "staging"
	second := createChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, staging)
	banner := configurationRequest("Banner", proposedOn())
	banner.Flag, banner.Open = "banner", ptr(true)
	third := createChangeRequest(t, other.ClientWithResponses, f.project.Key, banner)

	list := func(params *management.ListChangeRequestsParams) []string {
		t.Helper()
		res := do(f.dev.ListChangeRequestsWithResponse(t.Context(), f.project.Key, params))
		if res.JSON200 == nil {
			t.Fatalf("GET change-requests = %d: %s", res.StatusCode(), res.Body)
		}
		var titles []string
		for _, cr := range res.JSON200.Items {
			titles = append(titles, cr.Title)
		}
		return titles
	}
	kind := management.ChangeRequestKindFlagConfiguration
	open, draft := management.ChangeRequestStateOpen, management.ChangeRequestStateDraft
	otherID := openapi_types.UUID(otherMember.ID.Bytes())
	for name, tc := range map[string]struct {
		params *management.ListChangeRequestsParams
		want   []string
	}{
		"all":         {nil, []string{"Banner", "Staging", "First"}},
		"flag":        {&management.ListChangeRequestsParams{Flag: ptr("checkout_title")}, []string{"Staging", "First"}},
		"environment": {&management.ListChangeRequestsParams{Environment: ptr("staging")}, []string{"Staging"}},
		"state":       {&management.ListChangeRequestsParams{State: &open}, []string{"Banner"}},
		"draft kind":  {&management.ListChangeRequestsParams{State: &draft, Kind: &kind}, []string{"Staging", "First"}},
		"author":      {&management.ListChangeRequestsParams{Author: &otherID}, []string{"Banner"}},
		"unknown":     {&management.ListChangeRequestsParams{Flag: ptr("nothing")}, nil},
	} {
		if got := list(tc.params); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}

	var ids []openapi_types.UUID
	var cursor *string
	for {
		res := do(f.dev.ListChangeRequestsWithResponse(t.Context(), f.project.Key, &management.ListChangeRequestsParams{Limit: ptr(1), Cursor: cursor}))
		if res.JSON200 == nil || len(res.JSON200.Items) != 1 {
			t.Fatalf("page = %d: %s", res.StatusCode(), res.Body)
		}
		ids = append(ids, res.JSON200.Items[0].Id)
		if cursor = res.JSON200.NextCursor; cursor == nil {
			break
		}
	}
	if !slices.Equal(ids, []openapi_types.UUID{third.Id, second.Id, first.Id}) {
		t.Errorf("paged = %v", ids)
	}
	bad := do(f.dev.ListChangeRequestsWithResponse(t.Context(), f.project.Key, &management.ListChangeRequestsParams{Cursor: ptr("nope")}))
	assertProblem(t, "a cursor not answered", bad.StatusCode(), bad.Body, http.StatusBadRequest, "invalid_request")
	over := do(f.dev.ListChangeRequestsWithResponse(t.Context(), f.project.Key, &management.ListChangeRequestsParams{Limit: ptr(201)}))
	assertProblem(t, "a limit over 200", over.StatusCode(), over.Body, http.StatusBadRequest, "invalid_request")
	for name, params := range map[string]*management.ListChangeRequestsParams{
		"an unknown kind":  {Kind: ptr(management.ChangeRequestKind("flag"))},
		"an unknown state": {State: ptr(management.ChangeRequestState("pending"))},
	} {
		res := do(f.dev.ListChangeRequestsWithResponse(t.Context(), f.project.Key, params))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
}
