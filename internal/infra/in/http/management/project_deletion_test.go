package management_test

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// deleteProject deletes project through c based on version.
func deleteProject(t *testing.T, c *management.ClientWithResponses, project string, version int64) *management.DeleteProjectResponse {
	t.Helper()
	return do(c.DeleteProjectWithResponse(t.Context(), project, &management.DeleteProjectParams{IfMatch: ifMatch(version)}))
}

// instanceEntries returns the instance log's entries as an instance admin
// reads them with params, newest first.
func instanceEntries(t *testing.T, admin *management.ClientWithResponses, params management.ListAuditEntriesParams) []management.AuditEntry {
	t.Helper()
	res := do(admin.ListAuditEntriesWithResponse(t.Context(), &params))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func TestAnOwnerDeletesAProjectWithEverythingInItAndItsKeyIsFreeAtOnce(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	stringFlag(t, admin, p.Key, "checkout_title")
	owner, _ := grantedSession(t, srv, p, access.RoleOwner)
	token := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{Name: "ci", Role: "developer"})
	pipeline := testkit.Client(t, srv, token.Value)
	// A change request waiting for review ends with the project, recorded by
	// the project's deletion alone.
	dev, _ := grantedSession(t, srv, p, access.RoleDeveloper)
	cr := createChangeRequest(t, dev.ClientWithResponses, p.Key, configurationRequest("On", proposedOn()))
	if res := openChangeRequest(t, dev.ClientWithResponses, p.Key, cr); res.JSON200 == nil {
		t.Fatalf("open = %d: %s", res.StatusCode(), res.Body)
	}
	before := instanceEntries(t, admin, management.ListAuditEntriesParams{Project: &p.Id, Limit: ptr(200)})

	unconditional := do(owner.DeleteProjectWithResponse(t.Context(), p.Key, &management.DeleteProjectParams{}))
	assertProblem(t, "without If-Match", unconditional.StatusCode(), unconditional.Body, http.StatusPreconditionRequired, "precondition_required")
	stale := deleteProject(t, owner.ClientWithResponses, p.Key, p.Version+1)
	assertProblem(t, "on a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")

	if res := deleteProject(t, owner.ClientWithResponses, p.Key, p.Version); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /projects/%s = %d: %s", p.Key, res.StatusCode(), res.Body)
	}

	gone := do(admin.GetProjectWithResponse(t.Context(), p.Key))
	assertProblem(t, "reading the deleted project", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")
	again := deleteProject(t, owner.ClientWithResponses, p.Key, p.Version)
	if again.StatusCode() != http.StatusNotFound {
		t.Errorf("deleting it again = %d, want 404: %s", again.StatusCode(), again.Body)
	}
	if res := do(pipeline.GetProjectWithResponse(t.Context(), p.Key)); res.StatusCode() != http.StatusUnauthorized {
		t.Errorf("the project's token after the deletion = %d, want 401: %s", res.StatusCode(), res.Body)
	}
	listed := do(owner.ListProjectsWithResponse(t.Context()))
	if listed.JSON200 == nil || slices.ContainsFunc(listed.JSON200.Items, func(q management.Project) bool { return q.Id == p.Id }) {
		t.Errorf("the owner's projects after the deletion = %d %s, want it gone", listed.StatusCode(), listed.Body)
	}

	// The key names a new project at once, which shares nothing with the
	// deleted one: not its flags, not its history.
	reused := createProject(t, admin, p.Key)
	if reused.Id == p.Id {
		t.Fatal("the new project has the deleted one's identifier")
	}
	if res := do(admin.GetFlagWithResponse(t.Context(), p.Key, "checkout_title")); res.StatusCode() != http.StatusNotFound {
		t.Errorf("the deleted project's flag in the new project = %d, want 404", res.StatusCode())
	}
	newLog := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ListProjectAuditEntriesParams{Limit: ptr(200)}))
	if newLog.JSON200 == nil || slices.ContainsFunc(newLog.JSON200.Items, func(e management.AuditEntry) bool { return e.Entity.Key == "checkout_title" }) {
		t.Errorf("the new project's log = %d %s, want none of the deleted project's entries", newLog.StatusCode(), newLog.Body)
	}

	// The deleted project's entries stay, read by its identifier.
	after := instanceEntries(t, admin, management.ListAuditEntriesParams{Project: &p.Id, Limit: ptr(200)})
	if !slices.EqualFunc(after, before, func(a, b management.AuditEntry) bool { return a.Id == b.Id }) {
		t.Errorf("the deleted project's entries = %v, want the %d it had", actionsOf(after), len(before))
	}
	if slices.Contains(actionsOf(after), "change_request.superseded") {
		t.Error("the deletion superseded the change request with an entry of its own")
	}
	deleted := "project.deleted"
	entries := instanceEntries(t, admin, management.ListAuditEntriesParams{Action: &deleted, EntityId: ptr(p.Id.String())})
	if len(entries) != 1 {
		t.Fatalf("project.deleted entries of the project = %d, want 1", len(entries))
	}
	e := entries[0]
	want := map[string]any{"id": p.Id.String(), "key": p.Key, "name": p.Name}
	if e.ProjectId != nil || e.Entity.Kind != "project" || e.Entity.Key != p.Key || e.Actor.Kind != management.ActorKindMember ||
		!reflect.DeepEqual(e.Before, want) || e.After != nil {
		t.Errorf("project.deleted = %+v, want an instance entry by the owner with the project's id, key and name before", e)
	}
}

