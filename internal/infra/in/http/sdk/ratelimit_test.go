package sdk_test

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/config"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// behindAProxy serves as if the test's loopback peer were a trusted proxy,
// so X-Forwarded-For names the client.
func behindAProxy() testkit.ServerOption {
	return testkit.WithEdge(func(o *flaghttp.Options) {
		o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	})
}

// fromIP sends the request as forwarded for ip.
func fromIP(ip string) sdk.RequestEditorFn { return header("X-Forwarded-For", ip) }

// limits returns a server option changing the SDK API's limits.
func limits(change func(*config.RateLimits)) testkit.ServerOption {
	return testkit.WithRateLimits(change)
}

// budget fails unless h tells limit and remaining.
func budget(t *testing.T, name string, h http.Header, limit, remaining int) {
	t.Helper()
	if h.Get("RateLimit-Limit") != strconv.Itoa(limit) || h.Get("RateLimit-Remaining") != strconv.Itoa(remaining) {
		t.Errorf("%s: RateLimit-Limit %q, RateLimit-Remaining %q, want %d and %d", name,
			h.Get("RateLimit-Limit"), h.Get("RateLimit-Remaining"), limit, remaining)
	}
}

// rateLimited fails unless an answer is 429 rate_limited of limit, telling
// how long to wait in whole seconds of at least 1 and at most a minute.
func rateLimited(t *testing.T, name string, status int, h http.Header, body []byte, limit int) {
	t.Helper()
	refused(t, name, status, body, http.StatusTooManyRequests, "rate_limited")
	if status != http.StatusTooManyRequests {
		return
	}
	budget(t, name, h, limit, 0)
	retry, err1 := strconv.Atoi(h.Get("Retry-After"))
	reset, err2 := strconv.Atoi(h.Get("RateLimit-Reset"))
	if err1 != nil || err2 != nil || retry < 1 || retry > 60 || reset != retry {
		t.Errorf("%s: Retry-After %q, RateLimit-Reset %q, want the same whole seconds in 1..60", name, h.Get("Retry-After"), h.Get("RateLimit-Reset"))
	}
	if p := problemOf(t, body); p.RetryAfter == nil || *p.RetryAfter != retry {
		t.Errorf("%s: retry_after = %v, want %d", name, p.RetryAfter, retry)
	}
}

func TestAServerKeysRulesetDownloadsAreLimitedPerKeyWhateverTheClientIP(t *testing.T) {
	c := fakeClock()
	srv := testkit.StartServer(t, testkit.WithClock(c), behindAProxy(), limits(func(l *config.RateLimits) { l.ServerRuleset = 2 }))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	_, other := p.sdkKey(t, "production", management.SdkKeyKindServer)

	for i, ip := range []string{"203.0.113.1", "203.0.113.2"} {
		res := ruleset(t, srv, value, fromIP(ip))
		if res.StatusCode() != http.StatusOK {
			t.Fatalf("download %d = %d: %s", i+1, res.StatusCode(), res.Body)
		}
		budget(t, "download "+strconv.Itoa(i+1), res.HTTPResponse.Header, 2, 1-i)
	}
	res := ruleset(t, srv, value, fromIP("203.0.113.3"))
	rateLimited(t, "the third download, from a third IP", res.StatusCode(), res.HTTPResponse.Header, res.Body, 2)
	if res := ruleset(t, srv, other); res.StatusCode() != http.StatusOK {
		t.Errorf("another key = %d, want its own budget", res.StatusCode())
	}

	c.Advance(time.Minute)
	if res := ruleset(t, srv, value); res.StatusCode() != http.StatusOK {
		t.Errorf("the next minute = %d, want a new window", res.StatusCode())
	}
}

