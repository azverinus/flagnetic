// Package upgrade runs a rolling upgrade of a Flagnetic instance under light
// SDK load and judges it: the requests of a client key and a server key,
// and a Go SDK that holds the change stream open, must meet no failure
// while the replicas are replaced, and the SDK must receive a change made
// after the upgrade.
package upgrade

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/e2e"
	"github.com/azverinus/flagnetic/e2e/load"
	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// Totals are the requests of the load, summed over its chunks.
type Totals struct {
	Sent, Errors int
	// FirstError names the scenario and the error of the first failure.
	FirstError string
}

// Sustain runs chunk again and again until stop is closed or ctx ends, and
// totals what the chunks measured. The chunk that is running when stop closes
// finishes: a request cut off by the end of the run would count as a
// failure the server did not cause.
func Sustain(ctx context.Context, stop <-chan struct{}, chunk func(context.Context) []load.Result) Totals {
	var t Totals
	for {
		for _, r := range chunk(ctx) {
			t.Sent += r.Sent
			t.Errors += r.Errors
			if t.FirstError == "" && r.FirstError != "" {
				t.FirstError = r.Scenario + ": " + r.FirstError
			}
		}
		select {
		case <-stop:
			return t
		case <-ctx.Done():
			return t
		default:
		}
	}
}

// SDKTotals are the evaluations the streaming SDK made while the replicas
// were replaced.
type SDKTotals struct {
	Evaluations int
	// Fallbacks counts evaluations that did not serve the flag's value from
	// a ruleset, or served another value.
	Fallbacks int
	First     string
}

// Report is what a run observed.
type Report struct {
	Load Totals
	SDK  SDKTotals
	// SawChange is whether the SDK received a change made after the upgrade.
	SawChange bool
	// UpgradeError is the failure of the upgrade command, empty when it
	// succeeded.
	UpgradeError string
	UpgradeTook  time.Duration
}

// Verdict lists what makes the upgrade fail; none means it passed.
func (r Report) Verdict() []string {
	var failures []string
	if r.UpgradeError != "" {
		failures = append(failures, "the upgrade failed: "+r.UpgradeError)
	}
	switch {
	case r.Load.Sent == 0:
		failures = append(failures, "no request was sent during the rollout")
	case r.Load.Errors > 0:
		failures = append(failures, fmt.Sprintf("%d of %d requests failed during the rollout (first: %s)", r.Load.Errors, r.Load.Sent, r.Load.FirstError))
	}
	switch {
	case r.SDK.Evaluations == 0:
		failures = append(failures, "the SDK evaluated nothing during the rollout")
	case r.SDK.Fallbacks > 0:
		failures = append(failures, fmt.Sprintf("%d of %d evaluations of the SDK were not served from its ruleset (first: %s)", r.SDK.Fallbacks, r.SDK.Evaluations, r.SDK.First))
	}
	if !r.SawChange {
		failures = append(failures, "the SDK did not receive the change made after the upgrade")
	}
	return failures
}

// Markdown is the report as a table for a job summary.
func (r Report) Markdown() string {
	yes := "no"
	if r.SawChange {
		yes = "yes"
	}
	var b strings.Builder
	b.WriteString("| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| upgrade took | %s |\n", r.UpgradeTook.Round(time.Second))
	fmt.Fprintf(&b, "| requests sent | %d |\n", r.Load.Sent)
	fmt.Fprintf(&b, "| requests failed | %d |\n", r.Load.Errors)
	fmt.Fprintf(&b, "| SDK evaluations | %d |\n", r.SDK.Evaluations)
	fmt.Fprintf(&b, "| SDK evaluations not served | %d |\n", r.SDK.Fallbacks)
	fmt.Fprintf(&b, "| change seen after the upgrade | %s |\n", yes)
	return b.String()
}

// Options say how Run upgrades.
type Options struct {
	// URL is the base URL both replicas are reached at, such as a
	// NodePort: requests spread over whichever pods are ready.
	URL string
	// Rate is the requests per second of each of the two scenarios.
	Rate float64
	// Settle is how long the load goes on after the upgrade command
	// returned, to catch the old pods draining.
	Settle time.Duration
	// ChangeWithin bounds the wait for the SDK to see the change.
	ChangeWithin time.Duration
	// Upgrade replaces the replicas; Run returns when it does.
	Upgrade func(ctx context.Context) error
}

