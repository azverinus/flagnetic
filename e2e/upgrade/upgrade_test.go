package upgrade

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/e2e/load"
)

func TestSustainRunsChunksUntilStoppedAndTotalsThem(t *testing.T) {
	stop := make(chan struct{})
	var calls atomic.Int32
	chunk := func(context.Context) []load.Result {
		if calls.Add(1) == 3 {
			close(stop)
		}
		return []load.Result{
			{Scenario: "evaluate", Sent: 10, Errors: 1, FirstError: "status 503"},
			{Scenario: "ruleset", Sent: 5},
		}
	}

	got := Sustain(t.Context(), stop, chunk)

	if calls.Load() != 3 {
		t.Fatalf("chunks run = %d, want 3: the chunk that closes stop is the last", calls.Load())
	}
	if got.Sent != 45 || got.Errors != 3 {
		t.Fatalf("totals = %d sent, %d errors; want 45 and 3", got.Sent, got.Errors)
	}
	if got.FirstError != "evaluate: status 503" {
		t.Fatalf("first error = %q", got.FirstError)
	}
}

func TestSustainStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	chunk := func(context.Context) []load.Result {
		cancel()
		return []load.Result{{Scenario: "evaluate", Sent: 1}}
	}
	done := make(chan Totals, 1)
	go func() { done <- Sustain(ctx, make(chan struct{}), chunk) }()
	select {
	case got := <-done:
		if got.Sent != 1 {
			t.Fatalf("sent = %d, want the one chunk that ran", got.Sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Sustain kept running after its context ended")
	}
}

func TestVerdict(t *testing.T) {
	good := Report{
		Load:      Totals{Sent: 1000},
		SDK:       SDKTotals{Evaluations: 500},
		SawChange: true,
	}
	tests := []struct {
		name   string
		mutate func(*Report)
		want   string // a substring of the one failure; empty for none
	}{
		{"a clean rollout passes", func(*Report) {}, ""},
		{"a failed request fails it", func(r *Report) { r.Load.Errors, r.Load.FirstError = 2, "evaluate: status 502" }, "2 of 1000 requests failed"},
		{"no requests at all fails it", func(r *Report) { r.Load.Sent = 0 }, "no request was sent"},
		{"a fallback from the SDK fails it", func(r *Report) { r.SDK.Fallbacks, r.SDK.First = 3, "reason fallback, error no_ruleset" }, "3 of 500 evaluations"},
		{"no SDK evaluation fails it", func(r *Report) { r.SDK.Evaluations = 0 }, "the SDK evaluated nothing"},
		{"a change that never arrives fails it", func(r *Report) { r.SawChange = false }, "did not receive the change"},
		{"an upgrade that failed fails it", func(r *Report) { r.UpgradeError = "exit status 1" }, "the upgrade failed: exit status 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := good
			tt.mutate(&r)
			failures := r.Verdict()
			if tt.want == "" {
				if len(failures) != 0 {
					t.Fatalf("failures = %v, want none", failures)
				}
				return
			}
			if len(failures) != 1 || !strings.Contains(failures[0], tt.want) {
				t.Fatalf("failures = %v, want one containing %q", failures, tt.want)
			}
		})
	}
}

func TestMarkdownNamesTheOutcome(t *testing.T) {
	r := Report{Load: Totals{Sent: 10, Errors: 1, FirstError: "x"}, SDK: SDKTotals{Evaluations: 4}, SawChange: true, UpgradeTook: 42 * time.Second}
	table := r.Markdown()
	for _, want := range []string{"| requests sent | 10 |", "| requests failed | 1 |", "| SDK evaluations | 4 |", "| change seen after the upgrade | yes |", "42s"} {
		if !strings.Contains(table, want) {
			t.Errorf("summary lacks %q:\n%s", want, table)
		}
	}
}
