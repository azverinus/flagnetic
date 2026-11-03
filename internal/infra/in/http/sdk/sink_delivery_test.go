package sdk_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// sinkTargets lets the server deliver to receivers the tests run on loopback
// over plain http.
var sinkTargets = testkit.WithWebhookTargets(governance.TargetPolicy{AllowHTTP: true, AllowPrivateNetworks: true})

// delivering is a project whose events the tests export to receivers: the
// rollup fixture's project and clock, on a server that may call loopback.
type delivering struct{ *rollupFixture }

func startDelivering(t *testing.T, opts ...testkit.ServerOption) delivering {
	t.Helper()
	c := clock.NewFake(protocolTime)
	srv := testkit.StartServer(t, append([]testkit.ServerOption{testkit.WithClock(c), sinkTargets}, opts...)...)
	p := newProject(t, srv)
	p.flag(t, "checkout")
	f := exposuresFixture{srv: srv, clock: c, p: p, experiment: p.experiment(t, "checkout_test", "checkout", "user", "true", 5000, "false", 5000)}
	_, f.client = p.sdkKey(t, "production", management.SdkKeyKindClient)
	_, f.server = p.sdkKey(t, "production", management.SdkKeyKindServer)
	return delivering{&rollupFixture{exposuresFixture: f}}
}

// webhook is a webhook sink of kinds to the receiver, in the project's
// production unless the changes say otherwise.
func webhook(r *testkit.Receiver, kinds ...string) management.SinkCreate {
	body := management.SinkCreate{
		Name: "Warehouse", Kind: "webhook", Webhook: &management.SinkWebhookInput{Url: ptr(r.URL)},
	}
	if len(kinds) > 0 {
		body.EventKinds = &kinds
	}
	return body
}

// sinkSecret is the secret a webhook sink was created or rotated with.
type sinkSecret = string

func (f delivering) create(t *testing.T, body management.SinkCreate) (management.Sink, sinkSecret) {
	t.Helper()
	res := do(f.p.admin.CreateSinkWithResponse(t.Context(), f.p.key, body))
	if res.JSON201 == nil {
		t.Fatalf("POST sinks = %d: %s", res.StatusCode(), res.Body)
	}
	secret := ""
	if res.JSON201.Secret != nil {
		secret = *res.JSON201.Secret
		testkit.RecordSecret(secret)
	}
	return management.Sink{Id: res.JSON201.Id, Version: res.JSON201.Version}, secret
}