func TestEveryLimitedAnswerTellsTheDefaultLimitAndWhatIsLeft(t *testing.T) {
	srv := testkit.StartServer(t)
	p := newProject(t, srv)
	_, serverKey := p.sdkKey(t, "production", management.SdkKeyKindServer)
	_, clientKey := p.sdkKey(t, "production", management.SdkKeyKindClient)

	res := ruleset(t, srv, serverKey)
	budget(t, "server ruleset", res.HTTPResponse.Header, 600, 599)
	// A stream's answer tells the budget of the streams held at once,
	// which is what bounds it while it is open.
	s := openStream(t, srv, nil, bearer(serverKey))
	s.started(t)
	budget(t, "server stream", s.res.Header, 1000, 999)
	s = openStream(t, srv, nil, bearer(clientKey))
	s.started(t)
	budget(t, "client stream", s.res.Header, 10, 9)
}

func TestThe31stRefusedKeyFromOneIPIsRefusedBeforeAnyKeyIsLookedUp(t *testing.T) {
	c := fakeClock()
	srv := testkit.StartServer(t, testkit.WithClock(c), behindAProxy())
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	guessed := "fn_srv_" + strings.Repeat("x", 40)

	// Requests without a key are not guesses.
	for range 40 {
		res := do(client(t, srv).GetRulesetWithResponse(t.Context(), &sdk.GetRulesetParams{}, fromIP("198.51.100.7")))
		refused(t, "no key", res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_missing")
	}
	for i := range 30 {
		res := ruleset(t, srv, guessed, fromIP("198.51.100.7"))
		refused(t, "guess "+strconv.Itoa(i+1), res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_invalid")
	}
	res := ruleset(t, srv, guessed, fromIP("198.51.100.7"))
	rateLimited(t, "the 31st guess", res.StatusCode(), res.HTTPResponse.Header, res.Body, 30)
	res = ruleset(t, srv, value, fromIP("198.51.100.7"))
	rateLimited(t, "a valid key from the guessing IP", res.StatusCode(), res.HTTPResponse.Header, res.Body, 30)
	if res := ruleset(t, srv, value, fromIP("198.51.100.8")); res.StatusCode() != http.StatusOK {
		t.Errorf("another IP = %d, want 200", res.StatusCode())
	}

	c.Advance(time.Minute)
	if res := ruleset(t, srv, value, fromIP("198.51.100.7")); res.StatusCode() != http.StatusOK {
		t.Errorf("the guessing IP the next minute = %d, want 200", res.StatusCode())
	}
}

func TestTheStreamsAClientKeyHoldsAtOnceAreLimitedPerKeyAndIP(t *testing.T) {
	srv := testkit.StartServer(t, behindAProxy(), limits(func(l *config.RateLimits) { l.ClientConcurrentStreams = 2 }))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindClient)
	params := &sdk.StreamRulesetParams{SdkKey: &value}

	first := openStream(t, srv, params, fromIP("192.0.2.1"))
	first.started(t)
	budget(t, "the first stream", first.res.Header, 2, 1)
	second := openStream(t, srv, params, fromIP("192.0.2.1"))
	second.started(t)
	budget(t, "the second stream", second.res.Header, 2, 0)
	res := do(client(t, srv).StreamRulesetWithResponse(t.Context(), params, fromIP("192.0.2.1")))
	rateLimited(t, "the third stream", res.StatusCode(), res.HTTPResponse.Header, res.Body, 2)
	other := openStream(t, srv, params, fromIP("192.0.2.2"))
	other.started(t)

	// A stream that ends gives its slot back.
	_ = first.res.Body.Close()
	if !eventually(5*time.Second, func() bool {
		res := openStream(t, srv, params, fromIP("192.0.2.1"))
		defer func() { _ = res.res.Body.Close() }()
		return res.res.StatusCode == http.StatusOK
	}) {
		t.Error("the slot of the stream that ended was never given back")
	}
}

func TestTheStreamsAServerKeyHoldsAtOnceAreLimitedPerKey(t *testing.T) {
	srv := testkit.StartServer(t, behindAProxy(), limits(func(l *config.RateLimits) { l.ServerConcurrentStreams = 1 }))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)

	openStream(t, srv, nil, bearer(value), fromIP("192.0.2.1")).started(t)
	res := do(client(t, srv).StreamRulesetWithResponse(t.Context(), &sdk.StreamRulesetParams{}, bearer(value), fromIP("192.0.2.2")))
	rateLimited(t, "a second stream from another IP", res.StatusCode(), res.HTTPResponse.Header, res.Body, 1)
}

