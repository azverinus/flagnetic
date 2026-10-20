package flagnetic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// fixturesDir is the language-agnostic conformance suite. The evaluator
// module runs it against eval directly; this runner puts every case through
// Detail on an offline client, so the SDK surface can never distort what the
// evaluator computed.
const fixturesDir = "../../conformance/fixtures"

type fixture struct {
	Name    string          `json:"name"`
	Ruleset json.RawMessage `json:"ruleset"`
	// Unsupported is the evaluator's decode report, which the evaluator's
	// own runner checks; the SDK reports it as its rule_unsupported log line.
	Unsupported json.RawMessage `json:"unsupported"`
	Cases       []struct {
		Name    string                     `json:"name"`
		Context flagnetic.Context          `json:"context"`
		Flag    string                     `json:"flag"`
		Expect  map[string]json.RawMessage `json:"expect"`
	} `json:"cases"`
}

// flagTypes reads the flag types a fixture's ruleset declares.
type flagTypes struct {
	Flags []struct {
		Key  string `json:"key"`
		Type string `json:"type"`
	} `json:"flags"`
}

// defaults are the built-in defaults the runner passes per flag type:
// values no fixture serves, so a fallback is told apart from a served value.
var defaults = map[string]any{
	"boolean": false,
	"string":  "conformance-default",
	"number":  float64(-1),
	"json":    json.RawMessage(`{"conformance":"default"}`),
}

func TestConformance(t *testing.T) {
	var files []string
	err := filepath.WalkDir(fixturesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures found")
	}
	cases := 0
	for _, path := range files {
		rel, _ := filepath.Rel(fixturesDir, path)
		t.Run(strings.TrimSuffix(filepath.ToSlash(rel), ".json"), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch filepath.Base(path) {
			case "murmur3.json":
				murmur(t, data)
				return
			case "regex.json":
				regexVectors(t, data)
				return
			}
			var f fixture
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&f); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			var types flagTypes
			if err := json.Unmarshal(f.Ruleset, &types); err != nil {
				t.Fatal(err)
			}
			typeOf := map[string]string{}
			for _, fl := range types.Flags {
				typeOf[fl.Key] = fl.Type
			}
			c := offline(t, f.Ruleset)
			for _, tc := range f.Cases {
				cases++
				t.Run(tc.Name, func(t *testing.T) {
					typ := typeOf[tc.Flag]
					if typ == "" {
						typ = "boolean"
					}
					def := defaults[typ]
					agree(t, c.Detail(t.Context(), tc.Context, tc.Flag, def), def, tc.Expect)
				})
			}
		})
	}
	if cases == 0 {
		t.Fatal("no cases")
	}
}

