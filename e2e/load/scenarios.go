package load

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/azverinus/flagnetic/e2e"
)

// Target is the instance a run measures: the seed `up` wrote.
type Target struct {
	Seed e2e.Seed
	// Flags is the number of load flags seeded, for the events' summaries.
	Flags int

	http *http.Client
}

// NewTarget returns the target of seed with a client that keeps the
// connections of a run open, as an SDK does.
func NewTarget(seed e2e.Seed, flags int) *Target {
	return &Target{Seed: seed, Flags: flags, http: &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns: 4096, MaxIdleConnsPerHost: 2048, IdleConnTimeout: time.Minute,
		},
	}}
}

// replica spreads requests over the two replicas, as a load balancer would.
func (t *Target) replica(i int) string {
	if i%2 == 0 {
		return t.Seed.Replicas.A
	}
	return t.Seed.Replicas.B
}

// send makes one request and returns its status, the body read to the end.
func (t *Target) send(ctx context.Context, method, url, key string, body []byte, header map[string]string) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "flagnetic-load/0 (e2e)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := t.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	return res.StatusCode, b, err
}

func expect(status, want int, body []byte) error {
	if status == want {
		return nil
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return fmt.Errorf("status %d, want %d: %s", status, want, bytes.TrimSpace(body))
}

// Evaluate measures remote evaluation: a client key posting a context and
// being answered the served value of every flag of the project.
func (t *Target) Evaluate(ctx context.Context, shape Shape, warmup time.Duration) Result {
	do := func(ctx context.Context, i int) error {
		body, _ := json.Marshal(map[string]any{"context": map[string]any{"units": map[string]string{e2e.UnitType: fmt.Sprintf("user-%d", i%5000)}}})
		status, b, err := t.send(ctx, http.MethodPost, t.replica(i)+"/sdk/v1/evaluate", t.Seed.ClientKey, body, nil)
		if err != nil {
			return err
		}
		return expect(status, http.StatusOK, b)
	}
	return t.rate(ctx, Evaluate, shape, warmup, do)
}

// Ruleset measures the ruleset fetch with a server key, alternating a full
// answer and a revalidation that is answered 304.
func (t *Target) Ruleset(ctx context.Context, shape Shape, warmup time.Duration) Result {
	url := func(i int) string { return t.replica(i) + "/sdk/v1/ruleset" }
	res, err := t.http.Do(mustRequest(ctx, url(0), t.Seed.ServerKey))
	if err != nil {
		return failed(Ruleset, err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	etag := res.Header.Get("ETag")
	if res.StatusCode != http.StatusOK || etag == "" {
		return failed(Ruleset, fmt.Errorf("the first fetch answered %d with ETag %q", res.StatusCode, etag))
	}
	do := func(ctx context.Context, i int) error {
		want, header := http.StatusOK, map[string]string(nil)
		if i%4 >= 2 { // the replica alternates with i, so 200 and 304 meet on both
			want, header = http.StatusNotModified, map[string]string{"If-None-Match": etag}
		}
		status, b, err := t.send(ctx, http.MethodGet, url(i), t.Seed.ServerKey, nil, header)
		if err != nil {
			return err
		}
		return expect(status, want, b)
	}
	return t.rate(ctx, Ruleset, shape, warmup, do)
}

func mustRequest(ctx context.Context, url, key string) *http.Request {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		panic(err) // the URL is the seed's
	}
	req.Header.Set("Authorization", "Bearer "+key)
	return req
}

// Events measures the ingest of a batch of five events from a server key:
// three evaluation summaries of twenty rows and two exposures of the running
// experiment, judged and made durable before the 202.
func (t *Target) Events(ctx context.Context, shape Shape, warmup time.Duration) Result {
	do := func(ctx context.Context, i int) error {
		body, err := json.Marshal(t.batch(i, time.Now().UTC()))
		if err != nil {
			return err
		}
		status, b, err := t.send(ctx, http.MethodPost, t.replica(i)+"/sdk/v1/events", t.Seed.ServerKey, body, nil)
		if err != nil {
			return err
		}
		if err := expect(status, http.StatusAccepted, b); err != nil {
			return err
		}
		var answer struct {
			Rejected []struct{ Code, Detail string } `json:"rejected"`
		}
		if err := json.Unmarshal(b, &answer); err != nil {
			return fmt.Errorf("the answer is not JSON: %w", err)
		}
		if len(answer.Rejected) > 0 {
			return fmt.Errorf("an event was rejected: %s: %s", answer.Rejected[0].Code, answer.Rejected[0].Detail)
		}
		return nil
	}
	return t.rate(ctx, Events, shape, warmup, do)
}

// batch is the i-th batch of the events scenario sent at now.
func (t *Target) batch(i int, now time.Time) map[string]any {
	stamp := now.Format(time.RFC3339Nano)
	events := make([]map[string]any, 0, 5)
	for s := range 3 {
		rows := make([]map[string]any, 0, 20)
		for r := range 20 {
			rows = append(rows, map[string]any{
				"flag": e2e.LoadFlagKey((i*60 + s*20 + r) % max(t.Flags, 1)), "variation": "false", "reason": "fallthrough", "count": 1 + r,
			})
		}
		events = append(events, map[string]any{
			"type": "evaluation_summary", "event_id": EventID(),
			"interval_start": now.Add(-time.Minute).Format(time.RFC3339Nano), "interval_end": stamp,
			"ruleset_version": 1, "counts": rows,
		})
	}
	for x := range 2 {
		events = append(events, map[string]any{
			"type": "exposure", "event_id": EventID(), "occurred_at": stamp,
			"experiment_key": e2e.LoadExperiment, "flag_key": e2e.LoadExperimentFlag,
			"variation_key": []string{"control", "treatment"}[x], "unit_type": e2e.UnitType,
			"unit_id":           fmt.Sprintf("load-unit-%d", (i*2+x)%500),
			"evaluation_reason": "experiment", "override": false, "ruleset_version": 1,
		})
	}
	return map[string]any{
		"schema_version": 1, "sdk": map[string]string{"name": "flagnetic-load", "version": "0"}, "sent_at": stamp, "events": events,
	}
}

// EventID is a new event ID: a lowercase UUIDv7, as the SDK API requires.
func EventID() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err) // the system's random source failed
	}
	return id.String()
}