func TestStreamConnectsOfAClientKeyAreCountedPerKeyAndIP(t *testing.T) {
	c := fakeClock()
	srv := testkit.StartServer(t, testkit.WithClock(c), behindAProxy(), limits(func(l *config.RateLimits) { l.ClientStreamConnects = 1 }))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindClient)
	params := &sdk.StreamRulesetParams{SdkKey: &value}

	s := openStream(t, srv, params, fromIP("192.0.2.1"))
	s.started(t)
	_ = s.res.Body.Close()
	res := do(client(t, srv).StreamRulesetWithResponse(t.Context(), params, fromIP("192.0.2.1")))
	rateLimited(t, "the second connect", res.StatusCode(), res.HTTPResponse.Header, res.Body, 1)
	openStream(t, srv, params, fromIP("192.0.2.2")).started(t)

	c.Advance(time.Minute)
	openStream(t, srv, params, fromIP("192.0.2.1")).started(t)
}

func TestAForwardedClientIPIsBelievedOnlyFromATrustedProxy(t *testing.T) {
	one := func(l *config.RateLimits) { l.ClientStreamConnects = 1 }
	for _, tt := range []struct {
		name     string
		opts     []testkit.ServerOption
		believed bool
	}{
		{"no trusted proxy", []testkit.ServerOption{limits(one)}, false},
		{"behind a trusted proxy", []testkit.ServerOption{limits(one), behindAProxy()}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := testkit.StartServer(t, tt.opts...)
			p := newProject(t, srv)
			_, value := p.sdkKey(t, "production", management.SdkKeyKindClient)
			params := &sdk.StreamRulesetParams{SdkKey: &value}
			openStream(t, srv, params, fromIP("192.0.2.1")).started(t)
			s := openStream(t, srv, params, fromIP("192.0.2.2"))
			if got := s.res.StatusCode == http.StatusOK; got != tt.believed {
				t.Errorf("a second connect forwarded for another IP = %d, want it served %v", s.res.StatusCode, tt.believed)
			}
		})
	}
}

func TestARefusalIsCountedByEndpointAndKeyKind(t *testing.T) {
	srv := testkit.StartServer(t, limits(func(l *config.RateLimits) {
		l.ServerRuleset, l.ClientStreamConnects, l.FailedAuthentications = 1, 1, 1
	}))
	p := newProject(t, srv)
	_, serverKey := p.sdkKey(t, "production", management.SdkKeyKindServer)
	_, clientKey := p.sdkKey(t, "production", management.SdkKeyKindClient)
	params := &sdk.StreamRulesetParams{SdkKey: &clientKey}
	guessed := "fn_srv_" + strings.Repeat("x", 40)

	ruleset(t, srv, serverKey)
	ruleset(t, srv, serverKey)
	_ = openStream(t, srv, params).res.Body.Close()
	_ = openStream(t, srv, params).res.Body.Close()
	ruleset(t, srv, guessed)
	ruleset(t, srv, guessed)

	scrape := srv.Scrape(t)
	for _, series := range []string{
		`flagnetic_sdk_rate_limited_total{endpoint="ruleset",kind="server"}`,
		`flagnetic_sdk_rate_limited_total{endpoint="stream",kind="client"}`,
		`flagnetic_sdk_rate_limited_total{endpoint="ruleset",kind="none"}`,
	} {
		if got := testkit.Metric(t, scrape, series); got != 1 {
			t.Errorf("%s = %v, want 1", series, got)
		}
	}
}