func (f delivering) enable(t *testing.T, sink management.Sink, body management.SinkEnable) management.Sink {
	t.Helper()
	// A stream starts at the horizon: what was committed before the call is
	// below it only once the transactions running meanwhile have ended.
	f.srv.AwaitHorizon(t)
	res := do(f.p.admin.EnableSinkWithResponse(t.Context(), f.p.key, sink.Id, body))
	if res.JSON200 == nil {
		t.Fatalf("POST enable = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// enabled creates a sink and enables it.
func (f delivering) enabled(t *testing.T, body management.SinkCreate) (management.Sink, sinkSecret) {
	t.Helper()
	created, secret := f.create(t, body)
	return f.enable(t, created, management.SinkEnable{}), secret
}

func (f delivering) sink(t *testing.T, sink management.Sink) management.Sink {
	t.Helper()
	res := do(f.p.admin.GetSinkWithResponse(t.Context(), f.p.key, sink.Id))
	if res.JSON200 == nil {
		t.Fatalf("GET sink = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func (f delivering) patch(t *testing.T, sink management.Sink, body management.SinkPatch) management.Sink {
	t.Helper()
	current := f.sink(t, sink)
	res := do(f.p.admin.UpdateSinkWithResponse(t.Context(), f.p.key, sink.Id, &management.UpdateSinkParams{IfMatch: ifMatchOf(current.Version)}, body))
	if res.JSON200 == nil {
		t.Fatalf("PATCH sink = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// tick runs one round of the sink delivery worker of srv, which returns once
// every attempt it started has been recorded.
func tick(t *testing.T, srv *testkit.Server) {
	t.Helper()
	// A stream reads only below the snapshot horizon, which a transaction of
	// another test holds back while it runs.
	srv.AwaitHorizon(t)
	if err := srv.Tick(t, worker.SinkDeliveryName); err != nil {
		t.Fatalf("sink delivery round: %v", err)
	}
}

func streamOf(s management.Sink, kind management.SinkEventKind) *management.SinkStream {
	for i := range s.Streams {
		if s.Streams[i].EventKind == kind {
			return &s.Streams[i]
		}
	}
	return nil
}

// batchOf is a call to a webhook sink, its signature verified under secret as
// a receiver written from the documentation verifies it.
type batchOf struct {
	DeliveryID string           `json:"delivery_id"`
	SinkID     string           `json:"sink_id"`
	ProjectKey string           `json:"project_key"`
	EventKind  string           `json:"event_kind"`
	Sample     bool             `json:"sample"`
	Version    int              `json:"schema_version"`
	Events     []map[string]any `json:"events"`
}

func decodeBatch(t *testing.T, call testkit.ReceivedCall, secret string) batchOf {
	t.Helper()
	if !call.Verifies(secret) {
		t.Fatalf("the signature does not verify: %v, body %s", call.Header, call.Body)
	}
	var b batchOf
	if err := json.Unmarshal(call.Body, &b); err != nil {
		t.Fatalf("a webhook sink's body: %v: %s", err, call.Body)
	}
	if got := call.Header.Get("X-Flagnetic-Delivery"); got != b.DeliveryID || got == "" {
		t.Errorf("X-Flagnetic-Delivery = %q, body delivery_id = %q, want the same and set", got, b.DeliveryID)
	}
	return b
}

func idsOf(events []map[string]any) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i], _ = e["event_id"].(string)
	}
	return out
}

func eventIDOf(e any) string { return e.(map[string]any)["event_id"].(string) }

func TestAWebhookSinkDeliversWhatArrivesAfterItIsEnabledSignedAndInOrder(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	before := f.exposure("u-0", "true", f.clock.Now())
	f.post(t, before)

	created, secret := f.enabled(t, webhook(r, "exposure"))
	e1, e2, e3 := f.exposure("u-1", "true", f.clock.Now()), f.exposure("u-2", "false", f.clock.Now()), f.exposure("u-3", "true", f.clock.Now())
	f.post(t, e1, e2, e3)
	tick(t, f.srv)

	calls := batchCalls(r)
	if len(calls) != 1 {
		t.Fatalf("the receiver got %d calls, want one batch", len(calls))
	}
	b := decodeBatch(t, calls[0], secret)
	if b.Version != 1 || b.SinkID != created.Id.String() || b.ProjectKey != f.p.key || b.EventKind != "exposure" || b.Sample {
		t.Errorf("envelope %+v", b)
	}
	if got, want := idsOf(b.Events), []string{eventIDOf(e1), eventIDOf(e2), eventIDOf(e3)}; !slices.Equal(got, want) {
		t.Errorf("events %v, want %v in the order they were received, and not the one received before the sink was enabled", got, want)
	}
	if got := b.Events[0]; got["unit_id"] != "u-1" || got["project_key"] != f.p.key || got["environment_key"] != "production" || got["schema_version"] != float64(1) {
		t.Errorf("first event %v, want the export schema's row", got)
	}
	for header, want := range map[string]string{"Content-Type": "application/json"} {
		if got := calls[0].Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if call := calls[0]; call.Verifies("another secret") {
		t.Error("the signature verifies under another secret")
	}
	// A receiver refuses a call signed at a time far from its own clock, so
	// every call is signed now, the check enabling the sink made included.
	for _, call := range r.Calls() {
		if got, want := call.Header.Get("X-Flagnetic-Timestamp"), strconv.FormatInt(f.clock.Now().Unix(), 10); got != want {
			t.Errorf("X-Flagnetic-Timestamp = %q, want %q: the time of the call", got, want)
		}
	}

	got := f.sink(t, created)
	s := streamOf(got, management.SinkEventKind("exposure"))
	if got.Health != management.SinkHealthHealthy || got.LagSeconds == nil || *got.LagSeconds != 0 || len(got.Streams) != 1 ||
		s == nil || s.LastDeliveredAt == nil || s.LastFailure != nil {
		t.Errorf("sink after the delivery %+v", got)
	}
}

func TestASinkStreamFailsThenRecoversOnceTheReceiverAnswers(t *testing.T) {
	f := startDelivering(t)
	// The first call answers the check enabling the sink makes, the second fails.
	r := testkit.StartReceiver(t, testkit.Reply{}, testkit.Reply{Status: http.StatusInternalServerError, Body: "do not read this"}, testkit.Reply{})
	created, secret := f.enabled(t, webhook(r, "exposure"))
	e1 := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, e1)

	tick(t, f.srv)
	failing := f.sink(t, created)
	s := streamOf(failing, "exposure")
	if failing.Health != management.SinkHealthFailing || s == nil || s.LastFailure == nil || s.LastDeliveredAt != nil {
		t.Fatalf("sink after a 500 %+v, want failing", failing)
	}
	if lf := s.LastFailure; lf.Class != management.SinkFailureClass("status") || lf.Status == nil || *lf.Status != 500 || lf.Code != nil {
		t.Errorf("last failure %+v, want class status, 500 and no code", lf)
	}
	if strings.Contains(fmt.Sprintf("%+v", failing), "do not read this") {
		t.Error("the receiver's own words were answered")
	}

	tick(t, f.srv)
	if n := len(batchCalls(r)); n != 1 {
		t.Fatalf("the receiver got %d calls before the backoff ran out, want 1", n)
	}
	f.clock.Advance(2 * time.Second)
	tick(t, f.srv)
	healthy := f.sink(t, created)
	if healthy.Health != management.SinkHealthHealthy || streamOf(healthy, "exposure").LastFailure != nil || streamOf(healthy, "exposure").LastDeliveredAt == nil {
		t.Errorf("sink once the receiver answers %+v, want healthy", healthy)
	}
	calls := batchCalls(r)
	if len(calls) != 2 {
		t.Fatalf("the receiver got %d calls, want the failed attempt and the retry", len(calls))
	}
	first, second := decodeBatch(t, calls[0], secret), decodeBatch(t, calls[1], secret)
	if first.DeliveryID != second.DeliveryID || !slices.Equal(idsOf(second.Events), []string{eventIDOf(e1)}) {
		t.Errorf("retry %+v, want the same delivery ID and events as the attempt %+v", second, first)
	}
}

func TestALostCursorCommitResendsTheSameDeliveryIDAndNewEventsGetANewOne(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, secret := f.enabled(t, webhook(r, "exposure"))
	e1, e2 := f.exposure("u-1", "true", f.clock.Now()), f.exposure("u-2", "true", f.clock.Now())
	f.post(t, e1, e2)

	// The receiver has the batch, and the replica dies before it records it.
	f.srv.LoseNextSinkCommit()
	tick(t, f.srv)
	if n := len(batchCalls(r)); n != 1 {
		t.Fatalf("the receiver got %d calls, want 1", n)
	}
	if s := streamOf(f.sink(t, created), "exposure"); s.LastDeliveredAt != nil {
		t.Error("the delivery was recorded despite the lost commit")
	}

	// A third event arrives, and the batch size is lowered: neither changes
	// what is resent, which the batch in flight fixes.
	e3 := f.exposure("u-3", "true", f.clock.Now())
	f.post(t, e3)
	f.patch(t, created, management.SinkPatch{BatchSize: ptr(1)})
	f.clock.Advance(5*time.Minute + time.Second)
	tick(t, f.srv)

	calls := batchCalls(r)
	if len(calls) != 3 {
		t.Fatalf("the receiver got %d calls once the lease ran out, want the resend and the third event's batch", len(calls))
	}
	first, resent, third := decodeBatch(t, calls[0], secret), decodeBatch(t, calls[1], secret), decodeBatch(t, calls[2], secret)
	if resent.DeliveryID != first.DeliveryID || !slices.Equal(idsOf(resent.Events), []string{eventIDOf(e1), eventIDOf(e2)}) {
		t.Errorf("resend %+v, want the first batch under the same delivery ID", resent)
	}
	if third.DeliveryID == resent.DeliveryID || !slices.Equal(idsOf(third.Events), []string{eventIDOf(e3)}) {
		t.Errorf("third batch %+v, want the new event under a new delivery ID", third)
	}
}

func ifMatchOf(version int64) *management.IfMatch {
	v := fmt.Sprintf("\"%d\"", version)
	return &v
}

// batchCalls returns the calls the receiver got that carry a batch: enabling
// a sink calls its receiver once to check it, with no events.
func batchCalls(r *testkit.Receiver) []testkit.ReceivedCall {
	var out []testkit.ReceivedCall
	for _, c := range r.Calls() {
		var b struct {
			EventKind string `json:"event_kind"`
		}
		if json.Unmarshal(c.Body, &b) != nil || b.EventKind != "test" {
			out = append(out, c)
		}
	}
	return out
}

// postIn sends events with a server key of environment.
func (f delivering) postIn(t *testing.T, environment string, evs ...any) {
	t.Helper()
	_, key := f.p.sdkKey(t, environment, management.SdkKeyKindServer)
	if res := batchResult(t, f.srv, key, f.envelope(t, evs...)); res.Accepted != len(evs) {
		t.Fatalf("batch to %s = %+v, want all %d accepted", environment, res, len(evs))
	}
}

func (f delivering) alias(n int) map[string]any {
	return aliasEvent(eventID(f.clock.Now(), 5000+n))
}

func TestSinkFiltersRestrictEnvironmentAndEventKind(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	body := webhook(r, "alias")
	body.Environments = &[]string{"production"}
	_, secret := f.enabled(t, body)

	staging := f.alias(1)
	production := f.alias(2)
	f.postIn(t, "staging", f.exposure("u-1", "true", f.clock.Now()), staging)
	f.postIn(t, "production", f.exposure("u-2", "true", f.clock.Now()), production)
	tick(t, f.srv)

	calls := batchCalls(r)
	if len(calls) != 1 {
		t.Fatalf("the receiver got %d calls, want the production alias alone", len(calls))
	}
	b := decodeBatch(t, calls[0], secret)
	if b.EventKind != "alias" || !slices.Equal(idsOf(b.Events), []string{eventIDOf(production)}) {
		t.Errorf("batch %+v, want only the production alias: no staging row and no exposure", b)
	}
}

func TestSinkMetricsAreExportedAndNoSecretReachesLogsOrMetrics(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, testkit.Reply{}, testkit.Reply{Status: http.StatusInternalServerError}, testkit.Reply{})
	created, secret := f.enabled(t, webhook(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))

	tick(t, f.srv)
	f.clock.Advance(2 * time.Second)
	tick(t, f.srv)

	scrape := f.srv.Scrape(t)
	sink := created.Id.String()
	for result, want := range map[string]float64{"transient": 1, "ack": 1, "rejected": 0} {
		series := `flagnetic_sink_batches_total{event_kind="exposure",result="` + result + `",sink="` + sink + `"}`
		if got := testkit.Metric(t, scrape, series); got != want && !(want == 0 && got == -1) {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
	if lag := testkit.Metric(t, scrape, `flagnetic_sink_lag_seconds{event_kind="exposure",sink="`+sink+`"}`); lag != 0 {
		t.Errorf("flagnetic_sink_lag_seconds = %v once caught up, want 0", lag)
	}
	if strings.Contains(scrape, secret) {
		t.Error("the metrics hold the sink's secret")
	}
	for _, rec := range f.srv.Logs.Records() {
		if strings.Contains(fmt.Sprintf("%v", rec), secret) {
			t.Fatalf("the server logged the sink's secret: %+v", rec)
		}
	}
}

func TestALateIngestCommitIsNotSkippedByASinkThatReadPastIt(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, secret := f.enabled(t, webhook(r, "exposure"))
	now := f.clock.Now()

	written, release := f.srv.HoldNextIngest(t)
	held := f.exposure("u-1", "true", now)
	first := make(chan answer, 1)
	go func() { first <- postEvents(t, f.srv, f.server, f.envelope(t, held)) }()
	<-written
	after := f.exposure("u-2", "true", now)
	f.post(t, after)

	tick(t, f.srv)
	if n := len(batchCalls(r)); n != 0 {
		t.Fatalf("the receiver got %d calls while an older transaction is open, want none: the horizon holds the younger batch back too", n)
	}
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if lag := f.sink(t, created).LagSeconds; lag == nil || *lag < 60 {
		t.Errorf("lag_seconds = %v behind a transaction held open for a minute, want at least 60", lag)
	}
	series := `flagnetic_sink_lag_seconds{event_kind="exposure",sink="` + created.Id.String() + `"}`
	if lag := testkit.Metric(t, f.srv.Scrape(t), series); lag < 60 {
		t.Errorf("%s = %v behind a transaction held open for a minute, want at least 60", series, lag)
	}

	release()
	if a := <-first; a.status != http.StatusAccepted {
		t.Fatalf("the held batch = %d: %s", a.status, a.body)
	}
	f.clock.Advance(10 * time.Second)
	tick(t, f.srv)
	calls := batchCalls(r)
	if len(calls) != 1 {
		t.Fatalf("the receiver got %d calls once the transaction committed, want 1", len(calls))
	}
	if got, want := idsOf(decodeBatch(t, calls[0], secret).Events), []string{eventIDOf(held), eventIDOf(after)}; !slices.Equal(got, want) {
		t.Errorf("events %v, want %v: the late commit first, as it was ordered", got, want)
	}
}

func TestTwoReplicasDeliverEveryEventOnceInOrderWithNoOverlappingBatch(t *testing.T) {
	f := startDelivering(t)
	other := testkit.StartServer(t, testkit.WithClock(f.clock), sinkTargets)
	r := testkit.StartReceiver(t)
	body := webhook(r, "exposure")
	body.BatchSize = ptr(3)
	created, secret := f.enabled(t, body)

	var want []any
	for i := range 20 {
		e := f.exposure(fmt.Sprintf("u-%d", i), "true", f.clock.Now())
		want = append(want, e)
		f.post(t, e)
	}
	for range 12 {
		var wg sync.WaitGroup
		for _, srv := range []*testkit.Server{f.srv, other} {
			wg.Go(func() { tick(t, srv) })
		}
		wg.Wait()
		f.clock.Advance(10 * time.Second)
	}

	var got, wantIDs []string
	for _, e := range want {
		wantIDs = append(wantIDs, eventIDOf(e))
	}
	seen := map[string]bool{}
	for _, call := range batchCalls(r) {
		b := decodeBatch(t, call, secret)
		if len(b.Events) > 3 {
			t.Errorf("a batch of %d events, want at most the batch size, 3", len(b.Events))
		}
		for _, id := range idsOf(b.Events) {
			if seen[id] {
				t.Fatalf("event %s was delivered twice", id)
			}
			seen[id] = true
			got = append(got, id)
		}
	}
	if !slices.Equal(got, wantIDs) {
		t.Errorf("delivered %v, want %v in order", got, wantIDs)
	}
	if s := f.sink(t, created); s.Health != management.SinkHealthHealthy {
		t.Errorf("health = %s once every event is delivered, want healthy", s.Health)
	}
}

func TestAHangingReceiverIsRetriedAndResentTheSameDeliveryOnceItAnswers(t *testing.T) {
	f := startDelivering(t)
	// The receiver outlasts the ten seconds a call to a webhook sink may take.
	r := testkit.StartReceiver(t, testkit.Reply{}, testkit.Reply{Delay: 11 * time.Second}, testkit.Reply{})
	created, secret := f.enabled(t, webhook(r, "exposure"))
	e1 := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, e1)

	tick(t, f.srv)
	failing := f.sink(t, created)
	if lf := streamOf(failing, "exposure").LastFailure; failing.Health != management.SinkHealthFailing || lf == nil ||
		lf.Class != management.SinkFailureClass("timeout") || lf.Status != nil {
		t.Fatalf("sink after a timeout %+v, want failing with class timeout", failing)
	}
	f.clock.Advance(2 * time.Second)
	tick(t, f.srv)
	if s := f.sink(t, created); s.Health != management.SinkHealthHealthy {
		t.Fatalf("health once the receiver answers = %s, want healthy", s.Health)
	}
	calls := batchCalls(r)
	if len(calls) != 2 {
		t.Fatalf("the receiver got %d calls, want the one that timed out and the retry", len(calls))
	}
	if a, b := decodeBatch(t, calls[0], secret), decodeBatch(t, calls[1], secret); a.DeliveryID != b.DeliveryID || !slices.Equal(idsOf(b.Events), []string{eventIDOf(e1)}) {
		t.Errorf("retry %+v, want the same delivery ID and events as %+v", b, a)
	}
}

func TestSinkFailingIsToldOncePerEpisodeAndReachesASubscription(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, testkit.Reply{}, testkit.Reply{Status: http.StatusInternalServerError})
	subscriber := testkit.StartReceiver(t)
	sub := do(f.p.admin.CreateWebhookWithResponse(t.Context(), f.p.key, management.WebhookCreate{
		Name: "On call", Url: subscriber.URL, Events: []management.WebhookEventType{"sink.failing"},
	}))
	if sub.JSON201 == nil {
		t.Fatalf("POST webhooks = %d: %s", sub.StatusCode(), sub.Body)
	}
	// Every call after the check enabling the sink makes fails, to both streams.
	created, _ := f.enabled(t, webhook(r, "exposure", "alias"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()), f.alias(1))

	tick(t, f.srv)
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if s := f.sink(t, created); s.Health != management.SinkHealthFailing {
		t.Fatalf("health = %s, want failing", s.Health)
	}
	for _, name := range []string{worker.WebhookFanOutName, worker.WebhookDeliveryName} {
		if err := f.srv.Tick(t, name); err != nil {
			t.Fatal(err)
		}
	}

	calls := subscriber.Calls()
	if len(calls) != 1 {
		t.Fatalf("the subscription got %d calls, want sink.failing once for one episode across two streams and two failures", len(calls))
	}
	envelope := calls[0].Envelope(t)
	data, _ := envelope["data"].(map[string]any)
	failure, _ := data["failure"].(map[string]any)
	sinkRef, _ := data["sink"].(map[string]any)
	if envelope["type"] != "sink.failing" || sinkRef["id"] != created.Id.String() || sinkRef["kind"] != "webhook" ||
		failure["class"] != "status" || failure["status"] != float64(500) || failure["code"] != nil {
		t.Errorf("event %v, want sink.failing for the sink with its failure's class and status", envelope)
	}
}

// audited returns the audit entries of action on the sink.
func (f delivering) audited(t *testing.T, action string, sink management.Sink) []management.AuditEntry {
	t.Helper()
	id := sink.Id.String()
	res := do(f.p.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Action: &action, EntityId: &id}))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func TestDisablingASinkLetsItsBatchInFlightFinishAndStartsNoNewOne(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, secret := f.enabled(t, webhook(r, "exposure"))
	e1 := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, e1)

	// The receiver has the batch and the commit is lost, so it stays in flight;
	// the sink is disabled before anyone resends it.
	f.srv.LoseNextSinkCommit()
	tick(t, f.srv)
	disabled := f.patch(t, created, management.SinkPatch{Enabled: ptr(false)})
	if disabled.Health != management.SinkHealthDisabled || disabled.LagSeconds != nil {
		t.Errorf("sink after it was disabled %+v, want disabled and no lag", disabled)
	}
	e2 := f.exposure("u-2", "true", f.clock.Now())
	f.post(t, e2)
	f.clock.Advance(5*time.Minute + time.Second)
	tick(t, f.srv)

	calls := batchCalls(r)
	if len(calls) != 2 {
		t.Fatalf("the receiver got %d calls, want the batch in flight resent and nothing else", len(calls))
	}
	if got := idsOf(decodeBatch(t, calls[1], secret).Events); !slices.Equal(got, []string{eventIDOf(e1)}) {
		t.Errorf("resent events %v, want only the batch in flight, %s", got, eventIDOf(e1))
	}
	if streamOf(f.sink(t, created), "exposure").LastDeliveredAt == nil {
		t.Error("the batch in flight was not recorded once it was acknowledged")
	}

	// Enabled again, the sink continues from where it stopped.
	f.enable(t, created, management.SinkEnable{})
	f.clock.Advance(10 * time.Second) // past the flush interval the resent batch left
	tick(t, f.srv)
	calls = batchCalls(r)
	if len(calls) != 3 || !slices.Equal(idsOf(decodeBatch(t, calls[2], secret).Events), []string{eventIDOf(e2)}) {
		t.Fatalf("the receiver got %d calls after the sink was enabled again, want the event it missed in the third", len(calls))
	}
	if n := len(f.audited(t, "sink.disabled", created)); n != 1 {
		t.Errorf("%d sink.disabled entries, want 1", n)
	}
	if n := len(f.audited(t, "sink.enabled", created)); n != 2 {
		t.Errorf("%d sink.enabled entries, want one per enabling", n)
	}
}

func TestAStreamClaimedBeforeItsSinkIsDisabledStartsNothing(t *testing.T) {
	f := startDelivering(t)
	var (
		mu      sync.Mutex
		calls   []batchOf
		disable func()
	)
	// The receiver disables the sink while it answers the first batch: the
	// sink's other stream, claimed in the same round with nothing in flight,
	// must not start one afterwards.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var b batchOf
		_ = json.NewDecoder(req.Body).Decode(&b)
		mu.Lock()
		calls = append(calls, b)
		first := disable != nil && b.EventKind != "test"
		mu.Unlock()
		if first {
			disable()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)
	created, _ := f.create(t, management.SinkCreate{
		Name: "Warehouse", Kind: "webhook", Webhook: &management.SinkWebhookInput{Url: ptr(receiver.URL)},
		EventKinds: &[]string{"exposure", "alias"},
	})
	created = f.enable(t, created, management.SinkEnable{})
	mu.Lock()
	disable = sync.OnceFunc(func() {
		res, err := f.p.admin.UpdateSinkWithResponse(t.Context(), f.p.key, created.Id,
			&management.UpdateSinkParams{IfMatch: ifMatchOf(created.Version)}, management.SinkPatch{Enabled: ptr(false)})
		if err != nil || res.JSON200 == nil {
			t.Errorf("disabling the sink from the receiver: %v: %s", err, res.Body)
		}
	})
	mu.Unlock()

	e1, a1 := f.exposure("u-1", "true", f.clock.Now()), f.alias(1)
	f.post(t, e1, a1)
	tick(t, f.srv)

	batches := func() []batchOf {
		mu.Lock()
		defer mu.Unlock()
		var out []batchOf
		for _, c := range calls {
			if c.EventKind != "test" {
				out = append(out, c)
			}
		}
		return out
	}
	if got := batches(); len(got) != 1 || got[0].EventKind != "exposure" {
		t.Fatalf("the receiver got %+v, want the exposure batch alone: disabling stops a sink before any new batch", got)
	}

	// Enabled again, the stream that was held delivers from where it was.
	f.enable(t, f.sink(t, created), management.SinkEnable{})
	tick(t, f.srv)
	if got := batches(); len(got) != 2 || got[1].EventKind != "alias" || !slices.Equal(idsOf(got[1].Events), []string{eventIDOf(a1)}) {
		t.Fatalf("the receiver got %+v after the sink was enabled again, want the alias batch second", got)
	}
}

func TestEnablingASinkIsRefusedWhileItsReceiverFailsAndCreatesNoStream(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusInternalServerError, Body: "secret internal words"})
	created, _ := f.create(t, webhook(r))

	res := do(f.p.admin.EnableSinkWithResponse(t.Context(), f.p.key, created.Id, management.SinkEnable{}))
	var problem map[string]any
	if err := json.Unmarshal(res.Body, &problem); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusUnprocessableEntity || problem["code"] != "sink_not_ready" || problem["failure"] != "status" ||
		problem["failure_status"] != float64(500) || problem["failure_code"] != nil {
		t.Fatalf("POST enable = %d: %s, want 422 sink_not_ready with class status and 500", res.StatusCode(), res.Body)
	}
	if strings.Contains(string(res.Body), "secret internal words") {
		t.Error("the receiver's own words were answered")
	}
	if s := f.sink(t, created); s.Enabled || len(s.Streams) != 0 || s.Health != management.SinkHealthDisabled {
		t.Errorf("sink after the refusal %+v, want disabled with no stream", s)
	}
	if n := len(f.audited(t, "sink.enabled", created)); n != 0 {
		t.Errorf("%d sink.enabled entries, want none", n)
	}
}

