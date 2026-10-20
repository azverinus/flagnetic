package eval_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/azverinus/flagnetic/eval"
)

var updateRegex = flag.Bool("update-regex", false, "rewrite conformance/fixtures/regex.json from Go regexp")

const regexFile = "regex.json"

type regexVectors struct {
	Name    string        `json:"name"`
	Source  string        `json:"source"`
	Vectors []regexVector `json:"vectors"`
}

type regexVector struct {
	Pattern string `json:"pattern"`
	Input   string `json:"input"`
	// Match is true, false, or "invalid" for a pattern RE2 refuses.
	Match any `json:"match"`
}

// oracle is what Go regexp, which is RE2, says of a vector: whether the
// pattern matches anywhere in the input, or "invalid".
func oracle(v regexVector) any {
	re, err := regexp.Compile(v.Pattern)
	if err != nil {
		return "invalid"
	}
	return re.MatchString(v.Input)
}

// runRegex checks the RE2 vectors twice: Go regexp must agree with every
// expectation, and so must the evaluator's matches operator, so a vector
// that Go regexp and the file disagree on can never stay in the suite.
func runRegex(t *testing.T, data []byte) {
	var f regexVectors
	decodeStrict(t, data, &f)
	if f.Name != "regex" || len(f.Vectors) == 0 {
		t.Fatalf("regex vectors: name %q, %d vectors", f.Name, len(f.Vectors))
	}
	for i, v := range f.Vectors {
		switch v.Match {
		case true, false, "invalid":
		default:
			t.Fatalf("vector %d: match %v is not true, false or \"invalid\"", i, v.Match)
		}
		if got := oracle(v); got != v.Match {
			t.Errorf("vector %d: Go regexp says %v for %q on %q, the file says %v", i, got, v.Pattern, v.Input, v.Match)
		}
		if got := evaluateMatches(t, v); got != v.Match {
			t.Errorf("vector %d: the evaluator says %v for %q on %q, the file says %v", i, got, v.Pattern, v.Input, v.Match)
		}
	}
}

// evaluateMatches evaluates a matches condition with the vector's pattern
// against its input, through Decode and Evaluate.
func evaluateMatches(t *testing.T, v regexVector) any {
	t.Helper()
	pattern, err := json.Marshal(v.Pattern)
	if err != nil {
		t.Fatal(err)
	}
	rs := decode(t, fmt.Appendf(nil, `{"schema_version":1,"version":1,"scope":"server",
		"attributes":[{"key":"s","type":"string"}],
		"flags":[{"key":"f","type":"boolean","on":true,"off_variation":"false","fallthrough":"false",
		  "variations":[{"key":"true","value":true},{"key":"false","value":false}],
		  "rules":[{"id":"r1","conditions":[{"kind":"attribute","attribute":"s","op":"matches","value":%s}],
		    "serve":{"kind":"variation","variation":"true"}}]}]}`, pattern))
	switch r := rs.Evaluate(eval.Context{Attributes: map[string]any{"s": v.Input}}, "f"); {
	case r.Reason == eval.ReasonRule:
		return true
	case r.Reason == eval.ReasonFallthrough:
		return false
	case r.ErrorCode == eval.ErrorInconsistentRuleset:
		return "invalid"
	default:
		t.Fatalf("vector %q on %q: unexpected result %+v", v.Pattern, v.Input, r)
		return nil
	}
}

// TestRegexVectorsUpdate rewrites the RE2 vectors from Go regexp when run
// with -update-regex: the patterns and inputs stay, every expectation is
// recomputed, and the file keeps one vector per line.
func TestRegexVectorsUpdate(t *testing.T) {
	if !*updateRegex {
		t.Skip("run with -update-regex to rewrite the RE2 vectors")
	}
	path := filepath.Join(fixturesDir, regexFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f regexVectors
	decodeStrict(t, data, &f)
	var out bytes.Buffer
	fmt.Fprintf(&out, "{\n  \"name\": \"regex\",\n  \"source\": %s,\n  \"vectors\": [\n", quote(f.Source))
	for i, v := range f.Vectors {
		match, err := json.Marshal(oracle(v))
		if err != nil {
			t.Fatal(err)
		}
		sep := ","
		if i == len(f.Vectors)-1 {
			sep = ""
		}
		fmt.Fprintf(&out, "    {\"pattern\": %s, \"input\": %s, \"match\": %s}%s\n", quote(v.Pattern), quote(v.Input), match, sep)
	}
	out.WriteString("  ]\n}\n")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// quote encodes s as JSON without escaping <, > and &, so the vectors read
// as the patterns they are.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		panic(err)
	}
	return string(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
}
