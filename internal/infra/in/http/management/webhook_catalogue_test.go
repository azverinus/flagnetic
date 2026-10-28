package management_test

import (
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// TestTheCatalogueEndpointIsTheOpenAPIEventTypeEnum holds the two lists
// together: the types a subscription form offers are the types a
// subscription is accepted with.
func TestTheCatalogueEndpointIsTheOpenAPIEventTypeEnum(t *testing.T) {
	srv := testkit.StartServer(t)
	res := do(testkit.Client(t, srv, testkit.InstanceAdminToken(t)).ListWebhookEventTypesWithResponse(t.Context()))
	if res.JSON200 == nil {
		t.Fatalf("GET /webhook-event-types = %d: %s", res.StatusCode(), res.Body)
	}
	var served []string
	for _, e := range res.JSON200.Items {
		served = append(served, e.Type)
		if e.Description == "" {
			t.Errorf("%s has no description", e.Type)
		}
	}

	raw, err := os.ReadFile("../../../../../api/management.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Enum []string `yaml:"enum"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	enum := slices.DeleteFunc(slices.Clone(doc.Components.Schemas["WebhookEventType"].Enum), func(s string) bool { return s == "*" })

	if !slices.Equal(served, enum) {
		t.Errorf("served catalogue %v\n  OpenAPI enum %v", served, enum)
	}
}

func TestAScheduledChangeRequestsEventsAreDeliveredAndTheApplicationNamesTheConfigurationsEntry(t *testing.T) {
	c := testkit.Clock()
	f := startChangeRequests(t, testkit.WithClock(c), reachable)
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	key := f.project.Key
	r := testkit.StartReceiver(t)
	receiving(t, f.admin, key, r)

	at := c.Now().Add(time.Hour)
	body := configurationRequest("Release the checkout title", proposedOn())
	body.Open, body.ApplyAt = ptr(true), &at
	cr := mustApprove(t, maintainer.ClientWithResponses, key, createChangeRequest(t, f.dev.ClientWithResponses, key, body))
	c.Advance(time.Hour)
	mustTick(t, f.srv)
	fanOut(t, f.srv)
	deliver(t, f.srv)

	byType := map[string]map[string]any{}
	for _, call := range r.Calls() {
		envelope := call.Envelope(t)
		byType[envelope["type"].(string)] = envelope
	}
	for _, typ := range []string{"change_request.opened", "change_request.approved", "change_request.scheduled", "change_request.applied", "flag.switched_on"} {
		if byType[typ] == nil {
			t.Errorf("no %s delivered; got %v", typ, slices.Sorted(maps.Keys(byType)))
		}
	}
	applied, switched := byType["change_request.applied"], byType["flag.switched_on"]
	if applied == nil || switched == nil {
		return
	}
	if applied["audit_entry_id"] == nil || applied["audit_entry_id"] != switched["audit_entry_id"] {
		t.Errorf("applied names entry %v, the configuration's is %v", applied["audit_entry_id"], switched["audit_entry_id"])
	}
	data, _ := applied["data"].(map[string]any)
	if data["state"] != "applied" || data["proposed"] != nil || data["diff"] == nil || data["apply_at"] == nil {
		t.Errorf("applied data %v, want the applied change request with its diff and without its proposal", data)
	}
	if entity, _ := applied["entity"].(map[string]any); entity["kind"] != "change_request" || entity["id"] != cr.Id.String() {
		t.Errorf("applied entity %v, want the change request", applied["entity"])
	}
	if scheduled, _ := byType["change_request.scheduled"]["data"].(map[string]any); scheduled["apply_at"] == nil {
		t.Errorf("scheduled data %v, want apply_at", scheduled)
	}
}

func TestAStoppedScheduleReachesReceiversAsScheduleFailed(t *testing.T) {
	c := testkit.Clock()
	f := startChangeRequests(t, testkit.WithClock(c), reachable)
	maintainer, _ := grantedSession(t, f.srv, f.project, access.RoleMaintainer)
	sf := scheduleFixture{changeRequestFixture: f, clock: c, maintainer: maintainer}
	r := testkit.StartReceiver(t)
	receiving(t, f.admin, f.project.Key, r, "change_request.schedule_failed")

	cr, _ := sf.failing(t)
	for range 5 {
		sf.failingTick(t, cr)
	}
	fanOut(t, f.srv)
	deliver(t, f.srv)

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("%d calls, want one when the scheduler stopped", len(calls))
	}
	data, _ := calls[0].Envelope(t)["data"].(map[string]any)
	if failure, _ := data["schedule_failure"].(map[string]any); failure["stopped"] != true || failure["attempts"] != 5.0 {
		t.Errorf("data %v, want the stopped failure", data)
	}
}
