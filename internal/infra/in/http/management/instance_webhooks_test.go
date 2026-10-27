package management_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func instanceArchive(url string) management.WebhookCreate {
	return management.WebhookCreate{Name: "Archive", Url: url, Events: []string{"audit.entry"}}
}

func createInstanceWebhook(t *testing.T, c *management.ClientWithResponses, body management.WebhookCreate) management.WebhookWithSecret {
	t.Helper()
	res := do(c.CreateInstanceWebhookWithResponse(t.Context(), body))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST /webhooks = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	return *res.JSON201
}

// instanceDeliveries returns the instance subscription's delivery log,
// newest first.
func instanceDeliveries(t *testing.T, c *management.ClientWithResponses, webhook openapi_types.UUID) []management.WebhookDelivery {
	t.Helper()
	res := do(c.ListInstanceWebhookDeliveriesWithResponse(t.Context(), webhook,
		&management.ListInstanceWebhookDeliveriesParams{Limit: ptr(200)}))
	if res.JSON200 == nil {
		t.Fatalf("GET /webhooks/{webhook}/deliveries = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// instanceEntriesSince returns the entries naming no project from the one
// with first on, as the instance log reads them, newest first.
func instanceEntriesSince(t *testing.T, admin *management.ClientWithResponses, first openapi_types.UUID) []management.AuditEntry {
	t.Helper()
	var out []management.AuditEntry
	for _, e := range instanceEntries(t, admin, management.ListAuditEntriesParams{Limit: ptr(200)}) {
		if e.ProjectId == nil && bytes.Compare(e.Id[:], first[:]) >= 0 {
			out = append(out, e)
		}
	}
	return out
}

// asRead returns e as the audit log answers it, in the form raw writes a
// decoded envelope's data.
func asRead(t *testing.T, e management.AuditEntry) string {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return raw(v)
}

func TestAnInstanceSubscriptionTakesAuditEntryAloneAndNoEnvironment(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	for name, tc := range map[string]struct {
		events       []string
		environments *[]string
		code         string
	}{
		"a project event":      {[]string{"flag.created"}, nil, "unknown_event_type"},
		"every event":          {[]string{"*"}, nil, "unknown_event_type"},
		"audit.entry and more": {[]string{"audit.entry", "grant.created"}, nil, "unknown_event_type"},
		"an environment":       {[]string{"audit.entry"}, &[]string{"production"}, "invalid_webhook"},
	} {
		body := instanceArchive("https://receiver.example/archive")
		body.Events, body.Environments = tc.events, tc.environments
		res := do(admin.CreateInstanceWebhookWithResponse(t.Context(), body))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, tc.code)
	}
	insecure := instanceArchive("http://receiver.example/archive")
	res := do(admin.CreateInstanceWebhookWithResponse(t.Context(), insecure))
	assertProblem(t, "plain http", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "insecure_url")
	if list := do(admin.ListInstanceWebhooksWithResponse(t.Context())); list.JSON200 == nil || len(list.JSON200.Items) != 0 {
		t.Fatalf("after refusals, list = %d: %s", list.StatusCode(), list.Body)
	}

	created := createInstanceWebhook(t, admin, instanceArchive("https://receiver.example/archive"))
	if !created.Enabled || len(created.Environments) != 0 || !slices.Equal(created.Events, []string{"audit.entry"}) {
		t.Errorf("created %+v", created)
	}
	e, _, after := webhookEntry(t, srv, "webhook.created", created)
	if e.ProjectId != nil || after["secret"] != "[redacted]" {
		t.Errorf("webhook.created of project %v, after %v: want an instance entry with the secret redacted", e.ProjectId, after)
	}

	widened := do(admin.UpdateInstanceWebhookWithResponse(t.Context(), created.Id,
		&management.UpdateInstanceWebhookParams{IfMatch: ifMatch(1)},
		management.WebhookPatch{Events: &[]string{"audit.entry", "flag.created"}}))
	assertProblem(t, "widening the events", widened.StatusCode(), widened.Body, http.StatusUnprocessableEntity, "unknown_event_type")
	renamed := do(admin.UpdateInstanceWebhookWithResponse(t.Context(), created.Id,
		&management.UpdateInstanceWebhookParams{IfMatch: ifMatch(1)},
		management.WebhookPatch{Name: ptr("Archive in the cloud"), Enabled: ptr(false)}))
	if renamed.JSON200 == nil || renamed.JSON200.Name != "Archive in the cloud" || renamed.JSON200.Enabled ||
		renamed.JSON200.Version != 2 {
		t.Fatalf("PATCH = %d: %s", renamed.StatusCode(), renamed.Body)
	}
	stale := do(admin.UpdateInstanceWebhookWithResponse(t.Context(), created.Id,
		&management.UpdateInstanceWebhookParams{IfMatch: ifMatch(1)}, management.WebhookPatch{Name: ptr("Late")}))
	if stale.StatusCode() != http.StatusPreconditionFailed {
		t.Errorf("a stale PATCH = %d, want 412", stale.StatusCode())
	}
	if res := do(admin.TestInstanceWebhookWithResponse(t.Context(), created.Id)); res.StatusCode() != http.StatusConflict {
		t.Errorf("pinging a disabled subscription = %d, want 409", res.StatusCode())
	}
	enabled := do(admin.EnableInstanceWebhookWithResponse(t.Context(), created.Id))
	if enabled.JSON200 == nil || !enabled.JSON200.Enabled {
		t.Fatalf("enable = %d: %s", enabled.StatusCode(), enabled.Body)
	}
	rotated := do(admin.RotateInstanceWebhookSecretWithResponse(t.Context(), created.Id))
	if rotated.JSON200 == nil || rotated.JSON200.Secret == created.Secret || rotated.JSON200.PreviousSecretExpiresAt == nil {
		t.Fatalf("rotate = %d: %s", rotated.StatusCode(), rotated.Body)
	}
	one := do(admin.GetInstanceWebhookWithResponse(t.Context(), created.Id))
	if one.JSON200 == nil || one.HTTPResponse.Header.Get("ETag") != `"4"` || strings.Contains(string(one.Body), rotated.JSON200.Secret) {
		t.Fatalf("get = %d, ETag %q: %s", one.StatusCode(), one.HTTPResponse.Header.Get("ETag"), one.Body)
	}

	// A project's subscription is not the instance's, nor the other way
	// round.
	p := createProject(t, admin, projectKey())
	projects := createWebhook(t, admin, p.Key, siem())
	if res := do(admin.GetInstanceWebhookWithResponse(t.Context(), projects.Id)); res.StatusCode() != http.StatusNotFound {
		t.Errorf("a project's subscription at /webhooks = %d, want 404", res.StatusCode())
	}
	if res := do(admin.GetWebhookWithResponse(t.Context(), p.Key, created.Id)); res.StatusCode() != http.StatusNotFound {
		t.Errorf("the instance's subscription under a project = %d, want 404", res.StatusCode())
	}
	if list := do(admin.ListInstanceWebhooksWithResponse(t.Context())); list.JSON200 == nil || len(list.JSON200.Items) != 1 ||
		list.JSON200.Items[0].Id != created.Id {
		t.Fatalf("list = %d: %s", list.StatusCode(), list.Body)
	}

	if res := do(admin.DeleteInstanceWebhookWithResponse(t.Context(), created.Id)); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", res.StatusCode(), res.Body)
	}
	if res := do(admin.GetInstanceWebhookWithResponse(t.Context(), created.Id)); res.StatusCode() != http.StatusNotFound {
		t.Errorf("a deleted subscription = %d, want 404", res.StatusCode())
	}
	for _, action := range []string{"webhook.updated", "webhook.enabled", "webhook.secret_rotated", "webhook.deleted"} {
		if e, _, _ := webhookEntry(t, srv, action, created); e.ProjectId != nil {
			t.Errorf("%s names project %v, want an instance entry", action, e.ProjectId)
		}
	}
}

func TestOnlyAnInstanceAdminManagesInstanceSubscriptions(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := createInstanceWebhook(t, admin, instanceArchive("https://receiver.example/archive"))
	owner, _ := grantedSession(t, srv, p, access.RoleOwner)
	for name, c := range map[string]*management.ClientWithResponses{
		"a project owner":   owner.ClientWithResponses,
		"a project's token": testkit.Client(t, srv, testkit.ProjectToken(t, testkit.Pool(t), projectIDOf(p), access.RoleOwner)),
	} {
		if res := do(c.ListInstanceWebhooksWithResponse(t.Context())); res.StatusCode() != http.StatusForbidden {
			t.Errorf("%s listing: status = %d, want 403", name, res.StatusCode())
		}
		if res := do(c.CreateInstanceWebhookWithResponse(t.Context(), instanceArchive("https://receiver.example/other"))); res.StatusCode() != http.StatusForbidden {
			t.Errorf("%s creating: status = %d, want 403", name, res.StatusCode())
		}
		if res := do(c.GetInstanceWebhookDeliveryWithResponse(t.Context(), created.Id, created.Id)); res.StatusCode() != http.StatusForbidden {
			t.Errorf("%s reading a delivery: status = %d, want 403", name, res.StatusCode())
		}
	}
}

// An instance subscription is sent `audit.entry` for every entry naming no
// project, as the instance log reads it, and nothing of a project.
func TestAnInstanceSubscriptionReceivesTheEntriesThatNameNoProject(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	created := createInstanceWebhook(t, admin, instanceArchive(r.URL))
	marker, _, _ := webhookEntry(t, srv, "webhook.created", created)

	m := testkit.NewMember(t, testkit.Pool(t))
	if res := do(admin.UpdateMemberWithResponse(t.Context(), m.ID.String(), management.MemberPatch{
		DisplayName: ptr("Renamed"),
	})); res.StatusCode() != http.StatusOK {
		t.Fatalf("PATCH member = %d: %s", res.StatusCode(), res.Body)
	}
	booleanFlag(t, admin, p.Key, "checkout")
	fanOut(t, srv)
	deliver(t, srv)

	instance := instanceEntriesSince(t, admin, marker.Id)
	if !slices.ContainsFunc(instance, func(e management.AuditEntry) bool { return e.Action == "member.updated" }) {
		t.Fatalf("no member.updated among the instance's entries %+v", instance)
	}
	want := map[string]string{}
	for _, e := range instance {
		want[e.Id.String()] = asRead(t, e)
	}
	calls := r.Calls()
	got := map[string]string{}
	for _, c := range calls {
		env := c.Envelope(t)
		if !c.Verifies(created.Secret) || env["type"] != "audit.entry" || env["project"] != nil {
			t.Errorf("received %v of project %v, verifying %t", env["type"], env["project"], c.Verifies(created.Secret))
		}
		entry, _ := env["audit_entry_id"].(string)
		got[entry] = raw(env["data"])
	}
	if len(got) != len(want) || len(calls) != len(want) {
		t.Errorf("received %d calls for %d entries, want one per instance entry (%d)", len(calls), len(got), len(want))
	}
	for entry, data := range want {
		if got[entry] != data {
			t.Errorf("audit.entry of %s carries %s, want the entry as read %s", entry, got[entry], data)
		}
	}

	log := instanceDeliveries(t, admin, created.Id)
	if len(log) != len(want) {
		t.Errorf("the delivery log holds %d deliveries, want %d", len(log), len(want))
	}
	one := do(admin.GetInstanceWebhookDeliveryWithResponse(t.Context(), created.Id, log[0].Id))
	if one.JSON200 == nil || one.JSON200.Status != management.WebhookDeliveryStatusSucceeded {
		t.Fatalf("GET delivery = %d: %s", one.StatusCode(), one.Body)
	}
	again := do(admin.RedeliverInstanceWebhookDeliveryWithResponse(t.Context(), created.Id, log[0].Id))
	if again.JSON202 == nil || again.JSON202.EventId != log[0].EventId || again.JSON202.Id == log[0].Id {
		t.Fatalf("redeliver = %d: %s", again.StatusCode(), again.Body)
	}
	pinged := do(admin.TestInstanceWebhookWithResponse(t.Context(), created.Id))
	if pinged.JSON202 == nil || pinged.JSON202.EventType != "ping" {
		t.Fatalf("ping = %d: %s", pinged.StatusCode(), pinged.Body)
	}
	deliver(t, srv)
	var pings int
	for _, c := range r.Calls()[len(calls):] {
		if env := c.Envelope(t); env["type"] == "ping" {
			pings++
			if env["project"] != nil {
				t.Errorf("the ping names project %v, want none", env["project"])
			}
		}
	}
	if pings != 1 || len(r.Calls()) != len(calls)+2 {
		t.Errorf("after a redelivery and a ping the receiver was called %d more times with %d pings, want 2 and 1",
			len(r.Calls())-len(calls), pings)
	}
}

// A project's deletion is an entry of the instance, so its subscriptions
// learn of it after the project's own are gone.
func TestAProjectsDeletionReachesTheInstanceSubscription(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	created := createInstanceWebhook(t, admin, instanceArchive(r.URL))
	session := signedIn(t, srv, testkit.NewMember(t, testkit.Pool(t), testkit.AsInstanceAdmin()))
	if res := deleteProject(t, session.ClientWithResponses, p.Key, p.Version); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE project = %d: %s", res.StatusCode(), res.Body)
	}
	fanOut(t, srv)
	deliver(t, srv)

	// The package's tests share one database, so the entry is found by the
	// project it names rather than by being the only deletion.
	deleted := instanceEntries(t, admin, management.ListAuditEntriesParams{
		Action: ptr("project.deleted"), EntityId: ptr(p.Id.String()),
	})
	if len(deleted) != 1 {
		t.Fatalf("%d project.deleted entries, want 1", len(deleted))
	}
	var received map[string]any
	for _, c := range r.Calls() {
		if env := c.Envelope(t); env["audit_entry_id"] == deleted[0].Id.String() {
			received = env
		}
	}
	if received == nil {
		t.Fatalf("the instance subscription %s was not sent the project's deletion", created.Id)
	}
	if received["type"] != "audit.entry" || received["project"] != nil || raw(received["data"]) != asRead(t, deleted[0]) {
		t.Errorf("received %v of project %v carrying %s, want audit.entry of the instance carrying %s",
			received["type"], received["project"], raw(received["data"]), asRead(t, deleted[0]))
	}
}
