package management_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// deliverAll runs delivery rounds until nothing is left due: a round stops
// claiming once every delivery due of a subscription is in flight, four at
// a time.
func deliverAll(t *testing.T, srv *testkit.Server, r *testkit.Receiver, calls int) {
	t.Helper()
	for range calls {
		if len(r.Calls()) >= calls {
			return
		}
		deliver(t, srv)
	}
}

// pings queues n test pings to the subscription.
func pings(t *testing.T, c *management.ClientWithResponses, project string, webhook openapi_types.UUID, n int) []management.WebhookDelivery {
	t.Helper()
	out := make([]management.WebhookDelivery, n)
	for i := range out {
		out[i] = ping(t, c, project, webhook)
	}
	return out
}

func enable(t *testing.T, c *management.ClientWithResponses, project string, webhook openapi_types.UUID) management.Webhook {
	t.Helper()
	res := do(c.EnableWebhookWithResponse(t.Context(), project, webhook))
	if res.JSON200 == nil {
		t.Fatalf("POST enable = %d: %s", res.StatusCode(), res.Body)
	}
	if got := res.HTTPResponse.Header.Get("ETag"); got != `"`+strconv.FormatInt(res.JSON200.Version, 10)+`"` {
		t.Errorf("enable ETag %q for version %d", got, res.JSON200.Version)
	}
	return *res.JSON200
}

func rotateSecret(t *testing.T, c *management.ClientWithResponses, project string, webhook openapi_types.UUID) management.WebhookWithSecret {
	t.Helper()
	res := do(c.RotateWebhookSecretWithResponse(t.Context(), project, webhook))
	if res.JSON200 == nil {
		t.Fatalf("POST rotate-secret = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func TestAPersistentlyFailingSubscriptionIsDisabledAndEnabledAgain(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	replies := make([]testkit.Reply, 25, 26)
	for i := range replies {
		replies[i] = testkit.Reply{Status: http.StatusInternalServerError, Body: "down"}
	}
	dead := testkit.StartReceiver(t, append(replies, testkit.Reply{})...)
	failing := receiving(t, admin, p.Key, dead, "flag.created")
	watcher := testkit.StartReceiver(t)
	watching := receiving(t, admin, p.Key, watcher, "webhook.disabled")

	// A day on, twenty-five attempts in a row fail.
	c.Advance(24 * time.Hour)
	queued := pings(t, admin, p.Key, failing.Id, 25)
	deliverAll(t, srv, dead, 25)

	disabled := subscription(t, admin, p.Key, failing.Id)
	if disabled.Enabled || disabled.DisabledReason == nil || *disabled.DisabledReason != management.WebhookDisabledReasonPersistentFailure ||
		disabled.ConsecutiveFailures != 25 {
		t.Fatalf("after twenty-five failures over a day the subscription is %+v", disabled)
	}
	if disabled.Version != 2 {
		t.Errorf("the disabled subscription is at version %d, want 2", disabled.Version)
	}
	for _, d := range queued {
		if got := deliveryOf(t, admin, p.Key, failing.Id, d.Id); got.Status != management.WebhookDeliveryStatusDropped ||
			got.NextAttemptAt != nil || len(got.Attempts) != 1 {
			t.Errorf("a pending ping is %+v, want dropped after its one attempt", got)
		}
	}

	entry, before, after := webhookEntry(t, srv, "webhook.disabled", failing)
	if entry.Actor.Kind != management.ActorKindSystem || before["enabled"] != true || after["enabled"] != false ||
		after["disabled_reason"] != "persistent_failure" || after["secret"] != "[redacted]" {
		t.Errorf("webhook.disabled entry by %+v: %v -> %v", entry.Actor, before, after)
	}

	// The project's other subscription is told; the disabled one is sent
	// nothing more.
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "after_disable", Name: "After disable", Type: management.FlagTypeBoolean})
	fanOut(t, srv)
	deliver(t, srv)
	calls := watcher.Calls()
	if len(calls) != 1 || calls[0].Header.Get("X-Flagnetic-Event-Type") != "webhook.disabled" || !calls[0].Verifies(watching.Secret) {
		t.Fatalf("the other subscription received %d calls, want one signed webhook.disabled", len(calls))
	}
	envelope := calls[0].Envelope(t)
	data, _ := envelope["data"].(map[string]any)
	entity, _ := envelope["entity"].(map[string]any)
	if data["subscription_id"] != failing.Id.String() || data["name"] != failing.Name || data["last_error"] != "the receiver answered 500" ||
		entity["kind"] != "webhook" || entity["id"] != failing.Id.String() || envelope["audit_entry_id"] != nil {
		t.Errorf("webhook.disabled envelope %v", envelope)
	}
	if body := string(calls[0].Body); strings.Contains(body, dead.URL) || strings.Contains(body, failing.Secret) {
		t.Errorf("webhook.disabled tells the other subscription the target or the secret: %s", body)
	}
	if n := len(dead.Calls()); n != 25 {
		t.Errorf("the disabled subscription was called %d times, want its twenty-five attempts", n)
	}
	if log := deliveredTypes(t, admin, p.Key, failing.Id); slices.Contains(log, "flag.created") {
		t.Errorf("the disabled subscription was queued %v", log)
	}

	// An edit cannot enable it, and nothing is sent to it meanwhile.
	on := true
	refused := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, failing.Id,
		&management.UpdateWebhookParams{IfMatch: ifMatch(disabled.Version)}, management.WebhookPatch{Enabled: &on}))
	assertProblem(t, "enable by an edit", refused.StatusCode(), refused.Body, http.StatusConflict, "webhook_disabled")
	stale := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, failing.Id,
		&management.UpdateWebhookParams{IfMatch: ifMatch(1)}, management.WebhookPatch{Name: ptr("Renamed")}))
	assertProblem(t, "an edit read before the disable", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	pinged := do(admin.TestWebhookWithResponse(t.Context(), p.Key, failing.Id))
	assertProblem(t, "ping a disabled subscription", pinged.StatusCode(), pinged.Body, http.StatusConflict, "webhook_disabled")

	enabled := enable(t, admin, p.Key, failing.Id)
	if !enabled.Enabled || enabled.DisabledReason != nil || enabled.ConsecutiveFailures != 0 || enabled.Version != 3 {
		t.Fatalf("enable answered %+v", enabled)
	}
	if _, before, after := webhookEntry(t, srv, "webhook.enabled", failing); before["enabled"] != false || after["enabled"] != true ||
		after["disabled_reason"] != nil {
		t.Errorf("webhook.enabled %v -> %v", before, after)
	}
	if again := enable(t, admin, p.Key, failing.Id); again.Version != enabled.Version {
		t.Errorf("enabling an enabled subscription moved it to version %d", again.Version)
	}
	webhookEntry(t, srv, "webhook.enabled", failing)

	// The receiver is back: what is queued from now on reaches it, and the
	// dropped pings stay dropped.
	resumed := ping(t, admin, p.Key, failing.Id)
	deliver(t, srv)
	if got := deliveryOf(t, admin, p.Key, failing.Id, resumed.Id); got.Status != management.WebhookDeliveryStatusSucceeded {
		t.Errorf("a ping after the enable is %+v", got)
	}
	if got := deliveryOf(t, admin, p.Key, failing.Id, queued[0].Id); got.Status != management.WebhookDeliveryStatusDropped {
		t.Errorf("the enable brought a dropped ping back as %s", got.Status)
	}
	if w := subscription(t, admin, p.Key, failing.Id); !w.Enabled || w.LastSuccessAt == nil || w.ConsecutiveFailures != 0 {
		t.Errorf("after a success the subscription is %+v", w)
	}
}

