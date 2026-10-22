package sdk_test

import (
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// served returns the ruleset srv answers value, failing unless it answers
// one.
func served(t *testing.T, srv *testkit.Server, value string) document {
	t.Helper()
	res := ruleset(t, srv, value)
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("GET /ruleset = %d: %s", res.StatusCode(), res.Body)
	}
	return decode(t, res.Body)
}

func on(d document, flag string) bool {
	for _, f := range d.Flags {
		if f.Key == flag {
			return f.On
		}
	}
	return false
}

const materializations = "flagnetic_ruleset_materializations_total"

func TestAWriteIsServedByTheReplicaThatTookItAtOnce(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "checkout.new-flow")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	before := served(t, srv, value)

	for _, flag := range []string{"search.ranking", "search.filters", "search.history"} {
		p.flag(t, flag)
		after := served(t, srv, value)
		if after.Version <= before.Version {
			t.Fatalf("version after a write = %d, want above %d at once", after.Version, before.Version)
		}
		before = after
	}
}

func TestTheReplicaThatTookAWriteObservesItsPropagationOnce(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	series := `flagnetic_ruleset_propagation_seconds_count{environment="` + p.environmentID(t, "production").String() + `"}`
	served(t, srv, value)

	p.flag(t, "search.ranking")
	served(t, srv, value)
	// Let the write's own notification arrive, which must not count it again.
	time.Sleep(200 * time.Millisecond)
	served(t, srv, value)
	if got := testkit.Metric(t, srv.Scrape(t), series); got != 1 {
		t.Errorf("%s = %v, want 1: the write's version, observed when first answered", series, got)
	}
}

func TestAWriteThroughOneReplicaIsServedByTheOtherWithinASecond(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	p := newProject(t, a)
	p.flag(t, "search.ranking")
	p.switchOn(t, "search.ranking", "production")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	before := served(t, b, value)

	p.kill(t, "search.ranking", "production")
	if !eventually(time.Second, func() bool { return served(t, b, value).Version > before.Version }) {
		t.Error("b still serves the old version a second after a's write")
	}
}

func TestAnUnchangedRulesetIsMaterializedOnceHoweverManyAskAtOnce(t *testing.T) {
	fault := &pgdelivery.RulesetLoadFault{}
	srv := testkit.StartServer(t, testkit.WithRulesetLoadFault(fault))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	if !eventually(5*time.Second, func() bool {
		return testkit.Metric(t, srv.Scrape(t), `flagnetic_listener_up{channel="rulesets"}`) == 1
	}) {
		t.Fatal("the rulesets listener never came up")
	}

	fault.Stall()
	var wg sync.WaitGroup
	statuses := make([]int, 8)
	for i := range statuses {
		wg.Go(func() { statuses[i] = ruleset(t, srv, value).StatusCode() })
	}
	if !eventually(time.Second, func() bool { return fault.Stalled() == 1 }) {
		t.Fatalf("stalled loads = %d, want the one every request shares", fault.Stalled())
	}
	fault.Release()
	wg.Wait()
	for i, s := range statuses {
		if s != http.StatusOK {
			t.Errorf("request %d = %d, want 200", i, s)
		}
	}
	for range 3 {
		served(t, srv, value)
	}
	if got := testkit.Metric(t, srv.Scrape(t), materializations); got != 1 {
		t.Errorf("%s = %v, want 1: one load for every request at once, then memory", materializations, got)
	}
}

func TestAReplicaWhoseListenerIsDownMaterializesEveryRequest(t *testing.T) {
	fault := &pglisten.Fault{}
	// Once found deaf, the listener waits long before reconnecting, so it
	// is still down while the requests below are answered.
	srv := testkit.StartServer(t, testkit.WithListenerOptions(pglisten.Options{
		PingInterval: 50 * time.Millisecond, PingTimeout: 50 * time.Millisecond, Fault: fault,
		MinBackoff: time.Minute, MaxBackoff: time.Minute,
	}))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	fault.Silence()
	if !eventually(5*time.Second, func() bool {
		return testkit.Metric(t, srv.Scrape(t), `flagnetic_listener_up{channel="rulesets"}`) == 0
	}) {
		t.Fatal("the listener was never found deaf")
	}
	start := testkit.Metric(t, srv.Scrape(t), materializations)
	served(t, srv, value)
	served(t, srv, value)
	if got := testkit.Metric(t, srv.Scrape(t), materializations); got-start < 2 {
		t.Errorf("materializations grew by %v over two requests, want 2: nothing is kept while the listener is down", got-start)
	}
}

func TestAReplicaThatCannotLoadTheRulesetAnswers503UntilItCan(t *testing.T) {
	fault := &pgdelivery.RulesetLoadFault{}
	srv := testkit.StartServer(t, testkit.WithRulesetLoadFault(fault))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)

	fault.Stall()
	res := ruleset(t, srv, value)
	refused(t, "while it cannot load", res.StatusCode(), res.Body, http.StatusServiceUnavailable, "ruleset_unavailable")
	if got := res.HTTPResponse.Header.Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2", got)
	}
	if fault.Stalled() != 1 {
		t.Errorf("stalled loads = %d, want the load to outlive the request that started it", fault.Stalled())
	}
	fault.Release()
	served(t, srv, value)
}

func TestStoppingAReplicaCancelsALoadNobodyWaitsFor(t *testing.T) {
	fault := &pgdelivery.RulesetLoadFault{}
	srv := testkit.StartServer(t, testkit.WithRulesetLoadFault(fault))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)

	fault.Stall()
	t.Cleanup(fault.Release)
	if res := ruleset(t, srv, value); res.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("while it cannot load: %d %s", res.StatusCode(), res.Body)
	}
	if fault.Stalled() != 1 {
		t.Fatalf("stalled loads = %d, want 1", fault.Stalled())
	}
	srv.CloseBackground()
	if fault.Stalled() != 0 {
		t.Errorf("stalled loads after stopping = %d, want none", fault.Stalled())
	}
}

