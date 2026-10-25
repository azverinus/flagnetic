package sdk_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// flagWithVariations creates a string flag with variations given as key, value pairs.
func (p project) flagWithVariations(t *testing.T, key string, kv ...string) {
	t.Helper()
	var vs []management.VariationInput
	for i := 0; i+1 < len(kv); i += 2 {
		vs = append(vs, management.VariationInput{Key: kv[i], Value: json.RawMessage(`"` + kv[i+1] + `"`)})
	}
	res := do(p.admin.CreateFlagWithResponse(t.Context(), p.key, management.FlagCreate{
		Key: key, Name: "Flag " + key, Type: "string", Variations: &vs,
	}))
	if res.JSON201 == nil {
		t.Fatalf("POST …/flags = %d: %s", res.StatusCode(), res.Body)
	}
}

// experiment creates a draft experiment keyed key on flag, assigning unitType,
// with the weights given as variation, basis points pairs.
func (p project) experiment(t *testing.T, key, flag, unitType string, weights ...any) experiments.ExperimentID {
	t.Helper()
	var ws []management.ExperimentWeight
	for i := 0; i+1 < len(weights); i += 2 {
		ws = append(ws, management.ExperimentWeight{Variation: weights[i].(string), Weight: weights[i+1].(int)})
	}
	res := do(p.admin.CreateExperimentWithResponse(t.Context(), p.key, management.ExperimentCreate{
		Key: key, Name: "Experiment " + key, Flag: flag, UnitType: unitType, Variations: ws,
	}))
	if res.JSON201 == nil {
		t.Fatalf("POST …/experiments = %d: %s", res.StatusCode(), res.Body)
	}
	return id.FromBytes[experiments.Experiment](res.JSON201.Id)
}

// exposureEvent is an exposure of the experiment checkout_test, which assigns
// units of type user and weighs the variations true and false of the flag
// checkout, numbered n, with changes applied: a nil value removes the member.
func exposureEvent(n int, changes map[string]any) map[string]any {
	return with(map[string]any{
		"type": "exposure", "event_id": eventID(protocolTime, n), "occurred_at": protocolTime.Format(time.RFC3339),
		"experiment_key": "checkout_test", "flag_key": "checkout", "variation_key": "true", "unit_type": "user",
		"unit_id": "u-1001", "platform": "android", "app_version": "1.18.0", "evaluation_reason": "experiment",
		"override": false, "ruleset_version": 1,
	}, changes)
}

