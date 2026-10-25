package management_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// changeNotice is the data of a change stream's change event.
type changeNotice struct {
	Kind        string           `json:"kind"`
	Key         string           `json:"key"`
	Environment *string          `json:"environment"`
	Version     *int64           `json:"version"`
	Actor       management.Actor `json:"actor"`
}

// openChanges opens the change stream of project on srv as token, resuming
// after lastEventID unless it is "". The replica is listening by then, so
// the stream does not begin with the refetch a stream opened before it
// listened is owed.
func openChanges(t *testing.T, srv *testkit.Server, token, project, lastEventID string) *testkit.EventStream {
	t.Helper()
	srv.AwaitListening(t, pggovernance.ChangesChannel)
	req := changesRequest(t, srv, project, lastEventID)
	req.Header.Set("Authorization", "Bearer "+token)
	return testkit.OpenEventStream(t, nil, req)
}

func changesRequest(t *testing.T, srv *testkit.Server, project, lastEventID string) *http.Request {
	t.Helper()
	req, err := management.NewStreamProjectChangesRequest(srv.URL+management.BaseURL+"/", project, &management.StreamProjectChangesParams{})
	if err != nil {
		t.Fatal(err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	return req
}

// nextChange returns the id and notice of the next frame, failing unless
// it is a change event.
func nextChange(t *testing.T, s *testkit.EventStream) (string, changeNotice) {
	t.Helper()
	f := s.Next(t)
	if f["event"] != "change" || f["id"] == "" {
		t.Fatalf("frame = %v, want a change event", f)
	}
	var n changeNotice
	if err := json.Unmarshal([]byte(f["data"]), &n); err != nil {
		t.Fatalf("change data %q: %v", f["data"], err)
	}
	return f["id"], n
}

// isRefetch fails unless f is a refetch event.
func isRefetch(t *testing.T, name string, f testkit.Frame) {
	t.Helper()
	if f["event"] != "refetch" || f["data"] != "{}" {
		t.Errorf("%s: frame = %v, want a refetch event", name, f)
	}
}

// assertNoticeOf fails unless n names what project's audit entry with
// entryID records, in the environment with environmentKey, at version.
func assertNoticeOf(t *testing.T, c *management.ClientWithResponses, project, entryID string, n changeNotice, environmentKey string, version *int64) {
	t.Helper()
	res := do(c.GetProjectAuditEntryWithResponse(t.Context(), project, entryID))
	if res.JSON200 == nil {
		t.Fatalf("GET audit/%s = %d %s", entryID, res.StatusCode(), res.Body)
	}
	e := res.JSON200
	got, _ := json.Marshal(n.Actor)
	want, _ := json.Marshal(e.Actor)
	if n.Kind != e.Entity.Kind || n.Key != e.Entity.Key || string(got) != string(want) {
		t.Errorf("notice %+v, want the entry's entity %+v and actor %s", n, e.Entity, want)
	}
	if orZero(n.Environment) != environmentKey {
		t.Errorf("notice of %s: environment %q, want %q", e.Action, orZero(n.Environment), environmentKey)
	}
	if (n.Version == nil) != (version == nil) || (version != nil && *n.Version != *version) {
		t.Errorf("notice of %s: version %v, want %v", e.Action, orZero(n.Version), orZero(version))
	}
}

// turnOn switches flag on in env, returning its configuration.
func turnOn(t *testing.T, c *management.ClientWithResponses, project, flag, env string) management.FlagConfiguration {
	t.Helper()
	current := getConfiguration(t, c, project, flag, env)
	put := asPut(current)
	put.On = true
	return mustPut(t, c, project, flag, env, current.Version, put)
}

// orZero returns what p points at, or the zero value for nil.
func orZero[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// assertStreamRefused fails unless s was refused with status and code.
func assertStreamRefused(t *testing.T, name string, s *testkit.EventStream, status int, code string) {
	t.Helper()
	if s.Response.StatusCode == http.StatusOK {
		t.Errorf("%s: stream = 200, want %d %s", name, status, code)
		return
	}
	defer func() { _ = s.Response.Body.Close() }()
	body, _ := io.ReadAll(s.Response.Body)
	assertProblem(t, name, s.Response.StatusCode, body, status, code)
}

func TestAConfigurationWriteOnOneReplicaIsNoticedOnAnotherWithinTwoSeconds(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	token := testkit.InstanceAdminToken(t)
	admin := testkit.Client(t, a, token)
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Name: "Checkout", Type: "boolean"})
	s := openChanges(t, b, token, p.Key, "")
	s.Started(t)

	start := time.Now()
	written := turnOn(t, admin, p.Key, "checkout", "staging")
	entryID, n := nextChange(t, s)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("notice after %v, want within 2 s", elapsed)
	}
	assertNoticeOf(t, admin, p.Key, entryID, n, "staging", &written.Version)
	if n.Kind != "flag" || n.Key != "checkout" || n.Actor.Kind != management.ActorKindToken {
		t.Errorf("notice %+v, want the flag checkout by the admin's token", n)
	}
	s.Quiet(t, 200*time.Millisecond)
}