func TestEnablingAnEnabledSinkChangesAndRecordsNothing(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r))
	again := f.enable(t, created, management.SinkEnable{})
	if again.Version != 2 || !again.Enabled {
		t.Errorf("sink enabled twice %+v, want it as the first enabling left it", again)
	}
	if n := len(f.audited(t, "sink.enabled", created)); n != 1 {
		t.Errorf("%d sink.enabled entries, want 1", n)
	}
	if n := len(r.Calls()); n != 1 {
		t.Errorf("the receiver got %d calls, want the one check the first enabling made", n)
	}
}

func TestASinkStartsAtTheTimeItWasAskedToAndOnlyItsNewStreamsDo(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, secret := f.create(t, webhook(r, "exposure"))
	old := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, old)
	f.clock.Advance(2 * time.Hour)
	recent := f.exposure("u-2", "true", f.clock.Now())
	f.post(t, recent)

	res := do(f.p.admin.EnableSinkWithResponse(t.Context(), f.p.key, created.Id,
		management.SinkEnable{StartFrom: ptr(f.clock.Now().Add(-time.Hour))}))
	if res.JSON200 == nil {
		t.Fatalf("POST enable = %d: %s", res.StatusCode(), res.Body)
	}
	tick(t, f.srv)
	calls := batchCalls(r)
	if len(calls) != 1 || !slices.Equal(idsOf(decodeBatch(t, calls[0], secret).Events), []string{eventIDOf(recent)}) {
		t.Fatalf("the receiver got %d calls, want the event received after the start alone", len(calls))
	}

	// An existing stream keeps its place: enabling again from the beginning
	// of retention sends nothing twice.
	disabled := f.patch(t, created, management.SinkPatch{Enabled: ptr(false)})
	f.enable(t, disabled, management.SinkEnable{StartFrom: ptr(f.clock.Now().Add(-24 * time.Hour))})
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if n := len(batchCalls(r)); n != 1 {
		t.Errorf("the receiver got %d calls after the sink was enabled again, want none more", n)
	}
}

