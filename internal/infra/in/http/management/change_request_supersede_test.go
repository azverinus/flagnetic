package management_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// openedOn returns a change request of c's proposing proposed for flag in
// env, opened for review.
func openedOn(t *testing.T, c *management.ClientWithResponses, project, flag, env string, proposed management.FlagConfigurationPut,
) management.ChangeRequest {
	t.Helper()
	body := configurationRequest("Change "+flag, proposed)
	body.Flag, body.Environment, body.Open = ptr(flag), ptr(env), ptr(true)
	return createChangeRequest(t, c, project, body)
}

// lastEntryOf returns the newest audit entry of the entity with entityID.
func lastEntryOf(t *testing.T, srv *testkit.Server, entityID openapi_types.UUID) management.AuditEntry {
	t.Helper()
	entity := entityID.String()
	res := do(testkit.Client(t, srv, testkit.InstanceAdminToken(t)).ListAuditEntriesWithResponse(t.Context(),
		&management.ListAuditEntriesParams{EntityId: &entity, Limit: ptr(1)}))
	if res.JSON200 == nil || len(res.JSON200.Items) != 1 {
		t.Fatalf("GET /audit?entity_id=%s = %d: %s", entity, res.StatusCode(), res.Body)
	}
	return res.JSON200.Items[0]
}