func TestAKillSwitchAndAGrantAreNoticed(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	token := testkit.InstanceAdminToken(t)
	admin := testkit.Client(t, a, token)
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Name: "Checkout", Type: "boolean"})
	turnOn(t, admin, p.Key, "checkout", "staging")
	s := openChanges(t, b, token, p.Key, "")
	s.Started(t)

	killed := kill(t, admin, p.Key, "checkout", "staging", nil)
	if killed.JSON200 == nil {
		t.Fatalf("kill = %d %s", killed.StatusCode(), killed.Body)
	}
	entryID, n := nextChange(t, s)
	assertNoticeOf(t, admin, p.Key, entryID, n, "staging", &killed.JSON200.Configuration.Version)

	m := testkit.NewMember(t, testkit.Pool(t))
	grant(t, admin, p.Key, m.ID, string(access.RoleViewer))
	entryID, n = nextChange(t, s)
	assertNoticeOf(t, admin, p.Key, entryID, n, "", nil)
	if n.Kind != "grant" || n.Environment != nil {
		t.Errorf("grant notice %+v, want a grant in no environment", n)
	}
}

func TestLastEventIDReplaysTheEntriesAfterIt(t *testing.T) {
	srv := testkit.StartServer(t)
	token := testkit.InstanceAdminToken(t)
	admin := testkit.Client(t, srv, token)
	p := createProject(t, admin, projectKey())
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Name: "Checkout", Type: "boolean"})
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "search", Name: "Search", Type: "boolean"})
	res := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ListProjectAuditEntriesParams{Limit: ptr(2)}))
	if res.JSON200 == nil || len(res.JSON200.Items) != 2 {
		t.Fatalf("GET audit = %d %s", res.StatusCode(), res.Body)
	}
	// Newest first: the flag search was created after the entry before it.
	checkout, search := res.JSON200.Items[1], res.JSON200.Items[0]

	s := openChanges(t, srv, token, p.Key, checkout.Id.String())
	s.Started(t)
	entryID, n := nextChange(t, s)
	if entryID != search.Id.String() || n.Key != "search" {
		t.Errorf("replayed %s %+v, want the entry %s of the flag search", entryID, n, search.Id)
	}
	s.Quiet(t, 200*time.Millisecond)

	// Live notices follow the replay.
	turnOn(t, admin, p.Key, "search", "staging")
	if _, n := nextChange(t, s); n.Key != "search" || orZero(n.Environment) != "staging" {
		t.Errorf("live notice %+v, want search in staging", n)
	}

	// An entry the project does not hold cannot be replayed from: the
	// client is told to refetch.
	other := createProject(t, admin, projectKey())
	for name, tc := range map[string]struct{ project, lastEventID string }{
		"unknown":       {p.Key, "0190d2a0-0000-7000-8000-000000000000"},
		"garbled":       {p.Key, "latest"},
		"another's own": {other.Key, search.Id.String()},
	} {
		s := openChanges(t, srv, token, tc.project, tc.lastEventID)
		s.Started(t)
		isRefetch(t, name, s.Next(t))
		s.Quiet(t, 100*time.Millisecond)
	}
}

