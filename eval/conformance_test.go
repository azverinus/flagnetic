package eval_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/eval"
)

// fixturesDir is the language-agnostic conformance suite shared by every
// evaluator implementation, in Go and in every SDK.
const fixturesDir = "../conformance/fixtures"

type murmurVectors struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Vectors []struct {
		Input  string `json:"input"`
		Hash   uint32 `json:"hash"`
		Bucket int    `json:"bucket"`
	} `json:"vectors"`
}

type fixture struct {
	Name    string          `json:"name"`
	Ruleset json.RawMessage `json:"ruleset"`
	// Unsupported is the decode report the ruleset must produce, by flag
	// key; a fixture without it expects an empty report.
	Unsupported map[string][]eval.Construct `json:"unsupported"`
	Cases       []struct {
		Name    string          `json:"name"`
		Context eval.Context    `json:"context"`
		Flag    string          `json:"flag"`
		Expect  json.RawMessage `json:"expect"`
	} `json:"cases"`
}

// TestConformance runs every fixture file under conformance/fixtures.
// murmur3.json holds hash vectors and regex.json RE2 vectors; every other
// file is a ruleset with cases, each compared exactly against the
// evaluation result.
func TestConformance(t *testing.T) {
	files := fixtureFiles(t)
	if len(files) == 0 {
		t.Fatal("no fixtures found")
	}
	for _, path := range files {
		rel, _ := filepath.Rel(fixturesDir, path)
		t.Run(strings.TrimSuffix(filepath.ToSlash(rel), ".json"), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch filepath.Base(path) {
			case "murmur3.json":
				runMurmur(t, data)
				return
			case regexFile:
				runRegex(t, data)
				return
			}
			runFixture(t, rel, data)
		})
	}
}

func fixtureFiles(t *testing.T) []string {
	t.Helper()
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
	return files
}

func runMurmur(t *testing.T, data []byte) {
	var v murmurVectors
	decodeStrict(t, data, &v)
	if len(v.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, vec := range v.Vectors {
		if got := eval.Murmur3([]byte(vec.Input)); got != vec.Hash {
			t.Errorf("Murmur3(%q) = %d, want %d", vec.Input, got, vec.Hash)
		}
		if int(vec.Hash%eval.Buckets) != vec.Bucket {
			t.Errorf("vector %q: bucket %d is not hash mod %d", vec.Input, vec.Bucket, eval.Buckets)
		}
		salt, unitID, ok := strings.Cut(vec.Input, ".")
		if !ok {
			continue
		}
		if got := eval.Bucket(salt, unitID); got != vec.Bucket {
			t.Errorf("Bucket(%q, %q) = %d, want %d", salt, unitID, got, vec.Bucket)
		}
	}
}

func runFixture(t *testing.T, rel string, data []byte) {
	var f fixture
	decodeStrict(t, data, &f)
	if want := strings.TrimSuffix(filepath.ToSlash(rel), ".json"); f.Name != want {
		t.Errorf("fixture name %q does not match its path %q", f.Name, want)
	}
	rs, err := eval.Decode(f.Ruleset)
	if err != nil {
		t.Fatalf("decode ruleset: %v", err)
	}
	if got := rs.UnsupportedRules(); (len(got) != 0 || len(f.Unsupported) != 0) && !reflect.DeepEqual(got, f.Unsupported) {
		t.Errorf("unsupported rules\n got:  %s\n want: %s", mustJSON(got), mustJSON(f.Unsupported))
	}
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := rs.Evaluate(c.Context, c.Flag)
			assertJSONEqual(t, got, c.Expect)

			// Evaluate-all memoizes prerequisites within one call; it must
			// agree with evaluating the flag on its own.
			if all, ok := rs.EvaluateAll(c.Context)[c.Flag]; ok && !reflect.DeepEqual(all, got) {
				t.Errorf("EvaluateAll disagrees with Evaluate:\n all: %s\n one: %s", mustJSON(all), mustJSON(got))
			}
		})
	}
}

func assertJSONEqual(t *testing.T, got eval.Result, expect json.RawMessage) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(mustJSON(got), &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(expect, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("result mismatch\n got:  %s\n want: %s", mustJSON(got), compact(expect))
	}
}

func decodeStrict(t *testing.T, data []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func compact(b []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return string(b)
	}
	return buf.String()
}

// TestAllocationGrowthFixtureHoldsTheInvariant reads
// experiments/allocation-growth and checks the invariant its expectations
// exist to pin: a unit included at one traffic allocation is included, with
// the same variation, at every higher one. The conformance run would accept
// a fixture encoding the opposite, so the fixture is checked here too.
func TestAllocationGrowthFixtureHoldsTheInvariant(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixturesDir, "experiments", "allocation-growth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	decodeStrict(t, data, &f)
	var rs eval.Ruleset
	if err := json.Unmarshal(f.Ruleset, &rs); err != nil {
		t.Fatal(err)
	}
	// By flag key, the traffic allocation of the experiment it serves.
	allocationOf := map[string]int{}
	for _, flag := range rs.Flags {
		for _, rule := range flag.Rules {
			if rule.Serve.Kind == eval.ServeExperiment {
				allocationOf[flag.Key] = rule.Serve.TrafficAllocation
			}
		}
	}

	type outcome struct{ reason, variation string }
	// By unit ID, the outcome at each traffic allocation of the fixture.
	units := map[string]map[int]outcome{}
	var allocations []int
	for _, c := range f.Cases {
		allocation, ok := allocationOf[c.Flag]
		if !ok {
			t.Fatalf("case %q: flag %q serves no experiment", c.Name, c.Flag)
		}
		var expect struct {
			Reason    string `json:"reason"`
			Variation string `json:"variation"`
		}
		if err := json.Unmarshal(c.Expect, &expect); err != nil {
			t.Fatal(err)
		}
		unitID := c.Context.Units["anonymous"]
		if units[unitID] == nil {
			units[unitID] = map[int]outcome{}
		}
		units[unitID][allocation] = outcome{expect.Reason, expect.Variation}
		if !slices.Contains(allocations, allocation) {
			allocations = append(allocations, allocation)
		}
	}
	slices.Sort(allocations)
	if len(allocations) < 2 || len(units) < 2 {
		t.Fatalf("the fixture pins nothing: %d allocations, %d units", len(allocations), len(units))
	}

	for unitID, at := range units {
		for i, lower := range allocations {
			if at[lower].reason != string(eval.ReasonExperiment) {
				continue
			}
			for _, higher := range allocations[i+1:] {
				if got := at[higher]; got != at[lower] {
					t.Errorf("%s: included at %d as %+v, but %+v at %d", unitID, lower, at[lower], got, higher)
				}
			}
		}
	}
}