func TestASinkStartedFromATimeReadsATransactionStillOpenWhenItWasEnabled(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, secret := f.create(t, webhook(r, "exposure"))
	start := f.clock.Now()

	// An older transaction is still open when a younger one commits, and the
	// sink is enabled from before both: the first row it can see is the
	// younger one's, which must not leave the older one behind its start.
	written, release := f.srv.HoldNextIngest(t)
	held := f.exposure("u-1", "true", start)
	first := make(chan answer, 1)
	go func() { first <- postEvents(t, f.srv, f.server, f.envelope(t, held)) }()
	<-written
	after := f.exposure("u-2", "true", start)
	f.post(t, after)
	f.enable(t, created, management.SinkEnable{StartFrom: ptr(start.Add(-time.Minute))})

	release()
	if a := <-first; a.status != http.StatusAccepted {
		t.Fatalf("the held batch = %d: %s", a.status, a.body)
	}
	tick(t, f.srv)
	calls := batchCalls(r)
	if len(calls) != 1 {
		t.Fatalf("the receiver got %d calls, want 1", len(calls))
	}
	if got, want := idsOf(decodeBatch(t, calls[0], secret).Events), []string{eventIDOf(held), eventIDOf(after)}; !slices.Equal(got, want) {
		t.Errorf("events %v, want %v: the transaction open at the enabling too", got, want)
	}
}

