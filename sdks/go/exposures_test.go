package flagnetic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// Exposures and aliases: the explicit call, automatic exposure, the
// suppression of repeats, the alias event and the bounded queue that
// carries both to the events endpoint. An evaluation alone never records
// an exposure, so being assigned is never mistaken for being exposed.

// experimentDocument is a server ruleset holding two running experiments —
// one reported by the explicit call, one opted into automatic exposure — a
// flag with automatic exposure and no experiment at all, and an override
// pinning one unit of the first experiment.
const experimentDocument = `{
  "schema_version": 1, "version": 11, "scope": "server",
  "environment": {"key": "staging", "protected": false},
  "unit_types": ["user", "anonymous"],
  "attributes": [{"key": "platform", "type": "string"}, {"key": "app_version", "type": "string"}],
  "segments": [],
  "flags": [
    {"key": "pricing", "type": "string", "automatic_exposure": false, "on": true,
     "variations": [{"key": "control", "value": "old"}, {"key": "treatment", "value": "new"}],
     "off_variation": "control", "fallthrough": "control",
     "overrides": [{"unit_type": "user", "unit_id": "u-pinned", "variation": "control"}],
     "rules": [{"id": "exp", "conditions": [],
                "serve": {"kind": "experiment", "experiment": "pricing_test", "unit_type": "user",
                          "inclusion_salt": "in", "variation_salt": "var", "traffic_allocation": 10000,
                          "weights": [{"variation": "control", "weight": 0}, {"variation": "treatment", "weight": 10000}]}}]},
    {"key": "ranking", "type": "boolean", "automatic_exposure": true, "on": true,
     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
     "off_variation": "off", "fallthrough": "off",
     "rules": [{"id": "exp2", "conditions": [],
                "serve": {"kind": "experiment", "experiment": "ranking_test", "unit_type": "user",
                          "inclusion_salt": "rin", "variation_salt": "rvar", "traffic_allocation": 10000,
                          "weights": [{"variation": "off", "weight": 0}, {"variation": "on", "weight": 10000}]}}]},
    {"key": "greeting", "type": "string", "automatic_exposure": true, "on": true,
     "variations": [{"key": "hello", "value": "hello"}],
     "off_variation": "hello", "fallthrough": "hello"}
  ]
}`

// unitContext is the context of one unit, with the predeclared attributes
// an exposure carries.
func unitContext(id string) flagnetic.Context {
	return flagnetic.Context{
		Units:      map[string]string{"user": id},
		Attributes: map[string]any{"platform": "linux", "app_version": "1.18.0"},
	}
}

