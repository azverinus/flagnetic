package sdk_test

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// sampleRatioFixture is the running experiment checkout_test on the flag
// checkout, its rule held by staging and production, with a server key in
// each to send exposures with.
type sampleRatioFixture struct {
	srv   *testkit.Server
	clock *clock.Fake
	p     project
	keys  map[string]string
	exp   management.Experiment
	units int
	n     int
}

// startSampleRatio starts checkout_test weighing true and false as given,
// including allocation of the units.
func startSampleRatio(t *testing.T, srv *testkit.Server, c *clock.Fake, weightTrue, weightFalse, allocation int) *sampleRatioFixture {
	t.Helper()
	p := newProject(t, srv)
	p.flag(t, "checkout")
	created := do(p.admin.CreateExperimentWithResponse(t.Context(), p.key, management.ExperimentCreate{
		Key: "checkout_test", Name: "Checkout test", Flag: "checkout", UnitType: "user",
		Hypothesis:    ptr("The new checkout raises completed orders."),
		ExpectedEndAt: &openapi_types.Date{Time: time.Date(2099, time.December, 1, 0, 0, 0, 0, time.UTC)},
		Variations: []management.ExperimentWeight{
			{Variation: "true", Weight: weightTrue}, {Variation: "false", Weight: weightFalse},
		},
		TrafficAllocation: ptr(allocation),
	}))
	if created.JSON201 == nil {
		t.Fatalf("POST …/experiments = %d: %s", created.StatusCode(), created.Body)
	}
	f := &sampleRatioFixture{srv: srv, clock: c, p: p, keys: map[string]string{}}
	rules := []management.RuleInput{{Serve: management.Serve{Kind: "experiment", Experiment: ptr("checkout_test")}}}
	for _, env := range []string{"staging", "production"} {
		res := do(p.admin.PutFlagConfigurationWithResponse(t.Context(), p.key, "checkout", env,
			&management.PutFlagConfigurationParams{IfMatch: ptr(`"1"`)},
			management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "false", Rules: &rules}))
		if res.JSON200 == nil {
			t.Fatalf("PUT …/configurations/%s = %d: %s", env, res.StatusCode(), res.Body)
		}
		_, f.keys[env] = p.sdkKey(t, env, management.SdkKeyKindServer)
	}
	started := do(p.admin.StartExperimentWithResponse(t.Context(), p.key, "checkout_test",
		&management.StartExperimentParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(created.JSON201.Version, 10)))}))
	if started.JSON200 == nil {
		t.Fatalf("start = %d: %s", started.StatusCode(), started.Body)
	}
	f.exp = *started.JSON200
	// The package's tests share one schema and the checks go over every
	// running experiment, each test on a clock of its own: one left running
	// would be checked at another test's time, earlier than its alerts.
	t.Cleanup(func() {
		got := do(p.admin.GetExperimentWithResponse(context.Background(), p.key, "checkout_test"))
		if got.JSON200 == nil || got.JSON200.Status != management.ExperimentStatusRunning {
			return
		}
		res := do(p.admin.StopExperimentWithResponse(context.Background(), p.key, "checkout_test",
			&management.StopExperimentParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(got.JSON200.Version, 10)))},
			management.ExperimentStop{Outcome: "removed"}))
		if res.JSON200 == nil {
			t.Errorf("stopping checkout_test = %d: %s", res.StatusCode(), res.Body)
		}
	})
	return f
}

func startSampleRatioServer(t *testing.T) (*testkit.Server, *clock.Fake) {
	t.Helper()
	c := clock.NewFake(protocolTime)
	return testkit.StartServer(t, testkit.WithClock(c)), c
}

// exposure is an exposure of unit to variation now.
func (f *sampleRatioFixture) exposure(unit, variation string) map[string]any {
	f.n++
	now := f.clock.Now()
	return exposureEvent(f.n, map[string]any{
		"event_id": eventID(now, f.n), "occurred_at": now.Format(time.RFC3339Nano), "unit_id": unit, "variation_key": variation,
	})
}

