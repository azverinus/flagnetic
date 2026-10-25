package management_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func approveChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest,
	comment *string,
) *management.ApproveChangeRequestResponse {
	t.Helper()
	return do(c.ApproveChangeRequestWithResponse(t.Context(), project, cr.Id,
		&management.ApproveChangeRequestParams{IfMatch: ifMatch(cr.Version)}, management.ChangeRequestApprove{Comment: comment}))
}

func mustApprove(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest) management.ChangeRequest {
	t.Helper()
	res := approveChangeRequest(t, c, project, cr, nil)
	if res.JSON200 == nil {
		t.Fatalf("approve = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func rejectChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest,
	comment string,
) *management.RejectChangeRequestResponse {
	t.Helper()
	return do(c.RejectChangeRequestWithResponse(t.Context(), project, cr.Id,
		&management.RejectChangeRequestParams{IfMatch: ifMatch(cr.Version)}, management.ChangeRequestReject{Comment: comment}))
}

func applyChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest,
) *management.ApplyChangeRequestResponse {
	t.Helper()
	return do(c.ApplyChangeRequestWithResponse(t.Context(), project, cr.Id, &management.ApplyChangeRequestParams{IfMatch: ifMatch(cr.Version)}))
}

func mustApply(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest) management.ChangeRequest {
	t.Helper()
	res := applyChangeRequest(t, c, project, cr)
	if res.JSON200 == nil {
		t.Fatalf("apply = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func getChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, cr management.ChangeRequest) management.ChangeRequest {
	t.Helper()
	res := do(c.GetChangeRequestWithResponse(t.Context(), project, cr.Id))
	if res.JSON200 == nil {
		t.Fatalf("GET change request = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// openedIn returns a change request of c's proposing proposed for the
// checkout title in env, opened for review.
func openedIn(t *testing.T, c *management.ClientWithResponses, project, env string, proposed management.FlagConfigurationPut,
) management.ChangeRequest {
	t.Helper()
	body := configurationRequest("Change the checkout title", proposed)
	body.Environment, body.Open = env, ptr(true)
	return createChangeRequest(t, c, project, body)
}

// applicationEntries returns the audit entries the application of cr wrote,
// newest first.
func applicationEntries(t *testing.T, srv *testkit.Server, cr management.ChangeRequest) []management.AuditEntry {
	t.Helper()
	id := cr.Id
	res := do(testkit.Client(t, srv, testkit.InstanceAdminToken(t)).ListAuditEntriesWithResponse(t.Context(),
		&management.ListAuditEntriesParams{ChangeRequest: &id}))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func nameOf(t *testing.T, b *testkit.Browser) string {
	t.Helper()
	return do(b.GetMeWithResponse(t.Context())).JSON200.Member.DisplayName
}

func TestAChangeRequestIsApprovedOrRejectedOnceByAMemberWhoIsNotItsAuthor(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	author, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	owner, ownerMember := grantedSession(t, f.srv, f.project, access.RoleOwner)
	maintainer, maintainerMember := grantedSession(t, f.srv, f.project, access.RoleMaintainer)

	cr := openedIn(t, author.ClientWithResponses, key, "production", proposedOn())
	own := approveChangeRequest(t, author.ClientWithResponses, key, cr, nil)
	assertProblem(t, "the author approving", own.StatusCode(), own.Body, http.StatusForbidden, "author_cannot_approve")
	byDeveloper := approveChangeRequest(t, f.dev.ClientWithResponses, key, cr, nil)
	p := assertProblem(t, "a developer approving", byDeveloper.StatusCode(), byDeveloper.Body, http.StatusForbidden, "forbidden")
	if p.Permission == nil || *p.Permission != "change_request.approve" {
		t.Errorf("problem = %+v, want the permission change_request.approve named", p)
	}
	long := approveChangeRequest(t, owner.ClientWithResponses, key, cr, ptr(strings.Repeat("x", 2_001)))
	assertProblem(t, "a comment over 2,000 characters", long.StatusCode(), long.Body, http.StatusUnprocessableEntity, "invalid_change_request")

	res := approveChangeRequest(t, owner.ClientWithResponses, key, cr, ptr(" Looks right "))
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("approve = %d: %s", res.StatusCode(), res.Body)
	}
	approved := *res.JSON200
	if a := approved.Approval; approved.State != management.ChangeRequestStateApproved || a == nil ||
		a.Member != (management.ChangeRequestMember{Id: ownerMember.ID.Bytes(), Name: nameOf(t, owner)}) ||
		a.Comment != "Looks right" || a.At.IsZero() || approved.Rejection != nil || approved.AppliedAt != nil || approved.AppliedBy != nil {
		t.Errorf("approved = %+v, approval %+v", approved, a)
	}
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, cr); got.Approval == nil || *got.Approval != *approved.Approval {
		t.Errorf("read back = %+v, want the approval answered", got.Approval)
	}
	second := approveChangeRequest(t, maintainer.ClientWithResponses, key, approved, nil)
	assertProblem(t, "a second approval", second.StatusCode(), second.Body, http.StatusConflict, "invalid_state")
	// What was approved is what gets applied.
	edit := updateChangeRequest(t, author.ClientWithResponses, key, approved, management.ChangeRequestPatch{Title: ptr("Else")})
	assertProblem(t, "editing an approved change request", edit.StatusCode(), edit.Body, http.StatusConflict, "invalid_state")

	other := openedIn(t, author.ClientWithResponses, key, "staging", proposedOn())
	ownReject := rejectChangeRequest(t, author.ClientWithResponses, key, other, "no")
	assertProblem(t, "the author rejecting", ownReject.StatusCode(), ownReject.Body, http.StatusForbidden, "author_cannot_approve")
	blank := rejectChangeRequest(t, maintainer.ClientWithResponses, key, other, "  ")
	blankProblem := assertProblem(t, "rejecting without a comment", blank.StatusCode(), blank.Body, http.StatusUnprocessableEntity,
		"invalid_change_request")
	if blankProblem.Errors == nil || len(*blankProblem.Errors) != 1 || (*blankProblem.Errors)[0].Pointer != "/comment" {
		t.Errorf("problem = %+v, want /comment named", blankProblem.Errors)
	}
	rejectedRes := rejectChangeRequest(t, maintainer.ClientWithResponses, key, other, "Not before the release")
	if rejectedRes.JSON200 == nil {
		t.Fatalf("reject = %d: %s", rejectedRes.StatusCode(), rejectedRes.Body)
	}
	rejected := *rejectedRes.JSON200
	if r := rejected.Rejection; rejected.State != management.ChangeRequestStateRejected || r == nil ||
		r.Member.Id != maintainerMember.ID.Bytes() || r.Comment != "Not before the release" || rejected.Approval != nil {
		t.Errorf("rejected = %+v", rejected)
	}
	for name, status := range map[string]func() (int, []byte){
		"approving": func() (int, []byte) {
			r := approveChangeRequest(t, owner.ClientWithResponses, key, rejected, nil)
			return r.StatusCode(), r.Body
		},
		"cancelling": func() (int, []byte) {
			r := cancelChangeRequest(t, author.ClientWithResponses, key, rejected)
			return r.StatusCode(), r.Body
		},
		"applying": func() (int, []byte) {
			r := applyChangeRequest(t, owner.ClientWithResponses, key, rejected)
			return r.StatusCode(), r.Body
		},
	} {
		code, body := status()
		assertProblem(t, name+" a rejected change request", code, body, http.StatusConflict, "invalid_state")
	}

	if actions := changeRequestActions(t, f.srv, cr); !slices.Equal(actions, []string{"change_request.approved", "change_request.opened"}) {
		t.Errorf("audit of the approved = %v", actions)
	}
	if actions := changeRequestActions(t, f.srv, other); !slices.Equal(actions, []string{"change_request.rejected", "change_request.opened"}) {
		t.Errorf("audit of the rejected = %v", actions)
	}
	if entries := auditOf(t, f.srv, "change_request.approved", cr.Id.String()); len(entries) != 1 ||
		entries[0].Actor.Name != nameOf(t, owner) || entries[0].ChangeRequestId != nil {
		t.Errorf("approved entries = %+v, want one by the owner, not of an application", entries)
	}
}

// A developer applies their own approved change request in protected
// production: the configuration and the ruleset move once, evaluation
// serves what was proposed, and every entry of the application carries the
// change request's identifier.
func TestTheAuthorAppliesAnApprovedChangeRequestToAProtectedEnvironment(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	setProtected(t, f.admin, key, "production", true)
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)

	proposed := management.FlagConfigurationPut{On: true, OffVariation: "a", Fallthrough: "b",
		Overrides: &[]management.OverrideInput{{UnitType: "user", UnitId: "qa-1", Variation: "c", Note: ptr("QA")}},
		Rules:     &[]management.RuleInput{{Serve: rolloutOf("user", "c", 0)}},
	}
	cr := openedIn(t, f.dev.ClientWithResponses, key, "production", proposed)
	salt := valueOr(cr.Proposed.Rules[0].Serve.Salt)
	approved := mustApprove(t, maintainer.ClientWithResponses, key, cr)
	before := rulesetVersions(t, f.project)

	res := applyChangeRequest(t, f.dev.ClientWithResponses, key, approved)
	if res.JSON200 == nil || res.HTTPResponse.Header.Get("ETag") != `"3"` {
		t.Fatalf("apply = %d: %s", res.StatusCode(), res.Body)
	}
	applied := *res.JSON200
	devID := openapi_types.UUID(f.devID.Bytes())
	if by := applied.AppliedBy; applied.State != management.ChangeRequestStateApplied || applied.AppliedAt == nil || by == nil ||
		by.Kind != management.ChangeRequestApplierKindMember || by.Id == nil || *by.Id != devID || by.Name != nameOf(t, f.dev) ||
		applied.Approval == nil {
		t.Errorf("applied = %+v, by %+v", applied, by)
	}
	c := getConfiguration(t, f.admin, key, "checkout_title", "production")
	if !c.On || c.Fallthrough != "b" || c.Version != cr.BaseVersion+1 || len(c.Rules) != 1 || valueOr(c.Rules[0].Serve.Salt) != salt ||
		c.Rules[0].Id != cr.Proposed.Rules[0].Id {
		t.Errorf("configuration = %+v, want what was proposed, its rule as it was reviewed", c)
	}
	if len(c.Overrides) != 1 || c.Overrides[0].AddedBy == nil || *c.Overrides[0].AddedBy != devID {
		t.Errorf("overrides = %+v, want qa-1 added by the developer who applied it", c.Overrides)
	}
	if moved := bumped(t, "the application", before, rulesetVersions(t, f.project)); !slices.Equal(moved, []string{"production"}) {
		t.Errorf("rulesets moved in %v, want production once", moved)
	}
	got := mustEvaluate(t, f.admin, key, "production", management.EvaluationRequest{Context: contextOf(map[string]string{"user": "u-1"}, "")})
	if r := got.Flags["checkout_title"]; valueOr(r.Variation) != "b" {
		t.Errorf("evaluated = %+v, want b served", r)
	}

	entries := applicationEntries(t, f.srv, cr)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
		if e.ChangeRequestId == nil || *e.ChangeRequestId != cr.Id || e.Actor.Name != nameOf(t, f.dev) {
			t.Errorf("entry %s = %+v, want the change request's, by the developer", e.Action, e)
		}
	}
	if !slices.Equal(actions, []string{"change_request.applied", "flag.configuration_updated"}) {
		t.Errorf("entries of the application = %v", actions)
	}

	again := applyChangeRequest(t, f.dev.ClientWithResponses, key, applied)
	assertProblem(t, "applying twice", again.StatusCode(), again.Body, http.StatusConflict, "invalid_state")
	// The application let its own write through, and nothing else.
	direct := putConfiguration(t, f.dev.ClientWithResponses, key, "checkout_title", "production", c.Version, proposedOn())
	assertProblem(t, "a direct write after", direct.StatusCode(), direct.Body, http.StatusForbidden, "change_request_required")
}

// A maintainer applies another member's change request; a developer who is
// not its author may not. The applier becomes the author of every override
// whose variation or note changes, as with a direct write; an override left
// as it was keeps its author.
func TestAMaintainerAppliesAnothersChangeRequest(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	admin := getConfiguration(t, f.admin, key, "checkout_title", "staging")
	seeded := mustPut(t, f.admin, key, "checkout_title", "staging", admin.Version, management.FlagConfigurationPut{
		OffVariation: "a", Fallthrough: "a", Overrides: &[]management.OverrideInput{
			{UnitType: "user", UnitId: "kept", Variation: "b", Note: ptr("kept")},
			{UnitType: "user", UnitId: "renoted", Variation: "b", Note: ptr("before")},
		},
	})
	adminID := *seeded.Overrides[0].AddedBy
	other, _ := grantedSession(t, f.srv, f.project, access.RoleDeveloper)
	owner, _ := grantedSession(t, f.srv, f.project, access.RoleOwner)
	maintainer, maintainerMember := grantedSession(t, f.srv, f.project, access.RoleMaintainer)

	cr := openedIn(t, f.dev.ClientWithResponses, key, "staging", management.FlagConfigurationPut{
		OffVariation: "a", Fallthrough: "a", Overrides: &[]management.OverrideInput{
			{UnitType: "user", UnitId: "kept", Variation: "b", Note: ptr("kept")},
			{UnitType: "user", UnitId: "renoted", Variation: "b", Note: ptr("after")},
			{UnitType: "user", UnitId: "new", Variation: "c"},
		},
	})
	approved := mustApprove(t, owner.ClientWithResponses, key, cr)

	refused := applyChangeRequest(t, other.ClientWithResponses, key, approved)
	assertProblem(t, "another developer applying", refused.StatusCode(), refused.Body, http.StatusForbidden, "not_author")

	applied := mustApply(t, maintainer.ClientWithResponses, key, approved)
	if applied.AppliedBy == nil || applied.AppliedBy.Id == nil || *applied.AppliedBy.Id != maintainerMember.ID.Bytes() {
		t.Errorf("applied by = %+v, want the maintainer", applied.AppliedBy)
	}
	authors := map[string]string{}
	for _, o := range getConfiguration(t, f.admin, key, "checkout_title", "staging").Overrides {
		authors[o.UnitId] = o.AddedBy.String()
	}
	want := map[string]string{"kept": adminID.String(), "renoted": maintainerMember.ID.String(), "new": maintainerMember.ID.String()}
	if len(authors) != len(want) || authors["kept"] != want["kept"] || authors["renoted"] != want["renoted"] || authors["new"] != want["new"] {
		t.Errorf("override authors = %v, want %v", authors, want)
	}
}

// Approving or applying a change request whose configuration was written
// since it was based on it would decide or write what nobody reviewed: it
// is superseded and refused, and the configuration stays as it is.
func TestApprovingOrApplyingOnAMovedBaseSupersedesTheChangeRequest(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	move := func() management.FlagConfiguration {
		c := getConfiguration(t, f.admin, key, "checkout_title", "staging")
		put := asPut(c)
		put.Fallthrough = map[string]string{"a": "c", "b": "c", "c": "a"}[c.Fallthrough]
		return mustPut(t, f.admin, key, "checkout_title", "staging", c.Version, put)
	}

	toApprove := openedIn(t, f.dev.ClientWithResponses, key, "staging", proposedOn())
	move()
	res := approveChangeRequest(t, maintainer.ClientWithResponses, key, toApprove, nil)
	assertProblem(t, "approving on a moved base", res.StatusCode(), res.Body, http.StatusConflict, "base_moved")
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, toApprove); got.State != management.ChangeRequestStateSuperseded ||
		got.Approval != nil || got.Version != toApprove.Version+1 {
		t.Errorf("after approving on a moved base: %+v, want superseded and not approved", got)
	}
	if actions := changeRequestActions(t, f.srv, toApprove); !slices.Equal(actions, []string{"change_request.superseded", "change_request.opened"}) {
		t.Errorf("audit = %v", actions)
	}

	toApply := mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "staging", proposedOn()))
	before := move()
	res2 := applyChangeRequest(t, f.dev.ClientWithResponses, key, toApply)
	assertProblem(t, "applying on a moved base", res2.StatusCode(), res2.Body, http.StatusConflict, "base_moved")
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, toApply); got.State != management.ChangeRequestStateSuperseded ||
		got.AppliedAt != nil || got.Approval == nil {
		t.Errorf("after applying on a moved base: %+v, want superseded, still saying who approved it, and not applied", got)
	}
	if after := getConfiguration(t, f.admin, key, "checkout_title", "staging"); after.On || after.Version != before.Version {
		t.Errorf("configuration after a refused apply: %+v, want it unchanged at %d", after, before.Version)
	}
	if entries := applicationEntries(t, f.srv, toApply); len(entries) != 0 {
		t.Errorf("entries of a refused application = %+v", entries)
	}
}