// with returns a copy of event with changes applied: a nil value removes the
// member.
func with(event map[string]any, changes map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range event {
		out[k] = v
	}
	for k, v := range changes {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

// exposuresFixture is a project with the draft experiment checkout_test and a
// key of each kind in production.
type exposuresFixture struct {
	srv        *testkit.Server
	clock      *clock.Fake
	p          project
	experiment experiments.ExperimentID
	client     string
	server     string
}

func startExposures(t *testing.T) exposuresFixture {
	t.Helper()
	c := clock.NewFake(protocolTime)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	p.flag(t, "checkout")
	f := exposuresFixture{srv: srv, clock: c, p: p, experiment: p.experiment(t, "checkout_test", "checkout", "user", "true", 5000, "false", 5000)}
	_, f.client = p.sdkKey(t, "production", management.SdkKeyKindClient)
	_, f.server = p.sdkKey(t, "production", management.SdkKeyKindServer)
	return f
}

func (f exposuresFixture) stored(t *testing.T) []events.ExposureRecord {
	t.Helper()
	xs, err := pgevents.NewStore(testkit.Pool(t)).Exposures(t.Context(), f.p.environmentID(t, "production"))
	if err != nil {
		t.Fatal(err)
	}
	return xs
}

func (f exposuresFixture) refused(t *testing.T, environment string) map[events.RejectionCode]int64 {
	t.Helper()
	counts, err := pgevents.NewStore(testkit.Pool(t)).RejectionCountsOf(t.Context(), f.experiment, f.p.environmentID(t, environment))
	if err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestAnExposureOfAnExperimentIsStoredOnce(t *testing.T) {
	// The experiment is a draft: an exposure may belong to an experiment of
	// any status, since a mobile app flushes its queue days late.
	f := startExposures(t)
	body := batch(exposureEvent(1, map[string]any{"app_version": nil}), exposureEvent(2, map[string]any{
		"variation_key": "false", "unit_id": "u-1002", "override": true, "evaluation_reason": "override", "ruleset_version": 4,
	}))
	first := batchResult(t, f.srv, f.client, body)
	if first.Accepted != 2 || first.Duplicates != 0 || len(first.Rejected) != 0 {
		t.Fatalf("first post = %+v, want both accepted", first)
	}
	again := batchResult(t, f.srv, f.client, body)
	if again.Accepted != 0 || again.Duplicates != 2 {
		t.Errorf("the same batch again = %+v, want 2 duplicates", again)
	}
	stored := f.stored(t)
	if len(stored) != 2 {
		t.Fatalf("exposures stored = %+v, want each once", stored)
	}
	x := stored[0]
	if x.EventID.String() != eventID(protocolTime, 1) || x.Experiment != f.experiment || x.SDK.Name != "flagnetic-dart" ||
		!x.ReceivedAt.Equal(protocolTime) {
		t.Errorf("first exposure = %+v", x)
	}
	want := events.Exposure{
		OccurredAt: protocolTime, ExperimentKey: "checkout_test", FlagKey: "checkout", VariationKey: "true", UnitType: "user",
		UnitID: "u-1001", Platform: "android", Reason: "experiment", RulesetVersion: 1,
	}
	if x.Exposure != want {
		t.Errorf("first exposure = %+v, want %+v", x.Exposure, want)
	}
	if o := stored[1].Exposure; !o.Override || o.Reason != "override" || o.VariationKey != "false" || o.RulesetVersion != 4 {
		t.Errorf("override exposure = %+v", o)
	}
}

func TestAnExposureIsDeduplicatedPerProject(t *testing.T) {
	f := startExposures(t)
	other := newProject(t, f.srv)
	other.flag(t, "checkout")
	other.experiment(t, "checkout_test", "checkout", "user", "true", 5000, "false", 5000)
	_, otherKey := other.sdkKey(t, "production", management.SdkKeyKindServer)
	body := batch(exposureEvent(1, nil))
	for name, key := range map[string]string{"first project": f.client, "another project": otherKey} {
		if res := batchResult(t, f.srv, key, body); res.Accepted != 1 || res.Duplicates != 0 {
			t.Errorf("%s = %+v, want accepted", name, res)
		}
	}
	if res := batchResult(t, f.srv, f.client, batch(exposureEvent(2, nil), exposureEvent(2, nil))); res.Accepted != 1 || res.Duplicates != 1 {
		t.Errorf("an ID twice in one batch = %+v, want 1 accepted and 1 duplicate", res)
	}
}

func TestExposuresAreRefusedPerEventWithTheCodeOfTheFirstCheckThatFails(t *testing.T) {
	f := startExposures(t)
	f.p.flag(t, "pricing")
	got := batchResult(t, f.srv, f.server, batch(
		exposureEvent(1, map[string]any{"experiment_key": "absent_test"}),
		exposureEvent(2, nil),
		exposureEvent(3, map[string]any{"unit_type": "anonymous"}),
		exposureEvent(4, map[string]any{"variation_key": "maybe"}),
		exposureEvent(5, map[string]any{"flag_key": "pricing"}),
		exposureEvent(6, map[string]any{"evaluation_reason": "rule"}),
		// The unit type is judged before the variation, the variation before
		// the flag and the reason.
		exposureEvent(7, map[string]any{"unit_type": "anonymous", "variation_key": "maybe", "flag_key": "pricing"}),
		exposureEvent(8, map[string]any{"variation_key": "maybe", "flag_key": "pricing", "evaluation_reason": "rule"}),
		exposureEvent(9, map[string]any{"unit_id": nil}),
	))
	if got.Accepted != 1 || got.Duplicates != 0 {
		t.Errorf("accepted = %d, duplicates = %d, want 1 and 0", got.Accepted, got.Duplicates)
	}
	want := []struct {
		index  int
		code   string
		detail string
	}{
		{0, "unknown_experiment", `no experiment is keyed "absent_test"`},
		{2, "unit_type_mismatch", `experiment "checkout_test" assigns units of type "user", not "anonymous"`},
		{3, "unknown_variation", `experiment "checkout_test" has no variation "maybe"`},
		{4, "event_invalid", `flag_key is "pricing", but experiment "checkout_test" is on flag "checkout"`},
		{5, "event_invalid", `evaluation_reason is "rule", not "experiment" or "override"`},
		{6, "unit_type_mismatch", `experiment "checkout_test" assigns units of type "user", not "anonymous"`},
		{7, "unknown_variation", `experiment "checkout_test" has no variation "maybe"`},
		{8, "event_invalid", "unit_id is missing"},
	}
	if len(got.Rejected) != len(want) {
		t.Fatalf("rejected = %+v, want %d rejections", got.Rejected, len(want))
	}
	for i, w := range want {
		r := got.Rejected[i]
		if r.Index != w.index || r.Code != w.code || r.Detail != w.detail || r.EventId == nil || *r.EventId != eventID(protocolTime, w.index+1) {
			t.Errorf("rejection %d = %+v, want index %d %s %q", i, r, w.index, w.code, w.detail)
		}
	}
}

func TestAClientKeyLearnsNothingOfAnExperimentButTheCodeOfARefusal(t *testing.T) {
	f := startExposures(t)
	f.p.flag(t, "pricing")
	refusals := func() []byte {
		return batch(
			exposureEvent(1, map[string]any{"unit_type": "anonymous"}),
			exposureEvent(2, map[string]any{"variation_key": "maybe"}),
			exposureEvent(3, map[string]any{"flag_key": "pricing"}),
			exposureEvent(4, map[string]any{"evaluation_reason": "rule"}),
		)
	}
	client := batchResult(t, f.srv, f.client, refusals())
	server := batchResult(t, f.srv, f.server, refusals())
	if len(client.Rejected) != 4 || len(server.Rejected) != 4 {
		t.Fatalf("client rejections = %+v, server = %+v, want 4 each", client.Rejected, server.Rejected)
	}
	// What a client key must not learn of the experiment: its unit type, its
	// flag, its variations and its status.
	secrets := []string{"user", "checkout", "true", "false", "draft", "running", "stopped"}
	for i, c := range client.Rejected {
		s := server.Rejected[i]
		if c.Code != s.Code {
			t.Errorf("rejection %d: client code %s, server code %s, want the same", i, c.Code, s.Code)
		}
		// The reason is the one the exposure carried and the permitted ones
		// are the protocol's: the only detail both keys may share.
		if c.Detail == s.Detail && !strings.HasPrefix(c.Detail, "evaluation_reason is") {
			t.Errorf("rejection %d: the client key's detail %q is the server key's", i, c.Detail)
		}
		for _, secret := range secrets {
			if strings.Contains(c.Detail, secret) {
				t.Errorf("rejection %d: the client key's detail %q names %q", i, c.Detail, secret)
			}
		}
	}
}

func TestAnOverrideExposureMayNameAnyVariationOfTheFlag(t *testing.T) {
	f := startExposures(t)
	f.p.flagWithVariations(t, "banner", "short", "Try", "long", "Start", "classic", "Old")
	f.p.experiment(t, "banner_test", "banner", "user", "short", 5000, "long", 5000)
	exposure := func(n int, changes map[string]any) map[string]any {
		return with(with(exposureEvent(n, nil), map[string]any{
			"experiment_key": "banner_test", "flag_key": "banner", "variation_key": "classic",
		}), changes)
	}
	got := batchResult(t, f.srv, f.server, batch(
		exposure(1, map[string]any{"override": true, "evaluation_reason": "override"}),
		exposure(2, nil),
	))
	if got.Accepted != 1 || len(got.Rejected) != 1 || got.Rejected[0].Index != 1 || got.Rejected[0].Code != "unknown_variation" {
		t.Errorf("batch = %+v, want the override accepted and the plain exposure unknown_variation", got)
	}
}

func TestRefusedExposuresAreCountedPerExperimentEnvironmentAndReason(t *testing.T) {
	f := startExposures(t)
	_, stagingKey := f.p.sdkKey(t, "staging", management.SdkKeyKindServer)
	post := func(key string, n int, changes map[string]any) {
		t.Helper()
		if res := batchResult(t, f.srv, key, batch(exposureEvent(n, changes))); len(res.Rejected) != 1 {
			t.Fatalf("exposure %d = %+v, want refused", n, res)
		}
	}
	post(f.client, 1, map[string]any{"unit_type": "anonymous"})
	post(f.server, 2, map[string]any{"unit_type": "anonymous"})
	post(f.client, 3, map[string]any{"variation_key": "maybe"})
	post(f.server, 4, map[string]any{"evaluation_reason": "rule"})
	post(stagingKey, 5, map[string]any{"unit_type": "anonymous"})

	production := f.refused(t, "production")
	if production["unit_type_mismatch"] != 2 || production["unknown_variation"] != 1 || production["event_invalid"] != 1 || len(production) != 3 {
		t.Errorf("production counts = %v, want unit_type_mismatch 2, unknown_variation 1, event_invalid 1", production)
	}
	if staging := f.refused(t, "staging"); staging["unit_type_mismatch"] != 1 || len(staging) != 1 {
		t.Errorf("staging counts = %v, want unit_type_mismatch 1", staging)
	}
	// An exposure accepted, or malformed before it names an experiment, is
	// not a refusal of the experiment.
	batchResult(t, f.srv, f.client, batch(exposureEvent(6, nil), exposureEvent(7, map[string]any{"unit_id": nil})))
	if again := f.refused(t, "production"); again["unit_type_mismatch"] != 2 || len(again) != 3 {
		t.Errorf("production counts after an accepted and a malformed exposure = %v, want unchanged", again)
	}
}

func TestExposuresOfExperimentsThatDoNotExistAddNoCount(t *testing.T) {
	f := startExposures(t)
	var exposures []any
	for i := range 20 {
		exposures = append(exposures, exposureEvent(i+1, map[string]any{"experiment_key": "random_" + strings.Repeat("k", i+1)}))
	}
	got := batchResult(t, f.srv, f.client, batch(exposures...))
	if got.Accepted != 0 || len(got.Rejected) != 20 {
		t.Fatalf("batch = %+v, want 20 refused", got)
	}
	for _, r := range got.Rejected {
		if r.Code != "unknown_experiment" {
			t.Errorf("rejection = %+v, want unknown_experiment", r)
		}
	}
	// Keys of the caller's own making leave the existing experiment's counts
	// as they were, and no row could name them: a count references its
	// experiment.
	if counts := f.refused(t, "production"); len(counts) != 0 {
		t.Errorf("counts = %v, want none", counts)
	}
	if got := testkit.Metric(t, f.srv.Scrape(t), `flagnetic_events_dropped_total{reason="unknown_experiment"}`); got != 20 {
		t.Errorf("unknown_experiment drops counted = %v, want 20", got)
	}
}

func TestTheCountsOfRefusedExposuresStayWhileTheFlagCannotBeArchivedFromItsExperiment(t *testing.T) {
	f := startExposures(t)
	batchResult(t, f.srv, f.client, batch(exposureEvent(1, map[string]any{"unit_type": "anonymous"})))
	if counts := f.refused(t, "production"); counts["unit_type_mismatch"] != 1 {
		t.Fatalf("counts = %v, want one unit_type_mismatch", counts)
	}
	// A flag with a draft experiment is not archived, so the experiment and
	// its counts are never left behind a flag no SDK receives.
	archived := do(f.p.admin.ArchiveFlagWithResponse(t.Context(), f.p.key, "checkout", &management.ArchiveFlagParams{IfMatch: ptr(`"1"`)}))
	if archived.StatusCode() != http.StatusConflict || !strings.Contains(string(archived.Body), `"in_use"`) {
		t.Fatalf("archive = %d: %s, want 409 in_use", archived.StatusCode(), archived.Body)
	}
	if counts := f.refused(t, "production"); counts["unit_type_mismatch"] != 1 {
		t.Errorf("counts after the refused archive = %v, want them kept", counts)
	}
	got := batchResult(t, f.srv, f.client, batch(exposureEvent(2, nil)))
	if got.Accepted != 1 || len(got.Rejected) != 0 {
		t.Errorf("exposure after = %+v, want it accepted", got)
	}
}

func TestIngestCountsExposuresByKind(t *testing.T) {
	f := startExposures(t)
	batchResult(t, f.srv, f.client, batch(exposureEvent(1, nil), exposureEvent(2, nil), exposureEvent(3, map[string]any{"unit_type": "anonymous"})))
	scrape := f.srv.Scrape(t)
	for series, want := range map[string]float64{
		`flagnetic_events_ingested_total{kind="exposure"}`:            2,
		`flagnetic_events_dropped_total{reason="unit_type_mismatch"}`: 1,
	} {
		if got := testkit.Metric(t, scrape, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
}

// skewed posts events from an SDK whose clock reads slow before the server's
// at protocolTime, with the batch stamped by that clock.
func skewed(t *testing.T, f exposuresFixture, slow time.Duration, events ...any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"schema_version": 1, "sent_at": protocolTime.Add(-slow).Format(time.RFC3339),
		"sdk": map[string]any{"name": "flagnetic-dart", "version": "1.0.0", "platform": "linux"}, "events": events,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := batchResult(t, f.srv, f.server, body); res.Accepted != len(events) || len(res.Rejected) != 0 {
		t.Fatalf("batch = %+v, want all %d accepted", res, len(events))
	}
}

func TestAnEnvelopeFromAWrongClockHasEveryTimeOfEveryEventShifted(t *testing.T) {
	// A phone two hours slow stamps the batch two hours before the server
	// receives it, and its events with the same error.
	f := startExposures(t)
	slow := 2 * time.Hour
	at := func(offset time.Duration) string { return protocolTime.Add(-slow + offset).Format(time.RFC3339) }
	skewed(t, f, slow,
		exposureEvent(1, map[string]any{"occurred_at": at(-10 * time.Minute)}),
		with(aliasEvent(eventID(protocolTime, 2)), map[string]any{"occurred_at": at(-20 * time.Minute)}),
		with(summaryEvent(eventID(protocolTime, 3), map[string]any{"flag": "checkout", "variation": "true", "reason": "rule", "count": 1}),
			map[string]any{"interval_start": at(-time.Minute), "interval_end": at(0)}),
	)
	store := pgevents.NewStore(testkit.Pool(t))
	production := f.p.environmentID(t, "production")
	if x := f.stored(t); len(x) != 1 || !x[0].Exposure.OccurredAt.Equal(protocolTime.Add(-10*time.Minute)) {
		t.Errorf("exposure = %+v, want occurred 10 minutes before it was received", x)
	}
	aliases, err := store.Aliases(t.Context(), production)
	if err != nil || len(aliases) != 1 || !aliases[0].Alias.OccurredAt.Equal(protocolTime.Add(-20*time.Minute)) {
		t.Errorf("aliases = %+v, %v, want occurred 20 minutes before it was received", aliases, err)
	}
	summaries, err := store.EvaluationSummaries(t.Context(), production)
	if err != nil || len(summaries) != 1 || !summaries[0].PeriodStart.Equal(protocolTime.Add(-time.Minute)) ||
		!summaries[0].PeriodEnd.Equal(protocolTime) {
		t.Errorf("summaries = %+v, %v, want the minute before it was received", summaries, err)
	}
}

func TestAnEnvelopeWithinAMinuteOfTheServerClockIsNotShifted(t *testing.T) {
	f := startExposures(t)
	skewed(t, f, time.Minute, exposureEvent(1, map[string]any{"occurred_at": protocolTime.Add(-time.Hour).Format(time.RFC3339)}))
	if x := f.stored(t); len(x) != 1 || !x[0].Exposure.OccurredAt.Equal(protocolTime.Add(-time.Hour)) {
		t.Errorf("exposure = %+v, want occurred an hour before it was received, as sent", x)
	}
}

func TestTheShiftOfAWrongClockDecidesTheWindow(t *testing.T) {
	// A phone 40 days slow stamps an exposure 40 days and an hour old by the
	// server's clock, which retention would refuse; the shift makes it the
	// hour-old exposure it was.
	f := startExposures(t)
	slow := 40 * 24 * time.Hour
	skewed(t, f, slow, exposureEvent(1, map[string]any{"occurred_at": protocolTime.Add(-slow - time.Hour).Format(time.RFC3339)}))
	if x := f.stored(t); len(x) != 1 || !x[0].Exposure.OccurredAt.Equal(protocolTime.Add(-time.Hour)) {
		t.Errorf("exposure = %+v, want occurred an hour before it was received", x)
	}
}

func TestThePartitionWorkerKeepsPartitionsOfExposuresAheadOfTheClock(t *testing.T) {
	f := startExposures(t)
	f.clock.Advance(10 * 24 * time.Hour)
	if err := f.srv.Tick(t, worker.EventRetentionName); err != nil {
		t.Fatal(err)
	}
	// Received 3 days after the worker's round, whose partitions were made
	// ahead.
	f.clock.Advance(3 * 24 * time.Hour)
	at := f.clock.Now()
	exposure := exposureEvent(1, map[string]any{"event_id": eventID(at, 1), "occurred_at": at.Format(time.RFC3339)})
	body, err := json.Marshal(map[string]any{
		"schema_version": 1, "sent_at": at.Format(time.RFC3339),
		"sdk": map[string]any{"name": "flagnetic-dart", "version": "1.0.0"}, "events": []any{exposure},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := batchResult(t, f.srv, f.server, body); res.Accepted != 1 {
		t.Errorf("an exposure 3 days after the round = %+v", res)
	}
	if x := f.stored(t); len(x) != 1 {
		t.Errorf("exposures = %+v, want the one posted", x)
	}
}
