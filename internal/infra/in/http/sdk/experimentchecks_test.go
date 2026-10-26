package sdk_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
)

// ofKind is the alerts of kind among alerts.
func ofKind(alerts []management.ExperimentAlert, kind management.ExperimentAlertKind) []management.ExperimentAlert {
	var out []management.ExperimentAlert
	for _, a := range alerts {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

// environmentsOf is the sorted environments of alerts, "" for one about the
// experiment as a whole.
func environmentsOf(alerts []management.ExperimentAlert) []string {
	out := []string{}
	for _, a := range alerts {
		out = append(out, valueOf(a.Environment))
	}
	slices.Sort(out)
	return out
}

func inEnvironment(alerts []management.ExperimentAlert, env string) management.ExperimentAlert {
	for _, a := range alerts {
		if valueOf(a.Environment) == env {
			return a
		}
	}
	return management.ExperimentAlert{}
}

// conflictWith sends one exposure of variation for each of the units numbered,
// a second after the last exposures so that none ties with a first exposure.
func (f *sampleRatioFixture) conflictWith(t *testing.T, env, variation string, units ...int) {
	t.Helper()
	f.clock.Advance(time.Second)
	var all []any
	for _, u := range units {
		all = append(all, f.exposure("u-"+strconv.Itoa(u), variation))
	}
	f.send(t, env, all)
}

// list returns the keys of the experiments params lists.
func (f *sampleRatioFixture) list(t *testing.T, params management.ListExperimentsParams) []string {
	t.Helper()
	res := do(f.p.admin.ListExperimentsWithResponse(t.Context(), f.p.key, &params))
	if res.JSON200 == nil {
		t.Fatalf("GET …/experiments = %d: %s", res.StatusCode(), res.Body)
	}
	out := []string{}
	for _, e := range res.JSON200.Items {
		out = append(out, e.Key)
	}
	return out
}

func (f *sampleRatioFixture) patch(t *testing.T, patch management.ExperimentPatch) {
	t.Helper()
	res := do(f.p.admin.UpdateExperimentWithResponse(t.Context(), f.p.key, "checkout_test",
		&management.UpdateExperimentParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(f.exp.Version, 10)))}, patch))
	if res.JSON200 == nil {
		t.Fatalf("PATCH = %d: %s", res.StatusCode(), res.Body)
	}
	f.exp = *res.JSON200
}

func (f *sampleRatioFixture) stop(t *testing.T, stop management.ExperimentStop) management.Experiment {
	t.Helper()
	res := do(f.p.admin.StopExperimentWithResponse(t.Context(), f.p.key, "checkout_test",
		&management.StopExperimentParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(f.exp.Version, 10)))}, stop))
	if res.JSON200 == nil {
		t.Fatalf("stop = %d: %s", res.StatusCode(), res.Body)
	}
	f.exp = *res.JSON200
	return f.exp
}

func date(at time.Time) *openapi_types.Date {
	return &openapi_types.Date{Time: at.UTC().Truncate(24 * time.Hour)}
}

