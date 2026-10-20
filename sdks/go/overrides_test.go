package flagnetic_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// Refused without the option, and refused in a protected environment, are
// scenarios 22 runs from conformance/protocol/scenarios.json
// (TestProtocolScenarios in protocol_test.go); this file keeps only the
// facets those scenarios do not cover.

func TestLocalOverridesWarnAtStart(t *testing.T) {
	var l logs
	newOffline(t, flagnetic.WithLogger(l.logger()), flagnetic.WithLocalOverrides())
	warnings := l.named(flagnetic.EventLocalOverridesAllowed)
	if len(warnings) != 1 || warnings[0].Level != slog.LevelWarn {
		t.Errorf("local_overrides_allowed = %v, want one warning", warnings)
	}
}

func TestLocalOverrideReadsLocalOverride(t *testing.T) {
	var l logs
	c := newOffline(t, flagnetic.WithLogger(l.logger()), flagnetic.WithLocalOverrides())
	var changed [][]string
	c.OnChange(func(keys []string) { changed = append(changed, keys) })

	if err := c.LocalOverrides().Set("pricing", "forced"); err != nil {
		t.Fatal(err)
	}
	d := c.Detail(context.Background(), linux, "pricing", "old")
	if d.Value != "forced" || d.Reason != flagnetic.ReasonLocalOverride || d.Experiment != nil || d.Variation != "" {
		t.Errorf("detail = %+v", d)
	}
	if d.Stage != flagnetic.StageFresh || d.RulesetVersion != 7 {
		t.Errorf("stage and version stay as they were: %+v", d)
	}
	set := l.named(flagnetic.EventOverrideSet)
	if len(set) != 1 || set[0].Fields["flag"] != "pricing" {
		t.Errorf("override_set = %v", set)
	}
	if _, logged := set[0].Fields["value"]; logged {
		t.Error("override_set never logs the substituted value")
	}

	if err := c.LocalOverrides().Set("theme", map[string]any{"background": "white"}); err != nil {
		t.Fatal(err)
	}
	if got := c.JSON(context.Background(), linux, "theme", nil); string(got) != `{"background":"white"}` {
		t.Errorf("JSON = %s", got)
	}
	if err := c.LocalOverrides().Set("page_size", 25); err != nil {
		t.Fatal(err)
	}
	if got := c.Number(context.Background(), linux, "page_size", 0); got != 25 {
		t.Errorf("Number = %v", got)
	}
	wantList := map[string]any{"pricing": "forced", "theme": json.RawMessage(`{"background":"white"}`), "page_size": float64(25)}
	if got := c.LocalOverrides().List(); !reflect.DeepEqual(got, wantList) {
		t.Errorf("List = %#v, want %#v", got, wantList)
	}

	c.LocalOverrides().Remove("pricing")
	if d := c.Detail(context.Background(), linux, "pricing", "old"); d.Reason != flagnetic.ReasonExperiment {
		t.Errorf("after Remove: %+v", d)
	}
	c.LocalOverrides().Remove("pricing") // absent: no change
	c.LocalOverrides().Clear()
	if got := c.LocalOverrides().List(); len(got) != 0 {
		t.Errorf("after Clear: %v", got)
	}
	want := [][]string{{"pricing"}, {"theme"}, {"page_size"}, {"pricing"}, {"page_size", "theme"}}
	if !reflect.DeepEqual(changed, want) {
		t.Errorf("OnChange calls = %v, want %v", changed, want)
	}
}

func TestLocalOverrideMustMatchTheFlagType(t *testing.T) {
	c := newOffline(t, flagnetic.WithLocalOverrides())
	if err := c.LocalOverrides().Set("new_checkout", "yes"); err == nil {
		t.Error("a string override of a boolean flag was accepted")
	}
	if err := c.LocalOverrides().Set("theme", func() {}); err == nil {
		t.Error("an override that is not JSON was accepted")
	}
	// A flag the ruleset lacks takes the type of its value.
	if err := c.LocalOverrides().Set("not_yet_created", true); err != nil {
		t.Fatal(err)
	}
	if d := c.Detail(context.Background(), linux, "not_yet_created", false); d.Value != true || d.Reason != flagnetic.ReasonLocalOverride {
		t.Errorf("detail = %+v", d)
	}
	if d := c.Detail(context.Background(), linux, "not_yet_created", "s"); d.Reason != flagnetic.ReasonFallback || d.ErrorCode != flagnetic.ErrorTypeMismatch {
		t.Errorf("a getter of another type falls back: %+v", d)
	}
}

func TestLocalOverridesAreCountedUnderLocalOverride(t *testing.T) {
	c := newOffline(t, flagnetic.WithLocalOverrides())
	if err := c.LocalOverrides().Set("new_checkout", false); err != nil {
		t.Fatal(err)
	}
	c.Bool(context.Background(), linux, "new_checkout", true)
	want := []flagnetic.SummaryCount{{Flag: "new_checkout", Reason: flagnetic.ReasonLocalOverride, Count: 1}}
	if got := c.Recorder().Summary(); !reflect.DeepEqual(got, want) {
		t.Errorf("summary = %+v", got)
	}
}

func TestOverridesDropWhenTheEnvironmentBecomesProtected(t *testing.T) {
	var l logs
	doc := []byte(document)
	c, err := flagnetic.New("", "", flagnetic.WithOfflineSource(func() ([]byte, error) { return doc, nil }),
		flagnetic.WithLocalOverrides(), flagnetic.WithLogger(l.logger()))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.LocalOverrides().Set("new_checkout", false); err != nil {
		t.Fatal(err)
	}
	var changed [][]string
	c.OnChange(func(keys []string) { changed = append(changed, keys) })
	doc = []byte(strings.Replace(document, `"protected": false`, `"protected": true`, 1))
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if d := c.Detail(context.Background(), linux, "new_checkout", false); d.Reason != flagnetic.ReasonRule {
		t.Errorf("a protected ruleset never serves a local override: %+v", d)
	}
	if got := c.LocalOverrides().List(); len(got) != 0 {
		t.Errorf("List = %v, want the refused overrides dropped", got)
	}
	if refused := l.named(flagnetic.EventOverrideRefused); len(refused) != 1 || refused[0].Fields["flag"] != "new_checkout" {
		t.Errorf("override_refused = %v", refused)
	}
	if !reflect.DeepEqual(changed, [][]string{{"new_checkout"}}) {
		t.Errorf("OnChange = %v", changed)
	}
}

func TestPanickingListenerDoesNotEscape(t *testing.T) {
	var l logs
	c := newOffline(t, flagnetic.WithLocalOverrides(), flagnetic.WithLogger(l.logger()))
	c.OnChange(func([]string) { panic("boom") })
	if err := c.LocalOverrides().Set("greeting", "yo"); err != nil {
		t.Fatal(err)
	}
	if len(l.named(flagnetic.EventListenerFailed)) != 1 {
		t.Error("a panicking listener is logged")
	}
}
