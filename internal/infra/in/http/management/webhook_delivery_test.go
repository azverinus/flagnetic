package management_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// reachable lets a server deliver to a receiver the test runs on loopback
// over plain http.
var reachable = testkit.WithWebhookTargets(governance.TargetPolicy{AllowHTTP: true, AllowPrivateNetworks: true})

// deliver runs one round of the delivery worker of srv, which returns once
// every delivery due has been attempted.
func deliver(t *testing.T, srv *testkit.Server) {
	t.Helper()
	if err := srv.Tick(t, worker.WebhookDeliveryName); err != nil {
		t.Fatalf("delivery round: %v", err)
	}
}

// receiving creates a subscription of the project to the receiver.
func receiving(t *testing.T, c *management.ClientWithResponses, project string, r *testkit.Receiver, events ...string,
) management.WebhookWithSecret {
	t.Helper()
	if events == nil {
		events = []string{"*"}
	}
	return createWebhook(t, c, project, management.WebhookCreate{Name: "Local", Url: r.URL, Events: events})
}

// ping sends the subscription a test ping and returns its pending delivery.
func ping(t *testing.T, c *management.ClientWithResponses, project string, webhook openapi_types.UUID) management.WebhookDelivery {
	t.Helper()
	res := do(c.TestWebhookWithResponse(t.Context(), project, webhook))
	if res.JSON202 == nil {
		t.Fatalf("POST test = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON202
}

func deliveryOf(t *testing.T, c *management.ClientWithResponses, project string, webhook, d openapi_types.UUID) management.WebhookDelivery {
	t.Helper()
	res := do(c.GetWebhookDeliveryWithResponse(t.Context(), project, webhook, d))
	if res.JSON200 == nil {
		t.Fatalf("GET delivery = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func subscription(t *testing.T, c *management.ClientWithResponses, project string, webhook openapi_types.UUID) management.Webhook {
	t.Helper()
	res := do(c.GetWebhookWithResponse(t.Context(), project, webhook))
	if res.JSON200 == nil {
		t.Fatalf("GET webhook = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func TestATestPingReachesTheReceiverSigned(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t, testkit.Reply{Body: `{"ok":true}`, Delay: 5 * time.Millisecond})
	created := receiving(t, admin, p.Key, r, "flag.kill_switch")

	queued := ping(t, admin, p.Key, created.Id)
	if queued.Status != management.WebhookDeliveryStatusPending || queued.EventType != "ping" || len(queued.Attempts) != 0 {
		t.Fatalf("queued ping %+v", queued)
	}
	deliver(t, srv)

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("the receiver was called %d times, want once", len(calls))
	}
	call := calls[0]
	if !call.Verifies(created.Secret) {
		t.Errorf("the signature %q does not verify", call.Header.Get("X-Flagnetic-Signature"))
	}
	if tampered := (testkit.ReceivedCall{Header: call.Header, Body: append(bytes.Clone(call.Body), ' ')}); tampered.Verifies(created.Secret) {
		t.Error("the signature verifies a tampered body")
	}
	if call.Verifies("another secret") {
		t.Error("the signature verifies under another secret")
	}
	for header, want := range map[string]string{
		"Content-Type": "application/json", "X-Flagnetic-Event-Type": "ping", "X-Flagnetic-Delivery": queued.Id.String(),
		"X-Flagnetic-Event": queued.EventId.String(), "X-Flagnetic-Attempt": "1",
	} {
		if got := call.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if s, err := strconv.ParseInt(call.Header.Get("X-Flagnetic-Timestamp"), 10, 64); err != nil ||
		time.Since(time.Unix(s, 0)).Abs() > time.Minute {
		t.Errorf("X-Flagnetic-Timestamp %q", call.Header.Get("X-Flagnetic-Timestamp"))
	}
	envelope := call.Envelope(t)
	if envelope["id"] != queued.EventId.String() || envelope["type"] != "ping" || envelope["timestamp"] == nil {
		t.Errorf("envelope %v", envelope)
	}
	if project, _ := envelope["project"].(map[string]any); project["key"] != p.Key || project["id"] != p.Id.String() {
		t.Errorf("envelope project %v", envelope["project"])
	}
	if actor, _ := envelope["actor"].(map[string]any); actor["kind"] == nil {
		t.Errorf("envelope actor %v, want the caller", envelope["actor"])
	}
	for _, k := range []string{"environment", "entity", "audit_entry_id"} {
		if v, present := envelope[k]; !present || v != nil {
			t.Errorf("envelope %s = %v, want null", k, v)
		}
	}

	settled := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if settled.Status != management.WebhookDeliveryStatusSucceeded || settled.NextAttemptAt != nil || len(settled.Attempts) != 1 {
		t.Fatalf("settled ping %+v", settled)
	}
	a := settled.Attempts[0]
	if a.N != 1 || a.StatusCode == nil || *a.StatusCode != 200 || a.Error != nil || a.ResponseSnippet == nil ||
		*a.ResponseSnippet != `{"ok":true}` {
		t.Errorf("attempt %+v", a)
	}
	// The receiver held the call, so a latency of zero would be an attempt
	// never timed.
	if a.LatencyMs < 5 {
		t.Errorf("latency_ms = %d, want at least the 5 ms the receiver took", a.LatencyMs)
	}
	if w := subscription(t, admin, p.Key, created.Id); w.LastSuccessAt == nil || w.ConsecutiveFailures != 0 {
		t.Errorf("after a success the subscription is %+v", w)
	}
}

// What the fan-out queued for a change reaches the receiver as the envelope
// it was written as: `audit.entry` carries the entry exactly as the audit
// log answers it.
func TestAChangeReachesTheReceiverAsItsAuditEntry(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	created := receiving(t, admin, p.Key, r, "audit.entry", "flag.created")

	booleanFlag(t, admin, p.Key, "checkout")
	fanOut(t, srv)
	deliver(t, srv)

	// The subscription's own creation is an audit entry too, received before
	// the flag's.
	calls := r.Calls()
	if len(calls) != 3 {
		t.Fatalf("the receiver was called %d times, want the subscription's audit.entry, flag.created and its audit.entry", len(calls))
	}
	var flagCreated, entry map[string]any
	for _, c := range calls {
		if !c.Verifies(created.Secret) {
			t.Errorf("a %s call does not verify", c.Header.Get("X-Flagnetic-Event-Type"))
		}
		if c.Header.Get("X-Flagnetic-Event-Type") == "flag.created" {
			flagCreated = c.Envelope(t)
		}
	}
	for _, c := range calls {
		if e := c.Envelope(t); e["type"] == "audit.entry" && flagCreated != nil && e["audit_entry_id"] == flagCreated["audit_entry_id"] {
			entry = e
		}
	}
	if entry == nil {
		t.Fatalf("no audit.entry names the entry of flag.created %v", flagCreated)
	}
	entryID, _ := entry["audit_entry_id"].(string)
	read := do(admin.GetProjectAuditEntryWithResponse(t.Context(), p.Key, entryID))
	var want any
	if err := json.Unmarshal(read.Body, &want); err != nil {
		t.Fatalf("GET audit entry = %d: %s", read.StatusCode(), read.Body)
	}
	if raw(entry["data"]) != raw(want) {
		t.Errorf("audit.entry data %s, want the entry as read %s", raw(entry["data"]), raw(want))
	}
}

func TestADeliveryIsRetriedWithBackoffUntilItSucceeds(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t, testkit.Reply{Status: 500, Body: "unwell"}, testkit.Reply{Drop: true}, testkit.Reply{})
	created := receiving(t, admin, p.Key, r)
	queued := ping(t, admin, p.Key, created.Id)

	deliver(t, srv)
	pending := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if pending.Status != management.WebhookDeliveryStatusPending || pending.NextAttemptAt == nil {
		t.Fatalf("after a 500 the delivery is %+v", pending)
	}
	if wait := pending.NextAttemptAt.Sub(c.Now()); wait < 8*time.Second || wait > 12*time.Second {
		t.Errorf("the first retry waits %v, want 10 s within 20 %%", wait)
	}
	deliver(t, srv)
	if n := len(r.Calls()); n != 1 {
		t.Fatalf("the receiver was called %d times before the retry was due, want once", n)
	}
	c.Advance(13 * time.Second)
	deliver(t, srv)
	dropped := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if dropped.Status != management.WebhookDeliveryStatusPending || len(dropped.Attempts) != 2 ||
		dropped.Attempts[1].StatusCode != nil || dropped.Attempts[1].Error == nil {
		t.Fatalf("after a dropped connection the delivery is %+v", dropped)
	}
	if wait := dropped.NextAttemptAt.Sub(c.Now()); wait < 48*time.Second || wait > 72*time.Second {
		t.Errorf("the second retry waits %v, want a minute within 20 %%", wait)
	}
	c.Advance(73 * time.Second)
	deliver(t, srv)

	settled := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if settled.Status != management.WebhookDeliveryStatusSucceeded || settled.NextAttemptAt != nil || len(settled.Attempts) != 3 {
		t.Fatalf("delivery %+v", settled)
	}
	if a := settled.Attempts[0]; a.StatusCode == nil || *a.StatusCode != 500 || a.ResponseSnippet == nil || *a.ResponseSnippet != "unwell" {
		t.Errorf("first attempt %+v", a)
	}
	for i, a := range settled.Attempts {
		if a.N != i+1 || (i > 0 && !a.At.After(settled.Attempts[i-1].At)) {
			t.Errorf("attempt %d is %+v", i+1, a)
		}
	}
	// Every call carried the same delivery and event and a rising attempt
	// number, so a receiver deduplicating on the event sees one event.
	for i, call := range r.Calls() {
		if call.Header.Get("X-Flagnetic-Delivery") != queued.Id.String() || call.Header.Get("X-Flagnetic-Event") != queued.EventId.String() ||
			call.Header.Get("X-Flagnetic-Attempt") != strconv.Itoa(i+1) || !call.Verifies(created.Secret) {
			t.Errorf("call %d carried %v", i+1, call.Header)
		}
	}
	if w := subscription(t, admin, p.Key, created.Id); w.ConsecutiveFailures != 0 || w.LastSuccessAt == nil {
		t.Errorf("after the success the subscription is %+v", w)
	}
}

func TestADeliveryFailsWithoutARetryOnARefusal(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusBadRequest, Body: "no such hook"})
	created := receiving(t, admin, p.Key, r)
	queued := ping(t, admin, p.Key, created.Id)

	deliver(t, srv)
	c.Advance(time.Hour)
	deliver(t, srv)

	settled := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if settled.Status != management.WebhookDeliveryStatusFailed || settled.NextAttemptAt != nil || len(settled.Attempts) != 1 ||
		len(r.Calls()) != 1 {
		t.Fatalf("a 400 settled as %+v after %d calls", settled, len(r.Calls()))
	}
	if w := subscription(t, admin, p.Key, created.Id); w.ConsecutiveFailures != 1 || w.LastSuccessAt != nil {
		t.Errorf("after one failure the subscription is %+v", w)
	}
}

func TestADeliveryIsExhaustedAfterEightAttempts(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusServiceUnavailable})
	created := receiving(t, admin, p.Key, r)
	queued := ping(t, admin, p.Key, created.Id)

	for range 10 {
		deliver(t, srv)
		c.Advance(15 * time.Hour)
	}

	settled := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if settled.Status != management.WebhookDeliveryStatusExhausted || settled.NextAttemptAt != nil || len(settled.Attempts) != 8 {
		t.Fatalf("delivery %+v", settled)
	}
	if n := len(r.Calls()); n != 8 {
		t.Errorf("the receiver was called %d times, want the eight attempts", n)
	}
	if w := subscription(t, admin, p.Key, created.Id); w.ConsecutiveFailures != 8 {
		t.Errorf("consecutive_failures = %d after eight failed attempts", w.ConsecutiveFailures)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	elsewhere := testkit.StartReceiver(t)
	r := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusFound, Redirect: elsewhere.URL})
	created := receiving(t, admin, p.Key, r)
	queued := ping(t, admin, p.Key, created.Id)

	deliver(t, srv)

	settled := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if settled.Status != management.WebhookDeliveryStatusFailed || len(settled.Attempts) != 1 {
		t.Fatalf("delivery %+v", settled)
	}
	if code := settled.Attempts[0].StatusCode; code == nil || *code != http.StatusFound {
		t.Errorf("the attempt recorded %v, want the 302 itself", code)
	}
	if n := len(elsewhere.Calls()); n != 0 {
		t.Errorf("the redirect's target was called %d times", n)
	}
}

