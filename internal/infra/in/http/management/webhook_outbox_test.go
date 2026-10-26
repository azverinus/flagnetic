package management_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// fanOut runs one round of the fan-out of srv.
func fanOut(t *testing.T, srv *testkit.Server) {
	t.Helper()
	if err := srv.Tick(t, worker.WebhookFanOutName); err != nil {
		t.Fatalf("fan-out round: %v", err)
	}
}

// deliveries returns the subscription's delivery log as c reads it with
// params, newest first, failing unless it is answered.
func deliveries(t *testing.T, c *management.ClientWithResponses, project string, webhook openapi_types.UUID,
	params management.ListWebhookDeliveriesParams,
) management.WebhookDeliveryList {
	t.Helper()
	res := do(c.ListWebhookDeliveriesWithResponse(t.Context(), project, webhook, &params))
	if res.JSON200 == nil {
		t.Fatalf("GET deliveries = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// deliveredTypes returns the event types of every delivery of the
// subscription, oldest first.
func deliveredTypes(t *testing.T, c *management.ClientWithResponses, project string, webhook openapi_types.UUID) []string {
	t.Helper()
	list := deliveries(t, c, project, webhook, management.ListWebhookDeliveriesParams{Limit: ptr(200)})
	out := make([]string, len(list.Items))
	for i, d := range list.Items {
		out[len(out)-1-i] = d.EventType
	}
	return out
}

func subscribe(t *testing.T, c *management.ClientWithResponses, project, name string, events []string, environments ...string) management.WebhookWithSecret {
	t.Helper()
	body := management.WebhookCreate{Name: name, Url: "https://receiver.example/" + name, Events: events}
	if environments != nil {
		body.Environments = &environments
	}
	return createWebhook(t, c, project, body)
}

// switchOn switches the boolean flag on in env, serving true to everyone,
// with rules when given.
func switchOn(t *testing.T, c *management.ClientWithResponses, project, flag, env string, rules ...management.RuleInput) {
	t.Helper()
	current := getConfiguration(t, c, project, flag, env)
	put := asPut(current)
	put.On = true
	if rules != nil {
		put.Rules = &rules
	}
	mustPut(t, c, project, flag, env, current.Version, put)
}

func booleanFlag(t *testing.T, c *management.ClientWithResponses, project, key string) {
	t.Helper()
	createFlag(t, c, project, management.FlagCreate{Key: key, Type: management.FlagTypeBoolean, Name: key})
}

func TestARolledBackWriteLeavesNoEvent(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	everything := subscribe(t, admin, p.Key, "everything", []string{"*"})
	pool := testkit.Pool(t)
	ids := id.NewSource(clock.System{})
	record := appgovernance.NewRecord(pggovernance.NewAuditLog(pool), pggovernance.NewChangeRequests(pool),
		appflags.NewSegmentReferrers(pgflags.NewStore(pool)), ids, appgovernance.WithOutbox(pggovernance.NewOutbox(pool), ids))
	write := func(key string, fail error) error {
		e, err := governance.NewEntry(id.New[governance.Entry](ids), time.Now().UTC(), governance.Request{Actor: governance.CLIActor("test")},
			governance.Record{
				Project: projectIDOf(p), Action: "flag.updated",
				Entity: governance.Entity{Kind: "flag", ID: id.New[governance.Entry](ids).String(), Key: key},
				Before: map[string]any{"key": key}, After: map[string]any{"key": key, "name": "renamed"},
			})
		if err != nil {
			t.Fatal(err)
		}
		return postgres.NewTransactor(pool).Within(t.Context(), func(ctx context.Context) error {
			if err := record.Record(ctx, e); err != nil {
				return err
			}
			return fail
		})
	}
	refused := errors.New("the write failed after recording its entry")

	if err := write("rolled_back", refused); !errors.Is(err, refused) {
		t.Fatalf("rolled-back write = %v", err)
	}
	fanOut(t, srv)
	if got := deliveredTypes(t, admin, p.Key, everything.Id); len(got) != 0 {
		t.Fatalf("deliveries %v after a rolled-back write, want none", got)
	}

	if err := write("committed", nil); err != nil {
		t.Fatal(err)
	}
	fanOut(t, srv)
	list := deliveries(t, admin, p.Key, everything.Id, management.ListWebhookDeliveriesParams{})
	if len(list.Items) != 1 || list.NextCursor != nil {
		t.Fatalf("deliveries %+v, want the committed write's one", list)
	}
	d := list.Items[0]
	if d.EventType != "flag.updated" || d.Status != management.WebhookDeliveryStatusPending || len(d.Attempts) != 0 ||
		d.NextAttemptAt == nil || d.CreatedAt.IsZero() {
		t.Errorf("delivery %+v, want flag.updated pending, due, not attempted", d)
	}
	got := do(admin.GetWebhookDeliveryWithResponse(t.Context(), p.Key, everything.Id, d.Id))
	if got.JSON200 == nil || raw(*got.JSON200) != raw(d) {
		t.Errorf("GET delivery = %d %s, want %s", got.StatusCode(), got.Body, raw(d))
	}
	if depth := testkit.Metric(t, srv.Scrape(t), "flagnetic_webhook_outbox_depth"); depth != 0 {
		t.Errorf("outbox depth %v after a round, want 0", depth)
	}
}

func TestAnEventReachesOnlyTheSubscriptionsThatMatch(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	booleanFlag(t, admin, p.Key, "checkout")
	switchOn(t, admin, p.Key, "checkout", "production")

	everything := subscribe(t, admin, p.Key, "everything", []string{"*"})
	oneType := subscribe(t, admin, p.Key, "one-type", []string{"flag.kill_switch"})
	oneEnvironment := subscribe(t, admin, p.Key, "one-environment", []string{"*"}, "production")
	disabled := createWebhook(t, admin, p.Key, management.WebhookCreate{
		Name: "disabled", Url: "https://receiver.example/disabled", Events: []string{"*"}, Enabled: ptr(false),
	})
	entries := subscribe(t, admin, p.Key, "entries", []string{governance.EventAuditEntry})

	if res := kill(t, admin, p.Key, "checkout", "production", ptr("checkout 500s")); res.StatusCode() != http.StatusOK {
		t.Fatalf("kill = %d %s", res.StatusCode(), res.Body)
	}
	switchOn(t, admin, p.Key, "checkout", "staging")
	member := testkit.NewMember(t, pool, testkit.WithEmail("viewer-"+strings.ToLower(projectKey())+"@example.com"))
	grant(t, admin, p.Key, member.ID, "viewer")
	fanOut(t, srv)

	for name, tc := range map[string]struct {
		webhook openapi_types.UUID
		want    []string
	}{
		"everything":      {everything.Id, []string{"flag.kill_switch", "flag.switched_on", "grant.created"}},
		"one type":        {oneType.Id, []string{"flag.kill_switch"}},
		"one environment": {oneEnvironment.Id, []string{"flag.kill_switch", "grant.created"}},
		"disabled":        {disabled.Id, []string{}},
		// Its own creation, recorded once it existed, and then every change.
		"audit entries": {entries.Id, []string{"audit.entry", "audit.entry", "audit.entry", "audit.entry"}},
	} {
		if got := deliveredTypes(t, admin, p.Key, tc.webhook); !slices.Equal(got, tc.want) {
			t.Errorf("%s: deliveries %v, want %v", name, got, tc.want)
		}
	}
}

func TestAnEventIsDeliveredOnceAcrossRounds(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	everything := subscribe(t, admin, p.Key, "everything", []string{"*"})
	booleanFlag(t, admin, p.Key, "checkout")

	fanOut(t, srv)
	fanOut(t, srv)

	if got := deliveredTypes(t, admin, p.Key, everything.Id); !slices.Equal(got, []string{"flag.created"}) {
		t.Errorf("deliveries %v, want flag.created once", got)
	}
}

func TestAKillSwitchIsOneEventAndChangesBeforeASubscriptionAreNotSent(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	// An earlier subscription keeps the events of these writes in the
	// outbox, so only the creation time keeps them from the later one.
	subscribe(t, admin, p.Key, "earlier", []string{"*"})
	booleanFlag(t, admin, p.Key, "checkout")
	switchOn(t, admin, p.Key, "checkout", "staging")
	later := subscribe(t, admin, p.Key, "later", []string{"*"})

	if res := kill(t, admin, p.Key, "checkout", "staging", ptr("checkout 500s")); res.StatusCode() != http.StatusOK {
		t.Fatalf("kill = %d %s", res.StatusCode(), res.Body)
	}
	fanOut(t, srv)

	if got := deliveredTypes(t, admin, p.Key, later.Id); !slices.Equal(got, []string{"flag.kill_switch"}) {
		t.Errorf("deliveries %v, want the kill switch alone: no switch off, nothing from before the subscription", got)
	}
}

func TestRulesSegmentsGrantsAndSDKKeysAreCatalogued(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	everything := subscribe(t, admin, p.Key, "everything", []string{"*"})

	booleanFlag(t, admin, p.Key, "checkout")
	switchOn(t, admin, p.Key, "checkout", "staging", management.RuleInput{
		Conditions: conditions(attr("country", "eq", `"de"`)), Serve: serves("true"),
	})
	declare(t, admin, p.Key, "email")
	createSegment(t, admin, p.Key, management.SegmentCreate{
		Key: "vips", Name: "VIPs", Private: ptr(true), Conditions: conditions(attr("email", "eq", `"a@example.com"`)),
	})
	member := testkit.NewMember(t, pool, testkit.WithEmail("dev-"+strings.ToLower(projectKey())+"@example.com"))
	grant(t, admin, p.Key, member.ID, "developer")
	key := mustCreateSDKKey(t, admin, p.Key, "staging", management.SdkKeyCreate{Kind: "server", Name: "checkout-api"})
	if res := revoke(t, admin, p.Key, "staging", key.Key.Id.String()); res.JSON200 == nil {
		t.Fatalf("revoke = %d %s", res.StatusCode(), res.Body)
	}
	fanOut(t, srv)

	// Declaring an attribute is audited and catalogues no event.
	want := []string{"flag.created", "flag.switched_on", "flag.rules_changed", "segment.created", "grant.created",
		"sdk_key.created", "sdk_key.revoked"}
	if got := deliveredTypes(t, admin, p.Key, everything.Id); !slices.Equal(got, want) {
		t.Errorf("deliveries %v, want %v and no audit.entry", got, want)
	}
}

func TestAnEnvironmentFilterPassesProjectLevelEvents(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	staging := subscribe(t, admin, p.Key, "staging", []string{"*"}, "staging")

	booleanFlag(t, admin, p.Key, "checkout")
	switchOn(t, admin, p.Key, "checkout", "staging")
	switchOn(t, admin, p.Key, "checkout", "development")
	member := testkit.NewMember(t, pool, testkit.WithEmail("viewer-"+strings.ToLower(projectKey())+"@example.com"))
	grant(t, admin, p.Key, member.ID, "viewer")
	fanOut(t, srv)

	if got, want := deliveredTypes(t, admin, p.Key, staging.Id), []string{"flag.created", "flag.switched_on", "grant.created"}; !slices.Equal(got, want) {
		t.Errorf("deliveries %v, want %v", got, want)
	}
}

func TestTheDeliveryLogIsFilteredAndPaged(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	everything := subscribe(t, admin, p.Key, "everything", []string{"*"})
	other := subscribe(t, admin, p.Key, "other", []string{"*"})
	start := time.Now().UTC()
	for _, key := range []string{"one", "two", "three"} {
		booleanFlag(t, admin, p.Key, key)
	}
	switchOn(t, admin, p.Key, "one", "staging")
	fanOut(t, srv)
	list := func(params management.ListWebhookDeliveriesParams) management.WebhookDeliveryList {
		return deliveries(t, admin, p.Key, everything.Id, params)
	}

	all := list(management.ListWebhookDeliveriesParams{})
	if len(all.Items) != 4 || all.Items[0].EventType != "flag.switched_on" {
		t.Fatalf("deliveries %s, want four, newest first", raw(all))
	}
	var paged []management.WebhookDelivery
	var cursor *string
	for range 4 {
		page := list(management.ListWebhookDeliveriesParams{Limit: ptr(3), Cursor: cursor})
		paged = append(paged, page.Items...)
		if cursor = page.NextCursor; cursor == nil {
			break
		}
	}
	if raw(paged) != raw(all.Items) {
		t.Errorf("pages of 3 = %s, want %s", raw(paged), raw(all.Items))
	}
	if got := list(management.ListWebhookDeliveriesParams{EventType: ptr("flag.created")}); len(got.Items) != 3 {
		t.Errorf("flag.created deliveries %s, want three", raw(got))
	}
	pending := management.WebhookDeliveryStatusPending
	if got := list(management.ListWebhookDeliveriesParams{Status: &pending}); len(got.Items) != 4 {
		t.Errorf("pending deliveries %s, want four", raw(got))
	}
	succeeded := management.WebhookDeliveryStatusSucceeded
	if got := list(management.ListWebhookDeliveriesParams{Status: &succeeded}); len(got.Items) != 0 {
		t.Errorf("succeeded deliveries %s, want none", raw(got))
	}
	before, after := start.Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	if got := list(management.ListWebhookDeliveriesParams{From: &before, To: &after}); len(got.Items) != 4 {
		t.Errorf("deliveries within the hour %s, want four", raw(got))
	}
	if got := list(management.ListWebhookDeliveriesParams{From: &after}); len(got.Items) != 0 {
		t.Errorf("deliveries from later %s, want none", raw(got))
	}

	for name, params := range map[string]management.ListWebhookDeliveriesParams{
		"unknown status": {Status: ptr(management.WebhookDeliveryStatus("lost"))},
		"foreign cursor": {Cursor: ptr("not-a-cursor")},
		"limit too big":  {Limit: ptr(201)},
	} {
		res := do(admin.ListWebhookDeliveriesWithResponse(t.Context(), p.Key, everything.Id, &params))
		if res.StatusCode() != http.StatusBadRequest {
			t.Errorf("%s: GET deliveries = %d %s, want 400", name, res.StatusCode(), res.Body)
		}
	}

	// A delivery is read under its own subscription only.
	d := all.Items[0].Id
	if res := do(admin.GetWebhookDeliveryWithResponse(t.Context(), p.Key, other.Id, d)); res.StatusCode() != http.StatusNotFound {
		t.Errorf("GET another subscription's delivery = %d, want 404", res.StatusCode())
	}
	elsewhere := createProject(t, admin, projectKey())
	if res := do(admin.GetWebhookDeliveryWithResponse(t.Context(), elsewhere.Key, everything.Id, d)); res.StatusCode() != http.StatusNotFound {
		t.Errorf("GET a delivery under another project = %d, want 404", res.StatusCode())
	}
	if res := do(admin.ListWebhookDeliveriesWithResponse(t.Context(), elsewhere.Key, everything.Id, nil)); res.StatusCode() != http.StatusNotFound {
		t.Errorf("GET deliveries under another project = %d, want 404", res.StatusCode())
	}
}

func TestADeletedEnvironmentReachesOnlyItsOwnFilter(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	staging := subscribe(t, admin, p.Key, "staging", []string{governance.EventAuditEntry}, "staging")
	development := subscribe(t, admin, p.Key, "development", []string{governance.EventAuditEntry}, "development")

	if res := do(admin.DeleteEnvironmentWithResponse(t.Context(), p.Key, "development",
		&management.DeleteEnvironmentParams{IfMatch: ifMatch(1)})); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE environment = %d: %s", res.StatusCode(), res.Body)
	}
	fanOut(t, srv)

	// Each receives the project-level entries of the subscriptions' creation
	// from its own on; only the development filter receives the deletion.
	if got := deliveredTypes(t, admin, p.Key, staging.Id); len(got) != 2 {
		t.Errorf("staging deliveries %v, want the two creations and not the deletion", got)
	}
	if got := deliveredTypes(t, admin, p.Key, development.Id); len(got) != 2 {
		t.Errorf("development deliveries %v, want its creation and the deletion", got)
	}
}