// A day without exposures raises an alert in each environment holding the
// rule, not at 23 hours 59; the next exposure clears it, an override's
// aside, and a new day without any raises a new one, unacknowledged.
func TestADayWithoutExposuresRaisesAnAlert(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	started := *f.exp.StartedAt

	c.Advance(23*time.Hour + 59*time.Minute)
	check(t, srv)
	if alerts := f.alerts(t, nil); len(alerts) != 0 {
		t.Fatalf("alerts at 23:59 = %+v, want none", alerts)
	}

	c.Advance(time.Minute)
	for range 3 {
		check(t, srv)
		c.Advance(10 * time.Minute)
	}
	raised := ofKind(f.alerts(t, ptr(true)), management.ExperimentAlertKindNoExposures)
	if got := environmentsOf(raised); !slices.Equal(got, []string{"production", "staging"}) {
		t.Fatalf("no_exposures alerts in %v, want production and staging, never development, which holds no rule", got)
	}
	for _, a := range raised {
		d := a.Details.NoExposures
		if d == nil || d.LastExposureAt != nil || !d.Since.Equal(started) || a.Details.SampleRatio != nil ||
			!a.RaisedAt.Equal(started.Add(24*time.Hour)) {
			t.Errorf("alert = %+v, details %+v, want raised at 24 hours, never exposed since the start", a, d)
		}
	}
	if got := f.audited(t, "experiment.alert_raised"); len(got) != 2 || got[0].Actor.Kind != management.ActorKindSystem {
		t.Errorf("experiment.alert_raised entries = %+v, want two by the system", got)
	}
	staging := inEnvironment(raised, "staging")
	f.acknowledge(t, staging.Id)

	// An override exposure is a QA session, not the missing call; the first
	// exposure in staging clears staging's alert alone.
	lastExposure := c.Now()
	f.send(t, "production", []any{with(f.exposure("qa-1", "true"), map[string]any{"override": true, "evaluation_reason": "override"})})
	f.expose(t, "staging", map[string]int{"true": 1})
	check(t, srv)
	active := ofKind(f.alerts(t, ptr(true)), management.ExperimentAlertKindNoExposures)
	if got := environmentsOf(active); !slices.Equal(got, []string{"production"}) {
		t.Fatalf("active no_exposures alerts in %v after staging's exposure, want production alone", got)
	}
	cleared := f.alerts(t, ptr(false))
	if len(cleared) != 1 || cleared[0].Id != staging.Id || !cleared[0].ClearedAt.Equal(c.Now()) {
		t.Fatalf("cleared alerts = %+v, want staging's, cleared now", cleared)
	}

	c.Advance(24*time.Hour + time.Minute)
	check(t, srv)
	again := inEnvironment(ofKind(f.alerts(t, ptr(true)), management.ExperimentAlertKindNoExposures), "staging")
	if d := again.Details.NoExposures; again.Id == staging.Id || again.AcknowledgedAt != nil || d == nil ||
		d.LastExposureAt == nil || !d.LastExposureAt.Equal(lastExposure) || !d.Since.Equal(lastExposure) {
		t.Errorf("staging's alert a day later = %+v, want a new, unacknowledged one since the exposure at %s", again, lastExposure)
	}
}

// An environment that stops holding the rule no longer waits for exposures.
func TestANoExposuresAlertClearsWhereTheRuleIsRemoved(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	c.Advance(24 * time.Hour)
	check(t, srv)
	if got := environmentsOf(f.alerts(t, ptr(true))); !slices.Equal(got, []string{"production", "staging"}) {
		t.Fatalf("active alerts in %v, want production and staging", got)
	}

	res := do(f.p.admin.PutFlagConfigurationWithResponse(t.Context(), f.p.key, "checkout", "staging",
		&management.PutFlagConfigurationParams{IfMatch: ptr(`"2"`)},
		management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "false"}))
	if res.JSON200 == nil {
		t.Fatalf("PUT …/configurations/staging = %d: %s", res.StatusCode(), res.Body)
	}
	c.Advance(time.Minute)
	check(t, srv)
	if got := environmentsOf(f.alerts(t, ptr(true))); !slices.Equal(got, []string{"production"}) {
		t.Errorf("active alerts in %v once staging dropped the rule, want production alone", got)
	}
}