// What a name resolves to can change after a subscription is written, so
// the address is checked again at every connection. Here the subscription
// is written on a replica allowing private networks and delivered by one
// that does not.
func TestAPrivateTargetIsRefusedAtEveryConnection(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	allowing := testkit.StartServer(t, reachable, testkit.WithClock(c))
	refusing := testkit.StartServer(t, testkit.WithWebhookTargets(governance.TargetPolicy{AllowHTTP: true}), testkit.WithClock(c))
	admin := testkit.Client(t, allowing, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	created := receiving(t, admin, p.Key, r)
	queued := ping(t, admin, p.Key, created.Id)

	deliver(t, refusing)

	if n := len(r.Calls()); n != 0 {
		t.Fatalf("a replica refusing private networks reached the receiver %d times", n)
	}
	refused := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if refused.Status != management.WebhookDeliveryStatusPending || len(refused.Attempts) != 1 {
		t.Fatalf("delivery %+v, want one refused attempt and another due", refused)
	}
	if a := refused.Attempts[0]; a.StatusCode != nil || a.Error == nil || !strings.Contains(*a.Error, "private") {
		t.Errorf("attempt %+v, want one naming the refused address", a)
	}

	c.Advance(15 * time.Second)
	deliver(t, allowing)
	if delivered := deliveryOf(t, admin, p.Key, created.Id, queued.Id); delivered.Status != management.WebhookDeliveryStatusSucceeded ||
		len(delivered.Attempts) != 2 || len(r.Calls()) != 1 {
		t.Errorf("on the allowing replica the delivery is %+v", delivered)
	}
}

// Disabling a subscription stops its deliveries, not only the queueing of
// new ones: one queued while it was enabled waits, and a ping is refused
// rather than left waiting.
func TestADisabledSubscriptionIsDeliveredNothing(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	created := receiving(t, admin, p.Key, r)
	queued := ping(t, admin, p.Key, created.Id)
	setEnabled := func(version int64, enabled bool) {
		t.Helper()
		res := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id,
			&management.UpdateWebhookParams{IfMatch: ifMatch(version)}, management.WebhookPatch{Enabled: &enabled}))
		if res.JSON200 == nil || res.JSON200.Enabled != enabled {
			t.Fatalf("PATCH enabled %v = %d: %s", enabled, res.StatusCode(), res.Body)
		}
	}
	setEnabled(1, false)

	deliver(t, srv)
	if n := len(r.Calls()); n != 0 {
		t.Errorf("a disabled subscription was called %d times", n)
	}
	if waiting := deliveryOf(t, admin, p.Key, created.Id, queued.Id); waiting.Status != management.WebhookDeliveryStatusPending ||
		len(waiting.Attempts) != 0 {
		t.Errorf("the queued ping is %+v, want it pending and unattempted", waiting)
	}
	refused := do(admin.TestWebhookWithResponse(t.Context(), p.Key, created.Id))
	assertProblem(t, "ping a disabled subscription", refused.StatusCode(), refused.Body, http.StatusConflict, "webhook_disabled")

	setEnabled(2, true)
	deliver(t, srv)
	if delivered := deliveryOf(t, admin, p.Key, created.Id, queued.Id); delivered.Status != management.WebhookDeliveryStatusSucceeded ||
		len(r.Calls()) != 1 {
		t.Errorf("once enabled the waiting ping is %+v", delivered)
	}
}

