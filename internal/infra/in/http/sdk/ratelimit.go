package sdk

import (
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/infra/config"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// endpoint names an operation of the SDK API as its limits count it.
type endpoint string

const (
	endpointRuleset  endpoint = "ruleset"
	endpointEvaluate endpoint = "evaluate"
	endpointEvents   endpoint = "events"
	endpointStream   endpoint = "stream"
)

const (
	// rateWindow is the length of the fixed windows the per-minute limits
	// count in.
	rateWindow = time.Minute
	// sweepAt is the number of windows above which the ended ones are
	// swept, so the counters of keys and addresses no longer seen do not
	// grow the replica's memory.
	sweepAt = 1024
	// noKeyKind labels a refusal made before any key is known.
	noKeyKind = "none"
)

// limiter counts the requests of the SDK API against its limits, in memory
// on each replica: fixed one-minute windows per endpoint and key (and
// client IP for a client key), the refused keys per client IP, and the
// streams each key holds open.
type limiter struct {
	limits  config.RateLimits
	clock   clock.Clock
	refused *prometheus.CounterVec

	mu       sync.Mutex
	windows  map[string]*window
	failures map[netip.Addr]*window
	streams  map[string]int
}

// window is the count of one fixed window, which begins with the first
// request counted in it.
type window struct {
	start time.Time
	count int
}

func newLimiter(limits config.RateLimits, c clock.Clock, reg prometheus.Registerer) *limiter {
	l := &limiter{
		limits: limits, clock: c,
		refused: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "sdk", Name: "rate_limited_total",
			Help: "SDK API requests refused 429 rate_limited, by endpoint and key kind (none for a refusal of the client IP before its key is known).",
		}, []string{"endpoint", "kind"}),
		windows: map[string]*window{}, failures: map[netip.Addr]*window{}, streams: map[string]int{},
	}
	reg.MustRegister(l.refused)
	return l
}

// perMinute returns the per-minute limit of ep for a key of kind, 0 when
// none applies: a server key's stream connects are bounded by its
// concurrent streams alone, and a client key never reaches the ruleset.
func (l *limiter) perMinute(ep endpoint, kind delivery.SDKKeyKind) int {
	switch {
	case kind == delivery.SDKKeyClient && ep == endpointEvaluate:
		return l.limits.ClientEvaluate
	case kind == delivery.SDKKeyClient && ep == endpointEvents:
		return l.limits.ClientEvents
	case kind == delivery.SDKKeyClient && ep == endpointStream:
		return l.limits.ClientStreamConnects
	case kind == delivery.SDKKeyServer && ep == endpointRuleset:
		return l.limits.ServerRuleset
	case kind == delivery.SDKKeyServer && ep == endpointEvaluate:
		return l.limits.ServerEvaluate
	case kind == delivery.SDKKeyServer && ep == endpointEvents:
		return l.limits.ServerEvents
	}
	return 0
}

// concurrent returns the limit of the streams a key of kind holds at once.
func (l *limiter) concurrent(kind delivery.SDKKeyKind) int {
	if kind == delivery.SDKKeyClient {
		return l.limits.ClientConcurrentStreams
	}
	return l.limits.ServerConcurrentStreams
}

// counterKey is what a key's requests to ep are counted under: a client
// key ships in many apps, so each client IP using it is counted apart, and
// one abusive install cannot use up every other's budget.
func counterKey(ep endpoint, key delivery.SDKKey, ip netip.Addr) string {
	k := string(ep) + "\x00" + key.ID.String()
	if key.Kind == delivery.SDKKeyClient {
		k += "\x00" + ip.String()
	}
	return k
}

// current returns the window of w that now falls in, a new one when it
// has ended. l.mu is held.
func current(w *window, now time.Time) *window {
	if w == nil || !now.Before(w.start.Add(rateWindow)) {
		return &window{start: now}
	}
	return w
}

// sweep drops the ended windows of m once it holds sweepAt of them. l.mu
// is held.
func sweep[K comparable](m map[K]*window, now time.Time) {
	if len(m) < sweepAt {
		return
	}
	for k, w := range m {
		if !now.Before(w.start.Add(rateWindow)) {
			delete(m, k)
		}
	}
}