func TestABatchInFlightIsPassedOnceTheEnvironmentsItWasFilteredToAreGone(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, testkit.Reply{}, testkit.Reply{Status: http.StatusInternalServerError})
	body := webhook(r, "exposure")
	body.Environments = &[]string{"staging"}
	created, _ := f.enabled(t, body)
	f.postIn(t, "staging", f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv)
	if s := f.sink(t, created); s.Health != management.SinkHealthFailing {
		t.Fatalf("health = %s after a 500, want failing", s.Health)
	}

	got := do(f.p.admin.GetEnvironmentWithResponse(t.Context(), f.p.key, "staging"))
	etag := got.HTTPResponse.Header.Get("ETag")
	if res := do(f.p.admin.DeleteEnvironmentWithResponse(t.Context(), f.p.key, "staging", &management.DeleteEnvironmentParams{IfMatch: &etag})); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE …/environments/staging = %d: %s", res.StatusCode(), res.Body)
	}
	f.clock.Advance(2 * time.Second)
	tick(t, f.srv)

	s := f.sink(t, created)
	if st := streamOf(s, "exposure"); s.Health != management.SinkHealthHealthy || st == nil || st.LastFailure != nil {
		t.Errorf("sink once its environment is gone %+v, want the batch in flight passed and the sink healthy", s)
	}
	if n := len(batchCalls(r)); n != 1 {
		t.Errorf("the receiver got %d calls, want only the failed one: the rows went with their environment", n)
	}
	if n := testkit.Metric(t, f.srv.Scrape(t), `flagnetic_sink_rows_skipped_total{reason="environment_deleted"}`); n != 1 {
		t.Errorf("rows skipped = %v, want the row of the deleted environment", n)
	}
}