// Twenty-five failures alone disable nothing: a success, or the enable,
// within the day says the receiver is alive, however many attempts failed
// since.
func TestASubscriptionIsNotDisabledWithinADayOfItsEnable(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusServiceUnavailable})
	created := receiving(t, admin, p.Key, r)
	pings(t, admin, p.Key, created.Id, 25)

	c.Advance(time.Hour)
	deliverAll(t, srv, r, 25)
	if still := subscription(t, admin, p.Key, created.Id); !still.Enabled || still.ConsecutiveFailures != 25 {
		t.Fatalf("within the day the subscription is %+v, want enabled with twenty-five failures", still)
	}

	c.Advance(23 * time.Hour)
	deliverAll(t, srv, r, 50)
	if disabled := subscription(t, admin, p.Key, created.Id); disabled.Enabled || disabled.DisabledReason == nil {
		t.Errorf("a day after its enable the subscription is %+v, want disabled", disabled)
	}
}

func TestARotatedSecretSignsBesideTheNewOneUntilTheOverlapEnds(t *testing.T) {
	c := clock.NewFake(time.Now().UTC().Truncate(time.Second))
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	created := receiving(t, admin, p.Key, r)

	rotated := rotateSecret(t, admin, p.Key, created.Id)
	if rotated.Secret == "" || rotated.Secret == created.Secret || rotated.Version != 2 {
		t.Fatalf("rotation answered %+v", rotated)
	}
	if rotated.PreviousSecretExpiresAt == nil || !rotated.PreviousSecretExpiresAt.Equal(c.Now().Add(24*time.Hour)) {
		t.Errorf("previous_secret_expires_at = %v, want a day from now", rotated.PreviousSecretExpiresAt)
	}
	entry, _, after := webhookEntry(t, srv, "webhook.secret_rotated", created)
	if raw, _ := json.Marshal(entry); strings.Contains(string(raw), rotated.Secret) || after["secret"] != "[redacted]" {
		t.Errorf("webhook.secret_rotated records %s", raw)
	}

	ping(t, admin, p.Key, created.Id)
	deliver(t, srv)
	during := r.Calls()[0]
	if !during.Verifies(created.Secret) || !during.Verifies(rotated.Secret) ||
		len(strings.Split(during.Header.Get("X-Flagnetic-Signature"), ",")) != 2 {
		t.Errorf("during the overlap the call is signed %q, want both secrets", during.Header.Get("X-Flagnetic-Signature"))
	}

	c.Advance(24*time.Hour + time.Second)
	ping(t, admin, p.Key, created.Id)
	deliver(t, srv)
	after2 := r.Calls()[1]
	if after2.Verifies(created.Secret) || !after2.Verifies(rotated.Secret) {
		t.Errorf("after the overlap the call is signed %q, want the new secret alone", after2.Header.Get("X-Flagnetic-Signature"))
	}

	// A second rotation within the overlap of a first drops the oldest.
	second := rotateSecret(t, admin, p.Key, created.Id)
	third := rotateSecret(t, admin, p.Key, created.Id)
	ping(t, admin, p.Key, created.Id)
	deliver(t, srv)
	last := r.Calls()[2]
	if last.Verifies(rotated.Secret) || !last.Verifies(second.Secret) || !last.Verifies(third.Secret) {
		t.Errorf("after two rotations the call is signed %q, want the last two secrets", last.Header.Get("X-Flagnetic-Signature"))
	}
}

