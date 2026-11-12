package sdk_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// rejected is a receiver that refuses what it is sent: a 422 is a rejection
// the receiver will repeat, not a failure it may recover from.
var rejected = testkit.Reply{Status: http.StatusUnprocessableEntity}

// script is the replies of a receiver in the order it gives them: the check
// that enables the sink is acknowledged first, then each group in turn, the
// last reply repeating.
func script(groups ...[]testkit.Reply) []testkit.Reply {
	out := []testkit.Reply{{}}
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func times(n int, r testkit.Reply) []testkit.Reply {
	out := make([]testkit.Reply, n)
	for i := range out {
		out[i] = r
	}
	return out
}

// small is a webhook sink of kinds that sends one event at a time, so that each
// event is a batch of its own.
func small(r *testkit.Receiver, kinds ...string) management.SinkCreate {
	body := webhook(r, kinds...)
	body.BatchSize = ptr(1)
	return body
}

// rounds runs n rounds of the sink delivery worker, each after the clock moved
// past any backoff the round before left, as an operator waiting would see.
func (f delivering) rounds(t *testing.T, n int) {
	t.Helper()
	for range n {
		tick(t, f.srv)
		f.clock.Advance(6 * time.Minute)
	}
}

func (f delivering) deadLetters(t *testing.T, sink management.Sink) []management.SinkDeadLetter {
	t.Helper()
	res := do(f.p.admin.ListSinkDeadLettersWithResponse(t.Context(), f.p.key, sink.Id))
	if res.JSON200 == nil {
		t.Fatalf("GET dead-letters = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func (f delivering) redeliver(t *testing.T, sink management.Sink, dl management.SinkDeadLetter) management.SinkDeadLetter {
	t.Helper()
	res := do(f.p.admin.RedeliverSinkDeadLetterWithResponse(t.Context(), f.p.key, sink.Id, dl.Id))
	if res.JSON200 == nil {
		t.Fatalf("POST redeliver = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func (f delivering) resume(t *testing.T, sink management.Sink) management.Sink {
	t.Helper()
	res := do(f.p.admin.ResumeSinkWithResponse(t.Context(), f.p.key, sink.Id))
	if res.JSON200 == nil {
		t.Fatalf("POST resume = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// callsOf returns the batches the receiver got, decoded.
func callsOf(t *testing.T, r *testkit.Receiver, secret string) []batchOf {
	t.Helper()
	var out []batchOf
	for _, c := range batchCalls(r) {
		out = append(out, decodeBatch(t, c, secret))
	}
	return out
}

func TestABatchRejectedFiveTimesIsSetAsideTheNextFlowsAndARedeliveryDeliversIt(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(5, rejected), []testkit.Reply{{}})...)
	created, secret := f.enabled(t, small(r, "exposure"))
	e1, e2 := f.exposure("u-1", "true", f.clock.Now()), f.exposure("u-2", "true", f.clock.Now())
	f.post(t, e1, e2)

	f.rounds(t, 4)
	if s := f.sink(t, created); s.DeadLetters != 0 || s.Health != management.SinkHealthFailing || len(f.deadLetters(t, created)) != 0 {
		t.Fatalf("sink after four rejections %s, want failing with no dead letter: the fifth sets the batch aside", brief(s))
	}
	f.rounds(t, 1)

	calls := callsOf(t, r, secret)
	if len(calls) != 6 {
		t.Fatalf("the receiver got %d batches, want the five rejections and the next batch in the same round", len(calls))
	}
	for _, c := range calls[:5] {
		if c.DeliveryID != calls[0].DeliveryID || !slices.Equal(idsOf(c.Events), []string{eventIDOf(e1)}) {
			t.Errorf("rejected attempt %+v, want the first event under one delivery ID", c)
		}
	}
	if next := calls[5]; next.DeliveryID == calls[0].DeliveryID || !slices.Equal(idsOf(next.Events), []string{eventIDOf(e2)}) {
		t.Errorf("batch after the dead letter %+v, want the second event under a new delivery ID", next)
	}

	list := f.deadLetters(t, created)
	if len(list) != 1 {
		t.Fatalf("dead letters %+v, want one", list)
	}
	dl := list[0]
	if dl.EventKind != "exposure" || dl.Status != management.SinkDeadLetterStatusPending || dl.Attempts != 5 || dl.Rows != 1 ||
		dl.RedeliveredAt != nil || dl.LastFailure.Class != management.SinkFailureClass("rejected") ||
		dl.LastFailure.Status == nil || *dl.LastFailure.Status != http.StatusUnprocessableEntity {
		t.Errorf("dead letter %+v, want pending after five rejections, class rejected, 422", dl)
	}
	if s := f.sink(t, created); s.Health != management.SinkHealthDegraded || s.DeadLetters != 1 || s.StoppedAt != nil {
		t.Errorf("sink with a dead letter waiting %s, want degraded, one dead letter, not stopped", brief(s))
	}
	scrape := f.srv.Scrape(t)
	for series, want := range map[string]float64{
		`flagnetic_sink_dead_letters{sink="` + created.Id.String() + `"}`:                                               1,
		`flagnetic_sink_batches_total{event_kind="exposure",result="dead_lettered",sink="` + created.Id.String() + `"}`: 1,
		`flagnetic_sink_batches_total{event_kind="exposure",result="rejected",sink="` + created.Id.String() + `"}`:      5,
	} {
		if got := testkit.Metric(t, scrape, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
	if n := len(f.audited(t, "sink.dead_lettered", created)); n != 1 {
		t.Errorf("%d sink.dead_lettered entries, want 1", n)
	}

	redelivered := f.redeliver(t, created, dl)
	if redelivered.Status != management.SinkDeadLetterStatusRedelivered || redelivered.RedeliveredAt == nil || redelivered.Attempts != 6 {
		t.Errorf("dead letter after the redelivery %+v, want redelivered at a time, six attempts", redelivered)
	}
	calls = callsOf(t, r, secret)
	if len(calls) != 7 {
		t.Fatalf("the receiver got %d batches, want one more for the redelivery", len(calls))
	}
	again := calls[6]
	if !slices.Equal(idsOf(again.Events), []string{eventIDOf(e1)}) || again.DeliveryID == calls[0].DeliveryID || again.DeliveryID == calls[5].DeliveryID {
		t.Errorf("redelivery %+v, want the dead letter's event under a delivery ID of its own", again)
	}
	if s := f.sink(t, created); s.Health != management.SinkHealthHealthy || s.DeadLetters != 0 {
		t.Errorf("sink once the dead letter was redelivered %s, want healthy", brief(s))
	}
	scrape = f.srv.Scrape(t)
	for series, want := range map[string]float64{
		`flagnetic_sink_dead_letters{sink="` + created.Id.String() + `"}`:                                             0,
		`flagnetic_sink_batches_total{event_kind="exposure",result="redelivered",sink="` + created.Id.String() + `"}`: 1,
	} {
		if got := testkit.Metric(t, scrape, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
	if n := len(f.audited(t, "sink.dead_letter_redelivered", created)); n != 1 {
		t.Errorf("%d sink.dead_letter_redelivered entries, want 1", n)
	}
	if left := f.deadLetters(t, created); len(left) != 1 || left[0].Status != management.SinkDeadLetterStatusRedelivered {
		t.Errorf("dead letters after the redelivery %+v, want it listed as redelivered", left)
	}
}

func TestAFailedRedeliveryLeavesTheDeadLetterPendingAndMayBeRepeated(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(5, rejected), []testkit.Reply{{}}, []testkit.Reply{{Status: http.StatusInternalServerError}}, []testkit.Reply{{}})...)
	created, secret := f.enabled(t, small(r, "exposure"))
	e1 := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, e1, f.exposure("u-2", "true", f.clock.Now()))
	f.rounds(t, 5)
	dl := f.deadLetters(t, created)[0]

	failed := f.redeliver(t, created, dl)
	if failed.Status != management.SinkDeadLetterStatusPending || failed.RedeliveredAt != nil || failed.Attempts != 6 ||
		failed.LastFailure.Class != management.SinkFailureClass("status") || failed.LastFailure.Status == nil || *failed.LastFailure.Status != 500 {
		t.Errorf("dead letter after a failed redelivery %+v, want pending, six attempts, class status 500", failed)
	}
	if s := f.sink(t, created); s.Health != management.SinkHealthDegraded || s.DeadLetters != 1 {
		t.Errorf("sink after a failed redelivery %s, want degraded with its dead letter", brief(s))
	}
	acknowledged := f.redeliver(t, created, dl)
	if acknowledged.Status != management.SinkDeadLetterStatusRedelivered || acknowledged.Attempts != 7 {
		t.Errorf("dead letter after the repeat %+v, want redelivered after seven attempts", acknowledged)
	}
	calls := callsOf(t, r, secret)
	if len(calls) != 8 || calls[6].DeliveryID == calls[7].DeliveryID {
		t.Errorf("the receiver got %d batches, want each redelivery under a delivery ID of its own", len(calls))
	}
}

func TestConsecutiveDeadLettersStopASinkUntilAMemberResumesIt(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(15, rejected), []testkit.Reply{{}})...)
	subscriber := testkit.StartReceiver(t)
	sub := do(f.p.admin.CreateWebhookWithResponse(t.Context(), f.p.key, management.WebhookCreate{
		Name: "On call", Url: subscriber.URL, Events: []management.WebhookEventType{"sink.stopped"},
	}))
	if sub.JSON201 == nil {
		t.Fatalf("POST webhooks = %d: %s", sub.StatusCode(), sub.Body)
	}
	created, secret := f.enabled(t, small(r, "exposure"))
	e1, e2, e3 := f.exposure("u-1", "true", f.clock.Now()), f.exposure("u-2", "true", f.clock.Now()), f.exposure("u-3", "true", f.clock.Now())
	e4 := f.exposure("u-4", "true", f.clock.Now())
	f.post(t, e1, e2, e3, e4)

	f.rounds(t, 13)
	if n := len(batchCalls(r)); n != 15 {
		t.Fatalf("the receiver got %d batches, want 3 batches rejected 5 times each", n)
	}
	// Nothing more is attempted, however long the sink waits.
	f.rounds(t, 5)
	stopped := f.sink(t, created)
	if n := len(batchCalls(r)); n != 15 || stopped.Health != management.SinkHealthStopped || stopped.StoppedAt == nil || stopped.DeadLetters != 3 {
		t.Fatalf("sink after three dead letters %s with %d batches sent, want stopped with three dead letters and nothing more sent", brief(stopped), n)
	}
	for _, name := range []string{worker.WebhookFanOutName, worker.WebhookDeliveryName} {
		if err := f.srv.Tick(t, name); err != nil {
			t.Fatal(err)
		}
	}
	calls := subscriber.Calls()
	if len(calls) != 1 {
		t.Fatalf("the subscription got %d calls, want sink.stopped once", len(calls))
	}
	envelope := calls[0].Envelope(t)
	data, _ := envelope["data"].(map[string]any)
	failure, _ := data["failure"].(map[string]any)
	sinkRef, _ := data["sink"].(map[string]any)
	if envelope["type"] != "sink.stopped" || sinkRef["id"] != created.Id.String() || data["event_kind"] != "exposure" ||
		data["consecutive_dead_letters"] != float64(3) || failure["class"] != "rejected" || failure["status"] != float64(422) {
		t.Errorf("event %v, want sink.stopped for the sink after 3 dead letters, last failure class rejected 422", envelope)
	}

	// An event that arrives meanwhile waits, and editing the sink does not
	// resume it.
	late := f.exposure("u-5", "true", f.clock.Now())
	f.post(t, late)
	f.patch(t, created, management.SinkPatch{Name: ptr("Warehouse, fixed")})
	f.rounds(t, 2)
	if n := len(batchCalls(r)); n != 15 {
		t.Fatalf("the receiver got %d batches after an edit, want an edit not to resume the sink", n)
	}

	resumed := f.resume(t, created)
	if resumed.StoppedAt != nil || resumed.Health != management.SinkHealthDegraded {
		t.Errorf("sink after it was resumed %s, want degraded: its dead letters stay", brief(resumed))
	}
	f.resume(t, created)
	if n := len(f.audited(t, "sink.resumed", created)); n != 1 {
		t.Errorf("%d sink.resumed entries after resuming twice, want 1: a sink that is not stopped is left as it is", n)
	}
	f.rounds(t, 2)
	calls2 := callsOf(t, r, secret)
	if len(calls2) != 17 || !slices.Equal(idsOf(calls2[15].Events), []string{eventIDOf(e4)}) || !slices.Equal(idsOf(calls2[16].Events), []string{eventIDOf(late)}) {
		t.Fatalf("the receiver got %d batches, want the fourth event and the late one from the cursor, never a dead-lettered row", len(calls2))
	}

	list := f.deadLetters(t, created)
	if len(list) != 3 {
		t.Fatalf("dead letters %+v, want three", list)
	}
	for _, dl := range list {
		res := do(f.p.admin.DiscardSinkDeadLetterWithResponse(t.Context(), f.p.key, created.Id, dl.Id))
		if res.StatusCode() != http.StatusNoContent {
			t.Fatalf("DELETE dead letter = %d: %s", res.StatusCode(), res.Body)
		}
	}
	if gone := do(f.p.admin.DiscardSinkDeadLetterWithResponse(t.Context(), f.p.key, created.Id, list[0].Id)); gone.StatusCode() != http.StatusNotFound {
		t.Errorf("DELETE of a discarded dead letter = %d, want 404", gone.StatusCode())
	}
	if s := f.sink(t, created); len(f.deadLetters(t, created)) != 0 || s.Health != management.SinkHealthHealthy || s.DeadLetters != 0 {
		t.Errorf("sink once its dead letters were discarded %s, want healthy", brief(s))
	}
	if n := len(f.audited(t, "sink.dead_letter_discarded", created)); n != 3 {
		t.Errorf("%d sink.dead_letter_discarded entries, want 3", n)
	}
	if n := len(f.audited(t, "sink.stopped", created)); n != 1 {
		t.Errorf("%d sink.stopped entries, want 1", n)
	}
}

func TestAStopTakesEffectBeforeTheNextAttemptOfAStreamClaimedInTheSameRound(t *testing.T) {
	f := startDelivering(t)
	// Both streams are rejected in lockstep: the first stream's third dead letter
	// is its fifteenth call, and the second, claimed with it, has made 14.
	r := testkit.StartReceiver(t, script(times(29, rejected), []testkit.Reply{{}})...)
	created, secret := f.enabled(t, small(r, "exposure", "alias"))
	for n := range 3 {
		f.post(t, f.exposure("u-"+string(rune('1'+n)), "true", f.clock.Now()), f.alias(n+1))
	}

	f.rounds(t, 16)
	calls := callsOf(t, r, secret)
	stopped := f.sink(t, created)
	if len(calls) != 29 || stopped.Health != management.SinkHealthStopped || stopped.DeadLetters != 5 {
		t.Fatalf("receiver got %d batches, sink %s, want 29 batches, never 30, a stopped sink and 2+3 dead letters", len(calls), brief(stopped))
	}
	last := slices.IndexFunc(calls, func(b batchOf) bool { return b.EventKind == "alias" })
	for i, c := range calls {
		if c.EventKind == "alias" {
			last = i
		}
	}
	inFlight := calls[last]

	f.resume(t, created)
	f.rounds(t, 2)
	calls = callsOf(t, r, secret)
	if len(calls) != 30 {
		t.Fatalf("the receiver got %d batches after the resume, want the one the second stream had in flight", len(calls))
	}
	if resent := calls[29]; resent.EventKind != "alias" || resent.DeliveryID != inFlight.DeliveryID || !slices.Equal(idsOf(resent.Events), idsOf(inFlight.Events)) {
		t.Errorf("batch after the resume %+v, want the alias batch in flight again under delivery ID %s", resent, inFlight.DeliveryID)
	}
}

func TestADeliveredBatchResetsTheRunOfDeadLetters(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(5, rejected), []testkit.Reply{{}}, times(10, rejected), []testkit.Reply{{}})...)
	created, _ := f.enabled(t, small(r, "exposure"))
	for n := range 5 {
		f.post(t, f.exposure("u-"+string(rune('1'+n)), "true", f.clock.Now()))
	}

	f.rounds(t, 16)
	s := f.sink(t, created)
	if len(f.deadLetters(t, created)) != 3 || s.Health != management.SinkHealthDegraded || s.StoppedAt != nil {
		t.Errorf("sink %s, want 3 dead letters, degraded and not stopped: the second event's delivery ended the run", brief(s))
	}
	if n := len(f.audited(t, "sink.stopped", created)); n != 0 {
		t.Errorf("%d sink.stopped entries, want none", n)
	}
}

func TestRedeliveringADeadLetterWhoseRowsAreGoneIsRefused(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(5, rejected), []testkit.Reply{{}})...)
	created, _ := f.enabled(t, small(r, "exposure"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	f.rounds(t, 5)
	dl := f.deadLetters(t, created)
	if len(dl) != 1 {
		t.Fatalf("dead letters %+v, want one", dl)
	}
	sent := len(r.Calls())

	// Retention drops the whole day the rows were received on.
	f.clock.Advance(retention + 26*time.Hour)
	if err := f.srv.Tick(t, worker.EventRetentionName); err != nil {
		t.Fatalf("retention: %v", err)
	}
	res := do(f.p.admin.RedeliverSinkDeadLetterWithResponse(t.Context(), f.p.key, created.Id, dl[0].Id))
	if res.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(string(res.Body), `"rows_expired"`) {
		t.Fatalf("POST redeliver = %d: %s, want 422 rows_expired", res.StatusCode(), res.Body)
	}
	if n := len(r.Calls()); n != sent {
		t.Errorf("the receiver got %d calls, want nothing sent for rows that are gone", n-sent)
	}
	if after := f.deadLetters(t, created); len(after) != 1 || after[0].Attempts != 5 || after[0].Status != management.SinkDeadLetterStatusExpired {
		t.Errorf("dead letters after the refusal %+v, want it expired by the drop, its attempts unchanged", after)
	}
}

func TestDeadLetterActionsAreScopedToTheirSinkAndNeedSinkManage(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(5, rejected), []testkit.Reply{{}})...)
	// Limited to an environment, the dead letter keeps the filter its batch had.
	body := small(r, "exposure")
	body.Environments = &[]string{"production"}
	created, _ := f.enabled(t, body)
	other, _ := f.create(t, webhook(r, "alias"))
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	f.rounds(t, 5)
	dl := f.deadLetters(t, created)[0]

	redeliver := do(f.p.admin.RedeliverSinkDeadLetterWithResponse(t.Context(), f.p.key, other.Id, dl.Id))
	discard := do(f.p.admin.DiscardSinkDeadLetterWithResponse(t.Context(), f.p.key, other.Id, dl.Id))
	if redeliver.StatusCode() != http.StatusNotFound || discard.StatusCode() != http.StatusNotFound {
		t.Errorf("redeliver and discard through another sink = %d and %d, want 404", redeliver.StatusCode(), discard.StatusCode())
	}
	if n := len(f.deadLetters(t, other)); n != 0 {
		t.Errorf("another sink lists %d dead letters, want none", n)
	}

	for _, role := range []access.RoleName{access.RoleViewer, access.RoleDeveloper} {
		c := testkit.Client(t, f.srv, testkit.GrantedToken(t, testkit.Pool(t), f.p.projectID(t), role))
		for name, res := range map[string]interface{ StatusCode() int }{
			"list":      do(c.ListSinkDeadLettersWithResponse(t.Context(), f.p.key, created.Id)),
			"redeliver": do(c.RedeliverSinkDeadLetterWithResponse(t.Context(), f.p.key, created.Id, dl.Id)),
			"discard":   do(c.DiscardSinkDeadLetterWithResponse(t.Context(), f.p.key, created.Id, dl.Id)),
			"resume":    do(c.ResumeSinkWithResponse(t.Context(), f.p.key, created.Id)),
		} {
			if res.StatusCode() != http.StatusForbidden {
				t.Errorf("a %s's %s = %d, want 403", role, name, res.StatusCode())
			}
		}
	}
	if left := f.deadLetters(t, created); len(left) != 1 {
		t.Errorf("dead letters %+v, want the one nobody was allowed to touch", left)
	}
}

// brief tells a sink's standing in a failure message, in place of dumping it.
func brief(s management.Sink) string {
	return fmt.Sprintf("{health %s, %d dead letters, stopped at %v, enabled %v}", s.Health, s.DeadLetters, s.StoppedAt, s.Enabled)
}

func TestARedeliveryOfASinkStartedFromATimeSendsNothingReceivedBeforeTheStart(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(5, rejected), []testkit.Reply{{}})...)
	created, secret := f.create(t, webhook(r, "exposure"))

	// The stream starts behind the open transaction, so its first batch spans
	// two rows received before the start, which it leaves out, and one after.
	written, release := f.srv.HoldNextIngest(t)
	held := f.exposure("u-1", "true", f.clock.Now())
	first := make(chan answer, 1)
	go func() { first <- postEvents(t, f.srv, f.server, f.envelope(t, held)) }()
	<-written
	f.post(t, f.exposure("u-2", "true", f.clock.Now()))
	f.clock.Advance(2 * time.Hour)
	recent := f.exposure("u-3", "true", f.clock.Now())
	f.post(t, recent)
	f.enable(t, created, management.SinkEnable{StartFrom: ptr(f.clock.Now().Add(-time.Hour))})
	release()
	if a := <-first; a.status != http.StatusAccepted {
		t.Fatalf("the held batch = %d: %s", a.status, a.body)
	}

	f.rounds(t, 5)
	list := f.deadLetters(t, created)
	if len(list) != 1 || list[0].Rows != 1 {
		t.Fatalf("dead letters %+v, want one of the event received after the start", list)
	}
	f.redeliver(t, created, list[0])
	calls := callsOf(t, r, secret)
	if len(calls) != 6 {
		t.Fatalf("the receiver got %d batches, want the five rejections and the redelivery", len(calls))
	}
	if got, want := idsOf(calls[5].Events), []string{eventIDOf(recent)}; !slices.Equal(got, want) {
		t.Errorf("redelivered %v, want %v: the rows the batch left out stay out", got, want)
	}
}