const chunkLength = 5 * time.Second

// Run seeds the instance at o.URL, starts the SDK and the load, runs
// o.Upgrade under them, then switches the seed's "on" flag off and waits for
// the SDK to serve it off. The Report is returned even when parts failed.
func Run(ctx context.Context, o Options) (Report, error) {
	var rep Report
	state, err := e2e.SeedInstance(ctx, e2e.Replicas{A: o.URL, B: o.URL}, nil)
	if err != nil {
		return rep, fmt.Errorf("seed: %w", err)
	}
	seed := state.Seed
	client, err := flagnetic.New(seed.ServerKey, o.URL, flagnetic.WithChangeStream(true), flagnetic.WithSummaries(false), flagnetic.WithExposures(false))
	if err != nil {
		return rep, fmt.Errorf("start the SDK: %w", err)
	}
	defer func() { _ = client.Close(context.WithoutCancel(ctx)) }()
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	stage := client.Ready(readyCtx)
	cancel()
	if stage != flagnetic.StageFresh {
		return rep, fmt.Errorf("the SDK reached stage %q, not %q, before the upgrade", stage, flagnetic.StageFresh)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var sdk SDKTotals
	wg.Go(func() { sdk = watch(ctx, client, seed, stop) })
	var totals Totals
	wg.Go(func() { totals = Sustain(ctx, stop, loadChunk(seed, o.Rate)) })

	start := time.Now()
	if upErr := o.Upgrade(ctx); upErr != nil {
		rep.UpgradeError = upErr.Error()
	}
	rep.UpgradeTook = time.Since(start)
	sleep(ctx, o.Settle)
	close(stop)
	wg.Wait()
	rep.Load, rep.SDK = totals, sdk

	if rep.UpgradeError != "" {
		return rep, nil
	}
	rep.SawChange, err = changeArrives(ctx, client, state, o.ChangeWithin)
	return rep, err
}

func loadChunk(seed e2e.Seed, rate float64) func(context.Context) []load.Result {
	target := load.NewTarget(seed, 0)
	shape := load.Shape{Rate: rate, Duration: load.Duration(chunkLength)}
	return func(ctx context.Context) []load.Result {
		var (
			out [2]load.Result
			wg  sync.WaitGroup
		)
		wg.Go(func() { out[0] = target.Evaluate(ctx, shape, 0) })
		wg.Go(func() { out[1] = target.Ruleset(ctx, shape, 0) })
		wg.Wait()
		return out[:]
	}
}

// watch evaluates the seed's "on" flag ten times a second until stop closes.
func watch(ctx context.Context, c *flagnetic.Client, seed e2e.Seed, stop <-chan struct{}) SDKTotals {
	var t SDKTotals
	unit := flagnetic.Context{Units: map[string]string{seed.UnitType: seed.Units["outside_rollout"]}}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		d := c.Detail(ctx, unit, seed.Flags["on"], false)
		t.Evaluations++
		if d.Reason == flagnetic.ReasonFallback || d.Value != true {
			t.Fallbacks++
			if t.First == "" {
				t.First = fmt.Sprintf("value %v, reason %s, error %q", d.Value, d.Reason, d.ErrorCode)
			}
		}
		select {
		case <-stop:
			return t
		case <-ctx.Done():
			return t
		case <-tick.C:
		}
	}
}

// changeArrives switches the "on" flag off through the management API and
// waits for the SDK, still connected, to serve it off.
func changeArrives(ctx context.Context, c *flagnetic.Client, state e2e.State, within time.Duration) (bool, error) {
	seed := state.Seed
	if _, err := e2e.Kill(ctx, state, seed.Flags["on"]); err != nil {
		return false, fmt.Errorf("change a flag after the upgrade: %w", err)
	}
	unit := flagnetic.Context{Units: map[string]string{seed.UnitType: seed.Units["outside_rollout"]}}
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		d := c.Detail(ctx, unit, seed.Flags["on"], true)
		if d.Reason == flagnetic.ReasonOff && d.Value == false {
			return true, nil
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		sleep(ctx, 100*time.Millisecond)
	}
	return false, nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