func TestAnInstanceAdminDeletesAProjectFromASession(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	session := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin()))
	if res := deleteProject(t, session.ClientWithResponses, p.Key, p.Version); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("an instance admin's DELETE = %d: %s", res.StatusCode(), res.Body)
	}
}

func TestDeletingAProjectNeedsAnOwnersOrInstanceAdminsSession(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	maintainer, _ := grantedSession(t, srv, p, access.RoleMaintainer)
	owner, ownerMember := grantedSession(t, srv, p, access.RoleOwner)
	ownersToken := testkit.PersonalToken(t, testkit.Pool(t), ownerMember.ID, "laptop", access.ScopeProjectAdmin)
	projectToken := mustCreateProjectToken(t, owner, p.Key, management.ProjectTokenCreate{
		Name: "ci", Role: "owner", Scopes: ptr(scopes(management.ScopeProjectAdmin)),
	})

	for name, tc := range map[string]struct {
		c      *management.ClientWithResponses
		status int
		code   string
	}{
		"a maintainer":                   {maintainer.ClientWithResponses, http.StatusForbidden, "forbidden"},
		"the owner's personal token":     {testkit.Client(t, srv, ownersToken), http.StatusForbidden, "session_required"},
		"a project token with owner":     {testkit.Client(t, srv, projectToken.Value), http.StatusForbidden, "session_required"},
		"the instance admin's own token": {admin, http.StatusForbidden, "session_required"},
	} {
		res := deleteProject(t, tc.c, p.Key, p.Version)
		assertProblem(t, name, res.StatusCode(), res.Body, tc.status, tc.code)
	}
	if res := do(admin.GetProjectWithResponse(t.Context(), p.Key)); res.JSON200 == nil {
		t.Errorf("the project after every refusal = %d: %s", res.StatusCode(), res.Body)
	}
	me := do(owner.GetProjectMeWithResponse(t.Context(), p.Key))
	if me.JSON200 == nil || !slices.Contains(me.JSON200.Permissions, "project.delete") {
		t.Errorf("the owner's permissions = %d %s, want project.delete among them", me.StatusCode(), me.Body)
	}
}

func TestThePurgeRemovesWhatADeletedProjectLeftWithoutAForeignKey(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	deleted := createProject(t, admin, projectKey())
	kept := createProject(t, admin, projectKey())
	store := pgevents.NewStore(testkit.Pool(t))
	at := testkit.Start
	eventID := id.New[delivery.Event](id.NewSource(clock.System{}))
	// claim records eventID as received for project, reporting whether it
	// was new: a received ID is kept until it is purged.
	claim := func(project management.Project, kind events.EventType) bool {
		t.Helper()
		got, err := store.ClaimEvents(t.Context(), projectIDOf(project), kind, []delivery.EventID{eventID}, at)
		if err != nil {
			t.Fatal(err)
		}
		return got[eventID]
	}
	kinds := []events.EventType{events.EventEvaluationSummary, events.EventAlias, events.EventExposure}
	for _, kind := range kinds {
		claim(deleted, kind)
		claim(kept, kind)
	}
	session := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin()))
	if res := deleteProject(t, session.ClientWithResponses, deleted.Key, deleted.Version); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", res.StatusCode(), res.Body)
	}

	if err := srv.Tick(t, worker.ProjectPurgeName); err != nil {
		t.Fatalf("purge round: %v", err)
	}
	for _, kind := range kinds {
		if !claim(deleted, kind) {
			t.Errorf("the deleted project's %s event ID is still recorded after the purge", kind)
		}
		if claim(kept, kind) {
			t.Errorf("the purge forgot another project's %s event ID", kind)
		}
	}
}
