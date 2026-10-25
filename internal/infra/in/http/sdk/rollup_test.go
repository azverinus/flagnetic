package sdk_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// rollupFixture is a project with the draft experiment checkout_test, which
// weighs the variations true and false of the flag checkout for units of
// type user, its keys in production, and a counter numbering its events.
type rollupFixture struct {
	exposuresFixture
	mu sync.Mutex
	n  int
}

func startRollup(t *testing.T) *rollupFixture {
	t.Helper()
	return &rollupFixture{exposuresFixture: startExposures(t)}
}

// exposure is an exposure of unit to variation, occurring at at, with a new
// event ID minted then.
func (f *rollupFixture) exposure(unit, variation string, at time.Time) map[string]any {
	f.mu.Lock()
	f.n++
	n := f.n
	f.mu.Unlock()
	return exposureEvent(n, map[string]any{
		"event_id": eventID(at, n), "occurred_at": at.Format(time.RFC3339Nano), "unit_id": unit, "variation_key": variation,
	})
}

// override is an override exposure of unit to variation, occurring at at.
func (f *rollupFixture) override(unit, variation string, at time.Time) map[string]any {
	return with(f.exposure(unit, variation, at), map[string]any{"override": true, "evaluation_reason": "override"})
}

// post sends events with the server key, sent now by the server's clock so
// no clock skew moves them, and wants each accepted.
func (f *rollupFixture) post(t *testing.T, evs ...any) {
	t.Helper()
	f.postWith(t, f.srv, evs...)
}

func (f *rollupFixture) postWith(t *testing.T, srv *testkit.Server, evs ...any) {
	t.Helper()
	res := batchResult(t, srv, f.server, f.envelope(t, evs...))
	if res.Accepted != len(evs) {
		t.Fatalf("batch = %+v, want all %d accepted", res, len(evs))
	}
}

// envelope wraps events in a batch sent now by the server's clock.
func (f *rollupFixture) envelope(t *testing.T, evs ...any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"schema_version": 1, "sent_at": f.clock.Now().Format(time.RFC3339Nano),
		"sdk": map[string]any{"name": "flagnetic-dart", "version": "1.0.0", "platform": "linux"}, "events": evs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *rollupFixture) rollUp(t *testing.T) {
	t.Helper()
	f.srv.TickRollup(t, worker.ExposureRollupName)
}