// An instance admin without a role in the project decides and applies as an
// owner would, and every entry names them.
func TestAnInstanceAdminWithoutARoleApprovesAndApplies(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	cr := openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn())
	approved := mustApprove(t, f.admin, key, cr)
	applied := mustApply(t, f.admin, key, approved)
	adminName := approved.Approval.Member.Name
	if applied.AppliedBy == nil || applied.AppliedBy.Name != adminName || applied.State != management.ChangeRequestStateApplied {
		t.Errorf("applied = %+v, want applied by the admin", applied.AppliedBy)
	}
	adminID := approved.Approval.Member.Id
	for _, e := range applicationEntries(t, f.srv, cr) {
		if e.Actor.MemberId == nil || *e.Actor.MemberId != adminID {
			t.Errorf("%s by %+v, want the admin", e.Action, e.Actor)
		}
	}
	if !getConfiguration(t, f.admin, key, "checkout_title", "production").On {
		t.Error("the configuration was not written")
	}
}

// An approved change request is cancelled by its author or by whoever may
// decide change requests, until it is applied; a proposal no longer valid
// when it is applied is refused, writing nothing and leaving it approved.
func TestAnApprovedChangeRequestIsCancelledOrRefusedAtApplication(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	other, _ := grantedSession(t, f.srv, f.project, access.RoleDeveloper)

	own := mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn()))
	cancelled := cancelChangeRequest(t, f.dev.ClientWithResponses, key, own)
	if cancelled.JSON200 == nil || cancelled.JSON200.State != management.ChangeRequestStateCancelled {
		t.Fatalf("the author cancelling an approved change request = %d: %s", cancelled.StatusCode(), cancelled.Body)
	}
	res := applyChangeRequest(t, f.dev.ClientWithResponses, key, *cancelled.JSON200)
	assertProblem(t, "applying a cancelled change request", res.StatusCode(), res.Body, http.StatusConflict, "invalid_state")

	anothers := openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn())
	refused := cancelChangeRequest(t, other.ClientWithResponses, key, anothers)
	assertProblem(t, "another developer cancelling", refused.StatusCode(), refused.Body, http.StatusForbidden, "not_author")
	if c := cancelChangeRequest(t, maintainer.ClientWithResponses, key, anothers); c.JSON200 == nil ||
		c.JSON200.State != management.ChangeRequestStateCancelled {
		t.Errorf("a maintainer cancelling another's = %d: %s", c.StatusCode(), c.Body)
	}

	gate := stringFlag(t, f.admin, key, "gate")
	gated := proposedOn()
	gated.Prerequisites = &[]management.Prerequisite{{Flag: "gate", Variation: "b"}}
	approved := mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "staging", gated))
	if a := archive(t, f.admin, key, "gate", gate.Version); a.JSON200 == nil {
		t.Fatalf("archive gate = %d: %s", a.StatusCode(), a.Body)
	}
	before := getConfiguration(t, f.admin, key, "checkout_title", "staging")
	res = applyChangeRequest(t, f.dev.ClientWithResponses, key, approved)
	p := assertProblem(t, "applying what no longer validates", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_configuration")
	if p.Errors == nil || len(*p.Errors) == 0 || !strings.HasPrefix((*p.Errors)[0].Pointer, "/proposed/prerequisites") {
		t.Errorf("problem = %+v, want the prerequisite under /proposed named", p.Errors)
	}
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, approved); got.State != management.ChangeRequestStateApproved ||
		got.Version != approved.Version {
		t.Errorf("after a refused apply: %+v, want it approved as it was", got)
	}
	if after := getConfiguration(t, f.admin, key, "checkout_title", "staging"); after.Version != before.Version {
		t.Errorf("configuration version %d → %d, want unchanged", before.Version, after.Version)
	}
}