func TestAfterARestoreTheReplicaServesWhatTheDatabaseHoldsAndTheNextWriteIsNumberedAboveEveryVersionSeen(t *testing.T) {
	c := testkit.Clock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	p.flag(t, "checkout.new-flow")
	p.switchOn(t, "checkout.new-flow", "production")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	restorePoint := testkit.TakeRestorePoint(t, testkit.Pool(t), p.environmentID(t, "production"))
	kept := served(t, srv, value)

	c.Advance(time.Second)
	p.kill(t, "checkout.new-flow", "production")
	lost := served(t, srv, value)
	if lost.Version <= kept.Version || on(lost, "checkout.new-flow") {
		t.Fatalf("after the kill switch: version %d, on %v", lost.Version, on(lost, "checkout.new-flow"))
	}

	restorePoint.Restore(t, srv)
	// Served at once: the replica does not wait for the version the
	// database lost.
	restored := served(t, srv, value)
	if restored.Version != kept.Version || on(restored, "checkout.new-flow") != on(kept, "checkout.new-flow") {
		t.Errorf("after the restore: version %d, want %d, what the database holds again", restored.Version, kept.Version)
	}

	c.Advance(time.Second)
	p.flag(t, "checkout.after-restore")
	if next := served(t, srv, value); next.Version <= lost.Version {
		t.Errorf("the write after the restore is version %d, want above %d, every version handed out", next.Version, lost.Version)
	}
}

func TestDeletingAnEnvironmentDropsItsPropagationSeries(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	p := newProject(t, a)
	if res := do(p.admin.CreateEnvironmentWithResponse(t.Context(), p.key, management.EnvironmentCreate{Key: "qa", Name: "QA"})); res.JSON201 == nil {
		t.Fatalf("create qa = %d: %s", res.StatusCode(), res.Body)
	}
	p.flag(t, "search.ranking")
	p.switchOn(t, "search.ranking", "qa")
	_, value := p.sdkKey(t, "qa", management.SdkKeyKindServer)
	series := `flagnetic_ruleset_propagation_seconds_count{environment="` + p.environmentID(t, "qa").String() + `"}`
	before := served(t, b, value)

	p.kill(t, "search.ranking", "qa")
	if !eventually(time.Second, func() bool { return served(t, b, value).Version > before.Version }) {
		t.Fatal("b never served the write")
	}
	if got := testkit.Metric(t, b.Scrape(t), series); got < 1 {
		t.Fatalf("%s = %v, want the versions b served", series, got)
	}

	envVersion := do(p.admin.GetEnvironmentWithResponse(t.Context(), p.key, "qa")).JSON200.Version
	deleted := do(p.admin.DeleteEnvironmentWithResponse(t.Context(), p.key, "qa",
		&management.DeleteEnvironmentParams{IfMatch: ptr(`"` + strconv.Itoa(int(envVersion)) + `"`)}))
	if deleted.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete qa = %d: %s", deleted.StatusCode(), deleted.Body)
	}
	if !eventually(time.Second, func() bool { return testkit.Metric(t, b.Scrape(t), series) == -1 }) {
		t.Error("b still reports the propagation of the deleted environment a second later")
	}
}

func TestAMemberDownloadsTheRulesetServerSDKsReceive(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "checkout.new-flow")
	_, value := p.sdkKey(t, "staging", management.SdkKeyKindServer)
	viewer := testkit.Client(t, srv, testkit.GrantedToken(t, testkit.Pool(t), p.projectID(t), access.RoleViewer))

	got := do(viewer.GetEnvironmentRulesetWithResponse(t.Context(), p.key, "staging"))
	sdkAnswer := ruleset(t, srv, value)
	if got.StatusCode() != http.StatusOK || string(got.Body) != string(sdkAnswer.Body) ||
		got.HTTPResponse.Header.Get("ETag") != sdkAnswer.HTTPResponse.Header.Get("ETag") {
		t.Errorf("management read = %d %v %s, want the server document %s with its ETag",
			got.StatusCode(), got.HTTPResponse.Header, got.Body, sdkAnswer.Body)
	}
	missing := do(viewer.GetEnvironmentRulesetWithResponse(t.Context(), p.key, "nowhere"))
	refused(t, "unknown environment", missing.StatusCode(), missing.Body, http.StatusNotFound, "not_found")
	stranger := testkit.Client(t, srv, testkit.MemberToken(t, testkit.Pool(t)))
	denied := do(stranger.GetEnvironmentRulesetWithResponse(t.Context(), p.key, "staging"))
	if denied.StatusCode() == http.StatusOK {
		t.Errorf("a member without a grant read the ruleset: %s", denied.Body)
	}
}

func TestTheManagementReadAnswers503WhileTheReplicaCannotLoadTheRuleset(t *testing.T) {
	fault := &pgdelivery.RulesetLoadFault{}
	srv := testkit.StartServer(t, testkit.WithRulesetLoadFault(fault))
	p := newProject(t, srv)

	fault.Stall()
	t.Cleanup(fault.Release)
	res := do(p.admin.GetEnvironmentRulesetWithResponse(t.Context(), p.key, "production"))
	refused(t, "while it cannot load", res.StatusCode(), res.Body, http.StatusServiceUnavailable, "ruleset_unavailable")
	if got := res.HTTPResponse.Header.Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2", got)
	}
}
