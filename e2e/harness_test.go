package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/azverinus/flagnetic/e2e"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	flagnetic "github.com/azverinus/flagnetic/sdks/go"
	flagneticof "github.com/azverinus/flagnetic/sdks/go-openfeature"
)

// The end-to-end run builds the real binary and starts PostgreSQL, so it
// runs only when asked for (`task e2e`, and the CI job of that name), never
// as part of every module's unit tests.
const enabledBy = "FLAGNETIC_E2E"

// instance is the seeded pair of replicas every test of the package shares.
var instance *e2e.Instance

// logs keeps the replicas' output, printed when a test fails.
var logs syncBuffer

func TestMain(m *testing.M) {
	if os.Getenv(enabledBy) != "1" {
		fmt.Println("end-to-end run skipped: set " + enabledBy + "=1 or run `task e2e`")
		os.Exit(0)
	}
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	work, err := os.MkdirTemp("", "flagnetic-e2e-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(work) }()
	instance, err = e2e.Up(ctx, e2e.UpOptions{Root: "..", WorkDir: work, DatabaseURL: os.Getenv("DATABASE_URL"), Logs: &logs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "bring up the instance: %v\n%s", err, logs.String())
		return 1
	}
	binary = work + "/flagnetic"
	code := m.Run()
	if code != 0 {
		fmt.Fprintf(os.Stderr, "--- replicas' logs\n%s", logs.String())
	}
	if err := instance.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop the instance: %v\n", err)
		return 1
	}
	return code
}

// binary is the flagnetic binary TestMain built.
var binary string

func seed() e2e.Seed { return instance.State.Seed }

// rulesetVersion returns the version of the seed environment's ruleset as
// replica A serves it to the seed's server key.
func rulesetVersion(t *testing.T) string {
	t.Helper()
	res := sdkGet(t, seed().Replicas.A+"/sdk/v1/ruleset", seed().ServerKey)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ruleset = %d", res.StatusCode)
	}
	return res.Header.Get("ETag")
}

func sdkGet(t *testing.T, url, key string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res
}

func TestSeedingASeededInstanceChangesNothing(t *testing.T) {
	before := rulesetVersion(t)
	again, err := e2e.SeedInstance(t.Context(), seed().Replicas, instance.State.Keys)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(instance.State)
	got, _ := json.Marshal(again)
	if !bytes.Equal(got, want) {
		t.Errorf("seeding again answered\n%s\nwant the seed it had\n%s", got, want)
	}
	if after := rulesetVersion(t); after != before {
		t.Errorf("seeding again moved the ruleset from %s to %s", before, after)
	}
}

func TestEveryCaseServesItsValueThroughTheGoSDK(t *testing.T) {
	client := goSDK(t, seed().ServerKey, seed().Replicas.B)
	if stage := client.Ready(ctxWithin(t, 10*time.Second)); stage != flagnetic.StageFresh {
		t.Fatalf("ready = %s, want fresh", stage)
	}
	for _, c := range seed().Cases {
		t.Run(c.Name, func(t *testing.T) {
			var def any = ""
			if c.Type == "boolean" {
				def = false
			}
			d := client.Detail(t.Context(), flagnetic.Context{Units: c.Context.Units}, c.Flag, def)
			value, _ := json.Marshal(d.Value)
			if !jsonEqual(value, c.Value) || d.Variation != c.Variation || string(d.Reason) != c.Reason {
				t.Errorf("detail = %s %s %s, want %s %s %s", value, d.Variation, d.Reason, c.Value, c.Variation, c.Reason)
			}
		})
	}
}

func TestEveryCaseServesItsValueThroughTheOpenFeatureProvider(t *testing.T) {
	provider, err := flagneticof.New(seed().ServerKey, seed().Replicas.A, flagneticof.WithUnitType(e2e.UnitType))
	if err != nil {
		t.Fatal(err)
	}
	if err := of.SetNamedProviderWithContextAndWait(ctxWithin(t, 10*time.Second), "e2e", provider); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = of.SetNamedProviderAndWait("e2e", of.NoopProvider{}) })
	if stage := provider.Client().Stage(); stage != flagnetic.StageFresh {
		t.Fatalf("stage = %s, want fresh", stage)
	}
	client := of.NewClient("e2e")
	for _, c := range seed().Cases {
		t.Run(c.Name, func(t *testing.T) {
			evalCtx := of.NewEvaluationContext(c.Context.Units[e2e.UnitType], nil)
			var value any
			var metadata of.FlagMetadata
			switch c.Type {
			case "boolean":
				d, err := client.BooleanValueDetails(t.Context(), c.Flag, false, evalCtx)
				if err != nil {
					t.Fatal(err)
				}
				value, metadata = d.Value, d.FlagMetadata
			default:
				d, err := client.StringValueDetails(t.Context(), c.Flag, "", evalCtx)
				if err != nil {
					t.Fatal(err)
				}
				value, metadata = d.Value, d.FlagMetadata
			}
			got, _ := json.Marshal(value)
			reason, _ := metadata.GetString("flagnetic_reason")
			if !jsonEqual(got, c.Value) || reason != c.Reason {
				t.Errorf("value = %s, reason %s; want %s, %s", got, reason, c.Value, c.Reason)
			}
		})
	}
}