func (t *Target) rate(ctx context.Context, name string, shape Shape, warmup time.Duration, do func(context.Context, int) error) Result {
	if warmup > 0 {
		Run(ctx, Options{Scenario: name, Rate: shape.Rate, Duration: warmup}, do)
	}
	return Run(ctx, Options{Scenario: name, Rate: shape.Rate, Duration: time.Duration(shape.Duration)}, do)
}

func failed(name string, err error) Result {
	return Result{Scenario: name, Sent: 1, Errors: 1, FirstError: err.Error()}
}

// frameTimeout is how long a change may take to reach every subscriber
// before the missing arrivals count as failed samples.
const frameTimeout = 10 * time.Second

type arrival struct {
	subscriber int
	version    int64
	at         time.Time
}

// Stream measures the fan-out of a configuration change: subscribers
// streams held open across the two replicas with a server key, shape.Changes
// changes made through replica A, and for each subscriber and change the
// time from the change's commit to the frame that announces it. The
// scenario's samples are those arrivals; one that does not come within
// frameTimeout is a failed sample at that timeout.
func (t *Target) Stream(ctx context.Context, shape Shape) Result {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	frames := make(chan arrival, shape.Subscribers*4)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	opened := make(chan error, shape.Subscribers)
	for s := range shape.Subscribers {
		wg.Go(func() {
			opened <- t.subscribe(ctx, s, frames)
		})
		// Open them over a second or so, as apps starting over time do: a
		// burst of hundreds of connects overflows a listener's backlog.
		time.Sleep(2 * time.Millisecond)
	}
	// Every subscriber hears the current version at once; wait for all of
	// them, so the changes below find the full fan-out.
	initial := map[int]int64{}
	deadline := time.After(30 * time.Second)
	for len(initial) < shape.Subscribers {
		select {
		case a := <-frames:
			initial[a.subscriber] = a.version
		case err := <-opened:
			if err != nil && !errors.Is(err, context.Canceled) {
				return failed(Stream, err)
			}
		case <-deadline:
			return failed(Stream, fmt.Errorf("%d of %d subscribers heard the current version in 30s", len(initial), shape.Subscribers))
		}
	}
	m, err := e2e.SignIn(ctx, t.Seed.Replicas.A)
	if err != nil {
		return failed(Stream, err)
	}
	var (
		latencies  []time.Duration
		errs       int
		firstError string
	)
	prev := slices.Max(mapValues(initial))
	for c := range shape.Changes {
		committed, err := e2e.ToggleLoadFlag(ctx, m, 0, c%2 == 1)
		if err != nil {
			return failed(Stream, err)
		}
		heard := map[int]time.Duration{}
		newest := prev
		timeout := time.After(frameTimeout)
	collect:
		for len(heard) < shape.Subscribers {
			select {
			case a := <-frames:
				if _, seen := heard[a.subscriber]; !seen && a.version > prev {
					heard[a.subscriber] = max(a.at.Sub(committed), 0)
					newest = max(newest, a.version)
				}
			case <-timeout:
				break collect
			}
		}
		prev = newest
		for s := range shape.Subscribers {
			d, ok := heard[s]
			if !ok {
				d = frameTimeout
				errs++
				if firstError == "" {
					firstError = fmt.Sprintf("subscriber %d did not hear change %d in %v", s, c+1, frameTimeout)
				}
			}
			latencies = append(latencies, d)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return Summarize(Stream, 0, latencies, errs, firstError, 0)
}

func mapValues(m map[int]int64) []int64 {
	out := make([]int64, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// subscribe holds the ruleset stream of subscriber s open on one of the two
// replicas and sends every version frame to frames, until ctx ends.
func (t *Target) subscribe(ctx context.Context, s int, frames chan<- arrival) error {
	req := mustRequest(ctx, t.replica(s)+"/sdk/v1/stream", t.Seed.ServerKey)
	// A stream is held open for the whole run: the client's timeout covers
	// the body, so this one has none.
	res, err := (&http.Client{Transport: t.http.Transport}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 200))
		return expect(res.StatusCode, http.StatusOK, b)
	}
	err = ReadVersions(res.Body, func(v int64) {
		select {
		case frames <- arrival{subscriber: s, version: v, at: time.Now()}:
		case <-ctx.Done():
		}
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// Measure runs the named scenarios of p (all of them when names is empty)
// against t, in the order of Names, and returns their results.
func Measure(ctx context.Context, t *Target, p Profile, names []string) []Result {
	var results []Result
	for _, name := range Names {
		if len(names) > 0 && !slices.Contains(names, name) {
			continue
		}
		shape, warmup := p.Scenarios[name], time.Duration(p.Warmup)
		switch name {
		case Evaluate:
			results = append(results, t.Evaluate(ctx, shape, warmup))
		case Ruleset:
			results = append(results, t.Ruleset(ctx, shape, warmup))
		case Events:
			results = append(results, t.Events(ctx, shape, warmup))
		case Stream:
			results = append(results, t.Stream(ctx, shape))
		}
	}
	return results
}