// The threshold is 10 conflicts or 0.1 % of the first exposures, whichever
// is fewer, but never below 3; the alert clears as first exposures grow past
// the ratio, and more conflicts raise a new one.
func TestConflictsRaiseAnAlertAtTheThreshold(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	conflicts := func(f *sampleRatioFixture, active *bool) []management.ExperimentAlert {
		return ofKind(f.alerts(t, active), management.ExperimentAlertKindExposureConflicts)
	}

	// 10,000 first exposures: 0.1 % is 10, so 9 conflicts are not enough.
	large := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	large.expose(t, "staging", map[string]int{"true": 5000, "false": 5000})
	large.conflictWith(t, "staging", "false", 1, 2, 3, 4, 5, 6, 7, 8, 9)
	check(t, srv)
	if got := conflicts(large, nil); len(got) != 0 {
		t.Fatalf("conflict alerts at 9 conflicts = %+v, want none", got)
	}
	large.conflictWith(t, "staging", "false", 10)
	check(t, srv)
	check(t, srv)
	raised := conflicts(large, nil)
	if len(raised) != 1 || valueOf(raised[0].Environment) != "staging" {
		t.Fatalf("conflict alerts at 10 conflicts = %+v, want one in staging", raised)
	}
	want := management.ExposureConflictsAlertDetails{Conflicts: 10, ConflictingUnits: 10, FirstExposures: 10000, Threshold: 10}
	if d := raised[0].Details.ExposureConflicts; d == nil || *d != want {
		t.Errorf("details = %+v, want %+v", d, want)
	}

	// 3,000 first exposures: 0.1 % is 3, the floor too.
	small := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	small.expose(t, "production", map[string]int{"true": 1500, "false": 1500})
	small.conflictWith(t, "production", "false", 1, 2)
	check(t, srv)
	if got := conflicts(small, nil); len(got) != 0 {
		t.Fatalf("conflict alerts at 2 conflicts on 3,000 = %+v, want none", got)
	}
	small.conflictWith(t, "production", "false", 3)
	check(t, srv)
	first := conflicts(small, nil)
	want = management.ExposureConflictsAlertDetails{Conflicts: 3, ConflictingUnits: 3, FirstExposures: 3000, Threshold: 3}
	if len(first) != 1 || valueOf(first[0].Environment) != "production" || *first[0].Details.ExposureConflicts != want {
		t.Fatalf("conflict alerts at 3 conflicts on 3,000 = %+v, want one in production with %+v", first, want)
	}

	small.acknowledge(t, first[0].Id)
	small.expose(t, "production", map[string]int{"true": 1000, "false": 1000})
	check(t, srv)
	if got := conflicts(small, ptr(true)); len(got) != 0 {
		t.Fatalf("active conflict alerts at 3 conflicts on 5,000 = %+v, want none: the threshold is 5", got)
	}
	small.conflictWith(t, "production", "false", 4, 5)
	check(t, srv)
	active := conflicts(small, ptr(true))
	if len(active) != 1 || active[0].Id == first[0].Id || active[0].AcknowledgedAt != nil {
		t.Fatalf("active conflict alerts at 5 conflicts on 5,000 = %+v, want a new, unacknowledged one", active)
	}

	// The stop leaves it: it is what the exposures came to.
	small.stop(t, management.ExperimentStop{Outcome: "removed"})
	if got := conflicts(small, ptr(true)); len(got) != 1 || got[0].Id != active[0].Id {
		t.Errorf("active conflict alerts after the stop = %+v, want the one raised", got)
	}
}

// An experiment past its expected end is alerted once, about the experiment
// as a whole, and listed as overdue, until its end moves or it stops; the
// stop clears it with the no_exposures alerts beside it.
func TestAnOverdueExperimentIsListedAndAlertedOnceUntilItEnds(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	end := c.Now().AddDate(0, 0, 1)
	f.patch(t, management.ExperimentPatch{ExpectedEndAt: date(end)})
	overdue := func() []string { return f.list(t, management.ListExperimentsParams{PastExpectedEnd: ptr(true)}) }
	past := func(active *bool) []management.ExperimentAlert {
		return ofKind(f.alerts(t, active), management.ExperimentAlertKindPastExpectedEnd)
	}

	// On the expected end day itself the experiment is not overdue yet.
	c.Advance(24 * time.Hour)
	check(t, srv)
	if got := overdue(); len(got) != 0 {
		t.Fatalf("overdue on the expected end day = %v, want none", got)
	}
	if got := past(nil); len(got) != 0 {
		t.Fatalf("past_expected_end alerts on the expected end day = %+v, want none", got)
	}

	c.Advance(24 * time.Hour)
	for range 3 {
		check(t, srv)
		c.Advance(10 * time.Minute)
	}
	if got := overdue(); !slices.Equal(got, []string{"checkout_test"}) {
		t.Fatalf("overdue = %v, want [checkout_test]", got)
	}
	if got := f.list(t, management.ListExperimentsParams{PastExpectedEnd: ptr(false)}); !slices.Equal(got, []string{"checkout_test"}) {
		t.Errorf("listed with past_expected_end false = %v, want everything", got)
	}
	raised := past(ptr(true))
	if len(raised) != 1 || raised[0].Environment != nil || raised[0].Details.PastExpectedEnd == nil ||
		raised[0].Details.PastExpectedEnd.ExpectedEndAt.Format(time.DateOnly) != end.Format(time.DateOnly) {
		t.Fatalf("past_expected_end alerts = %+v, want one about the experiment naming %s", raised, end.Format(time.DateOnly))
	}
	if got := environmentsOf(ofKind(f.alerts(t, ptr(true)), management.ExperimentAlertKindNoExposures)); !slices.Equal(got,
		[]string{"production", "staging"}) {
		t.Errorf("no_exposures alerts in %v, want production and staging beside past_expected_end", got)
	}
	entries := f.audited(t, "experiment.alert_raised")
	var projectLevel int
	for _, e := range entries {
		if e.EnvironmentId == nil {
			projectLevel++
		}
	}
	if projectLevel != 1 {
		t.Errorf("experiment.alert_raised entries with no environment = %d, want 1", projectLevel)
	}

	// Moving the end to today clears it; moving it back raises a new one.
	f.patch(t, management.ExperimentPatch{ExpectedEndAt: date(c.Now())})
	check(t, srv)
	if got := past(ptr(true)); len(got) != 0 || len(overdue()) != 0 {
		t.Fatalf("past_expected_end alerts once the end is today = %+v, overdue %v, want none", got, overdue())
	}
	f.patch(t, management.ExperimentPatch{ExpectedEndAt: date(end)})
	check(t, srv)
	again := past(ptr(true))
	if len(again) != 1 || again[0].Id == raised[0].Id {
		t.Fatalf("past_expected_end alerts once the end is back = %+v, want a new one", again)
	}

	stopped := f.stop(t, management.ExperimentStop{Outcome: "removed"})
	check(t, srv)
	if got := overdue(); len(got) != 0 {
		t.Errorf("overdue after the stop = %v, want none", got)
	}
	if got := f.alerts(t, ptr(true)); len(got) != 0 {
		t.Errorf("active alerts after the stop = %+v, want none", got)
	}
	for _, a := range f.alerts(t, ptr(false)) {
		if a.Id == again[0].Id && !a.ClearedAt.Equal(*stopped.StoppedAt) {
			t.Errorf("past_expected_end cleared at %v, want at the stop %s", a.ClearedAt, stopped.StoppedAt)
		}
	}
}