func TestASinkCannotStartFurtherBackThanEventsAreKept(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.create(t, webhook(r))
	res := do(f.p.admin.EnableSinkWithResponse(t.Context(), f.p.key, created.Id,
		management.SinkEnable{StartFrom: ptr(f.clock.Now().Add(-400 * 24 * time.Hour))}))
	if res.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(string(res.Body), `"out_of_retention"`) {
		t.Fatalf("POST enable = %d: %s, want 422 out_of_retention", res.StatusCode(), res.Body)
	}
	if s := f.sink(t, created); s.Enabled || len(s.Streams) != 0 {
		t.Errorf("sink after the refusal %+v, want disabled with no stream", s)
	}
}

func TestEventKindsAddedOrRemovedOnAnEnabledSinkAddOrDropItsStreams(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, secret := f.enabled(t, webhook(r, "exposure"))
	f.post(t, f.alias(1))

	f.srv.AwaitHorizon(t)
	added := f.patch(t, created, management.SinkPatch{EventKinds: &[]string{"exposure", "alias"}})
	if len(added.Streams) != 2 || streamOf(added, "alias") == nil {
		t.Fatalf("streams after adding alias %+v, want exposure and alias", added.Streams)
	}
	tick(t, f.srv)
	if n := len(batchCalls(r)); n != 0 {
		t.Errorf("the receiver got %d calls, want none: an event kind added starts at the horizon, its history is a replay", n)
	}
	later := f.alias(2)
	f.post(t, later)
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	calls := batchCalls(r)
	if len(calls) != 1 || !slices.Equal(idsOf(decodeBatch(t, calls[0], secret).Events), []string{eventIDOf(later)}) {
		t.Fatalf("the receiver got %d calls, want the alias received after the kind was added", len(calls))
	}

	removed := f.patch(t, created, management.SinkPatch{EventKinds: &[]string{"alias"}})
	if len(removed.Streams) != 1 || removed.Streams[0].EventKind != "alias" {
		t.Errorf("streams after removing exposure %+v, want alias alone", removed.Streams)
	}

	// Disabled in the same edit, the sink still drops the stream of a kind
	// removed, and gains none for a kind added until it is enabled.
	both := f.patch(t, created, management.SinkPatch{EventKinds: &[]string{"exposure", "alias"}})
	if len(both.Streams) != 2 {
		t.Fatalf("streams after adding exposure back %+v, want two", both.Streams)
	}
	disabled := f.patch(t, created, management.SinkPatch{Enabled: ptr(false), EventKinds: &[]string{"exposure"}})
	if len(disabled.Streams) != 1 || disabled.Streams[0].EventKind != "exposure" {
		t.Errorf("streams after disabling the sink and removing alias %+v, want exposure alone", disabled.Streams)
	}
	widened := f.patch(t, created, management.SinkPatch{EventKinds: &[]string{"exposure", "alias"}})
	if len(widened.Streams) != 1 || widened.Streams[0].EventKind != "exposure" {
		t.Errorf("streams after adding alias to a disabled sink %+v, want exposure alone", widened.Streams)
	}
}

