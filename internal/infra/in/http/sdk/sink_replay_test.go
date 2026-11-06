package sdk_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func (f delivering) replay(t *testing.T, sink management.Sink, body management.SinkReplay) management.Sink {
	t.Helper()
	res := do(f.p.admin.ReplaySinkWithResponse(t.Context(), f.p.key, sink.Id, body))
	if res.JSON200 == nil {
		t.Fatalf("POST replay = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// timeline posts one exposure of a unit an hour after the one before, from
// the current time, and returns the events with the time each was received.
func (f delivering) timeline(t *testing.T, units ...string) (events []any, at []time.Time) {
	t.Helper()
	for _, u := range units {
		e := f.exposure(u, "true", f.clock.Now())
		f.post(t, e)
		events, at = append(events, e), append(at, f.clock.Now())
		f.clock.Advance(time.Hour)
	}
	return events, at
}

// delivered returns the event ids of the batches the receiver got since the
// first n calls, in order, and their delivery ids.
func delivered(t *testing.T, r *testkit.Receiver, secret string, n int) (events, deliveries []string) {
	t.Helper()
	for _, call := range batchCalls(r)[n:] {
		b := decodeBatch(t, call, secret)
		events = append(events, idsOf(b.Events)...)
		deliveries = append(deliveries, b.DeliveryID)
	}
	return events, deliveries
}

func ids(events ...any) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = eventIDOf(e)
	}
	return out
}

func TestReplayFromATimeRedeliversEveryRowSinceInOrderAndTheLiveStreamGoesOn(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.enabled(t, webhook(r, "exposure"))
	events, at := f.timeline(t, "u-1", "u-2", "u-3")
	tick(t, f.srv)
	first := len(batchCalls(r))
	if got, _ := delivered(t, r, secret, 0); !slices.Equal(got, ids(events...)) {
		t.Fatalf("delivered %v, want all three", got)
	}

	replayed := f.replay(t, sink, management.SinkReplay{From: at[1]})
	if replayed.Id != sink.Id {
		t.Errorf("replay answered sink %v, want %v", replayed.Id, sink.Id)
	}
	tick(t, f.srv)
	if got, _ := delivered(t, r, secret, first); !slices.Equal(got, ids(events[1:]...)) {
		t.Fatalf("replayed %v, want every row since the second in order", got)
	}

	// The replay ran on into the live stream: a new event is sent once.
	next := f.exposure("u-4", "true", f.clock.Now())
	f.post(t, next)
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	after := len(batchCalls(r))
	if got, _ := delivered(t, r, secret, first+1); !slices.Equal(got, ids(next)) {
		t.Errorf("after the replay the receiver got %v, want only the new event once", got)
	}
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if n := len(batchCalls(r)); n != after {
		t.Errorf("the receiver got %d calls, want none more with nothing new", n-after)
	}
	if n := len(f.audited(t, "sink.replayed", sink)); n != 1 {
		t.Errorf("%d sink.replayed entries, want one", n)
	}
}

func TestReplayWithToStopsAtItsBoundAndTheLiveStreamResumesWithoutRepeating(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.enabled(t, webhook(r, "exposure"))
	events, at := f.timeline(t, "u-1", "u-2", "u-3", "u-4")
	tick(t, f.srv)
	first := len(batchCalls(r))

	f.replay(t, sink, management.SinkReplay{From: at[1], To: ptr(at[2])})
	tick(t, f.srv)
	if got, _ := delivered(t, r, secret, first); !slices.Equal(got, ids(events[1], events[2])) {
		t.Fatalf("replayed %v, want the second and third alone: the fourth was delivered live and is not sent twice", got)
	}

	next := f.exposure("u-5", "true", f.clock.Now())
	f.post(t, next)
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if got, _ := delivered(t, r, secret, first+1); !slices.Equal(got, ids(next)) {
		t.Errorf("after the replay the receiver got %v, want the live stream from where it was", got)
	}
}

func TestReplayWithAToBeyondTheLivePositionRunsOnWithoutSendingTwice(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.enabled(t, webhook(r, "exposure"))
	events, at := f.timeline(t, "u-1", "u-2", "u-3")
	tick(t, f.srv)
	first := len(batchCalls(r))

	f.replay(t, sink, management.SinkReplay{From: at[1], To: ptr(f.clock.Now())})
	tick(t, f.srv)
	if got, _ := delivered(t, r, secret, first); !slices.Equal(got, ids(events[1:]...)) {
		t.Errorf("replayed %v, want the rows from the second once", got)
	}
}

func TestReplayWithToResolvingToNothingLeavesTheStreamUntouched(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.enabled(t, webhook(r, "exposure"))
	start := f.clock.Now()
	f.clock.Advance(3 * time.Hour)
	events, _ := f.timeline(t, "u-1", "u-2")
	tick(t, f.srv)
	first := len(batchCalls(r))

	f.replay(t, sink, management.SinkReplay{From: start.Add(-2 * time.Hour), To: ptr(start.Add(-time.Hour))})
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if n := len(batchCalls(r)); n != first {
		got, _ := delivered(t, r, secret, first)
		t.Errorf("the receiver got %v after a replay of a range with no row, want nothing", got)
	}

	// A start after the last row has nothing to replay either.
	f.replay(t, sink, management.SinkReplay{From: f.clock.Now().Add(time.Hour)})
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if n := len(batchCalls(r)); n != first {
		t.Errorf("the receiver got %d calls after a replay from after every row, want none more", n-first)
	}
	_ = events
}

func TestAReplayWithAWidenedEnvironmentsFilterSendsAnotherDeliveryID(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	body := webhook(r, "exposure")
	body.Environments = &[]string{"production"}
	sink, secret := f.enabled(t, body)
	start := f.clock.Now()
	f.postIn(t, "production", f.exposure("u-1", "true", f.clock.Now()))
	f.postIn(t, "staging", f.exposure("u-2", "true", f.clock.Now()))
	tick(t, f.srv)
	first := len(batchCalls(r))

	f.replay(t, sink, management.SinkReplay{From: start})
	tick(t, f.srv)
	once, onceIDs := delivered(t, r, secret, first)
	second := len(batchCalls(r))
	f.replay(t, sink, management.SinkReplay{From: start})
	tick(t, f.srv)
	twice, twiceIDs := delivered(t, r, secret, second)
	if len(once) != 1 || !slices.Equal(once, twice) || !slices.Equal(onceIDs, twiceIDs) {
		t.Fatalf("replays of one range sent %v (%v) then %v (%v), want the production row under one delivery id", once, onceIDs, twice, twiceIDs)
	}

	f.patch(t, sink, management.SinkPatch{Environments: &[]string{}})
	third := len(batchCalls(r))
	f.replay(t, sink, management.SinkReplay{From: start})
	tick(t, f.srv)
	widened, widenedIDs := delivered(t, r, secret, third)
	if len(widened) != 2 || len(widenedIDs) != 1 || widenedIDs[0] == onceIDs[0] {
		t.Errorf("the widened replay sent %v under %v, want both rows under another delivery id than %v", widened, widenedIDs, onceIDs)
	}
}

func TestAReplayFromBeforeRetentionOrOfABackwardsRangeIsRefused(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, _ := f.enabled(t, webhook(r, "exposure"))
	now := f.clock.Now()
	for name, tc := range map[string]struct {
		body management.SinkReplay
		code string
	}{
		"before retention":         {management.SinkReplay{From: now.Add(-400 * 24 * time.Hour)}, "out_of_retention"},
		"to before from":           {management.SinkReplay{From: now.Add(-time.Hour), To: ptr(now.Add(-2 * time.Hour))}, "invalid_replay_range"},
		"a kind the sink does not": {management.SinkReplay{From: now.Add(-time.Hour), EventKinds: &[]string{"alias"}}, "invalid_sink"},
		"a kind that is none":      {management.SinkReplay{From: now.Add(-time.Hour), EventKinds: &[]string{"nope"}}, "invalid_sink"},
	} {
		res := do(f.p.admin.ReplaySinkWithResponse(t.Context(), f.p.key, sink.Id, tc.body))
		if res.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(string(res.Body), `"`+tc.code+`"`) {
			t.Errorf("%s: POST replay = %d: %s, want 422 %s", name, res.StatusCode(), res.Body, tc.code)
		}
	}
	if n := len(f.audited(t, "sink.replayed", sink)); n != 0 {
		t.Errorf("%d sink.replayed entries after refusals, want none", n)
	}
	missing := do(f.p.admin.ReplaySinkWithResponse(t.Context(), f.p.key, uuid.New(), management.SinkReplay{From: now}))
	if missing.StatusCode() != http.StatusNotFound {
		t.Errorf("replay of an unknown sink = %d, want 404", missing.StatusCode())
	}
}

func TestReplayOnlyGoesBackOverTheEventKindsAsked(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.enabled(t, webhook(r))
	start := f.clock.Now()
	alias := f.alias(1)
	f.post(t, f.exposure("u-1", "true", f.clock.Now()), alias)
	tick(t, f.srv)
	first := len(batchCalls(r))

	f.replay(t, sink, management.SinkReplay{From: start, EventKinds: &[]string{"alias"}})
	tick(t, f.srv)
	calls := batchCalls(r)[first:]
	if len(calls) != 1 {
		t.Fatalf("the receiver got %d calls, want the alias stream's alone", len(calls))
	}
	if b := decodeBatch(t, calls[0], secret); b.EventKind != "alias" || !slices.Equal(idsOf(b.Events), []string{eventIDOf(alias)}) {
		t.Errorf("batch %+v, want the alias alone", b)
	}
}

func TestAReplaySentWhileABoundedOneIsPendingStillReturnsToTheLivePosition(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.enabled(t, webhook(r, "exposure"))
	events, at := f.timeline(t, "u-1", "u-2", "u-3", "u-4")
	tick(t, f.srv)
	first := len(batchCalls(r))

	f.replay(t, sink, management.SinkReplay{From: at[2], To: ptr(at[2])})
	f.replay(t, sink, management.SinkReplay{From: at[0], To: ptr(at[1])})
	tick(t, f.srv)
	if got, _ := delivered(t, r, secret, first); !slices.Equal(got, ids(events[0], events[1])) {
		t.Fatalf("replayed %v, want the second replay's range alone", got)
	}
	next := f.exposure("u-5", "true", f.clock.Now())
	f.post(t, next)
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if got, _ := delivered(t, r, secret, first+1); !slices.Equal(got, ids(next)) {
		t.Errorf("after the replays the receiver got %v, want the live stream from where it was", got)
	}
}

func TestADisabledSinkIsReplayedOnceItRunsAgain(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.enabled(t, webhook(r, "exposure"))
	events, at := f.timeline(t, "u-1", "u-2")
	tick(t, f.srv)
	first := len(batchCalls(r))

	disabled := f.patch(t, sink, management.SinkPatch{Enabled: ptr(false)})
	f.replay(t, disabled, management.SinkReplay{From: at[0]})
	f.clock.Advance(time.Minute)
	tick(t, f.srv)
	if n := len(batchCalls(r)); n != first {
		t.Fatalf("a disabled sink sent %d calls, want none", n-first)
	}
	f.enable(t, disabled, management.SinkEnable{})
	tick(t, f.srv)
	if got, _ := delivered(t, r, secret, first); !slices.Equal(got, ids(events...)) {
		t.Errorf("replayed %v after enabling, want both rows", got)
	}
}

func TestSampleSendsTenFlaggedRowsPerKindWithoutTouchingCursorsAndWorksDisabled(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.create(t, webhook(r, "exposure", "alias"))

	res := do(f.p.admin.SampleSinkWithResponse(t.Context(), f.p.key, sink.Id, management.SinkSample{}))
	if res.JSON200 == nil {
		t.Fatalf("POST sample = %d: %s", res.StatusCode(), res.Body)
	}
	results := res.JSON200.Results
	if len(results) != 2 || results[0].EventKind != "exposure" || results[1].EventKind != "alias" {
		t.Fatalf("results %+v, want exposure then alias", results)
	}
	for _, v := range results {
		if !v.Ok || v.Class != nil || v.Status != nil || v.Code != nil {
			t.Errorf("result %+v, want ok with no failure", v)
		}
	}
	calls := r.Calls()
	if len(calls) != 2 {
		t.Fatalf("the receiver got %d calls, want one per kind", len(calls))
	}
	for i, call := range calls {
		b := decodeBatch(t, call, secret)
		if !b.Sample || b.EventKind != results[i].EventKind || len(b.Events) != 10 || !strings.HasPrefix(b.DeliveryID, "sample_") ||
			b.ProjectKey != f.p.key || b.SinkID != sink.Id.String() {
			t.Errorf("call %d envelope %+v, want ten sample rows of %s under a sample delivery id", i, b, results[i].EventKind)
		}
		want := make([]string, 10)
		for n := range want {
			want[n] = "sample-" + string(rune('1'+n))
		}
		want[9] = "sample-10"
		if got := idsOf(b.Events); !slices.Equal(got, want) {
			t.Errorf("sample ids %v, want %v", got, want)
		}
		if b.Events[0]["project_key"] != f.p.key || b.Events[0]["environment_key"] != "sample" {
			t.Errorf("first sample row %v, want the project's own key and sample elsewhere", b.Events[0])
		}
	}
	if got := f.sink(t, sink); got.Enabled || len(got.Streams) != 0 || got.Version != sink.Version {
		t.Errorf("sink after the sample %+v, want it disabled, unchanged, with no stream", got)
	}
	if n := len(f.audited(t, "sink.sampled", sink)); n != 1 {
		t.Errorf("%d sink.sampled entries, want one", n)
	}
}

func TestSampleOfAnEnabledSinkMovesNoCursorAndTouchesNoStat(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, secret := f.enabled(t, webhook(r, "exposure", "alias"))
	before := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, before)

	res := do(f.p.admin.SampleSinkWithResponse(t.Context(), f.p.key, sink.Id, management.SinkSample{EventKinds: &[]string{"exposure"}}))
	if res.JSON200 == nil || len(res.JSON200.Results) != 1 || res.JSON200.Results[0].EventKind != "exposure" {
		t.Fatalf("POST sample = %d: %s, want the one kind asked", res.StatusCode(), res.Body)
	}
	got := f.sink(t, sink)
	if s := streamOf(got, management.SinkEventKind("exposure")); s == nil || s.LastDeliveredAt != nil || s.LastFailure != nil {
		t.Errorf("stream after the sample %+v, want nothing delivered or failed", s)
	}
	tick(t, f.srv)
	var live []string
	for _, call := range batchCalls(r) {
		if b := decodeBatch(t, call, secret); !b.Sample {
			live = append(live, idsOf(b.Events)...)
		}
	}
	if !slices.Equal(live, ids(before)) {
		t.Errorf("live delivery after the sample sent %v, want the event posted before it once", live)
	}
}