// A project token proposes and applies its own change requests but never
// decides one, whatever its role, and never applies another's: deciding
// and acting on another's change request stay with members.
func TestAProjectTokenNeverDecidesNorAppliesAnothersChangeRequest(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	cr := openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn())

	for _, role := range []string{"maintainer", "owner"} {
		ci := pipeline(t, f.srv, f.project, role, management.ScopeFlagsWrite)
		approved := approveChangeRequest(t, ci, key, cr, nil)
		assertProblem(t, role+" token approving", approved.StatusCode(), approved.Body, http.StatusForbidden, "token_cannot_approve")
		rejected := rejectChangeRequest(t, ci, key, cr, "no")
		assertProblem(t, role+" token rejecting", rejected.StatusCode(), rejected.Body, http.StatusForbidden, "token_cannot_approve")
	}
	devToken := pipeline(t, f.srv, f.project, "developer", management.ScopeFlagsWrite)
	res := approveChangeRequest(t, devToken, key, cr, nil)
	assertProblem(t, "developer token approving", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
	if got := getChangeRequest(t, f.admin, key, cr); got.State != management.ChangeRequestStateOpen {
		t.Errorf("state = %s, want still open", got.State)
	}

	approved := mustApprove(t, maintainer.ClientWithResponses, key, cr)
	for _, role := range []string{"developer", "maintainer"} {
		ci := pipeline(t, f.srv, f.project, role, management.ScopeFlagsWrite)
		refused := applyChangeRequest(t, ci, key, approved)
		assertProblem(t, role+" token applying another's", refused.StatusCode(), refused.Body, http.StatusForbidden, "not_author")
	}

	// A member approves what a pipeline proposed, and the pipeline applies it.
	own := openedIn(t, devToken, key, "production", proposedOn())
	applied := mustApply(t, devToken, key, mustApprove(t, maintainer.ClientWithResponses, key, own))
	if by := applied.AppliedBy; by == nil || by.Kind != management.ChangeRequestApplierKindToken || by.Name != "ci" ||
		by.Id == nil || *by.Id != own.Author.Id {
		t.Errorf("applied by = %+v, want the token", by)
	}
	for _, e := range applicationEntries(t, f.srv, own) {
		if e.Actor.Kind != management.ActorKindToken {
			t.Errorf("%s by %+v, want the token", e.Action, e.Actor)
		}
	}
	if c := getConfiguration(t, f.admin, key, "checkout_title", "production"); !c.On {
		t.Error("the token's change request was not applied")
	}
}

