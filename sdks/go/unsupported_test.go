package flagnetic_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// futureDocument is a ruleset at version v from a server newer than this
// SDK: smart_banner holds an operator and a serve kind the evaluator does
// not know, greeting is an ordinary flag.
func futureDocument(v int64) string {
	return fmt.Sprintf(`{
  "schema_version": 1, "version": %d, "scope": "server",
  "unit_types": ["user"],
  "attributes": [{"key": "plan", "type": "string"}],
  "flags": [
    {"key": "smart_banner", "type": "boolean", "on": true,
     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
     "off_variation": "off", "fallthrough": "off",
     "rules": [
       {"id": "r1", "conditions": [{"kind": "attribute", "attribute": "plan", "op": "glob_matches", "value": "pro*"}],
        "serve": {"kind": "variation", "variation": "on"}},
       {"id": "r2", "conditions": [], "serve": {"kind": "multi_armed_bandit", "unit_type": "user"}}]},
    {"key": "greeting", "type": "string", "on": true,
     "variations": [{"key": "hello", "value": "Hello"}], "off_variation": "hello", "fallthrough": "hello"}
  ]
}`, v)
}

func future(v int64) response {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, v))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(futureDocument(v)))
	}
}

// TestAnUnsupportedRuleIsLoggedOncePerFlagAndRulesetVersion: a flag holding
// an unsupported rule serves its fallback with inconsistent_ruleset, every
// other flag serves normally, and rule_unsupported names the flag and its
// constructs once per ruleset version, however often the flag is evaluated
// and however often the same version arrives.
func TestAnUnsupportedRuleIsLoggedOncePerFlagAndRulesetVersion(t *testing.T) {
	var l logs
	srv := newMockServer(t, future(7), future(7), future(8))
	clk := newSleeper()
	c := newOnline(t, srv.URL, clk.option(), flagnetic.WithLogger(l.logger()))
	fc := flagnetic.Context{Units: map[string]string{"user": "u-1"}, Attributes: map[string]any{"plan": "pro"}}

	clk.next(t).wake() // version 7 is applied; the next fetch delivers it again
	for range 50 {
		d := c.Detail(t.Context(), fc, "smart_banner", false)
		if d.Reason != flagnetic.ReasonFallback || d.ErrorCode != flagnetic.ErrorInconsistentRuleset || d.Value != false {
			t.Fatalf("smart_banner = %+v, want the default with inconsistent_ruleset", d)
		}
	}
	if got := c.String(t.Context(), fc, "greeting", "built-in"); got != "Hello" {
		t.Errorf("greeting = %q, want Hello beside the unsupported flag", got)
	}
	again := clk.next(t) // the second delivery of version 7 is applied
	logged := l.named(flagnetic.EventRuleUnsupported)
	if len(logged) != 1 {
		t.Fatalf("rule_unsupported logged %d times for one version, want once: %+v", len(logged), logged)
	}
	want := map[string]any{
		"flag":            "smart_banner",
		"constructs":      []string{"operator glob_matches", "serve_kind multi_armed_bandit"},
		"ruleset_version": int64(7),
	}
	if !reflect.DeepEqual(logged[0].Fields, want) {
		t.Errorf("rule_unsupported fields = %#v, want %#v", logged[0].Fields, want)
	}

	again.wake()
	clk.next(t) // version 8, still holding it, is applied
	logged = l.named(flagnetic.EventRuleUnsupported)
	if len(logged) != 2 || logged[1].Fields["ruleset_version"] != int64(8) {
		t.Errorf("after version 8 rule_unsupported = %+v, want a second line for version 8", logged)
	}
}

// TestAnOfflineRulesetReportsUnsupportedRulesToo: the report comes from
// decoding, so an offline document holding an unsupported rule logs it at
// construction, and a reload of the same version does not log it again.
func TestAnOfflineRulesetReportsUnsupportedRulesToo(t *testing.T) {
	var l logs
	c, err := flagnetic.New("", "", flagnetic.WithOffline([]byte(futureDocument(3))), flagnetic.WithLogger(l.logger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := len(l.named(flagnetic.EventRuleUnsupported)); got != 1 {
		t.Errorf("rule_unsupported logged %d times, want once", got)
	}
}
