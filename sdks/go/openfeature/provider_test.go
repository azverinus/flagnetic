package openfeature_test

import (
	"context"
	"sync"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
	fnof "github.com/azverinus/flagnetic/sdks/go/openfeature"
)

// document is a server ruleset document exercising every mapping the
// provider owns: a rule (new_checkout), an off flag (disabled_flag), a
// fallthrough (greeting), local-override-eligible flags of every type
// (page_size, theme), a running experiment with certain assignment
// (pricing) and one opted into automatic exposure (auto_flag).
const document = `{
  "schema_version": 1, "version": 3, "scope": "server",
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
    {"key": "disabled_flag", "type": "boolean", "automatic_exposure": false, "on": false,
     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
     "off_variation": "off", "fallthrough": "on"},
    {"key": "greeting", "type": "string", "automatic_exposure": false, "on": true,
     "variations": [{"key": "hello", "value": "hello"}, {"key": "hi", "value": "hi"}],
     "off_variation": "hello", "fallthrough": "hi"},
    {"key": "page_size", "type": "number", "automatic_exposure": false, "on": true,
     "variations": [{"key": "small", "value": 10}, {"key": "large", "value": 50}],
     "off_variation": "small", "fallthrough": "small"},
    {"key": "theme", "type": "json", "automatic_exposure": false, "on": true,
     "variations": [{"key": "dark", "value": {"background": "black"}}],
     "off_variation": "dark", "fallthrough": "dark"},
    {"key": "pricing", "type": "string", "automatic_exposure": false, "on": true,
     "variations": [{"key": "control", "value": "old"}, {"key": "treatment", "value": "new"}],
     "off_variation": "control", "fallthrough": "control",
     "rules": [{"id": "exp", "conditions": [],
                "serve": {"kind": "experiment", "experiment": "pricing_test", "unit_type": "user",
                          "inclusion_salt": "in", "variation_salt": "var", "traffic_allocation": 10000,
                          "weights": [{"variation": "control", "weight": 0}, {"variation": "treatment", "weight": 10000}]}}]},
    {"key": "auto_flag", "type": "string", "automatic_exposure": true, "on": true,
     "variations": [{"key": "control", "value": "old"}, {"key": "treatment", "value": "new"}],
     "off_variation": "control", "fallthrough": "control",
     "rules": [{"id": "exp2", "conditions": [],
                "serve": {"kind": "experiment", "experiment": "auto_test", "unit_type": "user",
                          "inclusion_salt": "in2", "variation_salt": "var2", "traffic_allocation": 10000,
                          "weights": [{"variation": "control", "weight": 0}, {"variation": "treatment", "weight": 10000}]}}]}
  ]
}`

// mutableDoc is an offline source Reload can change in place, so a test can
// fire a real configuration change.
type mutableDoc struct {
	mu  sync.Mutex
	doc []byte
}

func (m *mutableDoc) read() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.doc, nil
}

func (m *mutableDoc) set(doc string) {
	m.mu.Lock()
	m.doc = []byte(doc)
	m.mu.Unlock()
}