// A redelivery sends the same event, byte for byte, under a new delivery,
// so a receiver deduplicating on the event id recognises it.
func TestARedeliveryIsTheSameEventUnderANewDelivery(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusGone}, testkit.Reply{})
	created := receiving(t, admin, p.Key, r)
	first := ping(t, admin, p.Key, created.Id)
	deliver(t, srv)

	res := do(admin.RedeliverWebhookDeliveryWithResponse(t.Context(), p.Key, created.Id, first.Id))
	if res.JSON202 == nil {
		t.Fatalf("POST redeliver = %d: %s", res.StatusCode(), res.Body)
	}
	again := *res.JSON202
	if again.Id == first.Id || again.EventId != first.EventId || again.EventType != "ping" ||
		again.Status != management.WebhookDeliveryStatusPending || len(again.Attempts) != 0 {
		t.Fatalf("redelivery %+v of %+v", again, first)
	}
	deliver(t, srv)

	calls := r.Calls()
	if len(calls) != 2 || !bytes.Equal(calls[0].Body, calls[1].Body) {
		t.Fatalf("the receiver got %d calls, the bodies equal: %v", len(calls), len(calls) == 2 && bytes.Equal(calls[0].Body, calls[1].Body))
	}
	if calls[1].Header.Get("X-Flagnetic-Event") != first.EventId.String() || calls[1].Header.Get("X-Flagnetic-Delivery") != again.Id.String() ||
		calls[1].Header.Get("X-Flagnetic-Attempt") != "1" || !calls[1].Verifies(created.Secret) {
		t.Errorf("the redelivery carried %v", calls[1].Header)
	}
	if original := deliveryOf(t, admin, p.Key, created.Id, first.Id); original.Status != management.WebhookDeliveryStatusFailed {
		t.Errorf("the redelivered delivery became %+v", original)
	}
	if got := deliveryOf(t, admin, p.Key, created.Id, again.Id); got.Status != management.WebhookDeliveryStatusSucceeded {
		t.Errorf("the redelivery is %+v", got)
	}

	other := receiving(t, admin, p.Key, r)
	for name, res := range map[string]*management.RedeliverWebhookDeliveryResponse{
		"an unknown delivery":             do(admin.RedeliverWebhookDeliveryWithResponse(t.Context(), p.Key, created.Id, openapi_types.UUID{1})),
		"another subscription's delivery": do(admin.RedeliverWebhookDeliveryWithResponse(t.Context(), p.Key, other.Id, first.Id)),
	} {
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
	}
}

