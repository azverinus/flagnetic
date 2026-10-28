package management_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// mondayMorning is a Monday, 07:50 UTC: ten minutes before the default slot.
var mondayMorning = time.Date(2026, time.October, 26, 7, 50, 0, 0, time.UTC)

var mondayEight = testkit.WithDigestSlot(governance.DigestSlot{Weekday: time.Monday, Hour: 8})

// digests returns the report.stale_flags events the receiver was sent, each
// as its envelope.
func digests(t *testing.T, r *testkit.Receiver) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, call := range r.Calls() {
		if call.Header.Get("X-Flagnetic-Event-Type") == "report.stale_flags" {
			out = append(out, call.Envelope(t))
		}
	}
	return out
}

// digestKeys returns the keys of the stale flags a digest envelope lists.
func digestKeys(t *testing.T, envelope map[string]any) []string {
	t.Helper()
	data, _ := envelope["data"].(map[string]any)
	items, ok := data["items"].([]any)
	if !ok {
		t.Fatalf("data.items of %v is no list", envelope)
	}
	keys := []string{}
	for _, item := range items {
		keys = append(keys, item.(map[string]any)["key"].(string))
	}
	return keys
}

func tickDigest(t *testing.T, srv *testkit.Server) {
	t.Helper()
	if err := srv.Tick(t, worker.StaleFlagDigestName); err != nil {
		t.Fatalf("digest round: %v", err)
	}
}

// sendDigest sends the project's digest as c.
func sendDigest(t *testing.T, c *management.ClientWithResponses, project string) *management.SendStaleFlagDigestResponse {
	t.Helper()
	return do(c.SendStaleFlagDigestWithResponse(t.Context(), project))
}

// staleAndHealthy creates a temporary flag due for removal in five minutes,
// which is stale from then on, and a permanent one, which never is.
func staleAndHealthy(t *testing.T, c *management.ClientWithResponses, project string, now time.Time) {
	t.Helper()
	removal := now.Add(5 * time.Minute)
	createFlag(t, c, project, management.FlagCreate{
		Key: "expired", Name: "Expired", Type: management.FlagTypeBoolean,
		Lifetime: ptr(management.LifetimeTemporary), ExpectedRemovalAt: &removal,
	})
	createFlag(t, c, project, management.FlagCreate{
		Key: "healthy", Name: "Healthy", Type: management.FlagTypeBoolean, Lifetime: ptr(management.LifetimePermanent),
	})
}

