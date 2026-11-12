package sdk_test

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// failing is a receiver's reply that a stream retries without limit.
var failing = testkit.Reply{Status: http.StatusServiceUnavailable}

func (f delivering) gaps(t *testing.T, sink management.Sink) []management.SinkGap {
	t.Helper()
	res := do(f.p.admin.ListSinkGapsWithResponse(t.Context(), f.p.key, sink.Id))
	if res.JSON200 == nil {
		t.Fatalf("GET gaps = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func (f delivering) dismissGap(t *testing.T, sink management.Sink, gap management.SinkGap) management.SinkGap {
	t.Helper()
	res := do(f.p.admin.DismissSinkGapWithResponse(t.Context(), f.p.key, sink.Id, gap.Id))
	if res.JSON200 == nil {
		t.Fatalf("POST dismiss = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// retain runs a round of retention.
func (f delivering) retain(t *testing.T) {
	t.Helper()
	if err := f.srv.Tick(t, worker.EventRetentionName); err != nil {
		t.Fatalf("retention: %v", err)
	}
}

// dayEnd is when the UTC day of the fixture's clock ends: the first moment a
// row received now is on a partition retention could drop, retention later.
func (f delivering) dayEnd() time.Time {
	return f.clock.Now().Truncate(24 * time.Hour).Add(24 * time.Hour)
}

// pastRetention moves the clock to just after the day ending at dayEnd leaves
// the window, with the day after it still inside.
func (f delivering) pastRetention(dayEnd time.Time) {
	f.clock.Set(dayEnd.Add(retention + time.Hour))
}

func TestRetentionOvertakingAFailingSinkRecordsOneGapAndDeliveryContinues(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(2, failing), []testkit.Reply{{}})...)
	subscriber := testkit.StartReceiver(t)
	sub := do(f.p.admin.CreateWebhookWithResponse(t.Context(), f.p.key, management.WebhookCreate{
		Name: "On call", Url: subscriber.URL, Events: []management.WebhookEventType{"sink.data_lost"},
	}))
	if sub.JSON201 == nil {
		t.Fatalf("POST webhooks = %d: %s", sub.StatusCode(), sub.Body)
	}
	created, secret := f.enabled(t, webhook(r, "exposure"))
	dayEnd := f.dayEnd()
	received := f.clock.Now()
	lost := []any{f.exposure("u-1", "true", received), f.exposure("u-2", "true", received)}
	f.post(t, lost...)
	tick(t, f.srv)

	f.clock.Set(dayEnd.Add(2 * time.Hour))
	kept := f.exposure("u-3", "true", f.clock.Now())
	f.post(t, kept)
	tick(t, f.srv)
	failed := callsOf(t, r, secret)
	if len(failed) != 2 || failed[0].DeliveryID != failed[1].DeliveryID {
		t.Fatalf("the receiver got %d batches, want the same one twice", len(failed))
	}
	if before := f.sink(t, created); before.Gaps != 0 || len(f.gaps(t, created)) != 0 {
		t.Fatalf("gaps before any drop: %d", before.Gaps)
	}

	f.pastRetention(dayEnd)
	f.retain(t)

	gaps := f.gaps(t, created)
	if len(gaps) != 1 {
		t.Fatalf("gaps after the drop = %+v, want exactly one", gaps)
	}
	g := gaps[0]
	if g.EventKind != "exposure" || g.Rows != 2 || !g.PeriodStart.Equal(received) || !g.PeriodEnd.Equal(received) ||
		!g.DroppedAt.Equal(f.clock.Now()) || g.DismissedAt != nil {
		t.Errorf("gap %+v, want the 2 exposures received at %s, dropped now, not dismissed", g, received)
	}
	if got := f.sink(t, created).Gaps; got != 1 {
		t.Errorf("gaps count = %d, want 1", got)
	}
	if n := len(f.audited(t, "sink.data_lost", created)); n != 1 {
		t.Errorf("%d sink.data_lost entries, want 1", n)
	}
	if got := testkit.Metric(t, f.srv.Scrape(t), `flagnetic_event_gap_rows_total{stream="sink_exposure"}`); got < 2 {
		t.Errorf("gap rows of sink_exposure = %v, want at least the 2 lost", got)
	}
	for _, name := range []string{worker.WebhookFanOutName, worker.WebhookDeliveryName} {
		if err := f.srv.Tick(t, name); err != nil {
			t.Fatal(err)
		}
	}
	calls := subscriber.Calls()
	if len(calls) != 1 {
		t.Fatalf("the subscription got %d calls, want sink.data_lost once", len(calls))
	}
	envelope := calls[0].Envelope(t)
	data, _ := envelope["data"].(map[string]any)
	sinkRef, _ := data["sink"].(map[string]any)
	if envelope["type"] != "sink.data_lost" || sinkRef["id"] != created.Id.String() || data["event_kind"] != "exposure" || data["gap"] == nil {
		t.Errorf("event %v, want sink.data_lost for the sink and the exposure kind", envelope)
	}

	// The sink goes on with what is stored, under a new delivery ID: the
	// batch that was in flight held rows that are gone.
	tick(t, f.srv)
	all := callsOf(t, r, secret)
	if len(all) != 3 {
		t.Fatalf("the receiver got %d batches, want one more after the drop", len(all))
	}
	if next := all[2]; next.DeliveryID == failed[0].DeliveryID || !slices.Equal(idsOf(next.Events), []string{eventIDOf(kept)}) {
		t.Errorf("batch after the drop %+v, want the surviving event under a new delivery ID", next)
	}
	if s := f.sink(t, created); s.Health != management.SinkHealthHealthy {
		t.Errorf("sink after recovery %s, want healthy", brief(s))
	}
	for _, g := range gapsOfEvents(t) {
		if strings.HasPrefix(g, "sink_") {
			t.Errorf("event volume lists a gap of stream %q, want rollup gaps only", g)
		}
	}

	dismissed := f.dismissGap(t, created, g)
	if dismissed.DismissedAt == nil {
		t.Fatalf("dismissed gap %+v, want dismissed_at set", dismissed)
	}
	again := f.dismissGap(t, created, g)
	if again.DismissedAt == nil || !again.DismissedAt.Equal(*dismissed.DismissedAt) {
		t.Errorf("dismissed again %+v, want the first time kept: %v", again, dismissed.DismissedAt)
	}
	if got := f.sink(t, created).Gaps; got != 0 {
		t.Errorf("gaps count after dismissing = %d, want 0", got)
	}
	if listed := f.gaps(t, created); len(listed) != 1 || listed[0].DismissedAt == nil {
		t.Errorf("gaps after dismissing %+v, want the one still listed, dismissed", listed)
	}
	if n := len(f.audited(t, "sink.gap_dismissed", created)); n != 1 {
		t.Errorf("%d sink.gap_dismissed entries, want 1: the repeat records nothing", n)
	}
}

// gapsOfEvents returns the stream of every gap the events context lists,
// which are the rollups' alone.
func gapsOfEvents(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, g := range gaps(t) {
		out = append(out, string(g.Stream))
	}
	return out
}

func TestRetentionOvertakingASinkNobodyTickedRecordsAGap(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	dayEnd := f.dayEnd()
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))

	f.pastRetention(dayEnd)
	f.retain(t)

	if gaps := f.gaps(t, created); len(gaps) != 1 || gaps[0].Rows != 1 {
		t.Fatalf("gaps = %+v, want one of the single row", gaps)
	}
	tick(t, f.srv)
	if n := len(batchCalls(r)); n != 0 {
		t.Errorf("the receiver got %d batches, want nothing: the only row was dropped", n)
	}
	res := do(f.p.admin.ReplaySinkWithResponse(t.Context(), f.p.key, created.Id, management.SinkReplay{From: dayEnd.Add(-time.Hour)}))
	if res.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(string(res.Body), "out_of_retention") {
		t.Errorf("replay from before the drop = %d: %s, want 422 out_of_retention", res.StatusCode(), res.Body)
	}
}

func TestRetentionDropsNothingASinkAlreadyDeliveredAndRecordsNoGap(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	dayEnd := f.dayEnd()
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv)

	f.pastRetention(dayEnd)
	f.retain(t)

	if gaps := f.gaps(t, created); len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none: the row was delivered", gaps)
	}
}