// expose sends one first exposure of a new unit per count of each variation in
// env, in batches the API takes.
func (f *sampleRatioFixture) expose(t *testing.T, env string, counts map[string]int) {
	t.Helper()
	var all []any
	for _, variation := range []string{"true", "false"} {
		for range counts[variation] {
			f.units++
			all = append(all, f.exposure("u-"+strconv.Itoa(f.units), variation))
		}
	}
	f.send(t, env, all)
}

func (f *sampleRatioFixture) send(t *testing.T, env string, all []any) {
	t.Helper()
	for len(all) > 0 {
		chunk := all[:min(len(all), 500)]
		all = all[len(chunk):]
		b := (&rollupFixture{exposuresFixture: exposuresFixture{clock: f.clock}}).envelope(t, chunk...)
		if res := batchResult(t, f.srv, f.keys[env], b); res.Accepted != len(chunk) {
			t.Fatalf("batch = %+v, want all %d accepted", res, len(chunk))
		}
	}
}

// check rolls the exposures up and runs the checks once.
func check(t *testing.T, srv *testkit.Server) {
	t.Helper()
	srv.TickRollup(t, worker.ExposureRollupName)
	if err := srv.Tick(t, worker.ExperimentChecksName); err != nil {
		t.Fatalf("tick the checks: %v", err)
	}
}