func TestSendingTheDigestDeliversExactlyTheStaleFlags(t *testing.T) {
	c := clock.NewFake(mondayMorning)
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	staleAndHealthy(t, admin, p.Key, c.Now())
	c.Advance(time.Hour)
	r := testkit.StartReceiver(t)
	created := receiving(t, admin, p.Key, r, "report.stale_flags")

	res := sendDigest(t, admin, p.Key)
	if res.JSON202 == nil || len(res.JSON202.Items) != 1 {
		t.Fatalf("POST stale-flags/send = %d: %s, want 202 listing the one stale flag", res.StatusCode(), res.Body)
	}
	listed := res.JSON202.Items[0]
	if listed.Key != "expired" || len(listed.Reasons) != 1 || listed.Reasons[0] != management.StaleReasonLifetimeExceeded {
		t.Errorf("the response lists %+v", listed)
	}
	fanOut(t, srv)
	deliver(t, srv)

	calls := digests(t, r)
	if len(calls) != 1 {
		t.Fatalf("the receiver was sent %d digests, want 1", len(calls))
	}
	if !r.Calls()[0].Verifies(created.Secret) {
		t.Error("the digest is not signed under the subscription's secret")
	}
	e := calls[0]
	if got := digestKeys(t, e); len(got) != 1 || got[0] != "expired" {
		t.Errorf("digest lists %v, want [expired]", got)
	}
	project, _ := e["project"].(map[string]any)
	actor, _ := e["actor"].(map[string]any)
	if project["key"] != p.Key || actor == nil || actor["kind"] == "system" || e["environment"] != nil || e["entity"] != nil ||
		e["audit_entry_id"] != nil {
		t.Errorf("envelope %v, want the project, the caller, no environment, entity or audit entry", e)
	}
	// What the digest lists is what the report lists.
	item := e["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
	var want map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, listed)), &want); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"key", "name", "owner", "lifetime", "expected_removal_at", "reasons"} {
		if gotJSON, _ := json.Marshal(item[field]); string(gotJSON) != mustJSON(t, want[field]) {
			t.Errorf("digest %s = %s, want %s", field, gotJSON, mustJSON(t, want[field]))
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSendingTheDigestOfAProjectWithNothingStaleDeliversAnEmptyOne(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	receiving(t, admin, p.Key, r, "report.stale_flags")

	res := sendDigest(t, admin, p.Key)
	if res.JSON202 == nil || res.JSON202.Items == nil || len(res.JSON202.Items) != 0 {
		t.Fatalf("POST stale-flags/send = %d: %s, want 202 with an empty list", res.StatusCode(), res.Body)
	}
	fanOut(t, srv)
	deliver(t, srv)
	calls := digests(t, r)
	if len(calls) != 1 {
		t.Fatalf("the receiver was sent %d digests, want 1", len(calls))
	}
	if keys := digestKeys(t, calls[0]); len(keys) != 0 {
		t.Errorf("digest lists %v, want none", keys)
	}
}

func TestSendingTheDigestWithNoSubscriberKeepsNothing(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	if res := sendDigest(t, admin, p.Key); res.JSON202 == nil {
		t.Fatalf("POST stale-flags/send = %d: %s", res.StatusCode(), res.Body)
	}
	// A subscription made afterwards is never sent a backlog.
	r := testkit.StartReceiver(t)
	receiving(t, admin, p.Key, r, "report.stale_flags")
	fanOut(t, srv)
	deliver(t, srv)
	if got := digests(t, r); len(got) != 0 {
		t.Errorf("a later subscription was sent %d digests", len(got))
	}
}

func TestOnlyAMaintainerSendsTheDigest(t *testing.T) {
	srv := testkit.StartServer(t, reachable)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	r := testkit.StartReceiver(t)
	receiving(t, admin, p.Key, r, "report.stale_flags")

	for _, role := range []access.RoleName{access.RoleViewer, access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner} {
		c := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), role))
		res := sendDigest(t, c, p.Key)
		if role == access.RoleMaintainer || role == access.RoleOwner {
			if res.JSON202 == nil {
				t.Errorf("%s sends the digest: %d: %s", role, res.StatusCode(), res.Body)
			}
			continue
		}
		if pr := assertProblem(t, string(role)+" sends the digest", res.StatusCode(), res.Body, http.StatusForbidden, "forbidden"); pr.Permission == nil ||
			*pr.Permission != "webhook.manage" {
			t.Errorf("%s is refused naming %v", role, pr.Permission)
		}
	}
	fanOut(t, srv)
	deliver(t, srv)
	if got := digests(t, r); len(got) != 2 {
		t.Errorf("the receiver was sent %d digests, want the maintainer's and the owner's", len(got))
	}
	if res := sendDigest(t, admin, "nosuchproject"); res.StatusCode() != http.StatusNotFound {
		t.Errorf("sending the digest of an unknown project = %d, want 404", res.StatusCode())
	}
}