func TestRetentionOvertakingAStoppedSinkRecordsAGapAndExpiresItsDeadLetters(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t, script(times(15, rejected), []testkit.Reply{{}})...)
	created, _ := f.enabled(t, small(r, "exposure"))
	dayEnd := f.dayEnd()
	f.post(t, f.exposure("u-1", "true", f.clock.Now()), f.exposure("u-2", "true", f.clock.Now()),
		f.exposure("u-3", "true", f.clock.Now()), f.exposure("u-4", "true", f.clock.Now()))
	f.rounds(t, 13)
	stopped := f.sink(t, created)
	if stopped.Health != management.SinkHealthStopped || stopped.DeadLetters != 3 {
		t.Fatalf("sink %s, want stopped with three dead letters", brief(stopped))
	}

	f.pastRetention(dayEnd)
	f.retain(t)

	if gaps := f.gaps(t, created); len(gaps) != 1 || gaps[0].Rows != 1 {
		t.Errorf("gaps %+v, want one of the row the stopped stream had not read", gaps)
	}
	letters := f.deadLetters(t, created)
	if len(letters) != 3 {
		t.Fatalf("dead letters %+v, want the three still listed", letters)
	}
	for _, dl := range letters {
		if dl.Status != management.SinkDeadLetterStatusExpired {
			t.Errorf("dead letter %s is %s, want expired: none of its rows is stored", dl.Id, dl.Status)
		}
	}
	after := f.sink(t, created)
	if after.Health != management.SinkHealthStopped || after.DeadLetters != 0 || after.Gaps != 1 {
		t.Errorf("after the drop %s, gaps %d; want stopped, no dead letter waiting, one gap", brief(after), after.Gaps)
	}
	res := do(f.p.admin.RedeliverSinkDeadLetterWithResponse(t.Context(), f.p.key, created.Id, letters[0].Id))
	if res.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(string(res.Body), "rows_expired") {
		t.Errorf("redeliver an expired dead letter = %d: %s, want 422 rows_expired", res.StatusCode(), res.Body)
	}
}