func TestAViewerAndASessionFollowChangesAndOthersAreRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	token := testkit.InstanceAdminToken(t)
	admin := testkit.Client(t, srv, token)
	p := createProject(t, admin, projectKey())

	byViewer := openChanges(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), access.RoleViewer), p.Key, "")
	byViewer.Started(t)
	developer := testkit.NewMember(t, pool)
	testkit.Grant(t, pool, projectIDOf(p), developer.ID, access.RoleDeveloper)
	browser := signedIn(t, srv, developer)
	// The browser's own client reads every answer whole, which a stream
	// never is: a client sharing its cookies holds the stream.
	bySession := testkit.OpenEventStream(t, &http.Client{Jar: browser.HTTP.Jar}, changesRequest(t, srv, p.Key, ""))
	bySession.Started(t)

	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Name: "Checkout", Type: "boolean"})
	for name, s := range map[string]*testkit.EventStream{"viewer": byViewer, "session": bySession} {
		if _, n := nextChange(t, s); n.Kind != "flag" || n.Key != "checkout" {
			t.Errorf("%s: notice %+v, want the flag checkout", name, n)
		}
	}

	outsider := testkit.NewMember(t, pool)
	outsiderToken := testkit.PersonalToken(t, pool, outsider.ID, "outsider", access.ScopeRead)
	assertStreamRefused(t, "non-member", openChanges(t, srv, outsiderToken, p.Key, ""), http.StatusForbidden, "forbidden")
	assertStreamRefused(t, "unknown project", openChanges(t, srv, token, "no-such-project", ""), http.StatusNotFound, "not_found")
}

func TestAMemberLosingTheirGrantHasTheStreamClosedAtTheNextNotice(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, a, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	viewer := testkit.NewMember(t, pool)
	g := grant(t, admin, p.Key, viewer.ID, string(access.RoleViewer))
	s := openChanges(t, b, testkit.PersonalToken(t, pool, viewer.ID, "viewer", access.ScopeRead), p.Key, "")
	s.Started(t)

	res := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, g.Id.String()))
	if res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE grant = %d %s", res.StatusCode(), res.Body)
	}
	if !s.Ended(2 * time.Second) {
		t.Error("the stream of a member whose grant was deleted is open after the next notice")
	}
}

func TestHeartbeatsKeepAChangeStreamOpenUntilItsJitteredClose(t *testing.T) {
	c := clock.NewFake(time.Now())
	srv := testkit.StartServer(t, testkit.WithClock(c))
	token := testkit.InstanceAdminToken(t)
	p := createProject(t, testkit.Client(t, srv, token), projectKey())
	s := openChanges(t, srv, token, p.Key, "")
	s.Started(t)

	for range 3 {
		c.Advance(15 * time.Second)
		if f := s.Next(t); !f.IsHeartbeat() {
			t.Fatalf("frame = %v, want the heartbeat", f)
		}
	}
	c.Advance(59*time.Minute - 45*time.Second)
	if f := s.Next(t); !f.IsHeartbeat() {
		t.Fatalf("at 59 minutes: %v, want the heartbeat", f)
	}
	if s.Ended(100 * time.Millisecond) {
		t.Fatal("the stream ended before 60 minutes")
	}
	c.Advance(6 * time.Minute)
	if !s.Ended(5 * time.Second) {
		t.Error("the stream is open after 65 minutes")
	}
}

func TestAMemberLosingTheirGrantHasTheStreamClosedAtTheNextHeartbeat(t *testing.T) {
	c := clock.NewFake(time.Now())
	fault := &pglisten.Fault{}
	a := testkit.StartServer(t)
	b := testkit.StartServer(t, testkit.WithClock(c), testkit.WithListenerOptions(pglisten.Options{
		PingInterval: 50 * time.Millisecond, PingTimeout: 50 * time.Millisecond, Fault: fault,
	}))
	pool := testkit.Pool(t)
	admin := testkit.Client(t, a, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	viewer := testkit.NewMember(t, pool)
	g := grant(t, admin, p.Key, viewer.ID, string(access.RoleViewer))
	s := openChanges(t, b, testkit.PersonalToken(t, pool, viewer.ID, "viewer", access.ScopeRead), p.Key, "")
	s.Started(t)

	// b does not hear the grant's deletion: its heartbeat finds it.
	deafen(t, b, fault)
	res := do(admin.DeleteGrantWithResponse(t.Context(), p.Key, g.Id.String()))
	if res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE grant = %d %s", res.StatusCode(), res.Body)
	}
	s.Quiet(t, 100*time.Millisecond)
	c.Advance(15 * time.Second)
	if !s.Ended(2 * time.Second) {
		t.Error("the stream of a member whose grant was deleted is open after the heartbeat")
	}
}

