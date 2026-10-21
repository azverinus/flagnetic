package access

import (
	"net/netip"
	"strings"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// The lockout of an email: from the fifth consecutive failure on, each
// failure locks the email, for a minute at first and twice as long with each
// further failure, up to an hour. Guessing one member's password is slow,
// and since unknown emails lock the same way, enumeration learns nothing.
const (
	LockoutThreshold = 5
	firstLockout     = time.Minute
	longestLockout   = time.Hour
)

// LockoutWindows returns how long the failures from the threshold on lock
// the email: the first entry for the fifth failure, the second for the
// sixth, and the last for every failure after.
func LockoutWindows() []time.Duration {
	var out []time.Duration
	for w := firstLockout; ; w *= 2 {
		if w >= longestLockout {
			return append(out, longestLockout)
		}
		out = append(out, w)
	}
}

// LockoutWindow returns how long the failures-th consecutive failure on an
// email locks it, 0 below the threshold.
func LockoutWindow(failures int) time.Duration {
	if failures < LockoutThreshold {
		return 0
	}
	windows := LockoutWindows()
	return windows[min(failures-LockoutThreshold, len(windows)-1)]
}

// The throttle of a source: a hundred failures within a window of fifteen
// minutes refuse it until the window ends, so one source cannot spray
// guesses across many emails. A source's window opens at its first failure
// and does not slide; the first failure after it ends opens the next.
const (
	SourceFailureLimit = 100
	SourceWindow       = 15 * time.Minute
)

// SourceOf returns the source an address's failures are counted against:
// an IPv4 address alone, an IPv6 address with the rest of its /64, which one
// subscriber usually holds whole. Without an address, the source is the
// unspecified one.
func SourceOf(ip netip.Addr) netip.Prefix {
	ip = ip.Unmap()
	switch {
	case !ip.IsValid():
		return netip.PrefixFrom(netip.IPv4Unspecified(), 32)
	case ip.Is4():
		return netip.PrefixFrom(ip, 32)
	}
	p, _ := ip.WithZone("").Prefix(64)
	return p
}

// AttemptKey returns the key an email's failures are counted under: the
// email trimmed and in lower case, as members are found by it.
func AttemptKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// Reservation is what reserving a failure before a password is verified
// found: whether the email or the source refuses the attempt, and the
// failures counted with this one.
type Reservation struct {
	// LockedUntil is when an email locked before this attempt unlocks; zero
	// when it was not locked, and then the attempt counts as a failure.
	LockedUntil time.Time
	// Failures are the consecutive failures on the email, this one included.
	Failures int
	// SourceFailures are the failures of the source in the current window,
	// this one included.
	SourceFailures int
	// WindowStart is the start of the source's window the attempt counts in.
	WindowStart time.Time
}

// Refusal returns why the attempt is refused before its password is
// verified at now, or nil when it is verified: a throttled source first,
// then a locked email. A refused attempt takes no hashing slot.
func (r Reservation) Refusal(now time.Time) error {
	if r.SourceFailures > SourceFailureLimit {
		return apperrors.RateLimited("rate_limited", "too many failed attempts from this address; wait before trying again",
			r.WindowStart.Add(SourceWindow).Sub(now))
	}
	if r.LockedUntil.After(now) {
		wait := r.LockedUntil.Sub(now)
		return apperrors.RateLimited("locked", "too many failed sign-ins on this email; wait before trying again", wait)
	}
	return nil
}

// Locks reports whether this attempt, should it fail, locks the email.
func (r Reservation) Locks() bool { return r.LockedUntil.IsZero() && r.Failures >= LockoutThreshold }