func TestDecidingAndApplyingNeedTheVersion(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	open := openedIn(t, f.dev.ClientWithResponses, key, "staging", proposedOn())
	approved := mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "staging", proposedOn()))
	ctx := t.Context()
	for name, call := range map[string]func(*string) (int, []byte){
		"approve": func(v *string) (int, []byte) {
			r := do(maintainer.ApproveChangeRequestWithResponse(ctx, key, open.Id, &management.ApproveChangeRequestParams{IfMatch: v},
				management.ChangeRequestApprove{}))
			return r.StatusCode(), r.Body
		},
		"reject": func(v *string) (int, []byte) {
			r := do(maintainer.RejectChangeRequestWithResponse(ctx, key, open.Id, &management.RejectChangeRequestParams{IfMatch: v},
				management.ChangeRequestReject{Comment: "no"}))
			return r.StatusCode(), r.Body
		},
		"apply": func(v *string) (int, []byte) {
			r := do(f.dev.ApplyChangeRequestWithResponse(ctx, key, approved.Id, &management.ApplyChangeRequestParams{IfMatch: v}))
			return r.StatusCode(), r.Body
		},
	} {
		status, body := call(nil)
		assertProblem(t, name+" without If-Match", status, body, http.StatusPreconditionRequired, "precondition_required")
		status, body = call(ifMatch(7))
		assertProblem(t, name+" on another version", status, body, http.StatusPreconditionFailed, "version_mismatch")
	}
	if c := getConfiguration(t, f.admin, key, "checkout_title", "staging"); c.On {
		t.Error("an apply refused for its version wrote the configuration")
	}
}