// deafen silences srv's listening for announced entries, returning once
// the replica has found itself deaf.
func deafen(t *testing.T, srv *testkit.Server, fault *pglisten.Fault) {
	t.Helper()
	fault.Silence()
	series := `flagnetic_listener_up{channel="` + pggovernance.ChangesChannel + `"}`
	for deadline := time.Now().Add(5 * time.Second); testkit.Metric(t, srv.Scrape(t), series) != 0; {
		if time.Now().After(deadline) {
			t.Fatal("the listener was never found deaf")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAChangeStreamEndsWhenItsReplicaDrains(t *testing.T) {
	srv := testkit.StartServer(t)
	token := testkit.InstanceAdminToken(t)
	p := createProject(t, testkit.Client(t, srv, token), projectKey())
	s := openChanges(t, srv, token, p.Key, "")
	s.Started(t)

	srv.Drain()
	if !s.Ended(2 * time.Second) {
		t.Error("the stream is open 2 seconds after its replica began draining")
	}
}

func TestChangeStreamsPerCallerAreBoundedAndCounted(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	token := testkit.InstanceAdminToken(t)
	p := createProject(t, testkit.Client(t, srv, token), projectKey())
	viewer := testkit.NewMember(t, pool)
	testkit.Grant(t, pool, projectIDOf(p), viewer.ID, access.RoleViewer)
	// Two credentials of one member share the member's slots.
	first := testkit.PersonalToken(t, pool, viewer.ID, "first", access.ScopeRead)
	second := testkit.PersonalToken(t, pool, viewer.ID, "second", access.ScopeRead)
	const open, refused = "flagnetic_management_streams", "flagnetic_management_streams_refused_total"
	metric := func(series string) float64 { return testkit.Metric(t, srv.Scrape(t), series) }

	var held []*testkit.EventStream
	for i := range 10 {
		s := openChanges(t, srv, []string{first, second}[i%2], p.Key, "")
		s.Started(t)
		held = append(held, s)
	}
	if got := metric(open); got != 10 {
		t.Errorf("%s = %v, want 10", open, got)
	}
	s := openChanges(t, srv, first, p.Key, "")
	if s.Response.Header.Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
	assertStreamRefused(t, "eleventh stream", s, http.StatusTooManyRequests, "too_many_streams")
	if got := metric(refused); got != 1 {
		t.Errorf("%s = %v, want 1", refused, got)
	}
	// Another caller's streams are their own.
	openChanges(t, srv, token, p.Key, "").Started(t)

	// Closing one frees its slot.
	held[0].Close()
	deadline := time.Now().Add(2 * time.Second)
	for metric(open) != 10 {
		if time.Now().After(deadline) {
			t.Fatal("a closed stream is still counted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	openChanges(t, srv, first, p.Key, "").Started(t)
}

func TestAChangeStreamRefetchesOnceItsReplicaListensAgain(t *testing.T) {
	fault := &pglisten.Fault{}
	a := testkit.StartServer(t)
	b := testkit.StartServer(t, testkit.WithListenerOptions(pglisten.Options{
		PingInterval: 50 * time.Millisecond, PingTimeout: 50 * time.Millisecond, Fault: fault,
	}))
	token := testkit.InstanceAdminToken(t)
	admin := testkit.Client(t, a, token)
	p := createProject(t, admin, projectKey())
	s := openChanges(t, b, token, p.Key, "")
	s.Started(t)

	deafen(t, b, fault)
	// Its announcement lost, the change reaches the stream as a refetch
	// once b listens again.
	createFlag(t, admin, p.Key, management.FlagCreate{Key: "checkout", Name: "Checkout", Type: "boolean"})
	s.Quiet(t, 200*time.Millisecond)
	fault.Restore()
	isRefetch(t, "after listening again", s.Next(t))

	createFlag(t, admin, p.Key, management.FlagCreate{Key: "search", Name: "Search", Type: "boolean"})
	if _, n := nextChange(t, s); n.Key != "search" {
		t.Errorf("notice %+v after listening again, want the flag search", n)
	}
}
