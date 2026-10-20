package eval_test

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/azverinus/flagnetic/eval"
)

// TestBucketUniformity: buckets of real-shaped identifiers are uniform over
// 10000 buckets by a chi-squared test on one million identifiers per shape,
// since a biased bucket would bias every rollout and experiment built on it.
func TestBucketUniformity(t *testing.T) {
	if testing.Short() {
		t.Skip("hashes four million identifiers")
	}
	const n = 1_000_000
	shapes := map[string]func(r *rand.Rand, i int) string{
		// Sequential integers written in base62, so neighbouring IDs
		// share most characters.
		"base62":   base62Sequential,
		"uuid_v4":  func(r *rand.Rand, _ int) string { return uuid(r, 4, 0) },
		"uuid_v7":  func(r *rand.Rand, i int) string { return uuid(r, 7, 1_758_000_000_000+int64(i)) },
		"integers": func(_ *rand.Rand, i int) string { return strconv.Itoa(i + 1) },
	}
	// Upper critical value of chi-squared with 9999 degrees of freedom at
	// p = 0.001, by the Wilson–Hilferty approximation.
	const df = eval.Buckets - 1
	const z = 3.090232
	critical := df * math.Pow(1-2.0/(9*df)+z*math.Sqrt(2.0/(9*df)), 3)

	for name, gen := range shapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := rand.New(rand.NewPCG(42, 7))
			var counts [eval.Buckets]int
			for i := range n {
				counts[eval.Bucket("k3f9s0d2m1x8q7wz", gen(r, i))]++
			}
			expected := float64(n) / eval.Buckets
			var chi2 float64
			for _, c := range counts {
				d := float64(c) - expected
				chi2 += d * d / expected
			}
			t.Logf("chi-squared %.1f, critical %.1f", chi2, critical)
			if chi2 > critical {
				t.Errorf("chi-squared %.1f exceeds %.1f (df %d, p 0.001): buckets are not uniform", chi2, critical, df)
			}
		})
	}
}

func base62Sequential(_ *rand.Rand, i int) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 8)
	n := i + 1
	for p := len(buf) - 1; p >= 0; p-- {
		buf[p] = alphabet[n%len(alphabet)]
		n /= len(alphabet)
	}
	return string(buf)
}

func uuid(r *rand.Rand, version int, unixMilli int64) string {
	var b [16]byte
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	if version == 7 {
		for i := range 6 {
			b[i] = byte(unixMilli >> (8 * (5 - i)))
		}
	}
	b[6] = b[6]&0x0f | byte(version<<4)
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// TestRolloutMonotonicity: growing a rollout's percentage never moves a
// unit that was already in, over random salts, percentages and units.
func TestRolloutMonotonicity(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for range 200 {
		salt := randomSalt(r)
		low := r.IntN(eval.Buckets + 1)
		high := low + r.IntN(eval.Buckets+1-low)
		rs := decode(t, rolloutRuleset(salt, low, high))
		for range 200 {
			ctx := eval.Context{Units: map[string]string{"user": uuid(r, 4, 0)}}
			before, after := rs.Evaluate(ctx, "low"), rs.Evaluate(ctx, "high")
			if before.Reason != eval.ReasonRollout {
				continue
			}
			if after.Reason != eval.ReasonRollout || *after.Bucket != *before.Bucket {
				t.Fatalf("salt %s: unit %s inside at %d moved at %d: %+v", salt, ctx.Units["user"], low, high, after)
			}
		}
	}
}

func randomSalt(r *rand.Rand) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 16)
	for i := range b {
		b[i] = chars[r.IntN(len(chars))]
	}
	return string(b)
}

func rolloutRuleset(salt string, low, high int) []byte {
	flag := func(key string, p int) map[string]any {
		return map[string]any{
			"key": key, "type": "boolean", "on": true,
			"variations":    []any{map[string]any{"key": "true", "value": true}, map[string]any{"key": "false", "value": false}},
			"off_variation": "false", "fallthrough": "false",
			"rules": []any{map[string]any{"id": "r1", "conditions": []any{}, "serve": map[string]any{
				"kind": "rollout", "unit_type": "user", "variation": "true", "percentage": p, "salt": salt,
			}}},
		}
	}
	return mustJSON(map[string]any{
		"schema_version": 1, "version": 1, "scope": "server", "unit_types": []string{"user"},
		"flags": []any{flag("low", low), flag("high", high)},
	})
}

