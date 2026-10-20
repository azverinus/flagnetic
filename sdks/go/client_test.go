package flagnetic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// document is a server ruleset document with one flag of every flag type
// and one running experiment, as the ruleset endpoint serves it.
const document = `{
  "schema_version": 1, "version": 7, "scope": "server",
  "environment": {"key": "staging", "protected": false},
  "unit_types": ["user"],
  "attributes": [{"key": "os", "type": "string"}],
  "segments": [],
  "flags": [
    {"key": "new_checkout", "type": "boolean", "automatic_exposure": false, "on": true,
     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
     "off_variation": "off", "fallthrough": "off",
     "rules": [{"id": "r1", "conditions": [{"kind": "attribute", "attribute": "os", "op": "eq", "value": "linux"}],
                "serve": {"kind": "variation", "variation": "on"}}]},
    {"key": "greeting", "type": "string", "automatic_exposure": false, "on": true,
     "variations": [{"key": "hello", "value": "hello"}, {"key": "hi", "value": "hi"}],
     "off_variation": "hello", "fallthrough": "hi"},
    {"key": "page_size", "type": "number", "automatic_exposure": false, "on": false,
     "variations": [{"key": "small", "value": 10}, {"key": "large", "value": 50}],
     "off_variation": "small", "fallthrough": "large"},
    {"key": "theme", "type": "json", "automatic_exposure": false, "on": true,
     "variations": [{"key": "dark", "value": {"background": "black"}}],
     "off_variation": "dark", "fallthrough": "dark"},
    {"key": "pricing", "type": "string", "automatic_exposure": false, "on": true,
     "variations": [{"key": "control", "value": "old"}, {"key": "treatment", "value": "new"}],
     "off_variation": "control", "fallthrough": "control",
     "rules": [{"id": "exp", "conditions": [],
                "serve": {"kind": "experiment", "experiment": "pricing_test", "unit_type": "user",
                          "inclusion_salt": "in", "variation_salt": "var", "traffic_allocation": 10000,
                          "weights": [{"variation": "control", "weight": 0}, {"variation": "treatment", "weight": 10000}]}}]}
  ]
}`

var linux = flagnetic.Context{
	Units:      map[string]string{"user": "u-1"},
	Attributes: map[string]any{"os": "linux"},
}

// logEntry is one call of the logger hook.
type logEntry struct {
	Level  slog.Level
	Event  flagnetic.Event
	Fields map[string]any
}

// logs captures the logger hook.
type logs struct {
	mu      sync.Mutex
	entries []logEntry
}

func (l *logs) logger() flagnetic.Logger {
	return func(level slog.Level, event flagnetic.Event, fields map[string]any) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.entries = append(l.entries, logEntry{level, event, fields})
	}
}

func (l *logs) named(event flagnetic.Event) []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []logEntry
	for _, e := range l.entries {
		if e.Event == event {
			out = append(out, e)
		}
	}
	return out
}