func TestSampleOfAFailingTargetIsAnsweredByClassStatusAndCodeNeverItsWords(t *testing.T) {
	f := startDelivering(t)
	broken := testkit.StartReceiver(t, testkit.Reply{Status: http.StatusBadGateway, Body: "words never read"})
	sink, _ := f.create(t, webhook(broken, "alias"))

	res := do(f.p.admin.SampleSinkWithResponse(t.Context(), f.p.key, sink.Id, management.SinkSample{}))
	if res.JSON200 == nil || len(res.JSON200.Results) != 1 {
		t.Fatalf("POST sample = %d: %s, want a 200 with a result", res.StatusCode(), res.Body)
	}
	v := res.JSON200.Results[0]
	if v.Ok || v.Class == nil || *v.Class != management.SinkFailureClass("status") || v.Status == nil || *v.Status != 502 ||
		strings.Contains(string(res.Body), "words never read") {
		t.Errorf("result %+v in %s, want ok false, class status and 502", v, res.Body)
	}
}

func TestSampleRefusesAnEventKindTheSinkDoesNotExport(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	sink, _ := f.create(t, webhook(r, "exposure"))
	res := do(f.p.admin.SampleSinkWithResponse(t.Context(), f.p.key, sink.Id, management.SinkSample{EventKinds: &[]string{"alias"}}))
	if res.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(string(res.Body), `"invalid_sink"`) {
		t.Errorf("POST sample = %d: %s, want 422 invalid_sink", res.StatusCode(), res.Body)
	}
	if n := len(r.Calls()); n != 0 {
		t.Errorf("the receiver got %d calls from a refused sample, want none", n)
	}
}
