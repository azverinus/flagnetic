package management_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// revertRequest asks to revert the checkout title's configuration in env to
// version.
func revertRequest(env string, version int64) management.ChangeRequestCreate {
	return management.ChangeRequestCreate{
		Kind: management.ChangeRequestCreateKindFlagConfiguration, Flag: ptr("checkout_title"), Environment: ptr(env),
		Title: "Roll the checkout title back", RevertTo: &version,
	}
}

func postChangeRequest(t *testing.T, c *management.ClientWithResponses, project string, body management.ChangeRequestCreate,
) *management.CreateChangeRequestResponse {
	t.Helper()
	return do(c.CreateChangeRequestWithResponse(t.Context(), project, body))
}

// sameProposal reports whether cr proposes c whole: what it serves, its rule
// identifiers and salts, and its overrides' notes.
func sameProposal(t *testing.T, cr management.ChangeRequest, c management.FlagConfiguration) bool {
	t.Helper()
	p := proposedConfiguration(t, cr)
	if p.On != c.On || p.OffVariation != c.OffVariation || p.Fallthrough != c.Fallthrough || len(p.Rules) != len(c.Rules) ||
		len(p.Overrides) != len(c.Overrides) {
		return false
	}
	for i, r := range p.Rules {
		if r.Id != c.Rules[i].Id || valueOr(r.Serve.Salt) != valueOr(c.Rules[i].Serve.Salt) || r.Serve.Kind != c.Rules[i].Serve.Kind {
			return false
		}
	}
	for i, o := range p.Overrides {
		if o.UnitId != c.Overrides[i].UnitId || valueOr(o.Note) != valueOr(c.Overrides[i].Note) {
			return false
		}
	}
	return true
}

func TestARevertChangeRequestProposesTheVersionWholeAndAppliesItAsARevert(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	v2 := mustPut(t, f.admin, key, "checkout_title", "production", 1, targeting())
	cleared := asPut(v2)
	*cleared.Rules = (*cleared.Rules)[:1]
	cleared.Overrides = &[]management.OverrideInput{}
	mustPut(t, f.admin, key, "checkout_title", "production", 2, cleared)
	setProtected(t, f.admin, key, "production", true)
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)

	body := revertRequest("production", 2)
	body.Open = ptr(true)
	cr := createChangeRequest(t, f.dev.ClientWithResponses, key, body)
	if cr.RevertTo == nil || *cr.RevertTo != 2 || cr.BaseVersion != 3 || len(cr.Diff) == 0 || !sameProposal(t, cr, v2) {
		t.Fatalf("revert change request = %+v\nproposed %+v, want version 2 of %+v", cr, proposedConfiguration(t, cr), v2)
	}
	if ordinary := createChangeRequest(t, f.dev.ClientWithResponses, key, configurationRequest("Plain", proposedOn())); ordinary.RevertTo != nil {
		t.Errorf("an ordinary change request reverts to %d, want null", *ordinary.RevertTo)
	}

	// Nobody edits what a revert proposes, but its words and time are its
	// author's.
	edit := updateChangeRequest(t, f.dev.ClientWithResponses, key, cr, management.ChangeRequestPatch{
		Proposed: ptr(configurationInput(proposedOn())),
	})
	if got := violationsOf(t, "editing a revert's proposal", edit.StatusCode(), edit.Body, "invalid_change_request"); !slices.Equal(got,
		[]string{"/proposed not_allowed"}) {
		t.Errorf("violations = %v", got)
	}
	renamed := updateChangeRequest(t, f.dev.ClientWithResponses, key, cr, management.ChangeRequestPatch{Title: ptr("Undo the rollout removal")})
	if renamed.JSON200 == nil || renamed.JSON200.Title != "Undo the rollout removal" || !sameProposal(t, *renamed.JSON200, v2) {
		t.Fatalf("renaming = %d: %s", renamed.StatusCode(), renamed.Body)
	}

	approved := mustApprove(t, maintainer.ClientWithResponses, key, *renamed.JSON200)
	applied := mustApply(t, f.dev.ClientWithResponses, key, approved)
	if applied.State != management.ChangeRequestStateApplied || applied.RevertTo == nil || *applied.RevertTo != 2 {
		t.Errorf("applied = %+v", applied)
	}
	if got := getConfiguration(t, f.admin, key, "checkout_title", "production"); got.Version != 4 || !sameTargeting(got, v2) {
		t.Errorf("configuration = %+v\nwant version 2 whole = %+v", got, v2)
	}

	entries := applicationEntries(t, f.srv, cr)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	if !slices.Equal(actions, []string{"change_request.applied", "flag.reverted"}) {
		t.Fatalf("entries of the application = %v", actions)
	}
	data, _ := json.Marshal(entries[1].After)
	var after map[string]any
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if after["reverted_to"] != 2.0 || after["version"] != 4.0 {
		t.Errorf("flag.reverted after = %v, want reverted to 2 at version 4", after)
	}
}