// newExperiments builds an offline client on experimentDocument, whose
// exposures and aliases go to its Recorder.
func newExperiments(t *testing.T, opts ...flagnetic.Option) *flagnetic.Client {
	t.Helper()
	c, err := flagnetic.New("", "", append([]flagnetic.Option{
		flagnetic.WithOffline([]byte(experimentDocument)),
	}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func TestExposureReportsTheMostRecentDetailOfTheFlagAndContext(t *testing.T) {
	var l logs
	c := newExperiments(t, flagnetic.WithLogger(l.logger()))
	ctx := unitContext("u-1")

	// Nothing was evaluated yet: the call sends nothing and says why.
	c.Exposure(context.Background(), ctx, "pricing")
	if got := len(c.Recorder().Exposures()); got != 0 {
		t.Fatalf("%d exposures before any evaluation, want 0", got)
	}
	skipped := l.named(flagnetic.EventExposureSkipped)
	if len(skipped) != 1 || skipped[0].Fields["cause"] != "no_experiment" || skipped[0].Fields["flag"] != "pricing" {
		t.Fatalf("exposure_skipped = %v, want one naming pricing and no_experiment", skipped)
	}

	d := c.Detail(context.Background(), ctx, "pricing", "built-in")
	if d.Reason != flagnetic.ReasonExperiment || d.Experiment == nil {
		t.Fatalf("pricing served %+v, want reason experiment with an experiment block", d)
	}
	c.Exposure(context.Background(), ctx, "pricing")

	recorded := c.Recorder().Exposures()
	if len(recorded) != 1 {
		t.Fatalf("%d exposures recorded, want 1", len(recorded))
	}
	e := recorded[0]
	want := flagnetic.Exposure{
		EventID:        e.EventID,
		ExperimentKey:  "pricing_test",
		FlagKey:        "pricing",
		VariationKey:   "treatment",
		UnitType:       "user",
		UnitID:         "u-1",
		Platform:       "linux",
		AppVersion:     "1.18.0",
		Reason:         flagnetic.ReasonExperiment,
		Override:       false,
		RulesetVersion: 11,
		OccurredAt:     e.OccurredAt,
	}
	if e != want {
		t.Errorf("exposure = %+v, want %+v", e, want)
	}
	assertUUIDv7(t, e.EventID)
	if e.OccurredAt.IsZero() || e.OccurredAt.Location() != time.UTC {
		t.Errorf("occurred_at = %v, want a UTC time", e.OccurredAt)
	}
}

func TestExposureOfADetailWithoutAnExperimentSendsNothing(t *testing.T) {
	var l logs
	c := newExperiments(t, flagnetic.WithLogger(l.logger()), flagnetic.WithLocalOverrides())
	ctx := unitContext("u-1")

	// A fallthrough, a fallback and a local override all carry no
	// experiment block, so none of them is an exposure.
	c.String(context.Background(), ctx, "greeting", "built-in")
	c.Exposure(context.Background(), ctx, "greeting")
	c.Bool(context.Background(), ctx, "missing_flag", false)
	c.Exposure(context.Background(), ctx, "missing_flag")
	if err := c.LocalOverrides().Set("pricing", "local"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if d := c.Detail(context.Background(), ctx, "pricing", "built-in"); d.Reason != flagnetic.ReasonLocalOverride {
		t.Fatalf("pricing served %s, want local_override", d.Reason)
	}
	c.Exposure(context.Background(), ctx, "pricing")

	if got := c.Recorder().Exposures(); len(got) != 0 {
		t.Fatalf("%d exposures recorded, want none: %+v", len(got), got)
	}
	skipped := l.named(flagnetic.EventExposureSkipped)
	if len(skipped) != 3 {
		t.Fatalf("exposure_skipped logged %d times, want 3", len(skipped))
	}
	for _, e := range skipped {
		// None of these flags has ever carried an experiment block.
		if e.Fields["cause"] != "no_experiment" {
			t.Errorf("exposure_skipped for %v names cause %v, want no_experiment", e.Fields["flag"], e.Fields["cause"])
		}
	}
}

func TestExposureIsLookedUpPerContext(t *testing.T) {
	var l logs
	c := newExperiments(t, flagnetic.WithLogger(l.logger()))
	one, two := unitContext("u-1"), unitContext("u-2")
	c.Detail(context.Background(), one, "pricing", "built-in")

	// The detail of another unit is another unit's: the call for a context
	// nothing was evaluated for sends nothing, and the cause tells that
	// apart from a flag with no experiment at all.
	c.Exposure(context.Background(), two, "pricing")
	if got := len(c.Recorder().Exposures()); got != 0 {
		t.Fatalf("%d exposures for a context never evaluated, want 0", got)
	}
	skipped := l.named(flagnetic.EventExposureSkipped)
	if len(skipped) != 1 || skipped[0].Fields["cause"] != "no_detail" {
		t.Fatalf("exposure_skipped = %v, want one naming no_detail", skipped)
	}
	c.Exposure(context.Background(), one, "pricing")
	recorded := c.Recorder().Exposures()
	if len(recorded) != 1 || recorded[0].UnitID != "u-1" {
		t.Fatalf("exposures = %+v, want one for u-1", recorded)
	}
}

func TestAnExperimentTheRulesetDropsStopsBeingReported(t *testing.T) {
	// The ruleset the offline source hands out is rewritten under the
	// client, exactly as a stopped experiment rewrites it on the server.
	document := experimentDocument
	c, err := flagnetic.New("", "", flagnetic.WithOfflineSource(func() ([]byte, error) {
		return []byte(document), nil
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	ctx := unitContext("u-1")
	c.Detail(context.Background(), ctx, "pricing", "built-in")
	c.Exposure(context.Background(), ctx, "pricing")
	if got := len(c.Recorder().Exposures()); got != 1 {
		t.Fatalf("%d exposures while the experiment runs, want 1", got)
	}

	// The experiment rule is gone: pricing now serves its fallthrough.
	document = strings.Replace(experimentDocument, `"fallthrough": "control",
     "overrides": [{"unit_type": "user", "unit_id": "u-pinned", "variation": "control"}],
     "rules": [{"id": "exp", "conditions": [],
                "serve": {"kind": "experiment", "experiment": "pricing_test", "unit_type": "user",
                          "inclusion_salt": "in", "variation_salt": "var", "traffic_allocation": 10000,
                          "weights": [{"variation": "control", "weight": 0}, {"variation": "treatment", "weight": 10000}]}}]},`,
		`"fallthrough": "control"},`, 1)
	if document == experimentDocument {
		t.Fatal("the experiment rule was not removed from the document")
	}
	if err := c.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if d := c.Detail(context.Background(), ctx, "pricing", "built-in"); d.Reason != flagnetic.ReasonFallthrough {
		t.Fatalf("pricing served %s, want fallthrough", d.Reason)
	}
	c.Exposure(context.Background(), ctx, "pricing")
	if got := len(c.Recorder().Exposures()); got != 1 {
		t.Errorf("%d exposures after the experiment stopped, want the first one only", got)
	}
}

func TestAnOverrideExposureIsMarked(t *testing.T) {
	c := newExperiments(t)
	ctx := unitContext("u-pinned")
	d := c.Detail(context.Background(), ctx, "pricing", "built-in")
	if d.Reason != flagnetic.ReasonOverride || d.Experiment == nil || !d.Experiment.Override {
		t.Fatalf("the pinned unit served %+v, want reason override with an override-marked experiment block", d)
	}
	c.Exposure(context.Background(), ctx, "pricing")

	recorded := c.Recorder().Exposures()
	if len(recorded) != 1 {
		t.Fatalf("%d exposures recorded, want 1", len(recorded))
	}
	e := recorded[0]
	if e.Reason != flagnetic.ReasonOverride || !e.Override {
		t.Errorf("exposure reason %s, override %v; want override and true", e.Reason, e.Override)
	}
	if e.VariationKey != d.Experiment.Variation {
		t.Errorf("variation_key = %q, want the served %q", e.VariationKey, d.Experiment.Variation)
	}
}

func TestAutomaticExposureEmitsAfterEveryQualifyingEvaluation(t *testing.T) {
	c := newExperiments(t)

	// ranking is opted into automatic exposure: evaluating it is the
	// exposure, once per unit.
	c.Bool(context.Background(), unitContext("u-1"), "ranking", false)
	c.Bool(context.Background(), unitContext("u-2"), "ranking", false)
	c.Bool(context.Background(), unitContext("u-1"), "ranking", false)
	// greeting is opted in too, but serves its fallthrough: no experiment
	// block, no exposure.
	c.String(context.Background(), unitContext("u-1"), "greeting", "built-in")
	// pricing is not opted in: evaluating it records nothing.
	c.Detail(context.Background(), unitContext("u-1"), "pricing", "built-in")

	recorded := c.Recorder().Exposures()
	if len(recorded) != 2 {
		t.Fatalf("%d exposures recorded, want 2: %+v", len(recorded), recorded)
	}
	for _, e := range recorded {
		if e.FlagKey != "ranking" || e.ExperimentKey != "ranking_test" || e.VariationKey != "on" {
			t.Errorf("exposure %+v, want ranking/ranking_test/on", e)
		}
	}
	if recorded[0].UnitID == recorded[1].UnitID {
		t.Errorf("both exposures name %q, want one per unit", recorded[0].UnitID)
	}
}

func TestRepeatsAreSuppressedForAtLeastTheFlushInterval(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	clock := &testClock{at: now}
	const interval = 2 * time.Second
	c := newExperiments(t, flagnetic.WithFlushInterval(interval), flagnetic.WithExposureNow(clock.now))
	ctx := unitContext("u-1")
	c.Detail(context.Background(), ctx, "pricing", "built-in")

	c.Exposure(context.Background(), ctx, "pricing")
	clock.advance(interval - time.Millisecond)
	c.Exposure(context.Background(), ctx, "pricing")
	if got := len(c.Recorder().Exposures()); got != 1 {
		t.Fatalf("%d exposures within the flush interval, want 1", got)
	}

	// One window on, the set has rotated once and still reads the
	// generation the unit is in: an entry lives for at least one window,
	// not exactly one.
	clock.advance(2 * interval)
	c.Exposure(context.Background(), ctx, "pricing")
	if got := len(c.Recorder().Exposures()); got != 1 {
		t.Fatalf("%d exposures one window on, want the repeat still suppressed", got)
	}

	// Two rotations on, both generations have forgotten it, so the unit
	// may be recorded again; the platform's own deduplication is what
	// analysis relies on.
	clock.advance(2 * interval)
	c.Exposure(context.Background(), ctx, "pricing")
	if got := len(c.Recorder().Exposures()); got != 2 {
		t.Fatalf("%d exposures two windows on, want 2", got)
	}
}

func TestSuppressionIsPerExperimentUnitAndVariation(t *testing.T) {
	c := newExperiments(t)
	// The same unit in two experiments, and two units in one: four keys,
	// four exposures, and a repeat of each adds nothing.
	for range 2 {
		c.Detail(context.Background(), unitContext("u-1"), "pricing", "built-in")
		c.Exposure(context.Background(), unitContext("u-1"), "pricing")
		c.Detail(context.Background(), unitContext("u-2"), "pricing", "built-in")
		c.Exposure(context.Background(), unitContext("u-2"), "pricing")
		c.Bool(context.Background(), unitContext("u-1"), "ranking", false)
		c.Bool(context.Background(), unitContext("u-2"), "ranking", false)
	}
	if got := len(c.Recorder().Exposures()); got != 4 {
		t.Fatalf("%d exposures, want 4: %+v", got, c.Recorder().Exposures())
	}
}

func TestAliasRecordsBothUnitPairsAndChangesNoContext(t *testing.T) {
	c := newExperiments(t)
	ctx := flagnetic.Context{Units: map[string]string{"anonymous": "d-8f1c", "user": "u-1001"}}
	before := c.Detail(context.Background(), ctx, "pricing", "built-in")

	c.Alias(context.Background(), "anonymous", "d-8f1c", "user", "u-1001")

	recorded := c.Recorder().Aliases()
	if len(recorded) != 1 {
		t.Fatalf("%d aliases recorded, want 1", len(recorded))
	}
	a := recorded[0]
	if a.FromUnitType != "anonymous" || a.FromUnitID != "d-8f1c" || a.ToUnitType != "user" || a.ToUnitID != "u-1001" {
		t.Errorf("alias = %+v, want the anonymous unit linked to the user unit", a)
	}
	assertUUIDv7(t, a.EventID)
	if a.OccurredAt.IsZero() {
		t.Error("the alias carries no occurred_at")
	}
	// An alias links two unit IDs for analysis; it never changes what the
	// evaluator hashes on.
	if after := c.Detail(context.Background(), ctx, "pricing", "built-in"); after.Variation != before.Variation || after.Reason != before.Reason {
		t.Errorf("after the alias pricing serves %q/%s, want the unchanged %q/%s",
			after.Variation, after.Reason, before.Variation, before.Reason)
	}
	// A repeat is a second link: aliases are not coalesced.
	c.Alias(context.Background(), "anonymous", "d-8f1c", "user", "u-1001")
	if got := len(c.Recorder().Aliases()); got != 2 {
		t.Errorf("%d aliases after the second call, want 2", got)
	}
}

func TestExposuresAndAliasesAreOffWithTheOption(t *testing.T) {
	var l logs
	c := newExperiments(t, flagnetic.WithLogger(l.logger()), flagnetic.WithExposures(false))
	if got := len(l.named(flagnetic.EventExposuresDisabled)); got != 1 {
		t.Fatalf("exposures_disabled logged %d times at construction, want 1", got)
	}
	ctx := unitContext("u-1")
	c.Detail(context.Background(), ctx, "pricing", "built-in")
	c.Bool(context.Background(), ctx, "ranking", false) // automatic exposure, off with the rest
	c.Exposure(context.Background(), ctx, "pricing")
	c.Alias(context.Background(), "anonymous", "d-8f1c", "user", "u-1001")
	if err := c.Flush(context.Background()); err != nil {
		t.Errorf("Flush: %v", err)
	}
	if got := c.Recorder().Exposures(); len(got) != 0 {
		t.Errorf("%d exposures recorded with exposures off", len(got))
	}
	if got := c.Recorder().Aliases(); len(got) != 0 {
		t.Errorf("%d aliases recorded with exposures off", len(got))
	}
}

// wireExposure is an exposure event of a batch, as the events endpoint
// receives it.
type wireExposure struct {
	Type             string    `json:"type"`
	EventID          string    `json:"event_id"`
	ExperimentKey    string    `json:"experiment_key"`
	FlagKey          string    `json:"flag_key"`
	VariationKey     string    `json:"variation_key"`
	UnitType         string    `json:"unit_type"`
	UnitID           string    `json:"unit_id"`
	Platform         string    `json:"platform"`
	AppVersion       string    `json:"app_version"`
	EvaluationReason string    `json:"evaluation_reason"`
	Override         bool      `json:"override"`
	RulesetVersion   int64     `json:"ruleset_version"`
	OccurredAt       time.Time `json:"occurred_at"`
}

// wireAlias is an alias event of a batch.
type wireAlias struct {
	Type         string    `json:"type"`
	EventID      string    `json:"event_id"`
	FromUnitType string    `json:"from_unit_type"`
	FromUnitID   string    `json:"from_unit_id"`
	ToUnitType   string    `json:"to_unit_type"`
	ToUnitID     string    `json:"to_unit_id"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// rawEvents decodes a batch's events without interpreting them, so a test
// reads exactly the fields that went on the wire.
func rawEvents(t *testing.T, b batch) []json.RawMessage {
	t.Helper()
	var envelope struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(b.raw, &envelope); err != nil {
		t.Fatalf("decode the batch %s: %v", b.raw, err)
	}
	return envelope.Events
}

// onExperiments builds an online client against the mock, waits for the
// ruleset and hands back a queue whose ticker never fires and whose
// batches are sent one attempt at a time: the flush interval is shorter
// than any backoff, so one Flush is one request.
func onExperiments(t *testing.T, m *ingestMock, opts ...flagnetic.Option) *flagnetic.Client {
	t.Helper()
	opts = append([]flagnetic.Option{
		flagnetic.WithFlushInterval(time.Millisecond),
		flagnetic.WithExposureTick(make(chan time.Time)),
	}, opts...)
	c := newOnline(t, m.URL, opts...)
	if stage := readyNow(t, c); stage != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh", stage)
	}
	return c
}

func TestExposuresRideTheEnvelopeAndNameNoSDK(t *testing.T) {
	m := newExperimentMock(t)
	c := onExperiments(t, m)
	ctx := unitContext("u-1")
	c.Detail(context.Background(), ctx, "pricing", "built-in")
	c.Exposure(context.Background(), ctx, "pricing")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	b := m.nth(1)
	if b.wire.SDK.Name != "flagnetic-go" || b.wire.SDK.Version == "" {
		t.Errorf("the envelope names %+v, want this SDK", b.wire.SDK)
	}
	events := rawEvents(t, b)
	if len(events) != 1 {
		t.Fatalf("%d events in the batch, want 1", len(events))
	}
	var got wireExposure
	if err := json.Unmarshal(events[0], &got); err != nil {
		t.Fatalf("decode the exposure %s: %v", events[0], err)
	}
	want := wireExposure{
		Type: "exposure", EventID: got.EventID, ExperimentKey: "pricing_test", FlagKey: "pricing",
		VariationKey: "treatment", UnitType: "user", UnitID: "u-1",
		Platform: "linux", AppVersion: "1.18.0",
		EvaluationReason: "experiment", Override: false, RulesetVersion: 11, OccurredAt: got.OccurredAt,
	}
	if got != want {
		t.Errorf("exposure = %+v, want %+v", got, want)
	}
	// The SDK's name and version are the envelope's alone.
	for _, absent := range []string{"sdk_name", "sdk_version", "environment", "received_at"} {
		if strings.Contains(string(events[0]), absent) {
			t.Errorf("the exposure carries %q: %s", absent, events[0])
		}
	}
}

func TestAnExposureWithoutThePredeclaredAttributesCarriesNulls(t *testing.T) {
	m := newExperimentMock(t)
	c := onExperiments(t, m)
	ctx := flagnetic.Context{Units: map[string]string{"user": "u-1"}}
	c.Detail(context.Background(), ctx, "pricing", "built-in")
	c.Exposure(context.Background(), ctx, "pricing")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	events := rawEvents(t, m.nth(1))
	var got struct {
		Platform   *string `json:"platform"`
		AppVersion *string `json:"app_version"`
	}
	if err := json.Unmarshal(events[0], &got); err != nil {
		t.Fatalf("decode the exposure %s: %v", events[0], err)
	}
	if got.Platform != nil || got.AppVersion != nil {
		t.Errorf("platform = %v, app_version = %v; want both null", got.Platform, got.AppVersion)
	}
	for _, field := range []string{`"platform":null`, `"app_version":null`} {
		if !strings.Contains(string(events[0]), field) {
			t.Errorf("the exposure leaves out %s rather than naming it null: %s", field, events[0])
		}
	}
}

func TestTheAliasShapeOnTheWire(t *testing.T) {
	m := newExperimentMock(t)
	c := onExperiments(t, m)
	c.Alias(context.Background(), "anonymous", "d-8f1c", "user", "u-1001")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	events := rawEvents(t, m.nth(1))
	if len(events) != 1 {
		t.Fatalf("%d events in the batch, want 1", len(events))
	}
	var got wireAlias
	if err := json.Unmarshal(events[0], &got); err != nil {
		t.Fatalf("decode the alias %s: %v", events[0], err)
	}
	if got.Type != "alias" || got.FromUnitType != "anonymous" || got.FromUnitID != "d-8f1c" ||
		got.ToUnitType != "user" || got.ToUnitID != "u-1001" {
		t.Errorf("alias = %+v", got)
	}
	assertUUIDv7(t, got.EventID)
	if got.OccurredAt.IsZero() {
		t.Error("the alias carries no occurred_at")
	}
}

func TestEventsEnqueuedDuringFailuresFlushOnceWithTheSameIDs(t *testing.T) {
	var l logs
	m := newExperimentMock(t,
		reply{status: http.StatusInternalServerError},
		reply{status: http.StatusInternalServerError},
		reply{status: http.StatusAccepted, body: accepted})
	// A clock the test moves and a jitter that always draws its ceiling,
	// so the backoff a failed flush leaves behind is exactly known.
	clock := &testClock{at: time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)}
	c := onExperiments(t, m, flagnetic.WithLogger(l.logger()),
		flagnetic.WithExposureNow(clock.now), ceiling())

	for _, unit := range []string{"u-1", "u-2"} {
		ctx := unitContext(unit)
		c.Detail(context.Background(), ctx, "pricing", "built-in")
		c.Exposure(context.Background(), ctx, "pricing")
	}

	// The first attempt fails and the events stay queued. The backoff it
	// left holds the queue back, so a flush before it is up sends
	// nothing: a platform that keeps failing is asked ever less often,
	// not once per flush interval for ever.
	_ = c.Flush(context.Background())
	_ = c.Flush(context.Background())
	if got := m.count(); got != 1 {
		t.Fatalf("%d batches sent, want 1: the backoff did not hold the queue back", got)
	}

	// One second on the backoff is up; the second attempt fails and
	// doubles it, so two more seconds pass before the third.
	clock.advance(time.Second)
	_ = c.Flush(context.Background())
	clock.advance(2 * time.Second)
	_ = c.Flush(context.Background())

	for n := 1; n <= 3; n++ {
		if got := m.nth(n).eventIDs(); len(got) != 2 {
			t.Fatalf("batch %d carries %d events, want 2", n, len(got))
		}
	}
	first, third := m.nth(1).eventIDs(), m.nth(3).eventIDs()
	if fmt.Sprint(first) != fmt.Sprint(third) {
		t.Errorf("the retry carries %v, want the first attempt's %v", third, first)
	}
	failed := l.named(flagnetic.EventIngestFailed)
	if len(failed) != 2 {
		t.Fatalf("ingest_failed logged %d times, want 2", len(failed))
	}
	if failed[0].Fields["retry_in"] != time.Second.String() || failed[1].Fields["retry_in"] != (2*time.Second).String() {
		t.Errorf("the backoff went %v then %v, want 1s then 2s", failed[0].Fields["retry_in"], failed[1].Fields["retry_in"])
	}

	// Accepted is done: the backoff is forgotten and nothing is sent again.
	_ = c.Flush(context.Background())
	if got := m.count(); got != 3 {
		t.Errorf("%d batches sent, want 3", got)
	}
}

func TestRejectedEventsAreNeverSentAgain(t *testing.T) {
	var l logs
	const oneRejected = `{"accepted": 1, "duplicates": 0, "dropped_rows": 0,
		"rejected": [{"index": 1, "event_id": "x", "code": "undeclared_unit_type", "detail": "ghost"}]}`
	m := newExperimentMock(t, reply{status: http.StatusAccepted, body: oneRejected})
	c := onExperiments(t, m, flagnetic.WithLogger(l.logger()))

	c.Alias(context.Background(), "anonymous", "d-8f1c", "user", "u-1001")
	c.Alias(context.Background(), "ghost", "g-1", "user", "u-1001")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := len(m.nth(1).eventIDs()); got != 2 {
		t.Fatalf("the batch carries %d events, want 2", got)
	}
	dropped := l.named(flagnetic.EventQueueDropped)
	if len(dropped) != 1 || dropped[0].Fields["cause"] != "refused_by_the_platform" {
		t.Fatalf("queue_dropped = %v, want one naming the platform's refusal", dropped)
	}
	_ = c.Flush(context.Background())
	if got := m.count(); got != 1 {
		t.Errorf("%d batches sent, want 1: a rejected event was sent again", got)
	}
}

func TestAFullQueueDropsTheOldest(t *testing.T) {
	var l logs
	m := newExperimentMock(t)
	c := onExperiments(t, m, flagnetic.WithLogger(l.logger()), flagnetic.WithQueueCapacity(3))

	for i := range 5 {
		c.Alias(context.Background(), "anonymous", fmt.Sprintf("d-%d", i), "user", "u-1001")
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	events := rawEvents(t, m.nth(1))
	if len(events) != 3 {
		t.Fatalf("the batch carries %d events, want the capacity of 3", len(events))
	}
	var kept []string
	for _, raw := range events {
		var a wireAlias
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatalf("decode the alias %s: %v", raw, err)
		}
		kept = append(kept, a.FromUnitID)
	}
	if want := []string{"d-2", "d-3", "d-4"}; fmt.Sprint(kept) != fmt.Sprint(want) {
		t.Errorf("the queue kept %v, want the newest %v", kept, want)
	}
	dropped := l.named(flagnetic.EventQueueDropped)
	if len(dropped) != 2 {
		t.Fatalf("queue_dropped logged %d times, want 2", len(dropped))
	}
	if dropped[0].Fields["cause"] != "queue_full" || dropped[0].Fields["kind"] != "exposure_or_alias" {
		t.Errorf("queue_dropped fields = %v", dropped[0].Fields)
	}
}

func TestTheQueueFlushesAtTheBatchSizeWithoutAFlush(t *testing.T) {
	m := newExperimentMock(t)
	// The ticker never fires, so only the batch size can send this.
	c := onExperiments(t, m)
	for i := range 500 {
		c.Alias(context.Background(), "anonymous", fmt.Sprintf("d-%d", i), "user", "u-1001")
	}
	if got := len(m.nth(1).eventIDs()); got != 500 {
		t.Errorf("the batch carries %d events, want 500", got)
	}
}

func TestTheQueueFlushesOnItsInterval(t *testing.T) {
	m := newExperimentMock(t)
	ticks := make(chan time.Time, 1)
	c := newOnline(t, m.URL, flagnetic.WithExposureTick(ticks))
	if stage := readyNow(t, c); stage != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh", stage)
	}
	c.Alias(context.Background(), "anonymous", "d-8f1c", "user", "u-1001")
	ticks <- time.Now()
	if got := len(m.nth(1).eventIDs()); got != 1 {
		t.Errorf("the batch carries %d events, want 1", got)
	}
}

func TestCloseFlushesTheQueue(t *testing.T) {
	m := newExperimentMock(t)
	c, err := flagnetic.New(serverKey, m.URL,
		flagnetic.WithChangeStream(false), flagnetic.WithSummaries(false),
		flagnetic.WithExposureTick(make(chan time.Time)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if stage := readyNow(t, c); stage != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh", stage)
	}
	c.Alias(context.Background(), "anonymous", "d-8f1c", "user", "u-1001")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := len(m.nth(1).eventIDs()); got != 1 {
		t.Errorf("Close sent %d events, want the queued one", got)
	}
}

// newExperimentMock serves experimentDocument as the ruleset and scripts
// the answers to every ingest batch.
func newExperimentMock(t *testing.T, answers ...reply) *ingestMock {
	t.Helper()
	m := newIngestMock(t, answers...)
	m.ruleset = experimentDocument
	return m
}

func TestTheQueueIsSafeUnderConcurrentUse(t *testing.T) {
	// The concurrency scenario every Go client must pass: every call the
	// queue has, from many goroutines, while its flush loop runs and the
	// platform answers. The race detector is what asserts; the counts below assert
	// that nothing was lost or double-counted meanwhile.
	m := newExperimentMock(t)
	ticks := make(chan time.Time, 8)
	c := newOnline(t, m.URL, flagnetic.WithExposureTick(ticks))
	if stage := readyNow(t, c); stage != flagnetic.StageFresh {
		t.Fatalf("Ready = %q, want fresh", stage)
	}

	const goroutines, each = 8, 50
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				unit := fmt.Sprintf("u-%d-%d", g, i)
				ctx := unitContext(unit)
				// pricing is reported by the explicit call, ranking by
				// automatic exposure, and every unit is also aliased.
				c.Detail(context.Background(), ctx, "pricing", "built-in")
				c.Exposure(context.Background(), ctx, "pricing")
				c.Bool(context.Background(), ctx, "ranking", false)
				c.Alias(context.Background(), "anonymous", "d-"+unit, "user", unit)
				if i%10 == 0 {
					select {
					case ticks <- time.Now():
					default:
					}
				}
			}
		}()
	}
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// Two exposures and one alias per unit, every unit distinct, so
	// nothing is suppressed and nothing may go missing.
	want := goroutines * each * 3
	sent := 0
	for n := 1; n <= m.count(); n++ {
		sent += len(m.nth(n).eventIDs())
	}
	if sent != want {
		t.Errorf("%d events reached the platform over %d batches, want %d", sent, m.count(), want)
	}
}