func TestTheRetentionRoundPrunesOldDeliveriesAndExpiredSecretsOnly(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusBadRequest}, testkit.Reply{})
	created := receiving(t, admin, p.Key, r)
	failed := ping(t, admin, p.Key, created.Id)
	deliver(t, srv)
	old := ping(t, admin, p.Key, created.Id)
	deliver(t, srv)
	rotateSecret(t, admin, p.Key, created.Id)
	// A pending delivery waits on a subscription a member disabled, however
	// long: it is sent once the subscription is enabled again.
	waiting := receiving(t, admin, p.Key, testkit.StartReceiver(t))
	held := ping(t, admin, p.Key, waiting.Id)
	off := false
	if res := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, waiting.Id,
		&management.UpdateWebhookParams{IfMatch: ifMatch(1)}, management.WebhookPatch{Enabled: &off})); res.JSON200 == nil {
		t.Fatalf("PATCH enabled false = %d: %s", res.StatusCode(), res.Body)
	}

	c.Advance(30*24*time.Hour + time.Second)
	recent := ping(t, admin, p.Key, created.Id)
	deliver(t, srv)
	entries := len(auditOf(t, srv, "webhook.secret_rotated", created.Id.String()))
	if err := srv.Tick(t, worker.WebhookRetentionName); err != nil {
		t.Fatalf("retention round: %v", err)
	}

	for _, gone := range []management.WebhookDelivery{failed, old} {
		res := do(admin.GetWebhookDeliveryWithResponse(t.Context(), p.Key, created.Id, gone.Id))
		if res.StatusCode() != http.StatusNotFound {
			t.Errorf("a delivery made thirty days ago answers %d, want 404", res.StatusCode())
		}
	}
	if got := deliveryOf(t, admin, p.Key, created.Id, recent.Id); got.Status != management.WebhookDeliveryStatusSucceeded {
		t.Errorf("the recent delivery is %+v", got)
	}
	if got := deliveryOf(t, admin, p.Key, waiting.Id, held.Id); got.Status != management.WebhookDeliveryStatusPending {
		t.Errorf("the old pending delivery is %+v, want it kept", got)
	}
	if w := subscription(t, admin, p.Key, created.Id); w.PreviousSecretExpiresAt != nil || w.Version != 2 {
		t.Errorf("after the retention round the subscription is %+v, want no previous secret at the same version", w)
	}
	if got := len(auditOf(t, srv, "webhook.secret_rotated", created.Id.String())); got != entries {
		t.Errorf("the retention round recorded %d entries", got-entries)
	}
}

// Two replicas pruning at once share the work under one lock: neither
// fails, and everything old is gone.
func TestTwoReplicasPruneAtOnce(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	pool := testkit.Pool(t)
	first := testkit.StartServer(t, reachable, testkit.WithClock(c), testkit.WithPool(pool))
	second := testkit.StartServer(t, reachable, testkit.WithClock(c), testkit.WithPool(pool))
	admin := testkit.Client(t, first, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := receiving(t, admin, p.Key, testkit.StartReceiver(t))
	sent := pings(t, admin, p.Key, created.Id, 5)
	deliver(t, first)

	c.Advance(31 * 24 * time.Hour)
	done := make(chan error, 2)
	for _, srv := range []*testkit.Server{first, second} {
		go func() { done <- srv.Tick(t, worker.WebhookRetentionName) }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Errorf("retention round: %v", err)
		}
	}
	if log := deliveries(t, admin, p.Key, created.Id, management.ListWebhookDeliveriesParams{}); len(log.Items) != 0 {
		t.Errorf("after both rounds the log holds %d of the %d deliveries", len(log.Items), len(sent))
	}
}