func TestARevertChangeRequestIsRefusedAsADirectRevertIs(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	c := f.dev.ClientWithResponses
	mustPut(t, f.admin, key, "checkout_title", "production", 1, management.FlagConfigurationPut{OffVariation: "b", Fallthrough: "b"})
	mustPut(t, f.admin, key, "checkout_title", "production", 2, proposedOn())

	unknown := postChangeRequest(t, c, key, revertRequest("production", 9))
	assertProblem(t, "a version not yet written", unknown.StatusCode(), unknown.Body, http.StatusNotFound, "version_not_found")

	both := revertRequest("production", 1)
	both.Proposed = ptr(configurationInput(proposedOn()))
	res := postChangeRequest(t, c, key, both)
	if got := violationsOf(t, "both", res.StatusCode(), res.Body, "invalid_change_request"); !slices.Equal(got,
		[]string{"/revert_to not_allowed"}) {
		t.Errorf("violations of both = %v", got)
	}
	neither := revertRequest("production", 1)
	neither.RevertTo = nil
	res = postChangeRequest(t, c, key, neither)
	if got := violationsOf(t, "neither", res.StatusCode(), res.Body, "invalid_change_request"); !slices.Equal(got,
		[]string{"/proposed required"}) {
		t.Errorf("violations of neither = %v", got)
	}
	definition := management.ChangeRequestCreate{
		Kind: management.ChangeRequestCreateKindDefinition, Flag: ptr("checkout_title"), Title: "Revert the definition",
		RevertTo: ptr(int64(1)),
	}
	res = postChangeRequest(t, c, key, definition)
	if got := violationsOf(t, "a definition", res.StatusCode(), res.Body, "invalid_change_request"); !slices.Equal(got,
		[]string{"/revert_to not_allowed"}) {
		t.Errorf("violations of a definition = %v", got)
	}

	updateFlag(t, f.admin, key, "checkout_title", 1, management.FlagPatch{Variations: variations("a", `"A"`, "c", `"C"`)})
	res = postChangeRequest(t, c, key, revertRequest("production", 2))
	if got := violationsOf(t, "a variation gone", res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got,
		[]string{"/proposed/off_variation unknown_variation", "/proposed/fallthrough unknown_variation"}) {
		t.Errorf("violations of a variation gone = %v", got)
	}
}

func TestARevertChangeRequestIsRefusedWhenItWouldChangeWhichRulesServeAnExperiment(t *testing.T) {
	f := startExperiments(t)
	runningExperiment(t, f)
	res := postChangeRequest(t, f.admin, f.project.Key, revertRequest("staging", 1))
	assertProblem(t, "reverting across the rule's arrival", res.StatusCode(), res.Body, http.StatusConflict, "experiment_rules_changed")
}

func TestRebaseAndCopyOfARevertKeepItsTarget(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	c := f.dev.ClientWithResponses
	v2 := mustPut(t, f.admin, key, "checkout_title", "production", 1, targeting())
	cleared := asPut(v2)
	*cleared.Rules = (*cleared.Rules)[:1]
	mustPut(t, f.admin, key, "checkout_title", "production", 2, cleared)

	draft := createChangeRequest(t, c, key, revertRequest("production", 2))
	mustPut(t, f.admin, key, "checkout_title", "production", 3, proposedOn())
	res := rebaseChangeRequest(t, c, key, draft)
	if res.JSON200 == nil {
		t.Fatalf("rebase = %d: %s", res.StatusCode(), res.Body)
	}
	rebased := *res.JSON200
	if rebased.BaseVersion != 4 || rebased.RevertTo == nil || *rebased.RevertTo != 2 || !sameProposal(t, rebased, v2) {
		t.Errorf("rebased = %+v, proposing %+v", rebased, proposedConfiguration(t, rebased))
	}

	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	copied := do(maintainer.CopyChangeRequestWithResponse(t.Context(), key, rebased.Id))
	if copied.JSON201 == nil {
		t.Fatalf("copy = %d: %s", copied.StatusCode(), copied.Body)
	}
	if cp := *copied.JSON201; cp.RevertTo == nil || *cp.RevertTo != 2 || cp.BaseVersion != 4 || !sameProposal(t, cp, v2) {
		t.Errorf("copy = %+v", cp)
	}

	// Rebased again onto a flag that lost a variation the version serves,
	// the revert is refused as creating it would be.
	updateFlag(t, f.admin, key, "checkout_title", 1, management.FlagPatch{Variations: variations("a", `"A"`, "c", `"C"`)})
	gone := rebaseChangeRequest(t, c, key, rebased)
	if got := violationsOf(t, "rebasing onto a variation gone", gone.StatusCode(), gone.Body, "invalid_configuration"); len(got) == 0 {
		t.Errorf("rebasing onto a variation gone = %d: %s", gone.StatusCode(), gone.Body)
	}
}
