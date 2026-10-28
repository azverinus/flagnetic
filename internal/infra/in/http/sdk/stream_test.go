package sdk_test

import (
	"bufio"
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// frame is one server-sent event frame: its fields by name, a comment
// under "comment".
type frame map[string]string

// stream is a ruleset stream held open by a test.
type stream struct {
	res    *http.Response
	frames chan frame
}

// openStream opens the ruleset stream of srv, answering the response
// whatever its status; a 200's frames are read as they arrive.
func openStream(t *testing.T, srv *testkit.Server, params *sdk.StreamRulesetParams, editors ...sdk.RequestEditorFn) *stream {
	t.Helper()
	if params == nil {
		params = &sdk.StreamRulesetParams{}
	}
	req, err := sdk.NewStreamRulesetRequest(srv.URL+sdk.BaseURL+"/", params)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	req = req.WithContext(ctx)
	if params.LastEventID != nil {
		req.Header.Set("Last-Event-ID", *params.LastEventID)
	}
	for _, edit := range editors {
		if editErr := edit(ctx, req); editErr != nil {
			t.Fatal(editErr)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	s := &stream{res: res, frames: make(chan frame, 64)}
	if res.StatusCode != http.StatusOK {
		close(s.frames)
		return s
	}
	go func() {
		defer close(s.frames)
		defer func() { _ = res.Body.Close() }()
		sc := bufio.NewScanner(res.Body)
		f := frame{}
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if len(f) > 0 {
					s.frames <- f
				}
				f = frame{}
				continue
			}
			if c, ok := strings.CutPrefix(line, ":"); ok {
				f["comment"] = strings.TrimSpace(c)
				continue
			}
			name, value, _ := strings.Cut(line, ":")
			f[name] = strings.TrimPrefix(value, " ")
		}
	}()
	return s
}

// started fails unless s is a stream whose first frame is retry: 3000
// alone; the stream's timers are armed by then.
func (s *stream) started(t *testing.T) {
	t.Helper()
	if s.res.StatusCode != http.StatusOK {
		t.Fatalf("stream = %d, want 200", s.res.StatusCode)
	}
	h := s.res.Header
	if !strings.HasPrefix(h.Get("Content-Type"), "text/event-stream") || h.Get("Cache-Control") != "no-cache" || h.Get("X-Accel-Buffering") != "no" {
		t.Errorf("headers = %v, want an event stream, no-cache, unbuffered", h)
	}
	if f := s.next(t); len(f) != 1 || f["retry"] != "3000" {
		t.Fatalf("first frame = %v, want retry: 3000 alone", f)
	}
}

// next returns the next frame, failing when none arrives within 5 seconds.
func (s *stream) next(t *testing.T) frame {
	t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			t.Fatal("the stream ended, want a frame")
		}
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5 seconds")
	}
	return nil
}

// version fails unless f is a ruleset event of version.
func version(t *testing.T, f frame, version int64) {
	t.Helper()
	v := strconv.FormatInt(version, 10)
	if f["event"] != "ruleset" || f["id"] != v || f["data"] != `{"version":`+v+`}` {
		t.Errorf("frame = %v, want the ruleset event of version %s", f, v)
	}
}

// heartbeat fails unless f is the heartbeat comment.
func heartbeat(t *testing.T, f frame) {
	t.Helper()
	if len(f) != 1 || f["comment"] != "hb" {
		t.Errorf("frame = %v, want the heartbeat", f)
	}
}

// quiet fails when a frame arrives within d.
func (s *stream) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case f, ok := <-s.frames:
		if ok {
			t.Errorf("frame %v, want none", f)
		} else {
			t.Error("the stream ended, want it quiet")
		}
	case <-time.After(d):
	}
}