// Balanced exposures every few hours, no conflict and a far expected end,
// over two days, raise nothing.
func TestAHealthyRunningExperimentRaisesNothing(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	for range 8 {
		f.expose(t, "staging", map[string]int{"true": 150, "false": 150})
		f.expose(t, "production", map[string]int{"true": 150, "false": 150})
		check(t, srv)
		c.Advance(6 * time.Hour)
	}
	check(t, srv)
	if alerts := f.alerts(t, nil); len(alerts) != 0 {
		t.Errorf("alerts = %+v, want none", alerts)
	}
}

// A stopped experiment is awaiting code removal once an SDK reports
// evaluating its flag after the stop, and until the evaluation's day leaves
// the last 7 UTC days; evaluations while it ran never count.
func TestAStoppedExperimentWhoseFlagIsStillEvaluatedAwaitsCodeRemoval(t *testing.T) {
	srv, c := startSampleRatioServer(t)
	f := startSampleRatio(t, srv, c, 5000, 5000, 10000)
	awaiting := func() []string { return f.list(t, management.ListExperimentsParams{AwaitingCodeRemoval: ptr(true)}) }
	summaries := func(env string) *summaryFixture {
		return &summaryFixture{srv: srv, clock: c, p: f.p, server: f.keys[env]}
	}
	evaluate := func(env string) {
		s := summaries(env)
		s.post(t, s.summary(c.Now(), counted("checkout", "true", "experiment", 5)))
		srv.TickRollup(t, worker.SummaryRollupName)
	}

	c.Advance(time.Hour)
	evaluate("production")
	if got := awaiting(); len(got) != 0 {
		t.Fatalf("awaiting code removal while running = %v, want none", got)
	}

	c.Advance(time.Minute)
	f.stop(t, management.ExperimentStop{Outcome: "winner", WinnerVariation: ptr("true")})
	// Production was evaluated the same day, but before the stop.
	if got := awaiting(); len(got) != 0 {
		t.Fatalf("awaiting code removal right after the stop = %v, want none", got)
	}

	c.Advance(time.Second)
	evaluated := c.Now()
	evaluate("staging")
	if got := awaiting(); !slices.Equal(got, []string{"checkout_test"}) {
		t.Fatalf("awaiting code removal = %v, want [checkout_test]", got)
	}
	if got := f.list(t, management.ListExperimentsParams{AwaitingCodeRemoval: ptr(false)}); !slices.Equal(got, []string{"checkout_test"}) {
		t.Errorf("listed with awaiting_code_removal false = %v, want everything", got)
	}

	day := evaluated.UTC().Truncate(24 * time.Hour)
	c.Advance(day.Add(6*24*time.Hour + 23*time.Hour).Sub(c.Now()))
	if got := awaiting(); !slices.Equal(got, []string{"checkout_test"}) {
		t.Fatalf("awaiting code removal 6 days on = %v, want [checkout_test]", got)
	}
	c.Advance(time.Hour)
	if got := awaiting(); len(got) != 0 {
		t.Errorf("awaiting code removal 7 days on = %v, want none", got)
	}
}