// changeRequestEntries returns the audit entries of the change request with
// changeRequest, newest first, which outlive it.
func changeRequestEntries(t *testing.T, srv *testkit.Server, changeRequest openapi_types.UUID) []management.AuditEntry {
	t.Helper()
	entity := changeRequest.String()
	res := do(testkit.Client(t, srv, testkit.InstanceAdminToken(t)).ListAuditEntriesWithResponse(t.Context(),
		&management.ListAuditEntriesParams{EntityId: &entity}))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// assertSupersededBy fails unless cr is superseded by the write recorded as
// entry, with a change_request.superseded entry of its own naming it, made
// by the writer in the write's request.
func assertSupersededBy(t *testing.T, f changeRequestFixture, cr management.ChangeRequest, entry management.AuditEntry) {
	t.Helper()
	got := getChangeRequest(t, f.dev.ClientWithResponses, f.project.Key, cr)
	if got.State != management.ChangeRequestStateSuperseded || got.SupersededBy == nil || *got.SupersededBy != entry.Id ||
		got.Version != cr.Version+1 {
		t.Errorf("%q: state %s, superseded_by %v, version %d; want superseded by %s (%s) at version %d",
			cr.Title, got.State, got.SupersededBy, got.Version, entry.Id, entry.Action, cr.Version+1)
	}
	assertSupersededEntry(t, f.srv, cr.Id, entry)
}

// assertSupersededEntry fails unless the newest entry of the change request
// with changeRequest records it superseded by the write recorded as entry.
func assertSupersededEntry(t *testing.T, srv *testkit.Server, changeRequest openapi_types.UUID, entry management.AuditEntry) {
	t.Helper()
	entries := changeRequestEntries(t, srv, changeRequest)
	if len(entries) == 0 || entries[0].Action != "change_request.superseded" {
		t.Fatalf("entries of %s = %+v, want change_request.superseded newest", changeRequest, entries)
	}
	superseded := entries[0]
	after, _ := json.Marshal(superseded.After)
	var values struct {
		State        string `json:"state"`
		SupersededBy string `json:"superseded_by"`
	}
	if err := json.Unmarshal(after, &values); err != nil {
		t.Fatal(err)
	}
	if values.State != "superseded" || values.SupersededBy != entry.Id.String() || superseded.ChangeRequestId != nil ||
		superseded.Actor.Kind != entry.Actor.Kind || superseded.Actor.Id != entry.Actor.Id || !superseded.OccurredAt.Equal(entry.OccurredAt) {
		t.Errorf("change_request.superseded = %+v, after %s; want superseded by %s, by %+v at %s, applying none",
			superseded, after, entry.Id, entry.Actor, entry.OccurredAt)
	}
}

func TestEveryWriteToAConfigurationSupersedesTheChangeRequestsWaitingOnIt(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	put := func(t *testing.T, flag, env string, change func(*management.FlagConfigurationPut)) {
		t.Helper()
		c := getConfiguration(t, f.admin, key, flag, env)
		body := asPut(c)
		change(&body)
		mustPut(t, f.admin, key, flag, env, c.Version, body)
	}
	switchOn := func(c *management.FlagConfigurationPut) { c.On = true }

	for i, tc := range []struct {
		name string
		// before brings the configuration in staging to where the write
		// changes it.
		before func(t *testing.T, flag string)
		write  func(t *testing.T, flag string)
	}{
		{"a direct write", nil, func(t *testing.T, flag string) {
			put(t, flag, "staging", func(c *management.FlagConfigurationPut) { c.Fallthrough = "b" })
		}},
		{"an override", nil, func(t *testing.T, flag string) {
			if res := putOverride(t, f.admin, key, flag, "staging", "user", "u-1", management.OverridePut{Variation: "b"}); res.JSON200 == nil {
				t.Fatalf("PUT override = %d: %s", res.StatusCode(), res.Body)
			}
		}},
		{"the kill switch", func(t *testing.T, flag string) { put(t, flag, "staging", switchOn) }, func(t *testing.T, flag string) {
			if res := kill(t, f.admin, key, flag, "staging", ptr("errors")); res.JSON200 == nil || !res.JSON200.Changed {
				t.Fatalf("kill = %d: %s", res.StatusCode(), res.Body)
			}
		}},
		{"a copy", func(t *testing.T, flag string) {
			put(t, flag, "production", func(c *management.FlagConfigurationPut) { c.Fallthrough = "b" })
		}, func(t *testing.T, flag string) {
			if res := copyFrom(t, f.admin, key, flag, "staging", ifMatch(getConfiguration(t, f.admin, key, flag, "staging").Version),
				"production", nil); res.JSON200 == nil {
				t.Fatalf("copy = %d: %s", res.StatusCode(), res.Body)
			}
		}},
		{"a revert", func(t *testing.T, flag string) { put(t, flag, "staging", switchOn) }, func(t *testing.T, flag string) {
			if res := revert(t, f.admin, key, flag, "staging", ifMatch(2), 1); res.JSON200 == nil {
				t.Fatalf("revert = %d: %s", res.StatusCode(), res.Body)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flag := stringFlag(t, f.admin, key, "flag_"+strconv.Itoa(i))
			if tc.before != nil {
				tc.before(t, flag.Key)
			}
			open := openedOn(t, f.dev.ClientWithResponses, key, flag.Key, "staging", management.FlagConfigurationPut{
				On: true, OffVariation: "a", Fallthrough: "c"})
			approved := mustApprove(t, maintainer.ClientWithResponses, key,
				openedOn(t, f.dev.ClientWithResponses, key, flag.Key, "staging", management.FlagConfigurationPut{
					On: false, OffVariation: "c", Fallthrough: "c"}))
			elsewhere := openedOn(t, f.dev.ClientWithResponses, key, flag.Key, "development", proposedOn())

			tc.write(t, flag.Key)
			written := lastEntryOf(t, f.srv, flag.Id)
			assertSupersededBy(t, f, open, written)
			assertSupersededBy(t, f, approved, written)
			if got := getChangeRequest(t, f.dev.ClientWithResponses, key, elsewhere); got.State != management.ChangeRequestStateOpen {
				t.Errorf("another environment's change request: %s, want open", got.State)
			}
		})
	}
}

func TestApplyingAChangeRequestSupersedesTheOthersOnItsConfiguration(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	first := mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn()))
	second := mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "production",
		management.FlagConfigurationPut{OffVariation: "b", Fallthrough: "b"}))
	stillOpen := openedIn(t, f.dev.ClientWithResponses, key, "production", management.FlagConfigurationPut{OffVariation: "c", Fallthrough: "c"})
	draft := createChangeRequest(t, f.dev.ClientWithResponses, key, configurationRequest("A draft", proposedOn()))
	staging := openedIn(t, f.dev.ClientWithResponses, key, "staging", proposedOn())

	mustApply(t, f.dev.ClientWithResponses, key, first)
	application := applicationEntries(t, f.srv, first)
	if actions := []string{application[0].Action, application[len(application)-1].Action}; len(application) != 2 ||
		!slices.Equal(actions, []string{"change_request.applied", "flag.switched_on"}) {
		t.Fatalf("entries of the application = %+v, want only the configuration's and change_request.applied", application)
	}
	written := application[1]
	assertSupersededBy(t, f, second, written)
	assertSupersededBy(t, f, stillOpen, written)
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, first); got.State != management.ChangeRequestStateApplied ||
		got.SupersededBy != nil {
		t.Errorf("the applied change request: %s, superseded_by %v; want applied", got.State, got.SupersededBy)
	}
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, draft); got.State != management.ChangeRequestStateDraft {
		t.Errorf("the draft: %s, want draft: only a change request under review or approved is superseded", got.State)
	}
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, staging); got.State != management.ChangeRequestStateOpen {
		t.Errorf("staging's: %s, want open", got.State)
	}

	res := applyChangeRequest(t, f.dev.ClientWithResponses, key, getChangeRequest(t, f.dev.ClientWithResponses, key, second))
	assertProblem(t, "applying a superseded change request", res.StatusCode(), res.Body, http.StatusConflict, "invalid_state")
}