func TestTheDigestWorkerSendsOncePerSlotAcrossReplicas(t *testing.T) {
	c := clock.NewFake(mondayMorning)
	pool := testkit.Pool(t)
	first := testkit.StartServer(t, reachable, testkit.WithClock(c), testkit.WithPool(pool), mondayEight)
	second := testkit.StartServer(t, reachable, testkit.WithClock(c), testkit.WithPool(pool), mondayEight)
	admin := testkit.Client(t, first, testkit.InstanceAdminToken(t))
	stale, clean := createProject(t, admin, projectKey()), createProject(t, admin, projectKey())
	staleAndHealthy(t, admin, stale.Key, c.Now())
	createFlag(t, admin, clean.Key, management.FlagCreate{
		Key: "kept", Name: "Kept", Type: management.FlagTypeBoolean, Lifetime: ptr(management.LifetimePermanent),
	})
	staleReceiver, cleanReceiver := testkit.StartReceiver(t), testkit.StartReceiver(t)
	receiving(t, admin, stale.Key, staleReceiver, "report.stale_flags")
	receiving(t, admin, clean.Key, cleanReceiver, "*")

	tickBoth := func() {
		t.Helper()
		var wg sync.WaitGroup
		for range 4 {
			for _, srv := range []*testkit.Server{first, second} {
				wg.Go(func() { tickDigest(t, srv) })
			}
		}
		wg.Wait()
		fanOut(t, first)
		deliver(t, first)
	}

	tickBoth()
	if got := digests(t, staleReceiver); len(got) != 0 {
		t.Fatalf("ten minutes before the slot %d digests were sent", len(got))
	}

	c.Advance(15 * time.Minute) // 08:05, five minutes into the slot, the flag now past its removal
	tickBoth()
	c.Advance(30 * time.Minute) // 08:35, still the slot's hour
	tickBoth()
	sent := digests(t, staleReceiver)
	if len(sent) != 1 {
		t.Fatalf("the slot sent %d digests to the project with a stale flag, want 1", len(sent))
	}
	if keys := digestKeys(t, sent[0]); len(keys) != 1 || keys[0] != "expired" {
		t.Errorf("digest lists %v, want [expired]", keys)
	}
	actor, _ := sent[0]["actor"].(map[string]any)
	if actor["kind"] != "system" || sent[0]["audit_entry_id"] != nil {
		t.Errorf("envelope %v, want by the system with no audit entry", sent[0])
	}
	if got := digests(t, cleanReceiver); len(got) != 0 {
		t.Errorf("a project with nothing stale was sent %d digests", len(got))
	}

	c.Advance(time.Hour) // 09:35, the slot's hour is over
	tickBoth()
	if got := digests(t, staleReceiver); len(got) != 1 {
		t.Errorf("after the slot's hour %d digests were sent, want still 1", len(got))
	}

	c.Advance(7*24*time.Hour - time.Hour) // 08:35 on the next Monday
	tickBoth()
	if got := digests(t, staleReceiver); len(got) != 2 {
		t.Errorf("next week's slot: %d digests, want 2", len(got))
	}
	if got := digests(t, cleanReceiver); len(got) != 0 {
		t.Errorf("a project with nothing stale was sent %d digests in two weeks", len(got))
	}
}

func TestADigestSlotMissedIsSkippedNotSentLate(t *testing.T) {
	c := clock.NewFake(mondayMorning)
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c), mondayEight)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	staleAndHealthy(t, admin, p.Key, c.Now())
	r := testkit.StartReceiver(t)
	receiving(t, admin, p.Key, r, "report.stale_flags")

	// No replica ran during the slot's hour.
	c.Advance(2 * time.Hour)
	tickDigest(t, srv)
	c.Advance(2 * 24 * time.Hour)
	tickDigest(t, srv)
	fanOut(t, srv)
	deliver(t, srv)
	if got := digests(t, r); len(got) != 0 {
		t.Fatalf("a slot whose hour passed was sent late: %d digests", len(got))
	}

	// The next one is sent.
	c.Advance(5*24*time.Hour - time.Hour) // the next Monday, 08:50
	tickDigest(t, srv)
	fanOut(t, srv)
	deliver(t, srv)
	if got := digests(t, r); len(got) != 1 {
		t.Errorf("the next slot sent %d digests, want 1", len(got))
	}
}

func TestAWeeklyDigestIsNotReplacedBySendingOne(t *testing.T) {
	c := clock.NewFake(mondayMorning)
	srv := testkit.StartServer(t, reachable, testkit.WithClock(c), mondayEight)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	staleAndHealthy(t, admin, p.Key, c.Now())
	r := testkit.StartReceiver(t)
	receiving(t, admin, p.Key, r, "report.stale_flags")

	c.Advance(15 * time.Minute)
	if res := sendDigest(t, admin, p.Key); res.JSON202 == nil {
		t.Fatalf("send = %d: %s", res.StatusCode(), res.Body)
	}
	tickDigest(t, srv)
	fanOut(t, srv)
	deliver(t, srv)
	if got := digests(t, r); len(got) != 2 {
		t.Errorf("a sent digest and the weekly one: %d digests, want 2", len(got))
	}
}