// newProvider builds an offline provider under a domain unique to the
// subtest, registers it with the real OpenFeature SDK and returns its
// client; t.Cleanup shuts the whole API down, so tests never leak a
// registered provider into one another.
func newProvider(t *testing.T, opts ...fnof.Option) (*fnof.Provider, of.IClient) {
	t.Helper()
	opts = append([]fnof.Option{fnof.WithClientOptions(flagnetic.WithOffline([]byte(document)))}, opts...)
	provider, err := fnof.New("", "", opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	domain := t.Name()
	if err := of.SetNamedProviderAndWait(domain, provider); err != nil {
		t.Fatalf("SetNamedProviderAndWait: %v", err)
	}
	t.Cleanup(of.Shutdown)
	return provider, of.NewClient(domain)
}

func TestReadyThroughTheOpenFeatureSDK(t *testing.T) {
	_, client := newProvider(t)
	if got := client.State(); got != of.ReadyState {
		t.Errorf("State() = %q, want ready", got)
	}
}

func TestBooleanRuleMatch(t *testing.T) {
	_, client := newProvider(t)
	ctx := of.NewEvaluationContext("u-1", map[string]any{"os": "linux"})
	got, err := client.BooleanValueDetails(context.Background(), "new_checkout", false, ctx)
	if err != nil {
		t.Fatalf("BooleanValueDetails: %v", err)
	}
	if !got.Value || got.Reason != of.TargetingMatchReason || got.Variant != "on" {
		t.Errorf("got %+v, want value true, reason TARGETING_MATCH, variant on", got)
	}
	if got.FlagMetadata["flagnetic_reason"] != "rule" {
		t.Errorf("flag metadata flagnetic_reason = %v, want rule", got.FlagMetadata["flagnetic_reason"])
	}
	if got.FlagMetadata["stage"] != "fresh" {
		t.Errorf("flag metadata stage = %v, want fresh", got.FlagMetadata["stage"])
	}
}

func TestBooleanRuleNoMatchFallsThrough(t *testing.T) {
	_, client := newProvider(t)
	ctx := of.NewEvaluationContext("u-1", map[string]any{"os": "macos"})
	got, err := client.BooleanValueDetails(context.Background(), "new_checkout", false, ctx)
	if err != nil {
		t.Fatalf("BooleanValueDetails: %v", err)
	}
	if got.Value || got.Reason != of.DefaultReason || got.Variant != "off" {
		t.Errorf("got %+v, want value false, reason DEFAULT, variant off (flagnetic fallthrough)", got)
	}
}

func TestOffFlagIsDisabled(t *testing.T) {
	_, client := newProvider(t)
	got, err := client.BooleanValueDetails(context.Background(), "disabled_flag", false, of.NewEvaluationContext("u-1", nil))
	if err != nil {
		t.Fatalf("BooleanValueDetails: %v", err)
	}
	if got.Value != false || got.Reason != of.DisabledReason || got.Variant != "off" {
		t.Errorf("got %+v, want value false, reason DISABLED, variant off", got)
	}
}

func TestStringFallthrough(t *testing.T) {
	_, client := newProvider(t)
	got, err := client.StringValueDetails(context.Background(), "greeting", "def", of.NewEvaluationContext("u-1", nil))
	if err != nil {
		t.Fatalf("StringValueDetails: %v", err)
	}
	if got.Value != "hi" || got.Reason != of.DefaultReason || got.Variant != "hi" {
		t.Errorf("got %+v, want value hi, reason DEFAULT, variant hi", got)
	}
}

// gatedDocument holds a flag whose prerequisite fails and a number flag
// with a fractional value.
const gatedDocument = `{
  "schema_version": 1, "version": 1, "scope": "server",
  "flags": [
    {"key": "base", "type": "boolean", "on": false,
     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
     "off_variation": "off", "fallthrough": "on"},
    {"key": "gated", "type": "boolean", "on": true,
     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
     "off_variation": "off", "fallthrough": "on",
     "prerequisites": [{"flag": "base", "variation": "on"}]},
    {"key": "ratio", "type": "number", "on": true,
     "variations": [{"key": "v", "value": 2.5}], "off_variation": "v", "fallthrough": "v"}
  ]
}`

// TestAFailedPrerequisiteIsATargetingMatch: targeting decided the value, so
// OpenFeature reads TARGETING_MATCH, while the metadata keeps the Flagnetic
// reason.
func TestAFailedPrerequisiteIsATargetingMatch(t *testing.T) {
	_, client := newProvider(t, fnof.WithClientOptions(flagnetic.WithOffline([]byte(gatedDocument))))
	got, err := client.BooleanValueDetails(context.Background(), "gated", true, of.NewEvaluationContext("u-1", nil))
	if err != nil {
		t.Fatalf("BooleanValueDetails: %v", err)
	}
	if got.Value || got.Reason != of.TargetingMatchReason || got.Variant != "off" {
		t.Errorf("got %+v, want value false, reason TARGETING_MATCH, variant off", got)
	}
	if got.FlagMetadata["flagnetic_reason"] != "prerequisite_failed" {
		t.Errorf("flag metadata flagnetic_reason = %v, want prerequisite_failed", got.FlagMetadata["flagnetic_reason"])
	}
}

// TestIntEvaluationRefusesAFractionalNumber: the SDK's Int rule serves the
// default with TYPE_MISMATCH rather than truncating 2.5 to 2.
func TestIntEvaluationRefusesAFractionalNumber(t *testing.T) {
	_, client := newProvider(t, fnof.WithClientOptions(flagnetic.WithOffline([]byte(gatedDocument))))
	got, _ := client.IntValueDetails(context.Background(), "ratio", 7, of.NewEvaluationContext("u-1", nil))
	if got.Value != 7 || got.Reason != of.ErrorReason || got.ErrorCode != of.TypeMismatchCode {
		t.Errorf("got %+v, want the default 7 with ERROR and TYPE_MISMATCH", got)
	}
}

func TestIntEvaluationNarrowsTheNumberType(t *testing.T) {
	_, client := newProvider(t)
	got, err := client.IntValueDetails(context.Background(), "page_size", 1, of.NewEvaluationContext("u-1", nil))
	if err != nil {
		t.Fatalf("IntValueDetails: %v", err)
	}
	if got.Value != 10 {
		t.Errorf("Value = %d, want 10", got.Value)
	}
}

func TestFloatEvaluation(t *testing.T) {
	_, client := newProvider(t)
	got, err := client.FloatValueDetails(context.Background(), "page_size", 1, of.NewEvaluationContext("u-1", nil))
	if err != nil {
		t.Fatalf("FloatValueDetails: %v", err)
	}
	if got.Value != 10 {
		t.Errorf("Value = %v, want 10", got.Value)
	}
}

func TestObjectEvaluationServed(t *testing.T) {
	_, client := newProvider(t)
	got, err := client.ObjectValueDetails(context.Background(), "theme", map[string]any{}, of.NewEvaluationContext("u-1", nil))
	if err != nil {
		t.Fatalf("ObjectValueDetails: %v", err)
	}
	m, ok := got.Value.(map[string]any)
	if !ok || m["background"] != "black" {
		t.Errorf("Value = %#v, want {background: black}", got.Value)
	}
}

func TestObjectEvaluationFallbackReturnsTheExactDefault(t *testing.T) {
	_, client := newProvider(t)
	def := map[string]any{"marker": 42}
	// An ERROR resolution surfaces both as err and in the details, per the
	// OpenFeature Go SDK's own contract; this test cares about the details.
	got, _ := client.ObjectValueDetails(context.Background(), "missing_flag", def, of.NewEvaluationContext("u-1", nil))
	m, ok := got.Value.(map[string]any)
	if !ok || m["marker"] != 42 {
		t.Errorf("Value = %#v, want the exact default", got.Value)
	}
	if got.Reason != of.ErrorReason || got.ErrorCode != of.FlagNotFoundCode {
		t.Errorf("reason = %q code = %q, want ERROR/FLAG_NOT_FOUND", got.Reason, got.ErrorCode)
	}
}

func TestFlagNotFound(t *testing.T) {
	_, client := newProvider(t)
	got, _ := client.StringValueDetails(context.Background(), "nope", "def", of.NewEvaluationContext("u-1", nil))
	if got.Value != "def" || got.Reason != of.ErrorReason || got.ErrorCode != of.FlagNotFoundCode {
		t.Errorf("got %+v, want default, ERROR, FLAG_NOT_FOUND", got)
	}
}

func TestTypeMismatch(t *testing.T) {
	_, client := newProvider(t)
	got, _ := client.StringValueDetails(context.Background(), "new_checkout", "def", of.NewEvaluationContext("u-1", nil))
	if got.Value != "def" || got.Reason != of.ErrorReason || got.ErrorCode != of.TypeMismatchCode {
		t.Errorf("got %+v, want default, ERROR, TYPE_MISMATCH", got)
	}
}

func TestExperimentReasonIsSplitAndCarriesMetadata(t *testing.T) {
	_, client := newProvider(t)
	got, err := client.StringValueDetails(context.Background(), "pricing", "def", of.NewEvaluationContext("u-1", nil))
	if err != nil {
		t.Fatalf("StringValueDetails: %v", err)
	}
	if got.Value != "new" || got.Reason != of.SplitReason || got.Variant != "treatment" {
		t.Errorf("got %+v, want value new, reason SPLIT, variant treatment", got)
	}
	if got.FlagMetadata["experiment_key"] != "pricing_test" {
		t.Errorf("flag metadata experiment_key = %v, want pricing_test", got.FlagMetadata["experiment_key"])
	}
	if got.FlagMetadata["experiment_unit_id"] != "u-1" {
		t.Errorf("flag metadata experiment_unit_id = %v, want u-1", got.FlagMetadata["experiment_unit_id"])
	}
}

func TestUnitsAttributeAddsFurtherUnitTypes(t *testing.T) {
	_, client := newProvider(t, fnof.WithUnitType("account"))
	// The targeting key names the account unit, irrelevant to pricing's
	// experiment; only the units attribute adds the user unit it buckets on.
	ctx := of.NewEvaluationContext("acct-1", map[string]any{"units": map[string]any{"user": "u-1"}})
	got, err := client.StringValueDetails(context.Background(), "pricing", "def", ctx)
	if err != nil {
		t.Fatalf("StringValueDetails: %v", err)
	}
	if got.Value != "new" || got.FlagMetadata["experiment_unit_id"] != "u-1" {
		t.Errorf("got %+v, want the experiment to bucket on the units attribute's user id", got)
	}
}

func TestLocalOverrideIsStatic(t *testing.T) {
	provider, client := newProvider(t, fnof.WithClientOptions(flagnetic.WithLocalOverrides()))
	if err := provider.Client().LocalOverrides().Set("greeting", "overridden"); err != nil {
		t.Fatalf("LocalOverrides.Set: %v", err)
	}
	got, err := client.StringValueDetails(context.Background(), "greeting", "def", of.NewEvaluationContext("u-1", nil))
	if err != nil {
		t.Fatalf("StringValueDetails: %v", err)
	}
	if got.Value != "overridden" || got.Reason != of.StaticReason {
		t.Errorf("got %+v, want value overridden, reason STATIC", got)
	}
	if got.FlagMetadata["overridden"] != false {
		t.Errorf("flag metadata overridden = %v, want false (a local override is not a platform override)", got.FlagMetadata["overridden"])
	}
}

func TestExposureTrack(t *testing.T) {
	provider, client := newProvider(t)
	ctx := of.NewEvaluationContext("u-1", nil)
	if _, err := client.StringValueDetails(context.Background(), "pricing", "def", ctx); err != nil {
		t.Fatalf("StringValueDetails: %v", err)
	}
	client.Track(context.Background(), "exposure", ctx, of.NewTrackingEventDetails(0).Add("flag", "pricing"))

	exposures := provider.Client().Recorder().Exposures()
	if len(exposures) != 1 {
		t.Fatalf("exposures recorded = %d, want 1", len(exposures))
	}
	if exposures[0].ExperimentKey != "pricing_test" || exposures[0].VariationKey != "treatment" || exposures[0].UnitID != "u-1" {
		t.Errorf("exposure = %+v", exposures[0])
	}
}

func TestExposureTrackWithoutAnExperimentSendsNothing(t *testing.T) {
	provider, client := newProvider(t)
	ctx := of.NewEvaluationContext("u-1", nil)
	if _, err := client.StringValueDetails(context.Background(), "greeting", "def", ctx); err != nil {
		t.Fatalf("StringValueDetails: %v", err)
	}
	client.Track(context.Background(), "exposure", ctx, of.NewTrackingEventDetails(0).Add("flag", "greeting"))

	if got := provider.Client().Recorder().Exposures(); len(got) != 0 {
		t.Errorf("exposures recorded = %d, want 0: evaluating greeting never carries an experiment", len(got))
	}
}

func TestAutomaticExposureNeedsNoTrackCall(t *testing.T) {
	provider, client := newProvider(t)
	ctx := of.NewEvaluationContext("u-1", nil)
	if _, err := client.StringValueDetails(context.Background(), "auto_flag", "def", ctx); err != nil {
		t.Fatalf("StringValueDetails: %v", err)
	}
	// Evaluation itself never records an exposure unless the flag opts in;
	// auto_flag does, so exactly one is recorded without Track.
	exposures := provider.Client().Recorder().Exposures()
	if len(exposures) != 1 || exposures[0].ExperimentKey != "auto_test" {
		t.Fatalf("exposures = %+v, want exactly one for auto_test", exposures)
	}
}

func TestOrdinaryEvaluationNeverRecordsAnExposure(t *testing.T) {
	provider, client := newProvider(t)
	ctx := of.NewEvaluationContext("u-1", nil)
	for i := 0; i < 3; i++ {
		if _, err := client.BooleanValueDetails(context.Background(), "new_checkout", false, ctx); err != nil {
			t.Fatalf("BooleanValueDetails: %v", err)
		}
	}
	if got := provider.Client().Recorder().Exposures(); len(got) != 0 {
		t.Errorf("exposures recorded = %d, want 0: evaluation alone must never expose", len(got))
	}
}

func TestAliasTrack(t *testing.T) {
	provider, client := newProvider(t)
	client.Track(context.Background(), "alias", of.EvaluationContext{}, of.NewTrackingEventDetails(0).
		Add("fromUnitType", "anonymous").Add("fromUnitId", "anon-1").
		Add("toUnitType", "user").Add("toUnitId", "u-1"))

	aliases := provider.Client().Recorder().Aliases()
	if len(aliases) != 1 {
		t.Fatalf("aliases recorded = %d, want 1", len(aliases))
	}
	a := aliases[0]
	if a.FromUnitType != "anonymous" || a.FromUnitID != "anon-1" || a.ToUnitType != "user" || a.ToUnitID != "u-1" {
		t.Errorf("alias = %+v", a)
	}
}

func TestConfigurationChangedEvent(t *testing.T) {
	md := &mutableDoc{doc: []byte(document)}
	provider, err := fnof.New("", "", fnof.WithClientOptions(flagnetic.WithOfflineSource(md.read)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	domain := t.Name()
	if err := of.SetNamedProviderAndWait(domain, provider); err != nil {
		t.Fatalf("SetNamedProviderAndWait: %v", err)
	}
	t.Cleanup(of.Shutdown)
	client := of.NewClient(domain)

	events := make(chan of.EventDetails, 4)
	handler := func(d of.EventDetails) { events <- d }
	client.AddHandler(of.ProviderConfigChange, &handler)

	changed := `{"schema_version": 1, "version": 4, "scope": "server",
	  "environment": {"key": "staging", "protected": false}, "unit_types": ["user"], "attributes": [], "segments": [],
	  "flags": [{"key": "greeting", "type": "string", "automatic_exposure": false, "on": true,
	    "variations": [{"key": "hello", "value": "hello"}, {"key": "hi", "value": "hi"}],
	    "off_variation": "hello", "fallthrough": "hello"}]}`
	md.set(changed)
	if err := provider.Client().Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	select {
	case d := <-events:
		found := false
		for _, k := range d.FlagChanges {
			if k == "greeting" {
				found = true
			}
		}
		if !found {
			t.Errorf("FlagChanges = %v, want it to include greeting", d.FlagChanges)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no CONFIGURATION_CHANGED event within 2s")
	}
}