func newOffline(t *testing.T, opts ...flagnetic.Option) *flagnetic.Client {
	t.Helper()
	c, err := flagnetic.New("", "", append([]flagnetic.Option{flagnetic.WithOffline([]byte(document))}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func TestOfflineClientIsFreshAndReadyAtOnce(t *testing.T) {
	var l logs
	c := newOffline(t, flagnetic.WithLogger(l.logger()))

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if got := c.Ready(ctx); got != flagnetic.StageFresh {
		t.Errorf("Ready = %q, want fresh", got)
	}
	if got := c.Stage(); got != flagnetic.StageFresh {
		t.Errorf("Stage = %q, want fresh", got)
	}
	if c.Recorder() == nil {
		t.Error("an offline client has a recorder")
	}
	loaded := l.named(flagnetic.EventRulesetLoaded)
	if len(loaded) != 1 {
		t.Fatalf("ruleset_loaded logged %d times, want 1", len(loaded))
	}
	if loaded[0].Fields["ruleset_version"] != int64(7) || loaded[0].Fields["flags"] != 5 {
		t.Errorf("ruleset_loaded fields = %v", loaded[0].Fields)
	}
}

func TestOfflineFileIsReadLikeTheDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ruleset.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := flagnetic.New("", "", flagnetic.WithOfflineFile(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Bool(context.Background(), linux, "new_checkout", false); !got {
		t.Error("new_checkout from a file = false, want true")
	}
}

func TestConstructionRefusesAnUnreadableDocument(t *testing.T) {
	for name, opt := range map[string]flagnetic.Option{
		"corrupt json":       flagnetic.WithOffline([]byte(`{`)),
		"unsupported schema": flagnetic.WithOffline([]byte(`{"schema_version": 99, "scope": "server"}`)),
		"missing file":       flagnetic.WithOfflineFile(filepath.Join(t.TempDir(), "absent.json")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := flagnetic.New("", "", opt); err == nil {
				t.Error("New succeeded, want an error")
			}
		})
	}
}

func TestTypedGettersServeTheRuleset(t *testing.T) {
	c := newOffline(t)
	if got := c.Bool(context.Background(), linux, "new_checkout", false); got != true {
		t.Errorf("Bool = %v", got)
	}
	if got := c.String(context.Background(), linux, "greeting", "default"); got != "hi" {
		t.Errorf("String = %q", got)
	}
	if got := c.Number(context.Background(), linux, "page_size", 1); got != 10 {
		t.Errorf("Number = %v", got)
	}
	if got := c.JSON(context.Background(), linux, "theme", json.RawMessage(`{}`)); string(got) != `{"background":"black"}` {
		t.Errorf("JSON = %s", got)
	}
}

func TestDetailCarriesWhyTheValueWasServed(t *testing.T) {
	c := newOffline(t)
	d := c.Detail(context.Background(), linux, "new_checkout", false)
	if d.Value != true || d.Reason != flagnetic.ReasonRule || d.Variation != "on" || d.RuleID != "r1" {
		t.Errorf("detail = %+v", d)
	}
	if d.RulesetVersion != 7 || d.Stage != flagnetic.StageFresh || d.ReceivedAt.IsZero() || d.Overridden {
		t.Errorf("detail metadata = %+v", d)
	}
	if d.Experiment != nil {
		t.Errorf("experiment = %+v, want none", d.Experiment)
	}
}

func TestDetailCarriesTheExperimentBlock(t *testing.T) {
	c := newOffline(t)
	d := c.Detail(context.Background(), linux, "pricing", "old")
	if d.Reason != flagnetic.ReasonExperiment || d.Value != "new" {
		t.Fatalf("detail = %+v", d)
	}
	want := &flagnetic.Experiment{
		Key: "pricing_test", Variation: "treatment", UnitType: "user", UnitID: "u-1",
		InclusionSalt: "in", VariationSalt: "var",
		InclusionBucket: d.Experiment.InclusionBucket, VariationBucket: d.Experiment.VariationBucket,
	}
	if !reflect.DeepEqual(d.Experiment, want) {
		t.Errorf("experiment = %+v, want %+v", d.Experiment, want)
	}
}

func TestDetailNumberAcceptsAnyGoNumberAsDefault(t *testing.T) {
	c := newOffline(t)
	if d := c.Detail(context.Background(), linux, "page_size", 3); d.Value != float64(10) || d.Reason != flagnetic.ReasonOff {
		t.Errorf("detail = %+v", d)
	}
	if d := c.Detail(context.Background(), linux, "absent", 3); d.Value != 3 || d.Reason != flagnetic.ReasonFallback {
		t.Errorf("detail = %+v", d)
	}
}

func TestGettersAreTotal(t *testing.T) {
	var l logs
	c := newOffline(t, flagnetic.WithLogger(l.logger()))

	t.Run("missing flag", func(t *testing.T) {
		d := c.Detail(context.Background(), linux, "absent", "built-in")
		if d.Value != "built-in" || d.Reason != flagnetic.ReasonFallback || d.ErrorCode != flagnetic.ErrorFlagNotFound {
			t.Errorf("detail = %+v", d)
		}
		if d.Stage != flagnetic.StageFresh || d.RulesetVersion != 7 {
			t.Errorf("a fallback still reports stage and version: %+v", d)
		}
	})

	t.Run("type mismatch is logged once per key", func(t *testing.T) {
		for range 3 {
			if got := c.String(context.Background(), linux, "new_checkout", "built-in"); got != "built-in" {
				t.Errorf("String on a boolean flag = %q", got)
			}
		}
		d := c.Detail(context.Background(), linux, "new_checkout", 5)
		if d.Reason != flagnetic.ReasonFallback || d.ErrorCode != flagnetic.ErrorTypeMismatch || d.Value != 5 {
			t.Errorf("detail = %+v", d)
		}
		c.Number(context.Background(), linux, "greeting", 0)
		mismatches := l.named(flagnetic.EventTypeMismatch)
		if len(mismatches) != 2 {
			t.Fatalf("type_mismatch logged %d times, want once per key: %v", len(mismatches), mismatches)
		}
		if f := mismatches[0].Fields; f["flag"] != "new_checkout" || f["flag_type"] != "boolean" || f["requested_type"] != "string" {
			t.Errorf("fields = %v", f)
		}
	})

	t.Run("nil client", func(t *testing.T) {
		var nilClient *flagnetic.Client
		if got := nilClient.Bool(context.Background(), linux, "new_checkout", true); got != true {
			t.Error("a nil client serves the built-in default")
		}
		if d := nilClient.Detail(context.Background(), linux, "x", "d"); d.Reason != flagnetic.ReasonFallback || d.Stage != flagnetic.StageBuiltIn {
			t.Errorf("detail = %+v", d)
		}
		if err := nilClient.LocalOverrides().Set("x", true); err == nil {
			t.Error("Set on a nil client succeeded")
		}
	})
}

func TestEvaluatorErrorsServeTheDefault(t *testing.T) {
	c, err := flagnetic.New("", "", flagnetic.WithOffline([]byte(`{
	  "schema_version": 1, "version": 1, "scope": "server",
	  "flags": [{"key": "broken", "type": "boolean", "on": true,
	    "variations": [{"key": "on", "value": true}], "off_variation": "on", "fallthrough": "missing"}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	d := c.Detail(context.Background(), linux, "broken", false)
	if d.Value != false || d.Reason != flagnetic.ReasonFallback || d.ErrorCode != flagnetic.ErrorInconsistentRuleset {
		t.Errorf("detail = %+v", d)
	}
}

func TestEmptyRulesetIsFreshAndServesDefaults(t *testing.T) {
	c, err := flagnetic.New("", "", flagnetic.WithOffline([]byte(`{"schema_version": 1, "version": 3, "scope": "server"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stage() != flagnetic.StageFresh {
		t.Errorf("stage = %q", c.Stage())
	}
	if d := c.Detail(context.Background(), linux, "anything", true); d.Value != true || d.Reason != flagnetic.ReasonFallback {
		t.Errorf("detail = %+v", d)
	}
}

func TestRecorderCountsEvaluationsWithoutUnitIDs(t *testing.T) {
	c := newOffline(t)
	for range 3 {
		c.Bool(context.Background(), linux, "new_checkout", false)
	}
	c.Bool(context.Background(), flagnetic.Context{}, "new_checkout", false)
	c.String(context.Background(), linux, "absent", "x")

	got := c.Recorder().Summary()
	want := []flagnetic.SummaryCount{
		{Flag: "absent", Reason: flagnetic.ReasonFallback, Count: 1},
		{Flag: "new_checkout", Variation: "off", Reason: flagnetic.ReasonFallthrough, Count: 1},
		{Flag: "new_checkout", Variation: "on", Reason: flagnetic.ReasonRule, Count: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary = %+v\nwant %+v", got, want)
	}
	c.Recorder().Reset()
	if got := c.Recorder().Summary(); len(got) != 0 {
		t.Errorf("after Reset: %+v", got)
	}
}

func TestOnChangeNamesFlagsAffectedThroughSegmentsAndPrerequisites(t *testing.T) {
	// beta reads the segment testers; gate has beta as a prerequisite and
	// outer has gate as one in a condition; plain depends on nothing that
	// changes.
	ruleset := func(version int, os, attributeType string) []byte {
		return []byte(fmt.Sprintf(`{"schema_version": 1, "version": %d, "scope": "server",
		  "unit_types": ["user"], "attributes": [{"key": "os", "type": %q}, {"key": "tier", "type": "string"}],
		  "segments": [
		    {"key": "testers", "private": false, "conditions": [{"kind": "attribute", "attribute": "os", "op": "eq", "value": %q}]}],
		  "flags": [
		    {"key": "beta", "type": "boolean", "automatic_exposure": false, "on": true,
		     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
		     "off_variation": "off", "fallthrough": "off",
		     "rules": [{"id": "r1", "conditions": [{"kind": "in_segment", "segment": "testers"}],
		                "serve": {"kind": "variation", "variation": "on"}}]},
		    {"key": "gate", "type": "boolean", "automatic_exposure": false, "on": true,
		     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
		     "off_variation": "off", "fallthrough": "on", "prerequisites": [{"flag": "beta", "variation": "on"}]},
		    {"key": "outer", "type": "boolean", "automatic_exposure": false, "on": true,
		     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
		     "off_variation": "off", "fallthrough": "off",
		     "rules": [{"id": "r1", "conditions": [{"kind": "prerequisite", "flag": "gate", "variation": "on"}],
		                "serve": {"kind": "variation", "variation": "on"}}]},
		    {"key": "plain", "type": "boolean", "automatic_exposure": false, "on": true,
		     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
		     "off_variation": "off", "fallthrough": "on",
		     "rules": [{"id": "r1", "conditions": [{"kind": "attribute", "attribute": "tier", "op": "eq", "value": "gold"}],
		                "serve": {"kind": "variation", "variation": "off"}}]}]}`, version, attributeType, os))
	}
	var mu sync.Mutex
	doc := ruleset(1, "linux", "string")
	c, err := flagnetic.New("", "", flagnetic.WithOfflineSource(func() ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		return doc, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	var changed [][]string
	c.OnChange(func(keys []string) { changed = append(changed, keys) })
	reload := func(next []byte) {
		t.Helper()
		mu.Lock()
		doc = next
		mu.Unlock()
		if err := c.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	if !c.Bool(context.Background(), linux, "outer", false) {
		t.Fatal("outer is off before the segment changes")
	}

	// Only a condition of the segment changes.
	reload(ruleset(2, "macos", "string"))
	if c.Bool(context.Background(), linux, "beta", true) || c.Bool(context.Background(), linux, "outer", true) {
		t.Error("beta and outer still on after the segment changed")
	}
	// Only the declared type of an attribute the segment reads changes.
	reload(ruleset(3, "macos", "semver"))
	// Nothing changes.
	reload(ruleset(4, "macos", "semver"))
	want := [][]string{{"beta", "gate", "outer"}, {"beta", "gate", "outer"}}
	if !reflect.DeepEqual(changed, want) {
		t.Errorf("OnChange calls = %v, want %v", changed, want)
	}
}

func TestReloadRereadsTheSourceAndFiresOnChange(t *testing.T) {
	var mu sync.Mutex
	doc := []byte(document)
	c, err := flagnetic.New("", "", flagnetic.WithOfflineSource(func() ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		return doc, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	var changed [][]string
	c.OnChange(func(keys []string) { changed = append(changed, keys) })

	mu.Lock()
	doc = []byte(`{"schema_version": 1, "version": 8, "scope": "server", "flags": [
	  {"key": "greeting", "type": "string", "automatic_exposure": false, "on": true,
	   "variations": [{"key": "hello", "value": "hello"}, {"key": "hi", "value": "hi"}],
	   "off_variation": "hello", "fallthrough": "hi"},
	  {"key": "added", "type": "boolean", "on": false,
	   "variations": [{"key": "off", "value": false}], "off_variation": "off", "fallthrough": "off"}]}`)
	mu.Unlock()
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"added", "new_checkout", "page_size", "pricing", "theme"}}
	if !reflect.DeepEqual(changed, want) {
		t.Errorf("OnChange calls = %v, want %v", changed, want)
	}
	if d := c.Detail(context.Background(), linux, "greeting", ""); d.RulesetVersion != 8 {
		t.Errorf("version after reload = %d", d.RulesetVersion)
	}

	mu.Lock()
	doc = []byte(`not json`)
	mu.Unlock()
	if err := c.Reload(); err == nil {
		t.Error("Reload of a corrupt document succeeded")
	}
	if got := c.String(context.Background(), linux, "greeting", ""); got != "hi" {
		t.Errorf("a failed reload keeps the held ruleset; got %q", got)
	}
}

func TestConcurrentUse(t *testing.T) {
	var l logs
	c := newOffline(t, flagnetic.WithLocalOverrides(), flagnetic.WithLogger(l.logger()))
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 200 {
				c.Bool(context.Background(), linux, "new_checkout", false)
				c.Detail(context.Background(), linux, "pricing", "old")
				if i == 0 {
					_ = c.LocalOverrides().Set("greeting", "yo")
					c.LocalOverrides().Remove("greeting")
				}
				if i == 1 && j%50 == 0 {
					_ = c.Reload()
				}
				_ = c.LocalOverrides().List()
				_ = c.Recorder().Summary()
			}
		})
	}
	wg.Wait()
}

func TestPanickingSourceIsAnError(t *testing.T) {
	calls := 0
	c, err := flagnetic.New("", "", flagnetic.WithOfflineSource(func() ([]byte, error) {
		calls++
		if calls > 1 {
			panic("disk gone")
		}
		return []byte(document), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Reload(); err == nil {
		t.Error("Reload with a panicking source succeeded")
	}
	if got := c.Bool(context.Background(), linux, "new_checkout", false); !got {
		t.Error("the held ruleset keeps serving")
	}
}
