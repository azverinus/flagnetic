package flagnetic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// largeFlags is how many flags the large ruleset holds: more than a busy
// environment, so a guarantee that holds here holds there.
const largeFlags = 1000

// largeRuleset is a ruleset of largeFlags flags shaped like real ones:
// attribute conditions on every attribute type, segments, prerequisites,
// percentage rollouts and running experiments, so an evaluation walks the
// paths a production ruleset makes it walk.
func largeRuleset() []byte {
	variations := []any{
		map[string]any{"key": "on", "value": true},
		map[string]any{"key": "off", "value": false},
	}
	var segments []any
	for s := range 20 {
		segments = append(segments, map[string]any{
			"key": fmt.Sprintf("segment_%d", s), "private": false,
			"conditions": []any{
				map[string]any{"kind": "attribute", "attribute": "country", "op": "in", "value": []any{"DE", "FR", "NL", "PL"}},
				map[string]any{"kind": "attribute", "attribute": "age", "op": "gte", "value": 18 + s},
			},
		})
	}
	var flags []any
	for i := range largeFlags {
		var rules []any
		var prerequisites []any
		switch i % 10 {
		case 0:
			rules = append(rules, map[string]any{"id": fmt.Sprintf("r%d", i), "conditions": []any{},
				"serve": map[string]any{
					"kind": "experiment", "experiment": fmt.Sprintf("experiment_%d", i), "unit_type": "user",
					"inclusion_salt": fmt.Sprintf("in-%d", i), "variation_salt": fmt.Sprintf("var-%d", i),
					"traffic_allocation": 5000,
					"weights":            []any{map[string]any{"variation": "on", "weight": 5000}, map[string]any{"variation": "off", "weight": 5000}},
				}})
		case 1:
			rules = append(rules, map[string]any{"id": fmt.Sprintf("r%d", i), "conditions": []any{},
				"serve": map[string]any{"kind": "rollout", "unit_type": "user", "variation": "on", "percentage": 2500, "salt": fmt.Sprintf("salt-%d", i)}})
		case 2:
			prerequisites = append(prerequisites, map[string]any{"flag": fmt.Sprintf("flag_%d", i-1), "variation": "on"})
		case 3:
			rules = append(rules, map[string]any{"id": fmt.Sprintf("r%d", i),
				"conditions": []any{map[string]any{"kind": "in_segment", "segment": fmt.Sprintf("segment_%d", i%20)}},
				"serve":      map[string]any{"kind": "variation", "variation": "on"}})
		}
		rules = append(rules,
			map[string]any{"id": fmt.Sprintf("a%d", i), "conditions": []any{
				map[string]any{"kind": "attribute", "attribute": "plan", "op": "eq", "value": "pro"},
				map[string]any{"kind": "attribute", "attribute": "app_version", "op": "gte", "value": fmt.Sprintf("2.%d.0", i%30)},
				map[string]any{"kind": "attribute", "attribute": "tags", "op": "contains", "value": "beta"},
			}, "serve": map[string]any{"kind": "variation", "variation": "on"}},
			map[string]any{"id": fmt.Sprintf("b%d", i), "conditions": []any{
				map[string]any{"kind": "attribute", "attribute": "email", "op": "matches", "value": `@(example|test)\.com$`},
				map[string]any{"kind": "attribute", "attribute": "signed_up", "op": "after", "value": "2025-01-01T00:00:00Z"},
				map[string]any{"kind": "attribute", "attribute": "beta", "op": "eq", "value": true},
			}, "serve": map[string]any{"kind": "variation", "variation": "on"}},
		)
		flags = append(flags, map[string]any{
			"key": fmt.Sprintf("flag_%d", i), "type": "boolean", "automatic_exposure": false, "on": true,
			"variations": variations, "off_variation": "off", "fallthrough": "off",
			"prerequisites": prerequisites, "rules": rules,
		})
	}
	doc, err := json.Marshal(map[string]any{
		"schema_version": 1, "version": 1, "scope": "server",
		"unit_types": []string{"user"},
		"attributes": []any{
			map[string]any{"key": "plan", "type": "string"},
			map[string]any{"key": "country", "type": "string"},
			map[string]any{"key": "email", "type": "string"},
			map[string]any{"key": "age", "type": "number"},
			map[string]any{"key": "beta", "type": "boolean"},
			map[string]any{"key": "app_version", "type": "semver"},
			map[string]any{"key": "signed_up", "type": "date"},
			map[string]any{"key": "tags", "type": "list"},
		},
		"segments": segments, "flags": flags,
	})
	if err != nil {
		panic(err)
	}
	return doc
}