func TestARejectedBatchIsCountedAndResentUnchanged(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, testkit.Reply{}, testkit.Reply{Status: http.StatusBadRequest}, testkit.Reply{Status: http.StatusBadRequest}, testkit.Reply{})
	created, secret := f.enabled(t, webhook(r, "exposure"))
	e1 := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, e1)

	tick(t, f.srv)
	f.clock.Advance(2 * time.Second)
	tick(t, f.srv)
	s := f.sink(t, created)
	if lf := streamOf(s, "exposure").LastFailure; s.Health != management.SinkHealthFailing || lf == nil ||
		lf.Class != management.SinkFailureClass("rejected") || lf.Status == nil || *lf.Status != 400 {
		t.Fatalf("sink after two rejections %+v, want failing with class rejected and 400", s)
	}
	f.clock.Advance(3 * time.Second)
	tick(t, f.srv)

	series := `flagnetic_sink_batches_total{event_kind="exposure",result="rejected",sink="` + created.Id.String() + `"}`
	if got := testkit.Metric(t, f.srv.Scrape(t), series); got != 2 {
		t.Errorf("%s = %v, want 2", series, got)
	}
	calls := batchCalls(r)
	if len(calls) != 3 {
		t.Fatalf("the receiver got %d calls, want the two rejected and the one acknowledged", len(calls))
	}
	first := decodeBatch(t, calls[0], secret)
	for _, c := range calls[1:] {
		if b := decodeBatch(t, c, secret); b.DeliveryID != first.DeliveryID || !slices.Equal(idsOf(b.Events), []string{eventIDOf(e1)}) {
			t.Errorf("resend %+v, want the rejected batch under its delivery ID: none is set aside yet", b)
		}
	}
	if s := f.sink(t, created); s.Health != management.SinkHealthHealthy || s.DeadLetters != 0 {
		t.Errorf("sink once the batch was acknowledged %+v, want healthy with no dead letter", s)
	}
}