func TestRetentionOvertakingABoundedReplaysResumePointRecordsAGapFromIt(t *testing.T) {
	f := startDelivering(t)
	// The check and the first batch are acknowledged, the replay's batch fails
	// once.
	r := testkit.StartReceiver(t, script([]testkit.Reply{{}}, []testkit.Reply{failing}, []testkit.Reply{{}})...)
	created, secret := f.enabled(t, webhook(r, "exposure"))
	dayEnd := f.dayEnd()
	first := f.clock.Now()
	e0 := f.exposure("u-1", "true", first)
	f.post(t, e0)
	f.clock.Advance(time.Minute)
	e1 := f.exposure("u-2", "true", f.clock.Now())
	f.post(t, e1)
	tick(t, f.srv)

	// The replay of the first event returns to the live position, after the
	// second, and the event that arrives meanwhile is read from there.
	f.replay(t, created, management.SinkReplay{From: first, To: &first})
	tick(t, f.srv)
	f.clock.Advance(time.Minute)
	late := f.exposure("u-3", "true", f.clock.Now())
	f.post(t, late)
	f.clock.Set(dayEnd.Add(2 * time.Hour))
	kept := f.exposure("u-4", "true", f.clock.Now())
	f.post(t, kept)

	f.pastRetention(dayEnd)
	f.retain(t)

	if gaps := f.gaps(t, created); len(gaps) != 1 || gaps[0].Rows != 1 {
		t.Fatalf("gaps %+v, want one of the single row received after the replay's resume point", gaps)
	}
	f.rounds(t, 3)
	var got [][]string
	for _, c := range callsOf(t, r, secret) {
		got = append(got, idsOf(c.Events))
	}
	want := [][]string{{eventIDOf(e0), eventIDOf(e1)}, {eventIDOf(e0)}, {eventIDOf(kept)}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("receiver got %v, want %v: the first two, the replay of the first that failed, and after the gap only the surviving one", got, want)
	}
}