func TestALimitSwitchedOffCountsNothing(t *testing.T) {
	srv := testkit.StartServer(t, limits(func(l *config.RateLimits) {
		l.ServerRuleset, l.ServerConcurrentStreams, l.FailedAuthentications = 0, 0, 0
	}))
	p := newProject(t, srv)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)
	guessed := "fn_srv_" + strings.Repeat("x", 40)

	for range 40 {
		res := ruleset(t, srv, guessed)
		refused(t, "a guess with the limit off", res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_invalid")
	}
	res := ruleset(t, srv, value)
	if h := res.HTTPResponse.Header; res.StatusCode() != http.StatusOK || h.Get("RateLimit-Limit") != "" || h.Get("RateLimit-Remaining") != "" {
		t.Errorf("ruleset with its limit off = %d %v, want 200 without a budget", res.StatusCode(), h)
	}
	s := openStream(t, srv, nil, bearer(value))
	s.started(t)
	if s.res.Header.Get("RateLimit-Limit") != "" {
		t.Errorf("stream with its limits off: RateLimit-Limit %q, want none", s.res.Header.Get("RateLimit-Limit"))
	}
}

func TestARevokedOrExpiredKeyStillPolledDoesNotBlockItsAddress(t *testing.T) {
	srv := testkit.StartServer(t, behindAProxy())
	p := newProject(t, srv)
	revoked, revokedValue := p.sdkKey(t, "production", management.SdkKeyKindServer)
	p.revoke(t, "production", revoked)
	expired, expiredValue := p.sdkKey(t, "production", management.SdkKeyKindServer)
	p.rotate(t, "production", expired, 0)
	_, value := p.sdkKey(t, "production", management.SdkKeyKindServer)

	for i := range 40 {
		res := ruleset(t, srv, revokedValue, fromIP("198.51.100.7"))
		refused(t, "revoked "+strconv.Itoa(i+1), res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_revoked")
		res = ruleset(t, srv, expiredValue, fromIP("198.51.100.7"))
		refused(t, "expired "+strconv.Itoa(i+1), res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_expired")
	}
	if res := ruleset(t, srv, value, fromIP("198.51.100.7")); res.StatusCode() != http.StatusOK {
		t.Errorf("a valid key from the address = %d, want 200: %s", res.StatusCode(), res.Body)
	}
}

func TestAnAddressRefusedForGuessingStillReachesTheAPIWithAKeyTheReplicaHolds(t *testing.T) {
	srv := testkit.StartServer(t, behindAProxy())
	// The replica holds keys only while its listener is up, and drops them
	// when it comes up: wait for it, so the key it holds stays held.
	const sdkKeysListenerUp = `flagnetic_listener_up{channel="sdk_keys"}`
	if !eventually(5*time.Second, func() bool { return testkit.Metric(t, srv.Scrape(t), sdkKeysListenerUp) == 1 }) {
		t.Fatal("the SDK keys listener never came up")
	}
	p := newProject(t, srv)
	_, held := p.sdkKey(t, "production", management.SdkKeyKindServer)
	_, unseen := p.sdkKey(t, "production", management.SdkKeyKindServer)
	// A lookup is not kept when a notification lands while it is under way:
	// a second one is kept should a late notification of an earlier test
	// have dropped the first.
	for range 2 {
		if res := ruleset(t, srv, held, fromIP("198.51.100.8")); res.StatusCode() != http.StatusOK {
			t.Fatalf("the key from another address = %d: %s", res.StatusCode(), res.Body)
		}
	}

	guessed := "fn_srv_" + strings.Repeat("x", 40)
	for i := range 30 {
		res := ruleset(t, srv, guessed, fromIP("198.51.100.7"))
		refused(t, "guess "+strconv.Itoa(i+1), res.StatusCode(), res.Body, http.StatusUnauthorized, "sdk_key_invalid")
	}
	res := ruleset(t, srv, guessed, fromIP("198.51.100.7"))
	rateLimited(t, "the 31st guess, a key the replica never holds", res.StatusCode(), res.HTTPResponse.Header, res.Body, 30)
	if res := ruleset(t, srv, held, fromIP("198.51.100.7")); res.StatusCode() != http.StatusOK {
		t.Errorf("a key the replica holds, from the guessing address = %d, want 200: %s", res.StatusCode(), res.Body)
	}
	// A key the replica does not hold is refused before it is looked up,
	// valid or not: looking it up is what guessing would cost.
	res = ruleset(t, srv, unseen, fromIP("198.51.100.7"))
	rateLimited(t, "a key the replica does not hold, from the guessing address", res.StatusCode(), res.HTTPResponse.Header, res.Body, 30)
}