// guessing refuses a request from ip once ip's refused keys reached their
// limit in the current window, before its key is looked up.
func (l *limiter) guessing(w http.ResponseWriter, ep endpoint, ip netip.Addr) error {
	limit := l.limits.FailedAuthentications
	if limit == 0 {
		return nil
	}
	now := l.clock.Now()
	l.mu.Lock()
	f := l.failures[ip]
	if f == nil || !now.Before(f.start.Add(rateWindow)) || f.count < limit {
		l.mu.Unlock()
		return nil
	}
	reset := f.start.Add(rateWindow).Sub(now)
	l.mu.Unlock()
	return l.refuse(w, ep, noKeyKind, limit, reset)
}

// failed counts a key refused for a request from ip.
func (l *limiter) failed(ip netip.Addr) {
	if l.limits.FailedAuthentications == 0 {
		return
	}
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	sweep(l.failures, now)
	f := current(l.failures[ip], now)
	f.count++
	l.failures[ip] = f
}

// take counts a request of key from ip to ep against its per-minute limit
// and, for a stream, holds one of the key's stream slots until release is
// called. It sets the headers telling the SDK its budget, or refuses the
// request.
func (l *limiter) take(w http.ResponseWriter, ep endpoint, key delivery.SDKKey, ip netip.Addr) (release func(), err error) {
	if limit := l.perMinute(ep, key.Kind); limit > 0 {
		now := l.clock.Now()
		ck := counterKey(ep, key, ip)
		l.mu.Lock()
		sweep(l.windows, now)
		win := current(l.windows[ck], now)
		l.windows[ck] = win
		if win.count >= limit {
			reset := win.start.Add(rateWindow).Sub(now)
			l.mu.Unlock()
			return nil, l.refuse(w, ep, string(key.Kind), limit, reset)
		}
		win.count++
		remaining := limit - win.count
		l.mu.Unlock()
		setBudget(w, limit, remaining)
	}
	if ep != endpointStream {
		return func() {}, nil
	}
	return l.hold(w, key, ip)
}

// hold takes one of the stream slots of key from ip. A stream's answer
// tells the budget of its slots, which is what bounds it while it is open.
func (l *limiter) hold(w http.ResponseWriter, key delivery.SDKKey, ip netip.Addr) (func(), error) {
	limit := l.concurrent(key.Kind)
	if limit == 0 {
		return func() {}, nil
	}
	sk := counterKey(endpointStream, key, ip)
	l.mu.Lock()
	held := l.streams[sk]
	if held >= limit {
		l.mu.Unlock()
		// No window ends a held stream: the SDK tries again shortly.
		return nil, l.refuse(w, endpointStream, string(key.Kind), limit, time.Second)
	}
	l.streams[sk] = held + 1
	l.mu.Unlock()
	setBudget(w, limit, limit-held-1)
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.streams[sk]--; l.streams[sk] <= 0 {
				delete(l.streams, sk)
			}
		})
	}, nil
}

// setBudget tells the SDK its limit and what is left of it.
func setBudget(w http.ResponseWriter, limit, remaining int) {
	h := w.Header()
	h.Set("RateLimit-Limit", strconv.Itoa(limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(remaining))
}

// refuse counts a refusal and returns its problem, with the headers telling
// the SDK how long to wait: whole seconds, rounded up and at least one, so
// an SDK never retries before the window ends.
func (l *limiter) refuse(w http.ResponseWriter, ep endpoint, kind string, limit int, reset time.Duration) error {
	l.refused.WithLabelValues(string(ep), kind).Inc()
	seconds := max(int(math.Ceil(reset.Seconds())), 1)
	setBudget(w, limit, 0)
	w.Header().Set("RateLimit-Reset", strconv.Itoa(seconds))
	return apperrors.RateLimited("rate_limited",
		"too many requests for this SDK key or client address; retry after the time Retry-After gives",
		time.Duration(seconds)*time.Second)
}