func TestTwoReplicasDropOnceAndRecordOneGapPerStream(t *testing.T) {
	f := startDelivering(t)
	other := testkit.StartServer(t, testkit.WithClock(f.clock), sinkTargets)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r, "exposure", "alias"))
	dayEnd := f.dayEnd()
	f.post(t, f.exposure("u-1", "true", f.clock.Now()), aliasEvent(eventID(f.clock.Now(), 910)))

	f.pastRetention(dayEnd)
	var wg sync.WaitGroup
	for _, srv := range []*testkit.Server{f.srv, other} {
		wg.Go(func() {
			if err := srv.Tick(t, worker.EventRetentionName); err != nil {
				t.Errorf("retention: %v", err)
			}
		})
	}
	wg.Wait()

	gaps := f.gaps(t, created)
	if len(gaps) != 2 {
		t.Fatalf("gaps %+v, want one per stream", gaps)
	}
	kinds := []string{string(gaps[0].EventKind), string(gaps[1].EventKind)}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{"alias", "exposure"}) {
		t.Errorf("gaps of %v, want one of each kind", kinds)
	}
}

func TestRetentionWaitsForASinkBeingChangedBeforeTakingItsStreams(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	dayEnd := f.dayEnd()
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	f.pastRetention(dayEnd)

	// A transaction taking the sink's row and then its streams, as resuming
	// or deleting the sink does, holds the row while retention starts.
	pool := testkit.Pool(t)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	var pid int
	if err = tx.QueryRow(t.Context(), `SELECT pg_backend_pid() FROM sinks WHERE id = $1 FOR UPDATE`, created.Id.String()).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.srv.Tick(t, worker.EventRetentionName) }()
	awaitLockWait(t, pid)

	if _, err = tx.Exec(t.Context(), `SELECT 1 FROM sink_streams WHERE sink_id = $1 FOR UPDATE`, created.Id.String()); err != nil {
		t.Fatalf("taking the sink's streams while retention waits: %v", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatalf("retention: %v", err)
	}
	if gaps := f.gaps(t, created); len(gaps) != 1 {
		t.Errorf("gaps %+v, want one", gaps)
	}
}

// awaitLockWait waits until a session of the database other than holder waits
// for a lock.
func awaitLockWait(t *testing.T, holder int) {
	t.Helper()
	pool := testkit.Pool(t)
	for range 500 {
		var waiting bool
		err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> $1)`, holder).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("retention never came to wait on a lock")
}

func TestGapActionsAreScopedToTheirSinkAndNeedSinkManage(t *testing.T) {
	f := startDelivering(t)
	r := testkit.StartReceiver(t)
	created, _ := f.enabled(t, webhook(r, "exposure"))
	other, _ := f.create(t, webhook(r, "alias"))
	dayEnd := f.dayEnd()
	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	f.pastRetention(dayEnd)
	f.retain(t)
	gaps := f.gaps(t, created)
	if len(gaps) != 1 {
		t.Fatalf("gaps %+v, want one", gaps)
	}

	if res := do(f.p.admin.DismissSinkGapWithResponse(t.Context(), f.p.key, other.Id, gaps[0].Id)); res.StatusCode() != http.StatusNotFound {
		t.Errorf("dismiss through another sink = %d, want 404", res.StatusCode())
	}
	if n := len(f.gaps(t, other)); n != 0 {
		t.Errorf("another sink lists %d gaps, want none", n)
	}
	for _, role := range []access.RoleName{access.RoleViewer, access.RoleDeveloper} {
		c := testkit.Client(t, f.srv, testkit.GrantedToken(t, testkit.Pool(t), f.p.projectID(t), role))
		for name, res := range map[string]interface{ StatusCode() int }{
			"list":    do(c.ListSinkGapsWithResponse(t.Context(), f.p.key, created.Id)),
			"dismiss": do(c.DismissSinkGapWithResponse(t.Context(), f.p.key, created.Id, gaps[0].Id)),
		} {
			if res.StatusCode() != http.StatusForbidden {
				t.Errorf("a %s's %s = %d, want 403", role, name, res.StatusCode())
			}
		}
	}
	if g := f.gaps(t, created); g[0].DismissedAt != nil {
		t.Error("a gap was dismissed by someone who may not")
	}
}
