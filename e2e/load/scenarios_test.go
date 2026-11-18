package load

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/e2e"
)

var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestEventIDIsALowercaseUUIDv7(t *testing.T) {
	a, b := EventID(), EventID()
	if !uuidV7.MatchString(a) || a == b {
		t.Errorf("EventID gave %q and %q", a, b)
	}
}

func TestBatchHoldsFiveEventsOfTheTypesTheAPITakes(t *testing.T) {
	target := NewTarget(e2e.Seed{}, 1000)
	b := target.batch(7, time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC))
	raw, _ := json.Marshal(b)
	var got struct {
		Events []struct {
			Type    string `json:"type"`
			EventID string `json:"event_id"`
			Counts  []struct{ Flag string }
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	types := map[string]int{}
	for _, e := range got.Events {
		types[e.Type]++
		if !uuidV7.MatchString(e.EventID) {
			t.Errorf("event ID %q is not a lowercase UUIDv7", e.EventID)
		}
		if e.Type == "evaluation_summary" && len(e.Counts) != 20 {
			t.Errorf("a summary has %d rows, want 20", len(e.Counts))
		}
	}
	if types["evaluation_summary"] != 3 || types["exposure"] != 2 {
		t.Errorf("event types = %v, want 3 summaries and 2 exposures", types)
	}
}

// fake answers the SDK API as far as the rate scenarios read it.
func fake(t *testing.T, rejectEvents bool) (*Target, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	var full, revalidated atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/sdk/v1/ruleset", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"42"`)
		if r.Header.Get("If-None-Match") == `"42"` {
			revalidated.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full.Add(1)
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/sdk/v1/evaluate", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer client-key") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"flags":{}}`))
	})
	mux.HandleFunc("/sdk/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		if rejectEvents {
			_, _ = w.Write([]byte(`{"accepted":0,"rejected":[{"index":3,"code":"unknown_experiment","detail":"no such experiment"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"accepted":5,"duplicates":0,"dropped_rows":0,"rejected":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	seed := e2e.Seed{Replicas: e2e.Replicas{A: srv.URL, B: srv.URL}, ServerKey: "server-key", ClientKey: "client-key"}
	return NewTarget(seed, 1000), &full, &revalidated
}

var small = Shape{Rate: 200, Duration: Duration(200 * time.Millisecond)}

func TestEvaluateAsksWithTheClientKey(t *testing.T) {
	target, _, _ := fake(t, false)
	if r := target.Evaluate(context.Background(), small, 0); r.Sent != 40 || r.Errors != 0 {
		t.Errorf("Evaluate = %+v, want 40 samples and no error", r)
	}
}

func TestRulesetAlternatesFullAnswersAndRevalidations(t *testing.T) {
	target, full, revalidated := fake(t, false)
	r := target.Ruleset(context.Background(), small, 0)
	if r.Errors != 0 || r.Sent != 40 {
		t.Fatalf("Ruleset = %+v", r)
	}
	// The first fetch that learns the ETag is a full answer of its own.
	if full.Load() != 21 || revalidated.Load() != 20 {
		t.Errorf("full %d, revalidated %d; want 21 and 20", full.Load(), revalidated.Load())
	}
}

func TestEventsCountsARejectedEventAsAnError(t *testing.T) {
	target, _, _ := fake(t, false)
	if r := target.Events(context.Background(), small, 0); r.Errors != 0 || r.Sent != 40 {
		t.Errorf("Events = %+v, want 40 samples and no error", r)
	}
	target, _, _ = fake(t, true)
	r := target.Events(context.Background(), small, 0)
	if r.Errors != r.Sent || !strings.Contains(r.FirstError, "unknown_experiment") {
		t.Errorf("Events = %+v, want every sample failed naming the rejection code", r)
	}
}

func TestMeasureRunsTheNamedScenariosInOrder(t *testing.T) {
	target, _, _ := fake(t, false)
	p := Profile{Scenarios: map[string]Shape{Evaluate: small, Ruleset: small, Events: small}}
	got := Measure(context.Background(), target, p, []string{Events, Evaluate})
	if len(got) != 2 || got[0].Scenario != Evaluate || got[1].Scenario != Events {
		t.Errorf("Measure = %+v, want evaluate then events", got)
	}
}
