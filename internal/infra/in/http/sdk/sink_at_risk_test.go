package sdk_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// lossWarning is FLAGNETIC_EVENTS_LOSS_WARNING_HOURS' default, and edge how old
// a row is when it comes within it of the end of retention.
const (
	lossWarning = 72 * time.Hour
	edge        = retention - lossWarning
)

// at risk gauge of sink.
func (f delivering) atRiskGauge(t *testing.T, sink management.Sink) float64 {
	t.Helper()
	return testkit.Metric(t, f.srv.Scrape(t), fmt.Sprintf(`flagnetic_sink_at_risk{sink=%q}`, sink.Id.String()))
}

// subscribe subscribes a receiver to the sink events of the types.
func (f delivering) subscribe(t *testing.T, types ...management.WebhookEventType) *testkit.Receiver {
	t.Helper()
	subscriber := testkit.StartReceiver(t)
	res := do(f.p.admin.CreateWebhookWithResponse(t.Context(), f.p.key, management.WebhookCreate{
		Name: "On call", Url: subscriber.URL, Events: types,
	}))
	if res.JSON201 == nil {
		t.Fatalf("POST webhooks = %d: %s", res.StatusCode(), res.Body)
	}
	return subscriber
}

// deliverEvents runs the webhook workers that send the events recorded so far.
func (f delivering) deliverEvents(t *testing.T) {
	t.Helper()
	for _, name := range []string{worker.WebhookFanOutName, worker.WebhookDeliveryName} {
		if err := f.srv.Tick(t, name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnUndeliveredRowNearTheRetentionEdgePutsTheSinkAtRiskOnceAndALaterCatchUpEndsTheEpisode(t *testing.T) {
	f := startDelivering(t)
	subscriber := f.subscribe(t, "sink.at_risk")
	// The receiver answers the check that enables the sink, fails the next
	// four calls and then recovers.
	r := testkit.StartReceiver(t, script(times(4, failing), []testkit.Reply{{}})...)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv)

	// A minute short of the warning: the row is not yet within it.
	f.clock.Advance(edge - time.Minute)
	tick(t, f.srv)
	if s := f.sink(t, created); s.AtRisk || s.Health != management.SinkHealthFailing {
		t.Fatalf("a minute short of the edge: %s, at risk %v; want failing and not at risk", brief(s), s.AtRisk)
	}
	if got := f.atRiskGauge(t, created); got != 0 {
		t.Errorf("flagnetic_sink_at_risk = %v before the edge, want 0", got)
	}
	if n := len(f.audited(t, "sink.at_risk", created)); n != 0 {
		t.Fatalf("%d sink.at_risk entries before the edge, want 0", n)
	}

	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	s := f.sink(t, created)
	if !s.AtRisk || s.Health != management.SinkHealthFailing {
		t.Fatalf("at the edge: %s, at risk %v; want failing and at risk, health not replaced", brief(s), s.AtRisk)
	}
	if got := f.atRiskGauge(t, created); got != 1 {
		t.Errorf("flagnetic_sink_at_risk = %v at the edge, want 1", got)
	}
	entries := f.audited(t, "sink.at_risk", created)
	if len(entries) != 1 || entries[0].Actor.Id != "sink_delivery" {
		t.Fatalf("sink.at_risk entries %+v, want one, by the sink delivery system actor", entries)
	}

	// Still behind a day later, and the receiver still failing: the same
	// episode, told once.
	f.clock.Advance(24 * time.Hour)
	tick(t, f.srv)
	if n := len(f.audited(t, "sink.at_risk", created)); n != 1 {
		t.Errorf("%d sink.at_risk entries on the next day, want the one", n)
	}
	f.deliverEvents(t)
	calls := subscriber.Calls()
	if len(calls) != 1 {
		t.Fatalf("the subscription got %d calls, want sink.at_risk once", len(calls))
	}
	envelope := calls[0].Envelope(t)
	data, _ := envelope["data"].(map[string]any)
	ref, _ := data["sink"].(map[string]any)
	if envelope["type"] != "sink.at_risk" || ref["id"] != created.Id.String() || data["event_kind"] != "exposure" ||
		data["lag_seconds"] != float64(edge/time.Second) || data["loss_warning_hours"] != float64(72) {
		t.Errorf("event %v, want sink.at_risk for the sink, exposure, a lag of %d s and the loss warning of 72 h", envelope, edge/time.Second)
	}

	// The receiver recovers and the sink catches up: it leaves being at risk
	// as the round that delivers ends, and says nothing.
	f.clock.Advance(10 * time.Minute)
	tick(t, f.srv)
	if caught := f.sink(t, created); caught.AtRisk || caught.Health != management.SinkHealthHealthy {
		t.Errorf("after catching up: %s, at risk %v; want healthy and not at risk", brief(caught), caught.AtRisk)
	}
	if got := f.atRiskGauge(t, created); got != 0 {
		t.Errorf("flagnetic_sink_at_risk = %v after catching up, want 0", got)
	}
	if n := len(f.audited(t, "sink.at_risk", created)); n != 1 {
		t.Errorf("%d sink.at_risk entries after catching up, want the one: there is no recovery event", n)
	}
}

func TestASinkThatFallsBehindAgainStartsANewEpisode(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(3, failing), times(1, testkit.Reply{}), times(2, failing), []testkit.Reply{{}})...)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv) // fails
	f.clock.Advance(edge)
	tick(t, f.srv) // fails, and is at risk
	if s := f.sink(t, created); !s.AtRisk {
		t.Fatalf("sink %s not at risk when behind by the edge", brief(s))
	}
	f.clock.Advance(10 * time.Minute)
	tick(t, f.srv) // fails
	f.clock.Advance(10 * time.Minute)
	tick(t, f.srv) // delivered, and no longer at risk
	if s := f.sink(t, created); s.AtRisk {
		t.Fatalf("sink %s still at risk after catching up", brief(s))
	}

	f.post(t, f.exposure("u-2", "true", f.clock.Now()))
	f.clock.Advance(10 * time.Minute)
	tick(t, f.srv) // fails
	f.clock.Advance(edge)
	tick(t, f.srv) // fails, and is at risk
	if s := f.sink(t, created); !s.AtRisk {
		t.Fatalf("sink %s not at risk when behind by the edge again", brief(s))
	}
	if n := len(f.audited(t, "sink.at_risk", created)); n != 2 {
		t.Errorf("%d sink.at_risk entries, want one for each of the two episodes", n)
	}
}