func TestAClientKeyIsServedEveryCaseMasked(t *testing.T) {
	c, err := sdk.NewClientWithResponses(seed().Replicas.B+sdk.BaseURL, sdk.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+seed().ClientKey)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range seed().Cases {
		t.Run(cs.Name, func(t *testing.T) {
			units := cs.Context.Units
			res, err := c.EvaluateWithResponse(t.Context(), nil, sdk.EvaluationRequest{Context: sdk.EvaluationContext{Units: &units}})
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Flags map[string]map[string]json.RawMessage `json:"flags"`
			}
			if res.StatusCode() != http.StatusOK || json.Unmarshal(res.Body, &body) != nil {
				t.Fatalf("evaluate = %d: %s", res.StatusCode(), res.Body)
			}
			got := body.Flags[cs.Flag]
			want := map[string]json.RawMessage{
				"value": cs.Value, "variation": json.RawMessage(`"` + cs.Variation + `"`), "reason": json.RawMessage(`"` + cs.ClientReason + `"`),
			}
			for k, v := range want {
				if !jsonEqual(got[k], v) {
					t.Errorf("%s = %s, want %s", k, got[k], v)
				}
			}
			if _, ok := got["rule_id"]; ok {
				t.Errorf("a client key learned the rule: %s", got["rule_id"])
			}
		})
	}
}

func TestAKillSwitchThroughOneReplicaReachesAnSDKStreamingFromTheOtherWithinTwoSeconds(t *testing.T) {
	t.Cleanup(func() {
		if err := e2e.RestoreSeed(context.Background(), instance.State); err != nil {
			t.Errorf("restore the seed: %v", err)
		}
	})
	client := goSDK(t, seed().ServerKey, seed().Replicas.B, flagnetic.WithChangeStream(true))
	if stage := client.Ready(ctxWithin(t, 10*time.Second)); stage != flagnetic.StageFresh {
		t.Fatalf("ready = %s, want fresh", stage)
	}
	// The stream is open once the replica has announced the version the
	// client holds; give it a moment so the kill switch is news to it.
	time.Sleep(500 * time.Millisecond)
	changed := make(chan time.Time, 1)
	client.OnChange(func(keys []string) {
		if slices.Contains(keys, e2e.FlagOn) {
			select {
			case changed <- time.Now():
			default:
			}
		}
	})

	killed, err := e2e.Kill(t.Context(), instance.State, e2e.FlagOn)
	if err != nil {
		t.Fatal(err)
	}
	if !killed.Changed {
		t.Fatal("the kill switch changed nothing")
	}
	select {
	case at := <-changed:
		elapsed := at.Sub(killed.CommittedAt)
		t.Logf("kill_switch_propagation_ms=%d", elapsed.Milliseconds())
		summary(t, fmt.Sprintf("Kill switch through replica A reaching a Go SDK streaming from replica B: %d ms (target 2000 ms)", elapsed.Milliseconds()))
		if elapsed > 2*time.Second {
			t.Errorf("the kill switch took %s to reach the SDK, more than 2 s", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the kill switch never reached the SDK")
	}
	unit := flagnetic.Context{Units: map[string]string{e2e.UnitType: seed().Units["outside_rollout"]}}
	if d := client.Detail(t.Context(), unit, e2e.FlagOn, true); d.Value != false || d.Reason != flagnetic.ReasonOff {
		t.Errorf("after the kill switch = %v %s, want false off", d.Value, d.Reason)
	}
}

func TestTheGoSDKAppearsInTheInventory(t *testing.T) {
	client := goSDK(t, seed().ServerKey, seed().Replicas.B)
	client.Ready(ctxWithin(t, 10*time.Second))
	// A replica writes what it saw once a minute.
	deadline := time.Now().Add(90 * time.Second)
	for {
		items, err := e2e.Inventory(t.Context(), instance.State)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.SdkName == "flagnetic-go" && it.SdkVersion == flagnetic.Version && it.SdkKey.Kind == management.SdkKeyKindServer {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no flagnetic-go %s in the inventory: %+v", flagnetic.Version, items)
		}
		time.Sleep(2 * time.Second)
	}
}

// The revocation ends the seed's server key, so it runs last.
func TestARevocationThroughOneReplicaRefusesTheSDKOnTheOther(t *testing.T) {
	if err := e2e.Revoke(t.Context(), instance.State, management.SdkKeyKindServer); err != nil {
		t.Fatal(err)
	}
	if err := e2e.Revoke(t.Context(), instance.State, management.SdkKeyKindServer); err != nil {
		t.Fatalf("revoking again: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for sdkGet(t, seed().Replicas.B+"/sdk/v1/ruleset", seed().ServerKey).StatusCode != http.StatusUnauthorized {
		if time.Now().After(deadline) {
			t.Fatal("replica B still serves the revoked key")
		}
		time.Sleep(100 * time.Millisecond)
	}
	client := goSDK(t, seed().ServerKey, seed().Replicas.B)
	if stage := client.Ready(ctxWithin(t, 10*time.Second)); stage != flagnetic.StageBuiltIn {
		t.Errorf("ready with a revoked key = %s, want built_in", stage)
	}
}

func goSDK(t *testing.T, key, baseURL string, opts ...flagnetic.Option) *flagnetic.Client {
	t.Helper()
	c, err := flagnetic.New(key, baseURL, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func ctxWithin(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

// summary appends a line to the CI job's summary when there is one.
func summary(t *testing.T, line string) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) //nolint:gosec // the CI runner names the file
	if err != nil {
		t.Errorf("job summary: %v", err)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, line)
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	p, _ := json.Marshal(x)
	q, _ := json.Marshal(y)
	return bytes.Equal(p, q)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
