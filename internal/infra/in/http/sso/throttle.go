package sso

import (
	"net/netip"
	"sync"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/subnet"
)

const (
	// startsPerWindow is how many sign-ins one client address may start in a
	// window. Starting one writes an attempt, signs an AuthnRequest with RSA
	// for a SAML provider and may fetch a discovery document, all for a
	// request that proves nothing: a person signs in a few times a minute at
	// most, even behind an address shared by an office.
	startsPerWindow = 60
	// startWindowLength is the length of the fixed window starts are counted in.
	startWindowLength = time.Minute
	// sweepAt is the number of counted addresses above which the ended
	// windows are dropped, so addresses no longer seen do not grow the
	// replica's memory.
	sweepAt = 1024
)

// startThrottle counts the sign-ins started from each client address, in
// memory on each replica; an IPv6 client is counted as subnet.Of groups it.
type startThrottle struct {
	clock clock.Clock

	mu      sync.Mutex
	windows map[netip.Prefix]*startCount
	swept   time.Time
}

// startCount is the count of one fixed window, which begins with the first
// start counted in it.
type startCount struct {
	begun time.Time
	count int
}

func newStartThrottle(c clock.Clock) *startThrottle {
	if c == nil {
		c = clock.System{}
	}
	return &startThrottle{clock: c, windows: map[netip.Prefix]*startCount{}}
}

// allow counts a start from ip and refuses it as rate_limited, saying how
// long to wait, once ip has started startsPerWindow in the current window.
func (t *startThrottle) allow(ip netip.Addr) error {
	now := t.clock.Now()
	key := subnet.Of(ip)
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.windows) >= sweepAt && !now.Before(t.swept.Add(startWindowLength)) {
		t.swept = now
		for k, w := range t.windows {
			if !now.Before(w.begun.Add(startWindowLength)) {
				delete(t.windows, k)
			}
		}
	}
	w := t.windows[key]
	if w == nil || !now.Before(w.begun.Add(startWindowLength)) {
		w = &startCount{begun: now}
		t.windows[key] = w
	}
	if w.count >= startsPerWindow {
		return apperrors.RateLimited("rate_limited", "too many sign-ins started from this address; wait a moment", w.begun.Add(startWindowLength).Sub(now))
	}
	w.count++
	return nil
}