func TestTestingASinkAnswersWhetherItsTargetWorksAndStoresNothingForAnUnsavedOne(t *testing.T) {
	f := startDelivering(t)
	working := testkit.StartReceiver(t)
	failing := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusBadRequest, Body: "words never read"})

	unsaved := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, webhook(working)))
	if r := unsaved.JSON200; r == nil || !r.Ok || r.Class != nil || r.Status != nil || r.Code != nil || len(r.Warnings) != 0 {
		t.Fatalf("test of a working configuration = %d: %s", unsaved.StatusCode(), unsaved.Body)
	}
	calls := working.Calls()
	if len(calls) != 1 || calls[0].Envelope(t)["event_kind"] != "test" || len(calls[0].Envelope(t)["events"].([]any)) != 0 ||
		calls[0].Envelope(t)["sample"] != false || !strings.HasPrefix(calls[0].Envelope(t)["delivery_id"].(string), "test_") {
		t.Errorf("the receiver was called %v, want one call of kind test with no events", calls)
	}
	refused := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, webhook(failing)))
	if r := refused.JSON200; r == nil || r.Ok || r.Class == nil || *r.Class != management.SinkFailureClass("rejected") || r.Status == nil ||
		*r.Status != 400 || r.Code != nil || strings.Contains(string(refused.Body), "words never read") {
		t.Errorf("test of a refusing receiver = %d: %s, want ok false, class rejected and 400", refused.StatusCode(), refused.Body)
	}
	invalid := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, management.SinkCreate{Name: "Bad", Kind: "webhook"}))
	if invalid.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("test of an invalid configuration = %d: %s, want 422", invalid.StatusCode(), invalid.Body)
	}
	if list := do(f.p.admin.ListSinksWithResponse(t.Context(), f.p.key)); list.JSON200 == nil || len(list.JSON200.Items) != 0 {
		t.Errorf("sinks after testing unsaved configurations %s, want none", list.Body)
	}

	saved, _ := f.create(t, webhook(working))
	stored := do(f.p.admin.TestSinkWithResponse(t.Context(), f.p.key, saved.Id, management.SinkTest{}))
	if stored.JSON200 == nil || !stored.JSON200.Ok {
		t.Fatalf("test of the stored sink = %d: %s", stored.StatusCode(), stored.Body)
	}
	edit := do(f.p.admin.TestSinkWithResponse(t.Context(), f.p.key, saved.Id,
		management.SinkTest{Webhook: &management.SinkWebhookInput{Url: ptr(failing.URL)}}))
	if edit.JSON200 == nil || edit.JSON200.Ok || edit.JSON200.Class == nil || *edit.JSON200.Class != management.SinkFailureClass("rejected") {
		t.Errorf("test of an edit = %d: %s, want the edit's receiver refused", edit.StatusCode(), edit.Body)
	}
	if s := f.sink(t, saved); s.Webhook == nil || s.Webhook.Url != working.URL || s.Version != saved.Version {
		t.Errorf("sink after testing an edit %+v, want it unchanged", s)
	}
	if n := len(f.audited(t, "sink.tested", saved)); n != 2 {
		t.Errorf("%d sink.tested entries, want one for each test of the saved sink", n)
	}
}

func TestARotatedSinkSecretSignsBesideTheNewOneForTheOverlap(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, old := f.enabled(t, webhook(r, "exposure"))
	rotated := do(f.p.admin.RotateSinkSecretWithResponse(t.Context(), f.p.key, created.Id))
	if rotated.JSON200 == nil || rotated.JSON200.Secret == nil {
		t.Fatalf("POST rotate-secret = %d: %s", rotated.StatusCode(), rotated.Body)
	}
	fresh := *rotated.JSON200.Secret
	testkit.RecordSecret(fresh)

	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv)
	calls := batchCalls(r)
	if len(calls) != 1 || !calls[0].Verifies(old) || !calls[0].Verifies(fresh) {
		t.Fatalf("calls %d, want one that verifies under the old and the new secret", len(calls))
	}
	f.clock.Advance(25 * time.Hour)
	f.post(t, f.exposure("u-2", "true", f.clock.Now()))
	tick(t, f.srv)
	calls = batchCalls(r)
	if len(calls) != 2 || calls[1].Verifies(old) || !calls[1].Verifies(fresh) {
		t.Fatalf("calls %d, want the second to verify under the new secret alone once the overlap is over", len(calls))
	}
}

func TestAWebhookSinkNeverDialsAnAddressItsInstanceDoesNotAllow(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))

	// Another replica, whose operator does not allow private networks, is the
	// one delivering: the receiver is on loopback.
	strict := testkit.StartServer(t, testkit.WithClock(f.clock),
		testkit.WithWebhookTargets(governance.TargetPolicy{AllowHTTP: true}))
	tick(t, strict)

	s := f.sink(t, created)
	if lf := streamOf(s, "exposure").LastFailure; s.Health != management.SinkHealthFailing || lf == nil ||
		lf.Class != management.SinkFailureClass("refused_target") {
		t.Errorf("sink %+v, want failing with class refused_target", s)
	}
	if n := len(batchCalls(r)); n != 0 {
		t.Errorf("the receiver got %d calls, want none", n)
	}
}