func TestTwoReplicasDeliverEachEventOnce(t *testing.T) {
	first, second := testkit.StartServer(t, reachable), testkit.StartServer(t, reachable)
	admin := testkit.Client(t, first, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	created := receiving(t, admin, p.Key, r)
	const events = 24
	for range events {
		ping(t, admin, p.Key, created.Id)
	}

	var wg sync.WaitGroup
	for _, srv := range []*testkit.Server{first, second} {
		wg.Go(func() { deliver(t, srv) })
	}
	wg.Wait()

	seen := map[string]int{}
	for _, c := range r.Calls() {
		seen[c.Header.Get("X-Flagnetic-Event")]++
	}
	if len(seen) != events {
		t.Errorf("the receiver saw %d distinct events, want %d", len(seen), events)
	}
	for e, n := range seen {
		if n != 1 {
			t.Errorf("event %s was delivered %d times", e, n)
		}
	}
	for _, d := range deliveries(t, admin, p.Key, created.Id, management.ListWebhookDeliveriesParams{Limit: ptr(100)}).Items {
		if d.Status != management.WebhookDeliveryStatusSucceeded || len(d.Attempts) != 1 {
			t.Errorf("delivery %s is %s after %d attempts", d.Id, d.Status, len(d.Attempts))
		}
	}
}

func TestAtMostFourDeliveriesOfASubscriptionAreInFlightAcrossReplicas(t *testing.T) {
	first, second := testkit.StartServer(t, reachable), testkit.StartServer(t, reachable)
	admin := testkit.Client(t, first, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	release := make(chan struct{})
	r := testkit.StartReceiver(t, testkit.Reply{Release: release})
	created := receiving(t, admin, p.Key, r)
	for range 6 {
		ping(t, admin, p.Key, created.Id)
	}

	var wg sync.WaitGroup
	for _, srv := range []*testkit.Server{first, second} {
		wg.Go(func() { deliver(t, srv) })
	}
	for range governance.MaxDeliveriesInFlight {
		r.AwaitCall(t)
	}
	// Both replicas have claimed by now or are about to; neither may add a
	// fifth call while four are held.
	time.Sleep(200 * time.Millisecond)
	if n := len(r.Calls()); n != governance.MaxDeliveriesInFlight {
		t.Errorf("%d calls in flight at once, want %d", n, governance.MaxDeliveriesInFlight)
	}
	close(release)
	wg.Wait()
	if n := len(r.Calls()); n != 6 {
		t.Errorf("the receiver was called %d times once released, want all six", n)
	}
}

// An attempt is recorded by the replica holding the delivery's lease and by
// nobody else. A receiver slow enough to hold an attempt past its lease
// lets another replica take the delivery and settle it; the replica still
// waiting must not write its outcome over that, which would put an accepted
// delivery back into pending and count a failure against the subscription.
func TestAnAttemptThatOutlivesItsLeaseIsNotRecorded(t *testing.T) {
	c := clock.NewFake(time.Now().UTC())
	first := testkit.StartServer(t, reachable, testkit.WithClock(c))
	second := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, first, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	release := make(chan struct{})
	r := testkit.StartReceiver(t, testkit.Reply{Status: 500, Release: release}, testkit.Reply{})
	created := receiving(t, admin, p.Key, r)
	queued := ping(t, admin, p.Key, created.Id)

	slow := make(chan struct{})
	go func() {
		defer close(slow)
		deliver(t, first)
	}()
	r.AwaitCall(t)
	// The lease runs out while that attempt waits, so the second replica
	// finds the delivery due and takes it.
	c.Advance(governance.DeliveryTimeout + 2*time.Minute)
	deliver(t, second)
	close(release)
	<-slow

	if n := len(r.Calls()); n != 2 {
		t.Fatalf("the receiver was called %d times, want once by each replica", n)
	}
	settled := deliveryOf(t, admin, p.Key, created.Id, queued.Id)
	if settled.Status != management.WebhookDeliveryStatusSucceeded || len(settled.Attempts) != 1 ||
		*settled.Attempts[0].StatusCode != 200 {
		t.Fatalf("delivery %+v, want only the second replica's success", settled)
	}
	if w := subscription(t, admin, p.Key, created.Id); w.ConsecutiveFailures != 0 || w.LastSuccessAt == nil {
		t.Errorf("the subscription is %+v after a success the late replica must not undo", w)
	}
	if len(first.Logs.Find("webhook delivery attempt not recorded: the lease was lost")) != 1 {
		t.Error("the late replica did not log the lost lease")
	}
}

func TestDeliveryOutcomesAreCounted(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	accepting := receiving(t, admin, p.Key, testkit.StartReceiver(t))
	refusing := receiving(t, admin, p.Key, testkit.StartReceiver(t, testkit.Reply{Status: http.StatusBadRequest}))
	before := srv.Scrape(t)
	ping(t, admin, p.Key, accepting.Id)
	ping(t, admin, p.Key, refusing.Id)

	deliver(t, srv)

	after := srv.Scrape(t)
	for _, status := range []string{"succeeded", "failed"} {
		series := `flagnetic_webhook_deliveries_total{status="` + status + `"}`
		// A series shows once it first moves, so its absence counts as 0.
		if got := testkit.Metric(t, after, series) - max(testkit.Metric(t, before, series), 0); got != 1 {
			t.Errorf("%s moved by %v, want 1", series, got)
		}
	}
}