// largeContext is the i-th of a varied set of flag contexts.
func largeContext(i int) flagnetic.Context {
	plans := []string{"free", "pro", "team"}
	countries := []string{"DE", "US", "FR", "BR", "NL"}
	return flagnetic.Context{
		Units: map[string]string{"user": fmt.Sprintf("user-%d", i)},
		Attributes: map[string]any{
			"plan":        plans[i%len(plans)],
			"country":     countries[i%len(countries)],
			"email":       fmt.Sprintf("person%d@example.com", i),
			"age":         float64(16 + i%50),
			"beta":        i%2 == 0,
			"app_version": fmt.Sprintf("2.%d.%d", i%40, i%7),
			"signed_up":   "2025-06-01T12:00:00Z",
			"tags":        []string{"early", "beta"}[:1+i%2],
		},
	}
}

// TestEvaluationStaysUnderAMillisecondAtP99 holds the SDK to its promise
// that evaluating a flag stays under a millisecond: the 99th percentile of
// one flag of a 1000-flag ruleset, through a public getter. The race
// detector slows every evaluation by an order of magnitude, so the
// measurement means nothing under it.
func TestEvaluationStaysUnderAMillisecondAtP99(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector distorts the timing")
	}
	c := offline(t, largeRuleset())
	const calls = 100_000
	contexts := make([]flagnetic.Context, 1000)
	for i := range contexts {
		contexts[i] = largeContext(i)
	}
	ctx := context.Background()
	reasons := map[flagnetic.Reason]int{}
	took := make([]time.Duration, calls)
	for i := range calls {
		// The flag and the context advance at unrelated strides, so every
		// flag meets every kind of context.
		fc, key := contexts[(i*31+i/largeFlags)%len(contexts)], fmt.Sprintf("flag_%d", (i*7919)%largeFlags)
		start := time.Now()
		d := c.Detail(ctx, fc, key, false)
		took[i] = time.Since(start)
		reasons[d.Reason]++
	}
	slices.Sort(took)
	p50, p99 := took[calls/2], took[calls*99/100]
	t.Logf("p50 %v, p99 %v over %d evaluations", p50, p99, calls)
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		writeSummary(t, path, fmt.Sprintf("Go SDK evaluation on a %d-flag ruleset: p50 %v, p99 %v (limit 1 ms)\n", largeFlags, p50, p99))
	}
	if p99 > time.Millisecond {
		t.Errorf("p99 = %v, want under 1 ms", p99)
	}
	// The ruleset must make evaluations walk its paths, or the number
	// measures less than it claims.
	for _, r := range []flagnetic.Reason{flagnetic.ReasonExperiment, flagnetic.ReasonRollout, flagnetic.ReasonRule, flagnetic.ReasonPrerequisiteFailed, flagnetic.ReasonFallthrough} {
		if reasons[r] == 0 {
			t.Errorf("no evaluation ended with reason %s: %v", r, reasons)
		}
	}
}

func writeSummary(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) // #nosec G304 -- CI names its own summary file
	if err != nil {
		t.Errorf("job summary: %v", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line); err != nil {
		t.Errorf("job summary: %v", err)
	}
}

// BenchmarkDetail evaluates one flag of the large ruleset, for profiling.
func BenchmarkDetail(b *testing.B) {
	c, err := flagnetic.New("", "", flagnetic.WithOffline(largeRuleset()))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	ctx, fc := context.Background(), largeContext(7)
	i := 0
	for b.Loop() {
		c.Detail(ctx, fc, fmt.Sprintf("flag_%d", (i*7919)%largeFlags), false)
		i++
	}
}

// BenchmarkEvaluateAll evaluates every flag of the large ruleset for one
// flag context, as a service rendering a whole page might, for profiling.
func BenchmarkEvaluateAll(b *testing.B) {
	c, err := flagnetic.New("", "", flagnetic.WithOffline(largeRuleset()))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	keys := make([]string, largeFlags)
	for i := range keys {
		keys[i] = fmt.Sprintf("flag_%d", i)
	}
	ctx, fc := context.Background(), largeContext(7)
	for b.Loop() {
		for _, key := range keys {
			c.Detail(ctx, fc, key, false)
		}
	}
}