func (f *rollupFixture) exposures(t *testing.T) management.ExperimentExposures {
	t.Helper()
	res := do(f.p.admin.GetExperimentExposuresWithResponse(t.Context(), f.p.key, "checkout_test",
		&management.GetExperimentExposuresParams{Environment: "production"}))
	if res.JSON200 == nil {
		t.Fatalf("GET …/exposures = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func byVariation(x management.ExperimentExposures) map[string]management.VariationExposures {
	out := map[string]management.VariationExposures{}
	for _, v := range x.Variations {
		out[v.Variation] = v
	}
	return out
}

func (f *rollupFixture) timeline(t *testing.T, from, to *time.Time) []management.ExposureDay {
	t.Helper()
	params := &management.GetExperimentExposureTimelineParams{Environment: "production"}
	if from != nil {
		params.From = &openapi_types.Date{Time: *from}
	}
	if to != nil {
		params.To = &openapi_types.Date{Time: *to}
	}
	res := do(f.p.admin.GetExperimentExposureTimelineWithResponse(t.Context(), f.p.key, "checkout_test", params))
	if res.JSON200 == nil {
		t.Fatalf("GET …/exposures/timeline = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func (f *rollupFixture) conflicts(t *testing.T) []management.ExposureConflict {
	t.Helper()
	res := do(f.p.admin.GetExperimentConflictsWithResponse(t.Context(), f.p.key, "checkout_test",
		&management.GetExperimentConflictsParams{Environment: "production"}))
	if res.JSON200 == nil {
		t.Fatalf("GET …/conflicts = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

// day returns the timeline row of variation on the UTC day of at, the zero
// row when there is none.
func day(rows []management.ExposureDay, variation string, at time.Time) management.ExposureDay {
	for _, r := range rows {
		if r.Variation == variation && r.Day.Format(time.DateOnly) == at.UTC().Format(time.DateOnly) {
			return r
		}
	}
	return management.ExposureDay{}
}

func TestAUnitsFirstExposureIsItsEarliestByOccurrenceNotByArrival(t *testing.T) {
	f := startRollup(t)
	base := f.clock.Now()
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }

	f.post(t, f.exposure("u", "true", at(1)))
	f.post(t, f.exposure("u", "true", at(2)))
	f.rollUp(t)
	x := f.exposures(t)
	if v := byVariation(x)["true"]; v.FirstExposures != 1 || v.Exposures != 2 || x.Conflicts.Total != 0 {
		t.Fatalf("after two exposures of one variation = %+v, want one first exposure of true and no conflict", x)
	}
	// The exposure rollup's cursor is one for the whole package, which
	// earlier tests may have moved, at other times than this test's clock.
	if x.AsOf == nil || !x.AsOf.Equal(f.clock.Now()) {
		t.Errorf("as_of = %v after the rollup moved, want %s", x.AsOf, f.clock.Now())
	}

	f.post(t, f.exposure("u", "false", at(3)))
	f.rollUp(t)
	x = f.exposures(t)
	if v := byVariation(x)["true"]; v.FirstExposures != 1 || x.Conflicts != (management.ConflictTotals{Total: 1, Units: 1}) {
		t.Fatalf("after a later exposure of the other variation = %+v, want true kept and 1 conflict", x)
	}

	// An exposure of the other variation that occurred before every other
	// one replaces the first exposure, arriving last as it does: the two
	// exposures of true are now the conflicts.
	f.post(t, f.exposure("u", "false", at(0)))
	f.rollUp(t)
	x = f.exposures(t)
	got := byVariation(x)
	if got["true"].FirstExposures != 0 || got["false"].FirstExposures != 1 {
		t.Errorf("first exposures = %+v, want the late earlier exposure of false", x.Variations)
	}
	if got["true"].Exposures != 2 || got["false"].Exposures != 2 {
		t.Errorf("exposures = %+v, want 2 of each", x.Variations)
	}
	if x.Conflicts != (management.ConflictTotals{Total: 2, Units: 1}) {
		t.Errorf("conflicts = %+v, want 2 in 1 unit", x.Conflicts)
	}
	if got["false"].ObservedShare != 1 || got["false"].ExpectedShare != 0.5 {
		t.Errorf("shares of false = %+v, want observed 1 and expected 0.5", got["false"])
	}
	conflicts := f.conflicts(t)
	want := management.ExposureConflict{UnitType: "user", UnitId: "u", FirstVariation: "false", ConflictingVariation: "true", ConflictingAt: at(2)}
	if len(conflicts) != 1 || conflicts[0].FirstVariation != want.FirstVariation ||
		conflicts[0].ConflictingVariation != want.ConflictingVariation || !conflicts[0].ConflictingAt.Equal(want.ConflictingAt) {
		t.Errorf("conflicts = %+v, want %+v", conflicts, want)
	}
}

func TestConflictsAreTheSameWhicheverBatchesTheExposuresArriveIn(t *testing.T) {
	one, two := startRollup(t), startRollup(t)
	base := one.clock.Now()
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }

	one.post(t, one.exposure("u", "false", at(2)), one.exposure("u", "false", at(3)), one.exposure("u", "true", at(1)))
	one.rollUp(t)

	two.post(t, two.exposure("u", "false", at(2)), two.exposure("u", "false", at(3)))
	two.rollUp(t)
	two.post(t, two.exposure("u", "true", at(1)))
	two.rollUp(t)

	for name, f := range map[string]*rollupFixture{"one batch": one, "two batches": two} {
		x := f.exposures(t)
		if byVariation(x)["true"].FirstExposures != 1 || x.Conflicts != (management.ConflictTotals{Total: 2, Units: 1}) {
			t.Errorf("%s: %+v, want the first exposure of true and 2 conflicts", name, x)
		}
	}
	if a, b := fmt.Sprint(one.timeline(t, nil, nil)), fmt.Sprint(two.timeline(t, nil, nil)); a != b {
		t.Errorf("timeline of one batch %s, of two %s, want the same", a, b)
	}
	if a, b := fmt.Sprint(one.conflicts(t)), fmt.Sprint(two.conflicts(t)); a != b {
		t.Errorf("conflicts of one batch %s, of two %s, want the same", a, b)
	}
}

func TestAnOverrideExposureIsCountedAsOverriddenAndNeverTouchesAFirstExposure(t *testing.T) {
	f := startRollup(t)
	now := f.clock.Now()
	f.post(t, f.exposure("u", "true", now), f.override("u", "false", now.Add(-time.Minute)), f.override("v", "qa", now))
	f.rollUp(t)

	x := f.exposures(t)
	got := byVariation(x)
	if got["true"] != (management.VariationExposures{Variation: "true", Exposures: 1, FirstExposures: 1, ObservedShare: 1, ExpectedShare: 0.5}) {
		t.Errorf("true = %+v, want its exposure and first exposure", got["true"])
	}
	if got["false"] != (management.VariationExposures{Variation: "false", Overridden: 1, ExpectedShare: 0.5}) {
		t.Errorf("false = %+v, want one overridden only", got["false"])
	}
	if x.Conflicts.Total != 0 {
		t.Errorf("conflicts = %+v, want none", x.Conflicts)
	}
	// A variation the experiment does not weigh, named by an override, is
	// listed after the experiment's own.
	if len(x.Variations) != 3 || x.Variations[2] != (management.VariationExposures{Variation: "qa", Overridden: 1}) {
		t.Errorf("variations = %+v, want true, false, then qa overridden once", x.Variations)
	}
}

func TestAReplacementMovesTheFirstExposureCountToItsOwnDay(t *testing.T) {
	f := startRollup(t)
	day1 := f.clock.Now()
	f.post(t, f.exposure("u", "true", day1))
	f.rollUp(t)
	if got := day(f.timeline(t, nil, nil), "true", day1); got.FirstExposures != 1 {
		t.Fatalf("day 1 = %+v, want one first exposure of true", got)
	}

	f.clock.Advance(24 * time.Hour)
	day2 := f.clock.Now()
	earlier := day1.Add(-24 * time.Hour)
	f.post(t, f.exposure("u", "false", earlier))
	f.rollUp(t)

	rows := f.timeline(t, nil, nil)
	if got := day(rows, "true", day1); got.FirstExposures != 0 || got.Exposures != 1 {
		t.Errorf("true on day 1 = %+v, want its exposure but no first exposure", got)
	}
	if got := day(rows, "false", earlier); got.FirstExposures != 1 || got.Exposures != 0 {
		t.Errorf("false on the day it occurred = %+v, want the first exposure", got)
	}
	if got := day(rows, "false", day2); got.Exposures != 1 || got.FirstExposures != 0 {
		t.Errorf("false on the day it arrived = %+v, want the exposure", got)
	}
}

func TestTheTimelineIsFilteredByDay(t *testing.T) {
	f := startRollup(t)
	day1 := f.clock.Now()
	f.post(t, f.exposure("u-1", "true", day1))
	f.clock.Advance(24 * time.Hour)
	day2 := f.clock.Now()
	f.post(t, f.exposure("u-2", "false", day2))
	f.clock.Advance(24 * time.Hour)
	day3 := f.clock.Now()
	f.post(t, f.exposure("u-3", "true", day3))
	f.rollUp(t)

	if rows := f.timeline(t, nil, nil); len(rows) != 3 {
		t.Fatalf("timeline = %+v, want a row per day", rows)
	}
	rows := f.timeline(t, &day2, &day2)
	if len(rows) != 1 || rows[0].Variation != "false" || rows[0].FirstExposures != 1 {
		t.Errorf("day 2 only = %+v, want the first exposure of false", rows)
	}
	if rows := f.timeline(t, &day2, nil); len(rows) != 2 {
		t.Errorf("from day 2 = %+v, want days 2 and 3", rows)
	}
}

func TestTheConflictsAreASampleOfAHundredTheMostRecentFirst(t *testing.T) {
	f := startRollup(t)
	base := f.clock.Now().Add(-3 * time.Hour)
	const units = 105
	var evs []any
	for i := range units {
		at := base.Add(time.Duration(i) * time.Minute)
		evs = append(evs, f.exposure(fmt.Sprintf("u-%03d", i), "true", at), f.exposure(fmt.Sprintf("u-%03d", i), "false", at.Add(time.Second)))
	}
	f.post(t, evs...)
	f.rollUp(t)

	conflicts := f.conflicts(t)
	if len(conflicts) != 100 {
		t.Fatalf("%d conflicting units, want 100", len(conflicts))
	}
	for i, c := range conflicts {
		if want := fmt.Sprintf("u-%03d", units-1-i); c.UnitId != want || c.FirstVariation != "true" || c.ConflictingVariation != "false" {
			t.Errorf("conflict %d = %+v, want %s from true to false", i, c, want)
		}
	}
	if x := f.exposures(t); x.Conflicts != (management.ConflictTotals{Total: units, Units: units}) {
		t.Errorf("conflicts = %+v, want %d in %d units", x.Conflicts, units, units)
	}
}

func TestTheExposureReadsAnswerNotFoundForAnUnknownEnvironmentOrExperiment(t *testing.T) {
	f := startRollup(t)
	ctx := t.Context()
	for name, status := range map[string]int{
		"exposures, environment": do(f.p.admin.GetExperimentExposuresWithResponse(ctx, f.p.key, "checkout_test", &management.GetExperimentExposuresParams{Environment: "nowhere"})).StatusCode(),
		"timeline, environment":  do(f.p.admin.GetExperimentExposureTimelineWithResponse(ctx, f.p.key, "checkout_test", &management.GetExperimentExposureTimelineParams{Environment: "nowhere"})).StatusCode(),
		"conflicts, environment": do(f.p.admin.GetExperimentConflictsWithResponse(ctx, f.p.key, "checkout_test", &management.GetExperimentConflictsParams{Environment: "nowhere"})).StatusCode(),
		"exposures, experiment":  do(f.p.admin.GetExperimentExposuresWithResponse(ctx, f.p.key, "nothing", &management.GetExperimentExposuresParams{Environment: "production"})).StatusCode(),
		"timeline, experiment":   do(f.p.admin.GetExperimentExposureTimelineWithResponse(ctx, f.p.key, "nothing", &management.GetExperimentExposureTimelineParams{Environment: "production"})).StatusCode(),
		"conflicts, experiment":  do(f.p.admin.GetExperimentConflictsWithResponse(ctx, f.p.key, "nothing", &management.GetExperimentConflictsParams{Environment: "production"})).StatusCode(),
	} {
		if status != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, status)
		}
	}
}

func TestTheExposuresShowTheRefusalsByReason(t *testing.T) {
	f := startRollup(t)
	body := batch(f.exposure("u", "maybe", f.clock.Now()), with(f.exposure("u", "true", f.clock.Now()), map[string]any{"unit_type": "account"}),
		f.exposure("v", "nope", f.clock.Now()))
	batchResult(t, f.srv, f.server, body)
	x := f.exposures(t)
	want := []management.ExposureRejections{{Reason: "unit_type_mismatch", Count: 1}, {Reason: "unknown_variation", Count: 2}}
	if fmt.Sprint(x.Rejections) != fmt.Sprint(want) {
		t.Errorf("rejections = %+v, want %+v", x.Rejections, want)
	}
}

func TestTheRollupCountsTheEventsReceivedPerDay(t *testing.T) {
	f := startRollup(t)
	day1 := f.clock.Now()
	f.post(t, f.exposure("u", "true", day1), f.override("v", "false", day1))
	f.clock.Advance(24 * time.Hour)
	f.post(t, f.exposure("u", "true", day1))
	f.rollUp(t)

	project := projectIDOf(t, f.p)
	counts, err := pgevents.NewStore(testkit.Pool(t)).EventCounts(t.Context(), project, day1, f.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	day := func(at time.Time) events.EventDay {
		return events.EventDay{Project: project, Day: at.UTC().Truncate(24 * time.Hour), Type: events.EventExposure}
	}
	if counts[day(day1)] != 2 || counts[day(f.clock.Now())] != 1 || len(counts) != 2 {
		t.Errorf("event counts = %+v, want 2 on day 1 and 1 on day 2", counts)
	}
}

func projectIDOf(t *testing.T, p project) workspace.ProjectID {
	t.Helper()
	res := do(p.admin.GetProjectWithResponse(t.Context(), p.key))
	if res.JSON200 == nil {
		t.Fatalf("GET /projects/%s = %d: %s", p.key, res.StatusCode(), res.Body)
	}
	return id.FromBytes[workspace.Project](res.JSON200.Id)
}

func TestAnOpenTransactionHoldsTheRollupBackUntilItCommits(t *testing.T) {
	f := startRollup(t)
	now := f.clock.Now()
	written, release := f.srv.HoldNextIngest(t)
	body := f.envelope(t, f.exposure("u-1", "true", now))
	held := make(chan answer, 1)
	go func() { held <- postEvents(t, f.srv, f.server, body) }()
	<-written
	// A batch committed after the held one, while it is still open.
	f.post(t, f.exposure("u-2", "false", now))

	f.rollUp(t)
	if x := f.exposures(t); x.Conflicts.Total != 0 || byVariation(x)["true"].Exposures != 0 || byVariation(x)["false"].Exposures != 0 {
		t.Fatalf("while a transaction is open = %+v, want nothing past it rolled up", x.Variations)
	}
	f.clock.Advance(30 * time.Second)
	f.rollUp(t)
	if lag := testkit.Metric(t, f.srv.Scrape(t), `flagnetic_rollup_lag_seconds{stream="exposures"}`); lag < 30 {
		t.Errorf("rollup lag = %v while held back, want at least 30", lag)
	}

	release()
	if a := <-held; a.status != http.StatusAccepted {
		t.Fatalf("the held batch = %d: %s", a.status, a.body)
	}
	f.rollUp(t)
	got := byVariation(f.exposures(t))
	if got["true"].FirstExposures != 1 || got["false"].FirstExposures != 1 {
		t.Errorf("after the commit = %+v, want both rolled up", got)
	}
	if lag := testkit.Metric(t, f.srv.Scrape(t), `flagnetic_rollup_lag_seconds{stream="exposures"}`); lag != 0 {
		t.Errorf("rollup lag = %v once caught up, want 0", lag)
	}
}

func TestTwoReplicasRollingUpAtOnceCountEachExposureOnce(t *testing.T) {
	f := startRollup(t)
	other := testkit.StartServer(t, testkit.WithClock(f.clock))
	var evs []any
	for i := range 50 {
		evs = append(evs, f.exposure(fmt.Sprintf("u-%d", i), "true", f.clock.Now()))
	}
	f.post(t, evs...)

	var wg sync.WaitGroup
	for _, srv := range []*testkit.Server{f.srv, other, f.srv, other} {
		wg.Go(func() { srv.TickRollup(t, worker.ExposureRollupName) })
	}
	wg.Wait()
	if v := byVariation(f.exposures(t))["true"]; v.Exposures != 50 || v.FirstExposures != 50 {
		t.Errorf("true = %+v, want each of the 50 exposures counted once", v)
	}
}

func TestTheRollupPassesOverTheExposuresOfADeletedExperiment(t *testing.T) {
	f := startRollup(t)
	f.p.experiment(t, "banner_test", "checkout", "user", "true", 5000, "false", 5000)
	f.post(t, f.exposure("u", "true", f.clock.Now()), with(f.exposure("u", "false", f.clock.Now()), map[string]any{"experiment_key": "banner_test"}))
	got := do(f.p.admin.GetExperimentWithResponse(t.Context(), f.p.key, "banner_test"))
	etag := got.HTTPResponse.Header.Get("ETag")
	res := do(f.p.admin.DeleteExperimentWithResponse(t.Context(), f.p.key, "banner_test",
		&management.DeleteExperimentParams{IfMatch: &etag}))
	if res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE …/experiments/banner_test = %d: %s", res.StatusCode(), res.Body)
	}

	f.rollUp(t)
	if v := byVariation(f.exposures(t))["true"]; v.FirstExposures != 1 {
		t.Errorf("true = %+v, want the exposure of the experiment still there rolled up", v)
	}
	project := projectIDOf(t, f.p)
	counts, err := pgevents.NewStore(testkit.Pool(t)).EventCounts(t.Context(), project, f.clock.Now(), f.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	received := events.EventDay{Project: project, Day: f.clock.Now().UTC().Truncate(24 * time.Hour), Type: events.EventExposure}
	if counts[received] != 2 {
		t.Errorf("event counts = %+v, want both exposures the project received, the deleted experiment's too", counts)
	}
}