func TestADisabledSinkWithAnUndeliveredRowIsStillAtRisk(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(1, failing))...)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv)
	f.patch(t, created, management.SinkPatch{Enabled: ptr(false)})

	f.clock.Advance(edge)
	tick(t, f.srv)
	s := f.sink(t, created)
	if !s.AtRisk || s.Health != management.SinkHealthDisabled || s.LagSeconds != nil {
		t.Errorf("disabled sink %s, at risk %v, lag %v; want disabled, at risk, no lag", brief(s), s.AtRisk, s.LagSeconds)
	}
	if n := len(f.audited(t, "sink.at_risk", created)); n != 1 {
		t.Errorf("%d sink.at_risk entries, want 1", n)
	}
}

func TestAStoppedSinkWithAnUndeliveredRowIsStillAtRisk(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(15, rejected), []testkit.Reply{{}})...)
	created, _ := f.enabled(t, small(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()), f.exposure("u-2", "true", f.clock.Now()),
		f.exposure("u-3", "true", f.clock.Now()), f.exposure("u-4", "true", f.clock.Now()))
	f.rounds(t, 13)
	if s := f.sink(t, created); s.Health != management.SinkHealthStopped || s.AtRisk {
		t.Fatalf("sink %s, at risk %v; want stopped and not yet at risk", brief(s), s.AtRisk)
	}

	f.clock.Advance(edge)
	tick(t, f.srv)
	if s := f.sink(t, created); s.Health != management.SinkHealthStopped || !s.AtRisk {
		t.Errorf("sink %s, at risk %v; want stopped and at risk: the fourth row is undelivered", brief(s), s.AtRisk)
	}
}

func TestASinkWithNothingUndeliveredIsNeverAtRisk(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv)

	f.clock.Advance(edge + 24*time.Hour)
	tick(t, f.srv)
	if s := f.sink(t, created); s.AtRisk || s.Health != management.SinkHealthHealthy {
		t.Errorf("a caught-up sink %s, at risk %v; want healthy and not at risk however long it sits", brief(s), s.AtRisk)
	}
}