// murmur asserts the hash vectors against the evaluator the SDK buckets
// with: every hash, and the bucket of every salt.unit_id input.
func murmur(t *testing.T, data []byte) {
	var v struct {
		Vectors []struct {
			Input  string `json:"input"`
			Hash   uint32 `json:"hash"`
			Bucket int    `json:"bucket"`
		} `json:"vectors"`
	}
	mustUnmarshal(t, data, &v)
	if len(v.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, vec := range v.Vectors {
		if got := eval.Murmur3([]byte(vec.Input)); got != vec.Hash {
			t.Errorf("Murmur3(%q) = %d, want %d", vec.Input, got, vec.Hash)
		}
		if salt, unitID, ok := strings.Cut(vec.Input, "."); ok {
			if got := eval.Bucket(salt, unitID); got != vec.Bucket {
				t.Errorf("Bucket(%q, %q) = %d, want %d", salt, unitID, got, vec.Bucket)
			}
		}
	}
}

// offline is an offline client over a ruleset document, closed with the
// test.
func offline(t *testing.T, document []byte) *flagnetic.Client {
	t.Helper()
	c, err := flagnetic.New("", "", flagnetic.WithOffline(document))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// regexVectors puts every RE2 vector through Detail: one flag per vector,
// whose only rule matches the vector's pattern against a string attribute.
// A pattern RE2 refuses serves the fallback with inconsistent_ruleset.
func regexVectors(t *testing.T, data []byte) {
	var v struct {
		Vectors []struct {
			Pattern string `json:"pattern"`
			Input   string `json:"input"`
			Match   any    `json:"match"`
		} `json:"vectors"`
	}
	mustUnmarshal(t, data, &v)
	if len(v.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	flags := make([]string, len(v.Vectors))
	for i, vec := range v.Vectors {
		pattern, err := json.Marshal(vec.Pattern)
		if err != nil {
			t.Fatal(err)
		}
		flags[i] = fmt.Sprintf(`{"key":"v%d","type":"boolean","on":true,"off_variation":"false","fallthrough":"false",
			"variations":[{"key":"true","value":true},{"key":"false","value":false}],
			"rules":[{"id":"r1","conditions":[{"kind":"attribute","attribute":"s","op":"matches","value":%s}],
			  "serve":{"kind":"variation","variation":"true"}}]}`, i, pattern)
	}
	c := offline(t, []byte(`{"schema_version":1,"version":1,"scope":"server",
		"attributes":[{"key":"s","type":"string"}],"flags":[`+strings.Join(flags, ",")+`]}`))
	for i, vec := range v.Vectors {
		d := c.Detail(t.Context(), flagnetic.Context{Attributes: map[string]any{"s": vec.Input}}, fmt.Sprintf("v%d", i), false)
		got := d.Value
		if d.Reason == flagnetic.ReasonFallback && d.ErrorCode == flagnetic.ErrorInconsistentRuleset {
			got = "invalid"
		}
		if got != vec.Match {
			t.Errorf("vector %d: %q on %q served %v (%s), the file says %v", i, vec.Pattern, vec.Input, got, d.Reason, vec.Match)
		}
	}
}

// agree checks a detail against a fixture expectation. A served result
// must match value, variation, reason, rule_id, experiment and
// override_influenced exactly; an evaluator error must surface as the
// built-in default with reason fallback and the same error code.
// prerequisite and bucket are evaluator diagnostics a detail does not
// carry, and neither are type and automatic_exposure: a getter reads the
// flag's type from the held ruleset. Any other key fails, so a new result
// field forces a decision here.
func agree(t *testing.T, d flagnetic.Detail, def any, expect map[string]json.RawMessage) {
	t.Helper()
	for key := range expect {
		switch key {
		case "value", "variation", "reason", "rule_id", "experiment", "override_influenced", "error_code",
			"prerequisite", "bucket", "type", "automatic_exposure":
		default:
			t.Errorf("expectation key %q is unknown to the SDK runner", key)
		}
	}
	var reason string
	mustUnmarshal(t, expect["reason"], &reason)

	if reason == "error" {
		var code string
		mustUnmarshal(t, expect["error_code"], &code)
		if d.Reason != flagnetic.ReasonFallback || string(d.ErrorCode) != code {
			t.Errorf("reason %q error_code %q, want fallback with %q", d.Reason, d.ErrorCode, code)
		}
		if !reflect.DeepEqual(d.Value, def) || d.Variation != "" || d.Experiment != nil || d.Overridden {
			t.Errorf("a fallback serves only the built-in default: %+v", d)
		}
		return
	}

	if string(d.Reason) != reason {
		t.Errorf("reason = %q, want %q", d.Reason, reason)
	}
	jsonEqual(t, "value", d.Value, expect["value"])
	jsonEqual(t, "variation", d.Variation, expect["variation"])
	ruleID := json.RawMessage(`""`)
	if raw, ok := expect["rule_id"]; ok {
		ruleID = raw
	}
	jsonEqual(t, "rule_id", d.RuleID, ruleID)
	experiment := json.RawMessage(`null`)
	if raw, ok := expect["experiment"]; ok {
		experiment = raw
	}
	jsonEqual(t, "experiment", d.Experiment, experiment)
	jsonEqual(t, "override_influenced", d.Overridden, expect["override_influenced"])
	if d.Stage != flagnetic.StageFresh {
		t.Errorf("stage = %q, want fresh", d.Stage)
	}
}

func jsonEqual(t *testing.T, field string, got any, want json.RawMessage) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	mustUnmarshal(t, b, &g)
	mustUnmarshal(t, want, &w)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", field, b, want)
	}
}

func mustUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
}