func (f *sampleRatioFixture) alerts(t *testing.T, active *bool) []management.ExperimentAlert {
	t.Helper()
	res := do(f.p.admin.ListExperimentAlertsWithResponse(t.Context(), f.p.key, "checkout_test",
		&management.ListExperimentAlertsParams{Active: active}))
	if res.JSON200 == nil {
		t.Fatalf("GET …/alerts = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func (f *sampleRatioFixture) sampleRatio(t *testing.T, env string) *management.SampleRatioCheck {
	t.Helper()
	res := do(f.p.admin.GetExperimentExposuresWithResponse(t.Context(), f.p.key, "checkout_test",
		&management.GetExperimentExposuresParams{Environment: env}))
	if res.JSON200 == nil {
		t.Fatalf("GET …/exposures = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.SampleRatio
}

func (f *sampleRatioFixture) acknowledge(t *testing.T, alert openapi_types.UUID) management.ExperimentAlert {
	t.Helper()
	res := do(f.p.admin.AcknowledgeExperimentAlertWithResponse(t.Context(), f.p.key, "checkout_test", alert))
	if res.JSON200 == nil {
		t.Fatalf("acknowledge = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

// audited returns the project's audit entries recording action.
func (f *sampleRatioFixture) audited(t *testing.T, action string) []management.AuditEntry {
	t.Helper()
	pid := openapi_types.UUID(f.p.projectID(t).Bytes())
	res := do(f.p.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Project: &pid, Action: &action}))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func near(got, want, relative float64) bool {
	return math.Abs(got-want) <= relative*math.Abs(want)
}

// The worked example: 5,200/4,800 against an even split is X² 16, p ≈ 6.3·10⁻⁵,
// an alert raised once however often the check runs, and only in the
// environment whose exposures are skewed.
func TestASkewedSplitRaisesOneSampleRatioAlert(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	f.expose(t, "production", map[string]int{"true": 560, "false": 540})
	for range 3 {
		check(t, srv)
		c.Advance(10 * time.Minute)
	}

	alerts := f.alerts(t, nil)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v, want one", alerts)
	}
	a := alerts[0]
	if a.Kind != "srm" || valueOf(a.Environment) != "staging" || a.ClearedAt != nil || a.AcknowledgedAt != nil ||
		!a.RaisedAt.Equal(protocolTime) {
		t.Errorf("alert = %+v, want an active, unacknowledged srm alert in staging raised at the first check", a)
	}
	d := a.Details.SampleRatio
	if d == nil || d.N != 10000 || d.ChiSquare != 16 || !near(d.PValue, 6.334e-5, 1e-3) || len(d.Variations) != 2 {
		t.Fatalf("details = %+v, want N 10000, X² 16, p ≈ 6.334e-5", d)
	}
	want := management.SampleRatioVariation{Variation: "true", Observed: 5200, Expected: 5000, ObservedShare: 0.52, ExpectedShare: 0.5}
	if d.Variations[0] != want {
		t.Errorf("first variation = %+v, want %+v", d.Variations[0], want)
	}
	raised := f.audited(t, "experiment.alert_raised")
	if len(raised) != 1 || raised[0].Actor.Kind != management.ActorKindSystem || raised[0].EnvironmentId == nil {
		t.Errorf("experiment.alert_raised entries = %+v, want one by the system in an environment", raised)
	}

	srm := f.sampleRatio(t, "staging")
	if srm == nil || srm.N != 10000 || valueOf(srm.ChiSquare) != 16 || !srm.Active || srm.SkippedReason != nil {
		t.Fatalf("staging sample_ratio = %+v, want N 10000, X² 16, active", srm)
	}
	if want := c.Now().Add(-10 * time.Minute); !srm.CheckedAt.Equal(want) {
		t.Errorf("checked_at = %s, want the last check's %s", srm.CheckedAt, want)
	}
	prod := f.sampleRatio(t, "production")
	if prod == nil || prod.N != 1100 || prod.Active || valueOf(prod.PValue) < 0.001 {
		t.Errorf("production sample_ratio = %+v, want N 1100 and no alert", prod)
	}
}

func TestABalancedSplitRaisesNothing(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	if srm := f.sampleRatio(t, "staging"); srm != nil {
		t.Fatalf("before any check = %+v, want null", srm)
	}
	f.expose(t, "staging", map[string]int{"true": 5050, "false": 4950})
	check(t, srv)

	srm := f.sampleRatio(t, "staging")
	if srm == nil || srm.N != 10000 || valueOf(srm.ChiSquare) != 1 || !near(valueOf(srm.PValue), 0.3173, 1e-3) || srm.Active {
		t.Fatalf("sample_ratio = %+v, want N 10000, X² 1, p ≈ 0.32, not active", srm)
	}
	if alerts := f.alerts(t, nil); len(alerts) != 0 {
		t.Errorf("alerts = %+v, want none", alerts)
	}
}

// Both minimums: fewer than 1,000 first exposures, and a variation expecting
// fewer than 5 however many there are.
func TestTooFewExposuresAreNotJudged(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	small := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	small.expose(t, "staging", map[string]int{"true": 400, "false": 300})
	// 9,960/40 expects 4 units of false in 1,000; all of them in true would
	// be a certain alert were the check to run.
	lopsided := startSampleRatio(t, srv, c, 9960, 40, 10000)
	lopsided.expose(t, "staging", map[string]int{"true": 1000})
	check(t, srv)

	for _, w := range []struct {
		f    *sampleRatioFixture
		n    int64
		want management.SampleRatioSkippedReason
	}{
		{small, 700, management.SampleRatioSkippedReasonBelowMinimumSample},
		{lopsided, 1000, management.SampleRatioSkippedReasonExpectedBelow5},
	} {
		srm := w.f.sampleRatio(t, "staging")
		if srm == nil || srm.N != w.n || valueOf(srm.SkippedReason) != w.want || srm.ChiSquare != nil || srm.PValue != nil || srm.Active {
			t.Errorf("sample_ratio = %+v, want N %d skipped as %s", srm, w.n, w.want)
		}
		if alerts := w.f.alerts(t, nil); len(alerts) != 0 {
			t.Errorf("alerts = %+v, want none", alerts)
		}
	}
}

// The hysteresis keeps an alert raised between p 0.001 and 0.01; an
// acknowledgement lasts as long as the alert, and a returning mismatch raises
// a new one.
func TestAnAcknowledgedAlertStaysUntilItClearsAndAReturningMismatchRaisesANewOne(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	check(t, srv)
	raised := f.alerts(t, ptr(true))
	if len(raised) != 1 {
		t.Fatalf("active alerts = %+v, want one", raised)
	}
	first := raised[0]

	c.Advance(time.Minute)
	ack := f.acknowledge(t, first.Id)
	if ack.AcknowledgedAt == nil || !ack.AcknowledgedAt.Equal(c.Now()) || ack.ClearedAt != nil {
		t.Fatalf("acknowledged = %+v, want acknowledged now and still active", ack)
	}
	c.Advance(time.Minute)
	if again := f.acknowledge(t, first.Id); !again.AcknowledgedAt.Equal(*ack.AcknowledgedAt) {
		t.Errorf("a second acknowledgement moved acknowledged_at to %s", again.AcknowledgedAt)
	}
	if got := f.audited(t, "experiment.alert_acknowledged"); len(got) != 1 || got[0].Actor.Kind == management.ActorKindSystem {
		t.Errorf("experiment.alert_acknowledged entries = %+v, want the first acknowledgement's only", got)
	}

	// 10,200/9,800: X² 8, p ≈ 0.0047, between the thresholds: still raised.
	c.Advance(10 * time.Minute)
	f.expose(t, "staging", map[string]int{"true": 5000, "false": 5000})
	check(t, srv)
	if srm := f.sampleRatio(t, "staging"); !srm.Active || !near(valueOf(srm.PValue), 0.004678, 1e-3) {
		t.Fatalf("between the thresholds = %+v, want p ≈ 0.0047 and the alert still active", srm)
	}

	// 10,200/10,200: p 1, cleared once.
	c.Advance(10 * time.Minute)
	f.expose(t, "staging", map[string]int{"false": 400})
	check(t, srv)
	clearedAt := c.Now()
	c.Advance(10 * time.Minute)
	check(t, srv)
	if active := f.alerts(t, ptr(true)); len(active) != 0 {
		t.Fatalf("active alerts after clearing = %+v, want none", active)
	}
	cleared := f.alerts(t, ptr(false))
	if len(cleared) != 1 || cleared[0].Id != first.Id || cleared[0].ClearedAt == nil || !cleared[0].ClearedAt.Equal(clearedAt) ||
		cleared[0].AcknowledgedAt == nil {
		t.Fatalf("cleared alerts = %+v, want the first, acknowledged and cleared at %s", cleared, clearedAt)
	}
	if got := f.audited(t, "experiment.alert_cleared"); len(got) != 1 || got[0].Actor.Kind != management.ActorKindSystem {
		t.Errorf("experiment.alert_cleared entries = %+v, want one by the system", got)
	}

	// 10,700/10,200: X² ≈ 11.96, p ≈ 0.00054: a new, unacknowledged alert.
	c.Advance(10 * time.Minute)
	f.expose(t, "staging", map[string]int{"true": 500})
	check(t, srv)
	active := f.alerts(t, ptr(true))
	if len(active) != 1 || active[0].Id == first.Id || active[0].AcknowledgedAt != nil || active[0].Details.SampleRatio == nil ||
		active[0].Details.SampleRatio.N != 20900 {
		t.Fatalf("active alerts after the return = %+v, want a new unacknowledged one over N 20,900", active)
	}
	if all := f.alerts(t, nil); len(all) != 2 || all[0].Id != active[0].Id {
		t.Errorf("every alert = %+v, want the new one first, then the cleared one", all)
	}
}

// An alert that cleared before anyone saw it can still be acknowledged: the
// acknowledgement is recorded and the alert stays cleared.
func TestAClearedAlertCanBeAcknowledged(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	check(t, srv)
	c.Advance(10 * time.Minute)
	f.expose(t, "staging", map[string]int{"false": 400})
	check(t, srv)
	clearedAt := c.Now()
	cleared := f.alerts(t, ptr(false))
	if len(cleared) != 1 || cleared[0].AcknowledgedAt != nil {
		t.Fatalf("cleared alerts = %+v, want one, unacknowledged", cleared)
	}

	c.Advance(time.Minute)
	ack := f.acknowledge(t, cleared[0].Id)
	if ack.AcknowledgedAt == nil || !ack.AcknowledgedAt.Equal(c.Now()) || ack.ClearedAt == nil || !ack.ClearedAt.Equal(clearedAt) {
		t.Fatalf("acknowledged = %+v, want acknowledged now and still cleared at %s", ack, clearedAt)
	}
	if got := f.audited(t, "experiment.alert_acknowledged"); len(got) != 1 {
		t.Errorf("experiment.alert_acknowledged entries = %+v, want one", got)
	}
}

func TestAcknowledgingAnAlertOfAnotherExperimentIsNotFound(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	check(t, srv)
	alert := f.alerts(t, nil)[0].Id

	other := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	for _, w := range []struct {
		project string
		alert   openapi_types.UUID
	}{{f.p.key, openapi_types.UUID{1}}, {other.p.key, alert}} {
		res := do(f.p.admin.AcknowledgeExperimentAlertWithResponse(t.Context(), w.project, "checkout_test", w.alert))
		if res.StatusCode() != http.StatusNotFound || problemOf(t, res.Body).Code != "not_found" {
			t.Errorf("acknowledge %s in %s = %d: %s, want 404 not_found", w.alert, w.project, res.StatusCode(), res.Body)
		}
	}
	res := do(f.p.admin.ListExperimentAlertsWithResponse(t.Context(), f.p.key, "nope", nil))
	if res.StatusCode() != http.StatusNotFound {
		t.Errorf("alerts of an unknown experiment = %d, want 404", res.StatusCode())
	}
}

// stopRemoved stops the experiment, removing its rules, and returns when.
func (f *sampleRatioFixture) stopRemoved(t *testing.T) time.Time {
	t.Helper()
	return *f.stop(t, management.ExperimentStop{Outcome: "removed"}).StoppedAt
}

// A running experiment's results are final: nothing waits there.
func TestARunningExperimentsSampleRatioIsFinal(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5050, "false": 4950})
	check(t, srv)
	if srm := f.sampleRatio(t, "staging"); srm == nil || !srm.Final {
		t.Errorf("sample_ratio = %+v, want final", srm)
	}
}

// Exposures accepted before the stop but not rolled up yet enter the final
// check: the stop takes its own at its time, not final, and the round of the
// checks that finds the rollup past every exposure received by the stop takes
// the final one, over exactly those exposures. Nothing replaces it, and an
// exposure received after the stop is not counted, though the rollup read it
// before the round.
func TestTheFinalCheckCountsEveryExposureAcceptedBeforeTheStop(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})

	c.Advance(time.Minute)
	stoppedAt := f.stopRemoved(t)
	atStop := f.sampleRatio(t, "staging")
	if atStop == nil || atStop.Final || atStop.N != 0 || !atStop.CheckedAt.Equal(stoppedAt) ||
		valueOf(atStop.SkippedReason) != management.SampleRatioSkippedReasonBelowMinimumSample {
		t.Fatalf("at the stop = %+v, want the stop's check: not final, N 0 (nothing rolled up), at %s", atStop, stoppedAt)
	}
	if alerts := f.alerts(t, nil); len(alerts) != 0 {
		t.Fatalf("alerts after the stop = %+v, want none", alerts)
	}

	// A round before the rollup has read the exposures leaves it pending.
	c.Advance(10 * time.Minute)
	if err := srv.Tick(t, worker.ExperimentChecksName); err != nil {
		t.Fatalf("tick the checks: %v", err)
	}
	if still := f.sampleRatio(t, "staging"); still.Final || still.N != 0 || !still.CheckedAt.Equal(stoppedAt) {
		t.Fatalf("after a round before the rollup = %+v, want the stop's check still pending", still)
	}

	// An exposure received after the stop is rolled up with the rest.
	c.Advance(time.Minute)
	f.expose(t, "staging", map[string]int{"false": 400})
	c.Advance(10 * time.Minute)
	roundAt := c.Now()
	check(t, srv)
	final := f.sampleRatio(t, "staging")
	if final == nil || !final.Final || final.N != 10000 || valueOf(final.ChiSquare) != 16 || !final.Active || !final.CheckedAt.Equal(roundAt) {
		t.Fatalf("final check = %+v, want final, N 10000, X² 16, the alert raised, at the round %s", final, roundAt)
	}
	if prod := f.sampleRatio(t, "production"); prod == nil || !prod.Final || prod.N != 0 ||
		valueOf(prod.SkippedReason) != management.SampleRatioSkippedReasonBelowMinimumSample || !prod.CheckedAt.Equal(roundAt) {
		t.Errorf("production's final check = %+v, want final, N 0 below the minimum, at the round", prod)
	}
	alerts := f.alerts(t, ptr(true))
	if len(alerts) != 1 || alerts[0].Kind != "srm" || valueOf(alerts[0].Environment) != "staging" || !alerts[0].RaisedAt.Equal(roundAt) {
		t.Errorf("active alerts = %+v, want the srm alert the final check raised", alerts)
	}

	// Nothing overwrites a final result.
	c.Advance(10 * time.Minute)
	f.expose(t, "staging", map[string]int{"false": 400})
	check(t, srv)
	after := f.sampleRatio(t, "staging")
	if !after.Final || after.N != 10000 || !after.CheckedAt.Equal(roundAt) || !after.Active {
		t.Errorf("after another round = %+v, want the final check unchanged", after)
	}
	if got := f.alerts(t, nil); len(got) != 1 {
		t.Errorf("alerts after another round = %+v, want the one", got)
	}
}

// With the rollup already past the stop, the next round makes the stop's check
// final, and a stopped experiment's late exposures change nothing after it.
func TestTheFinalCheckFollowsTheStopWhenTheRollupIsCurrent(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	srv.TickRollup(t, worker.ExposureRollupName)

	c.Advance(time.Minute)
	stoppedAt := f.stopRemoved(t)
	atStop := f.sampleRatio(t, "staging")
	if atStop == nil || atStop.Final || atStop.N != 10000 || valueOf(atStop.ChiSquare) != 16 || !atStop.Active ||
		!atStop.CheckedAt.Equal(stoppedAt) {
		t.Fatalf("at the stop = %+v, want not final, N 10000, X² 16, the alert raised, at %s", atStop, stoppedAt)
	}
	if prod := f.sampleRatio(t, "production"); prod == nil || prod.Final || prod.N != 0 {
		t.Errorf("production at the stop = %+v, want not final, N 0", prod)
	}

	c.Advance(10 * time.Minute)
	roundAt := c.Now()
	check(t, srv)
	final := f.sampleRatio(t, "staging")
	if final == nil || !final.Final || final.N != 10000 || !final.Active || !final.CheckedAt.Equal(roundAt) {
		t.Fatalf("final check = %+v, want final, N 10000, the alert still active, at the round %s", final, roundAt)
	}
	if prod := f.sampleRatio(t, "production"); prod == nil || !prod.Final {
		t.Errorf("production's check = %+v, want final", prod)
	}

	// Balancing exposures arrive late and are rolled up, but nothing
	// checks the stopped experiment again.
	c.Advance(10 * time.Minute)
	f.expose(t, "staging", map[string]int{"false": 400})
	check(t, srv)
	after := f.sampleRatio(t, "staging")
	if !after.Final || after.N != 10000 || !after.CheckedAt.Equal(roundAt) || !after.Active {
		t.Errorf("after a round past the final check = %+v, want it unchanged", after)
	}
}

// The final check clears an alert the running checks raised on exposures that
// had not yet balanced out, as a running check would.
func TestTheFinalCheckClearsAnAlertTheRestOfTheExposuresAnswer(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	check(t, srv)
	if alerts := f.alerts(t, ptr(true)); len(alerts) != 1 {
		t.Fatalf("active alerts = %+v, want the one the running check raised", alerts)
	}

	// 400 more of false, accepted before the stop and not rolled up: 5,200
	// each way.
	c.Advance(time.Minute)
	f.expose(t, "staging", map[string]int{"false": 400})
	c.Advance(time.Minute)
	f.stopRemoved(t)
	if atStop := f.sampleRatio(t, "staging"); atStop.Final || atStop.N != 10000 || !atStop.Active {
		t.Fatalf("at the stop = %+v, want not final, N 10000 (the 400 not rolled up), the alert still active", atStop)
	}

	c.Advance(10 * time.Minute)
	roundAt := c.Now()
	check(t, srv)
	final := f.sampleRatio(t, "staging")
	if final == nil || !final.Final || final.N != 10400 || valueOf(final.ChiSquare) != 0 || final.Active || !final.CheckedAt.Equal(roundAt) {
		t.Fatalf("final check = %+v, want final, N 10400, X² 0, no alert, at %s", final, roundAt)
	}
	cleared := f.alerts(t, ptr(false))
	if len(cleared) != 1 || cleared[0].ClearedAt == nil || !cleared[0].ClearedAt.Equal(roundAt) {
		t.Errorf("cleared alerts = %+v, want the one cleared by the final check at %s", cleared, roundAt)
	}
}

// The final check waits a minute after the stop, so that an exposure received
// by the stop and still being written is in.
func TestTheFinalCheckWaitsAMinuteAfterTheStop(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	srv.TickRollup(t, worker.ExposureRollupName)
	c.Advance(time.Minute)
	f.stopRemoved(t)

	c.Advance(59 * time.Second)
	check(t, srv)
	if early := f.sampleRatio(t, "staging"); early.Final {
		t.Fatalf("59 s after the stop = %+v, want the stop's check still pending", early)
	}
	c.Advance(time.Second)
	roundAt := c.Now()
	check(t, srv)
	if final := f.sampleRatio(t, "staging"); !final.Final || !final.CheckedAt.Equal(roundAt) {
		t.Errorf("a minute after the stop = %+v, want final at %s", final, roundAt)
	}
}

// Raw exposures are kept for a window; an experiment that started before it
// may have lost some, so its final check counts what the rollup holds
// instead, late exposures included.
func TestTheFinalCheckOfAnExperimentOlderThanTheRawWindowUsesTheRollup(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	c.Advance(40 * 24 * time.Hour)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	srv.TickRollup(t, worker.ExposureRollupName)
	c.Advance(time.Minute)
	f.stopRemoved(t)
	c.Advance(time.Minute)
	f.expose(t, "staging", map[string]int{"false": 400})

	c.Advance(10 * time.Minute)
	check(t, srv)
	if final := f.sampleRatio(t, "staging"); !final.Final || final.N != 10400 {
		t.Errorf("final check = %+v, want final over the rollup's 10400 first exposures", final)
	}
}

// A stop followed by an archive before the rollup has caught up still gets
// its final check.
func TestTheFinalCheckIsTakenForAnArchivedExperiment(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	c.Advance(time.Minute)
	f.stopRemoved(t)
	archived := do(f.p.admin.ArchiveExperimentWithResponse(t.Context(), f.p.key, "checkout_test",
		&management.ArchiveExperimentParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(f.exp.Version, 10)))}))
	if archived.JSON200 == nil {
		t.Fatalf("archive = %d: %s", archived.StatusCode(), archived.Body)
	}

	c.Advance(10 * time.Minute)
	check(t, srv)
	if final := f.sampleRatio(t, "staging"); final == nil || !final.Final || final.N != 10000 || !final.Active {
		t.Errorf("final check = %+v, want final, N 10000, the alert raised", final)
	}
}