// ended reports whether the stream ends within d, frames before the end
// skipped.
func (s *stream) ended(d time.Duration) bool {
	deadline := time.After(d)
	for {
		select {
		case _, ok := <-s.frames:
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// currentVersion returns the version of the ruleset a server key of
// environment downloads from srv.
func currentVersion(t *testing.T, srv *testkit.Server, value string) int64 {
	t.Helper()
	res := ruleset(t, srv, value)
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("GET /ruleset = %d: %s", res.StatusCode(), res.Body)
	}
	return decode(t, res.Body).Version
}

// fakeClock returns a fake clock reading the wall clock's time.
func fakeClock() *clock.Fake { return clock.NewFake(time.Now()) }

func TestAStreamOpensWithRetryAndSendsTheCurrentVersionAtOnce(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	p.flag(t, "checkout.new-flow")
	_, serverKey := p.sdkKey(t, "production", management.SdkKeyKindServer)
	_, clientKey := p.sdkKey(t, "production", management.SdkKeyKindClient)
	v := currentVersion(t, srv, serverKey)

	for name, s := range map[string]*stream{
		"server key":              openStream(t, srv, nil, bearer(serverKey)),
		"client key":              openStream(t, srv, nil, bearer(clientKey)),
		"client key in the query": openStream(t, srv, &sdk.StreamRulesetParams{SdkKey: &clientKey}),
	} {
		t.Run(name, func(t *testing.T) {
			s.started(t)
			version(t, s.next(t), v)
		})
	}
}

func TestAServerKeyInTheQueryIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, serverKey := p.sdkKey(t, "production", management.SdkKeyKindServer)
	unknown := "fn_srv_" + strings.Repeat("a", 40)

	for name, value := range map[string]string{"a server key": serverKey, "an unknown server key": unknown} {
		s := openStream(t, srv, &sdk.StreamRulesetParams{SdkKey: &value})
		if s.res.StatusCode != http.StatusForbidden {
			t.Errorf("%s in the query: %d, want 403", name, s.res.StatusCode)
		}
	}
	res := do(client(t, srv).StreamRulesetWithResponse(t.Context(), &sdk.StreamRulesetParams{SdkKey: &serverKey}))
	refused(t, "a server key in the query", res.StatusCode(), res.Body, http.StatusForbidden, "server_key_in_query")
	res = do(client(t, srv).StreamRulesetWithResponse(t.Context(), &sdk.StreamRulesetParams{}))
	refused(t, "no key", res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_missing")
}

func TestLastEventIDDecidesWhetherTheCurrentVersionIsSent(t *testing.T) {
	c := fakeClock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	v := currentVersion(t, srv, value)

	for _, last := range []string{strconv.FormatInt(v-1, 10), "latest"} {
		s := openStream(t, srv, &sdk.StreamRulesetParams{LastEventID: &last}, bearer(value))
		s.started(t)
		version(t, s.next(t), v)
	}
	for _, last := range []string{strconv.FormatInt(v, 10), strconv.FormatInt(v+1, 10)} {
		s := openStream(t, srv, &sdk.StreamRulesetParams{LastEventID: &last}, bearer(value))
		s.started(t)
		s.quiet(t, 200*time.Millisecond)
		c.Advance(15 * time.Second)
		heartbeat(t, s.next(t))
	}
}

func TestAKillSwitchOnOneReplicaIsStreamedByAnotherWithinTwoSeconds(t *testing.T) {
	a, b := testkit.StartServer(t), testkit.StartServer(t)
	// b observes the kill's propagation only once its listener hears the
	// notification, and drops its propagation series when the listener comes
	// up: wait for it before any write, so neither falls inside the test.
	listening(t, b)
	p := newProject(t, a)
	p.flag(t, "checkout.new-flow")
	p.switchOn(t, "checkout.new-flow", "production")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	before := currentVersion(t, b, value)
	s := openStream(t, b, nil, bearer(value))
	s.started(t)
	version(t, s.next(t), before)
	series := `flagnetic_ruleset_propagation_seconds_count{environment="` + p.environmentID(t, "production").String() + `"}`
	// The series is absent until b observes a version of the environment, as
	// it does not when its first answer comes before the switch's
	// notification: an absent series counts no observation.
	count := max(testkit.Metric(t, b.Scrape(t), series), 0)

	start := time.Now()
	p.kill(t, "checkout.new-flow", "production")
	select {
	case f, ok := <-s.frames:
		if !ok {
			t.Fatal("the stream ended")
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("the kill switch took %v to be streamed, want at most 2s", took)
		}
		after := currentVersion(t, b, value)
		if after <= before {
			t.Fatalf("b serves version %d after the kill, want above %d", after, before)
		}
		version(t, f, after)
	case <-time.After(2 * time.Second):
		t.Fatal("the kill switch was not streamed by b within 2 seconds")
	}
	// The stream carries no ruleset: the SDK's download after it is the
	// answer the version's propagation is measured to.
	if got := testkit.Metric(t, b.Scrape(t), series); got != count+1 {
		t.Errorf("propagation observations = %v, want %v", got, count+1)
	}
}

func TestChangesArrivingWhileAnEnvironmentReloadsCoalesce(t *testing.T) {
	fault := &pgdelivery.RulesetLoadFault{}
	srv := testkit.StartServer(t, testkit.WithRulesetLoadFault(fault))
	// The reloads counted below must be the changes': a listener coming up
	// later would wake the environment and start one of its own.
	listening(t, srv)
	p := newProject(t, srv)
	p.flag(t, "checkout.new-flow")
	p.flag(t, "search.ranking")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	s := openStream(t, srv, nil, bearer(value))
	s.started(t)
	version(t, s.next(t), currentVersion(t, srv, value))

	fault.Stall()
	p.switchOn(t, "checkout.new-flow", "production")
	if !eventually(5*time.Second, func() bool { return fault.Stalled() == 1 }) {
		t.Fatal("the first change never started a reload")
	}
	p.switchOn(t, "search.ranking", "production")
	s.quiet(t, 300*time.Millisecond)
	fault.Release()

	f := s.next(t)
	version(t, f, currentVersion(t, srv, value))
	s.quiet(t, 300*time.Millisecond)
}

func TestAMissedChangeIsCaughtUpOnTheNextHeartbeat(t *testing.T) {
	c := fakeClock()
	fault := &pgdelivery.RulesetLoadFault{}
	srv := testkit.StartServer(t, testkit.WithClock(c), testkit.WithRulesetLoadFault(fault))
	// The reloads counted below must be the change's: a listener coming up
	// later would wake the environment and reload it on its own.
	listening(t, srv)
	p := newProject(t, srv)
	p.flag(t, "checkout.new-flow")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	s := openStream(t, srv, nil, bearer(value))
	s.started(t)
	version(t, s.next(t), currentVersion(t, srv, value))

	// Every reload of the change fails, the one retry included.
	fault.Fail()
	p.switchOn(t, "checkout.new-flow", "production")
	if !eventually(5*time.Second, func() bool { return fault.Failed() >= 2 }) {
		t.Fatal("the change's reload was not tried twice")
	}
	s.quiet(t, 200*time.Millisecond)
	// A heartbeat while the ruleset still cannot be loaded keeps the
	// stream and sends nothing but itself.
	c.Advance(15 * time.Second)
	heartbeat(t, s.next(t))
	fault.Release()

	c.Advance(15 * time.Second)
	version(t, s.next(t), currentVersion(t, srv, value))
	heartbeat(t, s.next(t))
}

func TestAStreamWhoseRulesetCannotBeLoadedIsRefusedForNow(t *testing.T) {
	fault := &pgdelivery.RulesetLoadFault{}
	srv := testkit.StartServer(t, testkit.WithRulesetLoadFault(fault))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindClient)

	fault.Fail()
	res := do(client(t, srv).StreamRulesetWithResponse(t.Context(), &sdk.StreamRulesetParams{SdkKey: &value}))
	refused(t, "a stream while the ruleset cannot be loaded", res.StatusCode(), res.Body, http.StatusServiceUnavailable, "ruleset_unavailable")
	if res.HTTPResponse.Header.Get("Retry-After") != "2" {
		t.Errorf("Retry-After = %q, want 2", res.HTTPResponse.Header.Get("Retry-After"))
	}
}

func TestHeartbeatsKeepAStreamOpenUntilItsJitteredClose(t *testing.T) {
	c := fakeClock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	s := openStream(t, srv, nil, bearer(value))
	s.started(t)
	s.next(t) // the current version

	for range 3 {
		c.Advance(15 * time.Second)
		heartbeat(t, s.next(t))
	}
	c.Advance(59*time.Minute - 45*time.Second)
	heartbeat(t, s.next(t))
	if s.ended(100 * time.Millisecond) {
		t.Fatal("the stream ended before 60 minutes")
	}
	c.Advance(6 * time.Minute)
	if !s.ended(5 * time.Second) {
		t.Error("the stream is still open after 65 minutes")
	}
}

func TestAStreamEndsWhenItsReplicaDrains(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	s := openStream(t, srv, nil, bearer(value))
	s.started(t)

	srv.Drain()
	if !s.ended(2 * time.Second) {
		t.Error("the stream is still open 2 seconds after its replica began draining")
	}
}

func TestARevokedKeysStreamIsClosedOnItsNextHeartbeat(t *testing.T) {
	c := fakeClock()
	srv := testkit.StartServer(t, testkit.WithClock(c))
	p := newProject(t, srv)
	k, value := p.sdkKey(t, "production", management.SdkKeyKindClient)
	s := openStream(t, srv, &sdk.StreamRulesetParams{SdkKey: &value})
	s.started(t)
	s.next(t) // the current version

	p.revoke(t, "production", k)
	// The replica learns of the revocation by its notification; each
	// heartbeat until then still finds the key.
	if !eventually(2*time.Second, func() bool {
		c.Advance(15 * time.Second)
		return s.ended(100 * time.Millisecond)
	}) {
		t.Error("the revoked key's stream is still open")
	}
}

func TestAVersionMissedWhileTheListenerWasDeafIsStreamedOnceItListensAgain(t *testing.T) {
	fault := &pglisten.Fault{}
	a := testkit.StartServer(t)
	b := testkit.StartServer(t, testkit.WithListenerOptions(testkit.SilenceableListener(fault)))
	p := newProject(t, a)
	p.flag(t, "checkout.new-flow")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	s := openStream(t, b, nil, bearer(value))
	s.started(t)
	s.next(t) // the current version

	deafen(t, b, fault)
	p.switchOn(t, "checkout.new-flow", "production")
	// Its notification lost, b hears of the version when its listener
	// listens again, which wakes every environment it streams.
	fault.Restore()
	version(t, s.next(t), currentVersion(t, a, value))
}

func TestAVersionMissedWhileTheListenerWasDeafIsCaughtUpByAHeartbeatWhenItsReloadFails(t *testing.T) {
	c := fakeClock()
	deaf := &pglisten.Fault{}
	load := &pgdelivery.RulesetLoadFault{}
	a := testkit.StartServer(t)
	b := testkit.StartServer(t, testkit.WithClock(c), testkit.WithRulesetLoadFault(load), testkit.WithListenerOptions(testkit.SilenceableListener(deaf)))
	p := newProject(t, a)
	p.flag(t, "checkout.new-flow")
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	s := openStream(t, b, nil, bearer(value))
	s.started(t)
	s.next(t) // the current version

	deafen(t, b, deaf)
	p.switchOn(t, "checkout.new-flow", "production")
	// The reload woken by the listener listening again fails, its retry
	// too: no notification ever names the version missed.
	load.Fail()
	deaf.Restore()
	if !eventually(5*time.Second, func() bool { return load.Failed() >= 2 }) {
		t.Fatal("the reload woken by the listener was not tried twice")
	}
	s.quiet(t, 200*time.Millisecond)
	load.Release()

	c.Advance(15 * time.Second)
	version(t, s.next(t), currentVersion(t, a, value))
	heartbeat(t, s.next(t))
}