func TestAThreeDayOutageInsideRetentionLosesNothing(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(13, failing), []testkit.Reply{{}})...)
	created, secret := f.enabled(t, webhook(r, "exposure"))

	var ingested []string
	for i := range 12 { // every six hours for three days
		for _, unit := range []string{"a", "b"} {
			e := f.exposure(fmt.Sprintf("u-%d-%s", i, unit), "true", f.clock.Now())
			ingested = append(ingested, eventIDOf(e))
			f.post(t, e)
		}
		tick(t, f.srv)
		f.clock.Advance(6 * time.Hour)
	}
	tick(t, f.srv)
	if down := f.sink(t, created); down.Health != management.SinkHealthFailing || down.AtRisk || down.Gaps != 0 {
		t.Fatalf("after three days down: %s, at risk %v, gaps %d; want failing, not at risk, no gap", brief(down), down.AtRisk, down.Gaps)
	}

	f.rounds(t, 3)
	if got := f.sink(t, created); got.Health != management.SinkHealthHealthy || got.Gaps != 0 || got.AtRisk {
		t.Fatalf("after recovery: %s, gaps %d; want healthy, no gap", brief(got), got.Gaps)
	}
	var got []string
	for _, b := range callsOf(t, r, secret)[13:] {
		got = append(got, idsOf(b.Events)...)
	}
	if !slices.Equal(got, ingested) {
		t.Errorf("the receiver accepted %d events %v, want all %d ingested exactly once in order %v", len(got), got, len(ingested), ingested)
	}
	if n := len(f.audited(t, "sink.at_risk", created)); n != 0 {
		t.Errorf("%d sink.at_risk entries, want 0", n)
	}
}

func TestASinkLagsPastTheWarningThenCatchesUp(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))

	f.clock.Advance(10 * time.Minute)
	if s := f.sink(t, created); s.Health != management.SinkHealthHealthy {
		t.Errorf("10 min behind: %s, want healthy: inside the lag warning", brief(s))
	}
	f.clock.Advance(10 * time.Minute)
	lagging := f.sink(t, created)
	if lagging.Health != management.SinkHealthLagging || lagging.AtRisk || lagging.LagSeconds == nil || *lagging.LagSeconds != 20*60 {
		t.Errorf("20 min behind: %s, at risk %v, lag %v; want lagging, not at risk, 1200 s", brief(lagging), lagging.AtRisk, lagging.LagSeconds)
	}
	tick(t, f.srv)
	if s := f.sink(t, created); s.Health != management.SinkHealthHealthy || s.LagSeconds == nil || *s.LagSeconds != 0 {
		t.Errorf("after delivering: %s, lag %v; want healthy, 0", brief(s), s.LagSeconds)
	}
}

func TestASlowSinkLagsWithoutHoldingUpTheOthersOrIngestion(t *testing.T) {
	f := startDelivering(t)
	const hold = 2 * time.Second
	// The slow receiver answers the check at once, then every batch late.
	slow := testkit.StartReceiver(t, testkit.Reply{}, testkit.Reply{Delay: hold})
	fast := testkit.StartReceiver(t, script(times(1, testkit.Reply{Status: http.StatusInternalServerError}))...)
	// The slow sink flushes first, so a worker attempting one sink after
	// another would reach the fast one only once the slow one answered.
	var slowSink, fastSink management.Sink
	for i, r := range []*testkit.Receiver{slow, fast} {
		body := webhook(r, "exposure")
		body.FlushInterval = ptr(i + 1)
		created, _ := f.enabled(t, body)
		if i == 0 {
			slowSink = created
		} else {
			fastSink = created
		}
	}
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	f.clock.Advance(5 * time.Second)

	ticked := make(chan error, 1)
	go func() {
		f.srv.AwaitHorizon(t)
		ticked <- f.srv.Tick(t, worker.SinkDeliveryName)
	}()
	deadline := time.Now().Add(hold / 2)
	for len(batchCalls(fast)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the fast sink got nothing within %s while the slow one held its request", hold/2)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The slow receiver still holds its request: ingestion is unaffected.
	started := time.Now()
	f.post(t, f.exposure("u-2", "true", f.clock.Now()))
	if took := time.Since(started); took > hold/2 {
		t.Errorf("ingest while a sink hangs took %s", took)
	}
	if err := <-ticked; err != nil {
		t.Fatal(err)
	}
	if got := len(batchCalls(slow)); got != 1 {
		t.Errorf("the slow receiver got %d batches, want 1", got)
	}
	if s := f.sink(t, fastSink); s.Health != management.SinkHealthFailing {
		t.Errorf("fast sink %s, want failing", brief(s))
	}
	if s := f.sink(t, slowSink); s.AtRisk {
		t.Errorf("slow sink %s at risk, want only slow", brief(s))
	}
}