// Growing the allocation keeps every included unit in its variation, since the
// variation bucket is independent of inclusion, so the split stays in line
// with the weights across the growth. The salts are drawn anew on every run,
// so each phase exposes as many units of one variation as of the other: a
// split left to chance would fall below the alert's significance in about one
// run in a hundred, and the test would fail with nothing wrong.
func TestGrowingTheTrafficAllocationRaisesNoFalseAlert(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 7000)
	// exposeIncluded exposes, among the first 3,000 units, those whose
	// inclusion bucket lies in [from, to), each in the variation its
	// variation bucket assigns, as many of one variation as of the other,
	// and returns how many it exposed.
	exposeIncluded := func(from, to int) int {
		byVariation := map[string][]any{}
		for i := range 3000 {
			unit := "u_" + strconv.Itoa(i)
			if b := eval.Bucket(f.exp.InclusionSalt, unit); b < from || b >= to {
				continue
			}
			variation := "false"
			if eval.Bucket(f.exp.VariationSalt, unit) < 5000 {
				variation = "true"
			}
			byVariation[variation] = append(byVariation[variation], f.exposure(unit, variation))
		}
		n := min(len(byVariation["true"]), len(byVariation["false"]))
		f.send(t, "staging", append(byVariation["true"][:n], byVariation["false"][:n]...))
		return 2 * n
	}

	first := exposeIncluded(0, 7000)
	check(t, srv)
	before := f.sampleRatio(t, "staging")
	grown := do(f.p.admin.UpdateExperimentWithResponse(t.Context(), f.p.key, "checkout_test",
		&management.UpdateExperimentParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(f.exp.Version, 10)))},
		management.ExperimentPatch{TrafficAllocation: ptr(10000)}))
	if grown.JSON200 == nil {
		t.Fatalf("PATCH = %d: %s", grown.StatusCode(), grown.Body)
	}
	c.Advance(10 * time.Minute)
	second := exposeIncluded(7000, 10000)
	check(t, srv)
	after := f.sampleRatio(t, "staging")

	for name, srm := range map[string]*management.SampleRatioCheck{"at 70 %": before, "at 100 %": after} {
		if srm == nil || srm.SkippedReason != nil || srm.Active || valueOf(srm.PValue) <= 0.01 {
			t.Errorf("%s = %+v, want a check that ran and raised nothing", name, srm)
		}
	}
	if second == 0 || before.N != int64(first) || after.N != int64(first+second) {
		t.Errorf("N %d at 70 %% and %d at 100 %%, want %d and %d", before.N, after.N, first, first+second)
	}
	if alerts := f.alerts(t, nil); len(alerts) != 0 {
		t.Errorf("alerts = %+v, want none", alerts)
	}
}

// Two replicas checking at once raise an alert once.
func TestTwoReplicasRaiseAnAlertOnce(t *testing.T) {
	c := clock.NewFake(protocolTime)
	one := testkit.StartServer(t, testkit.WithClock(c))
	two := testkit.StartServer(t, testkit.WithClock(c), testkit.WithPool(testkit.Pool(t)))
	f := startSampleRatio(t, one, c, 5000, 5000, 10000)
	f.expose(t, "staging", map[string]int{"true": 5200, "false": 4800})
	one.TickRollup(t, worker.ExposureRollupName)

	errs := make(chan error, 2)
	for _, srv := range []*testkit.Server{one, two} {
		go func() { errs <- srv.Tick(t, worker.ExperimentChecksName) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if err := two.Tick(t, worker.ExperimentChecksName); err != nil {
		t.Fatal(err)
	}
	if alerts := f.alerts(t, nil); len(alerts) != 1 {
		t.Errorf("alerts = %+v, want one", alerts)
	}
	if got := f.audited(t, "experiment.alert_raised"); len(got) != 1 {
		t.Errorf("experiment.alert_raised entries = %d, want 1", len(got))
	}
}