func decode(t testing.TB, doc []byte) *eval.Ruleset {
	t.Helper()
	rs, err := eval.Decode(doc)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// FuzzParseSemver: parsing never panics, and precedence is a total order
// that ignores a leading v and build metadata.
func FuzzParseSemver(f *testing.F) {
	for _, s := range []string{"1.17.0", "v1.17", "1", "1.0.0-alpha.1", "1.0.0-rc.1+build.7", "1..2", "1.2.3-", "", "v", "1.05", "1.0.0-01"} {
		f.Add(s, "1.17.0-beta.2")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		va, okA := eval.ParseSemver(a)
		vb, okB := eval.ParseSemver(b)
		if okA {
			if va.Compare(va) != 0 {
				t.Fatalf("%q is not equal to itself", a)
			}
			if w, ok := eval.ParseSemver("v" + a); ok && a != "" && a[0] != 'v' && w.Compare(va) != 0 {
				t.Fatalf("v%q differs from %q", a, a)
			}
			if w, ok := eval.ParseSemver(a + "+b.1"); ok && w.Compare(va) != 0 {
				t.Fatalf("build metadata changed precedence of %q", a)
			}
		}
		if okA && okB && va.Compare(vb) != -vb.Compare(va) {
			t.Fatalf("Compare(%q, %q) is not antisymmetric", a, b)
		}
	})
}

// FuzzConditionDecoding: any condition, however malformed, decodes and
// evaluates without panicking to a rule match, the fallthrough or an
// inconsistent_ruleset error.
func FuzzConditionDecoding(f *testing.F) {
	for _, c := range []string{
		`{"kind":"attribute","attribute":"s","op":"eq","value":"x"}`,
		`{"kind":"attribute","attribute":"n","op":"in","value":[1,"2"]}`,
		`{"kind":"attribute","attribute":"v","op":"lt","value":"1.17"}`,
		`{"kind":"attribute","attribute":"d","op":"before","value":"2026-01-01T00:00:00Z"}`,
		`{"kind":"attribute","attribute":"l","op":"contains_all","value":["a"]}`,
		`{"kind":"attribute","attribute":"s","op":"matches","value":"(?=x)"}`,
		`{"kind":"attribute","attribute":"user","op":"in","value":["u1"]}`,
		`{"kind":"in_segment","segment":"seg"}`,
		`{"kind":"prerequisite","flag":"f","variation":"true"}`,
		`{"kind":"nope"}`,
		`{"kind":"attribute","attribute":"b","op":"eq","value":null}`,
	} {
		f.Add([]byte(c))
	}
	contexts := []eval.Context{
		{},
		{Units: map[string]string{"user": "u1"}, Attributes: map[string]any{
			"s": "x", "n": 2.0, "b": true, "v": "1.16.0", "d": "2025-01-01T00:00:00Z", "l": []any{"a"},
		}},
		{Attributes: map[string]any{"s": 1.0, "n": "2", "b": "true", "v": 1.0, "d": 1.5, "l": []any{1.0}}},
	}
	f.Fuzz(func(t *testing.T, condition []byte) {
		if !json.Valid(condition) {
			return
		}
		doc := fmt.Appendf(nil, `{"schema_version":1,"version":1,"scope":"server","unit_types":["user"],
			"attributes":[{"key":"s","type":"string"},{"key":"n","type":"number"},{"key":"b","type":"boolean"},
			              {"key":"v","type":"semver"},{"key":"d","type":"date"},{"key":"l","type":"list"}],
			"segments":[{"key":"seg","private":false,"conditions":[%s]}],
			"flags":[{"key":"f","type":"boolean","on":true,"off_variation":"false","fallthrough":"false",
			  "variations":[{"key":"true","value":true},{"key":"false","value":false}],
			  "rules":[{"id":"r1","conditions":[%s],"serve":{"kind":"variation","variation":"true"}}]}]}`, condition, condition)
		rs, err := eval.Decode(doc)
		if err != nil {
			return // not a condition object, e.g. a bare number
		}
		for _, ctx := range contexts {
			switch r := rs.Evaluate(ctx, "f"); {
			case r.Reason == eval.ReasonRule, r.Reason == eval.ReasonFallthrough:
			case r.Reason == eval.ReasonError && (r.ErrorCode == eval.ErrorInconsistentRuleset || r.ErrorCode == eval.ErrorPrerequisiteCycle):
			default:
				t.Fatalf("condition %s: unexpected result %+v", condition, r)
			}
		}
	})
}