func TestArchivingAFlagSupersedesItsChangeRequestsInEveryEnvironment(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	production := openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn())
	staging := mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "staging", proposedOn()))

	flag := getFlag(t, f.admin, key, "checkout_title")
	archived := archive(t, f.admin, key, "checkout_title", flag.Version)
	if archived.JSON200 == nil {
		t.Fatalf("archive = %d: %s", archived.StatusCode(), archived.Body)
	}
	entry := lastEntryOf(t, f.srv, flag.Id)
	if entry.Action != "flag.archived" {
		t.Fatalf("the flag's last entry = %s, want flag.archived", entry.Action)
	}
	assertSupersededBy(t, f, production, entry)
	assertSupersededBy(t, f, staging, entry)

	if res := deleteFlag(t, f.admin, key, "checkout_title", archived.JSON200.Version); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete the archived flag = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestStoppingAnExperimentSupersedesTheChangeRequestsOnTheConfigurationsItRewrites(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	createExperiment(t, f.admin, key, startableExperiment("title_test"))
	mustPut(t, f.admin, key, "checkout_title", "staging", 1, holding("title_test"))
	e := mustStart(t, f.admin, key, "title_test", 1)
	proposed := holding("title_test")
	proposed.Fallthrough = "b"
	staging := openedOn(t, f.dev.ClientWithResponses, key, "checkout_title", "staging", proposed)
	production := openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn())

	mustStop(t, f.admin, key, "title_test", e.Version, winner("b"))
	flag := getFlag(t, f.admin, key, "checkout_title")
	rewritten := lastEntryOf(t, f.srv, flag.Id)
	if rewritten.Action != "flag.configuration_updated" || rewritten.EnvironmentId == nil {
		t.Fatalf("the flag's last entry = %s in %v, want flag.configuration_updated in staging", rewritten.Action, rewritten.EnvironmentId)
	}
	assertSupersededBy(t, f, staging, rewritten)
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, production); got.State != management.ChangeRequestStateOpen {
		t.Errorf("production's, not holding the experiment: %s, want open", got.State)
	}
}

func TestDeletingAnEnvironmentSupersedesItsChangeRequestsWithEntriesThatOutliveThem(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	created := do(f.admin.CreateEnvironmentWithResponse(t.Context(), key, management.EnvironmentCreate{Key: "qa", Name: "QA"}))
	if created.JSON201 == nil {
		t.Fatalf("POST environments = %d: %s", created.StatusCode(), created.Body)
	}
	open := openedIn(t, f.dev.ClientWithResponses, key, "qa", proposedOn())
	approved := mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "qa", proposedOn()))
	staging := openedIn(t, f.dev.ClientWithResponses, key, "staging", proposedOn())

	if res := do(f.admin.DeleteEnvironmentWithResponse(t.Context(), key, "qa",
		&management.DeleteEnvironmentParams{IfMatch: ifMatch(created.JSON201.Version)})); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE qa = %d: %s", res.StatusCode(), res.Body)
	}
	deleted := lastEntryOf(t, f.srv, created.JSON201.Id)
	if deleted.Action != "environment.deleted" {
		t.Fatalf("the environment's last entry = %s, want environment.deleted", deleted.Action)
	}
	for _, cr := range []management.ChangeRequest{open, approved} {
		res := do(f.dev.GetChangeRequestWithResponse(t.Context(), key, cr.Id))
		assertProblem(t, "a change request of a deleted environment", res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
		assertSupersededEntry(t, f.srv, cr.Id, deleted)
	}
	if got := getChangeRequest(t, f.dev.ClientWithResponses, key, staging); got.State != management.ChangeRequestStateOpen {
		t.Errorf("staging's: %s, want open", got.State)
	}
}

func TestAConfigurationCountsTheChangeRequestsWaitingOnIt(t *testing.T) {
	f := startChangeRequests(t)
	key := f.project.Key
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	waiting := func(c management.FlagConfiguration) management.WaitingChangeRequests {
		t.Helper()
		if c.ChangeRequests == nil {
			t.Fatalf("configuration = %+v, want change_requests", c)
		}
		return *c.ChangeRequests
	}
	none := management.WaitingChangeRequests{}
	if got := waiting(getConfiguration(t, f.admin, key, "checkout_title", "production")); got != none {
		t.Errorf("with none: %+v, want zero counts and no time", got)
	}

	openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn())
	createChangeRequest(t, f.dev.ClientWithResponses, key, configurationRequest("A draft", proposedOn()))
	openedIn(t, f.dev.ClientWithResponses, key, "staging", proposedOn())
	for range 2 {
		mustApprove(t, maintainer.ClientWithResponses, key, openedIn(t, f.dev.ClientWithResponses, key, "production", proposedOn()))
	}
	c := getConfiguration(t, f.admin, key, "checkout_title", "production")
	if got := waiting(c); got != (management.WaitingChangeRequests{Open: 1, Approved: 2}) {
		t.Errorf("waiting = %+v, want 1 open and 2 approved, none scheduled", got)
	}

	// A write supersedes every one of them, and answers that none waits
	// any more.
	put := asPut(c)
	put.Fallthrough = "b"
	written := mustPut(t, f.admin, key, "checkout_title", "production", c.Version, put)
	if got := waiting(written); got != none {
		t.Errorf("after a write: %+v, want none waiting", got)
	}
}
