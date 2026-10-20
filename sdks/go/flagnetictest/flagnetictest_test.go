package flagnetictest_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
	"github.com/azverinus/flagnetic/sdks/go/flagnetictest"
)

const document = `{
  "schema_version": 1, "version": 4, "scope": "server",
  "unit_types": ["user"],
  "flags": [
    {"key": "new_checkout", "type": "boolean", "automatic_exposure": false, "on": false,
     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
     "off_variation": "off", "fallthrough": "on"},
    {"key": "greeting", "type": "string", "automatic_exposure": false, "on": true,
     "variations": [{"key": "hello", "value": "hello"}], "off_variation": "hello", "fallthrough": "hello"}
  ]
}`

var user = flagnetic.Context{Units: map[string]string{"user": "u-1"}}

func TestNewBuildsAnOfflineClientFromADocument(t *testing.T) {
	c := flagnetictest.New(t, []byte(document))
	if c.Stage() != flagnetic.StageFresh {
		t.Errorf("stage = %q", c.Stage())
	}
	if d := c.Detail(context.Background(), user, "new_checkout", true); d.Value != false || d.Reason != flagnetic.ReasonOff {
		t.Errorf("detail = %+v", d)
	}
	c.Bool(context.Background(), user, "new_checkout", true)
	if got := c.Recorder().Summary(); len(got) != 1 || got[0].Count != 2 {
		t.Errorf("summary = %+v", got)
	}
}

func TestFromValuesServesEachValueAsFallthrough(t *testing.T) {
	c := flagnetictest.FromValues(t, map[string]any{
		"new_checkout": true,
		"greeting":     "hi",
		"page_size":    25,
		"theme":        map[string]any{"background": "black"},
	})
	if d := c.Detail(context.Background(), user, "new_checkout", false); d.Value != true || d.Reason != flagnetic.ReasonFallthrough {
		t.Errorf("detail = %+v", d)
	}
	if got := c.String(context.Background(), user, "greeting", ""); got != "hi" {
		t.Errorf("String = %q", got)
	}
	if got := c.Number(context.Background(), user, "page_size", 0); got != 25 {
		t.Errorf("Number = %v", got)
	}
	if got := c.JSON(context.Background(), user, "theme", nil); string(got) != `{"background":"black"}` {
		t.Errorf("JSON = %s", got)
	}
	if got := c.Bool(context.Background(), user, "greeting", false); got != false {
		t.Error("the value decides the flag type; a boolean getter on a string falls back")
	}
}

func TestSetRewritesAFlagToOneVariation(t *testing.T) {
	c := flagnetictest.New(t, []byte(document))
	var changed [][]string
	c.OnChange(func(keys []string) { changed = append(changed, keys) })

	c.Set("new_checkout", true)
	if d := c.Detail(context.Background(), user, "new_checkout", false); d.Value != true || d.Reason != flagnetic.ReasonFallthrough {
		t.Errorf("detail = %+v", d)
	}
	c.Set("brand_new", 3.5)
	if got := c.Number(context.Background(), user, "brand_new", 0); got != 3.5 {
		t.Errorf("Number = %v", got)
	}
	if got := c.String(context.Background(), user, "greeting", ""); got != "hello" {
		t.Errorf("other flags keep their configuration: %q", got)
	}
	want := [][]string{{"new_checkout"}, {"brand_new"}}
	if !reflect.DeepEqual(changed, want) {
		t.Errorf("OnChange = %v, want %v", changed, want)
	}
}

func TestReloadRereadsTheSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flags.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	c := flagnetictest.FromFile(t, path)
	c.Set("greeting", "set by the test")

	var doc map[string]any
	if err := json.Unmarshal([]byte(document), &doc); err != nil {
		t.Fatal(err)
	}
	doc["version"] = 5
	updated, _ := json.Marshal(doc)
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	c.Reload()
	d := c.Detail(context.Background(), user, "greeting", "")
	if d.Value != "hello" || d.RulesetVersion != 5 {
		t.Errorf("after Reload: %+v", d)
	}
}

func TestLocalOverridesAreAvailable(t *testing.T) {
	c := flagnetictest.FromValues(t, map[string]any{"greeting": "hi"}, flagnetic.WithLocalOverrides())
	if err := c.LocalOverrides().Set("greeting", "qa"); err != nil {
		t.Fatal(err)
	}
	if d := c.Detail(context.Background(), user, "greeting", ""); d.Reason != flagnetic.ReasonLocalOverride {
		t.Errorf("detail = %+v", d)
	}
}

func TestSetKeepsAJSONFlagJSON(t *testing.T) {
	c := flagnetictest.FromValues(t, map[string]any{"theme": map[string]any{"a": 1}})
	c.Set("theme", "plain string is valid JSON")
	if got := c.JSON(context.Background(), user, "theme", nil); string(got) != `"plain string is valid JSON"` {
		t.Errorf("JSON = %s", got)
	}
}

// experimentDocument is a ruleset with one running experiment everyone is
// included in, so that a test can assert the exposure a handler left.
const experimentDocument = `{
  "schema_version": 1, "version": 5, "scope": "server",
  "unit_types": ["user", "anonymous"],
  "flags": [
    {"key": "pricing", "type": "string", "automatic_exposure": false, "on": true,
     "variations": [{"key": "control", "value": "old"}, {"key": "treatment", "value": "new"}],
     "off_variation": "control", "fallthrough": "control",
     "rules": [{"id": "exp", "conditions": [],
                "serve": {"kind": "experiment", "experiment": "pricing_test", "unit_type": "user",
                          "inclusion_salt": "in", "variation_salt": "var", "traffic_allocation": 10000,
                          "weights": [{"variation": "control", "weight": 0}, {"variation": "treatment", "weight": 10000}]}}]}
  ]
}`

func TestAssertExposureFindsTheOneExposureOfAFlag(t *testing.T) {
	c := flagnetictest.New(t, []byte(experimentDocument))
	ctx := flagnetic.Context{Units: map[string]string{"user": "u-1"}}

	// The handler under test evaluates and reports the exposure where the
	// variation changes what the unit sees, twice in one request.
	for range 2 {
		c.Detail(context.Background(), ctx, "pricing", "built-in")
		c.Exposure(context.Background(), ctx, "pricing")
	}

	e := c.AssertExposure("pricing")
	if e.ExperimentKey != "pricing_test" || e.VariationKey != "treatment" || e.UnitID != "u-1" {
		t.Errorf("exposure = %+v, want the treatment of pricing_test for u-1", e)
	}
	if got := c.Recorder().Aliases(); len(got) != 0 {
		t.Errorf("%d aliases recorded, want none", len(got))
	}
	c.Alias(context.Background(), "anonymous", "d-1", "user", "u-1")
	if got := c.Recorder().Aliases(); len(got) != 1 {
		t.Errorf("%d aliases recorded after the call, want 1", len(got))
	}
}
