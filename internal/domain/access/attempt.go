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

// LockoutWindow returns how long the failures-th consecutive failure on an
// email locks it, 0 below the threshold.
func LockoutWindow(failures int) time.Duration {
	if failures < LockoutThreshold {
		return 0
	}
	w := firstLockout
	for range failures - LockoutThreshold {
		if w >= longestLockout {
			break
		}
		w *= 2
	}
	return min(w, longestLockout)
}

// LockoutSchedule returns LockoutWindow of each consecutive failure, the
// first entry for the first failure, up to the first failure that locks for
// the longest window, which every failure after it locks for too. The store
// counting failures locks an email by it in the same step as it counts, so
// the lock it stores is the one this function decides.
func LockoutSchedule() []time.Duration {
	var out []time.Duration
	for n := 1; ; n++ {
		w := LockoutWindow(n)
		out = append(out, w)
		if w == longestLockout {
			return out
		}
	}
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

// Attempt is one attempt at a password, counted under its email and its
// source.
type Attempt struct {
	// Email is the email the attempt names, trimmed and in lower case, as
	// members are found by it.
	Email string
	// Source is the source the attempt comes from, as SourceOf gives it.
	Source netip.Prefix
	// At is when the attempt is made; it is judged at that time throughout.
	At time.Time
}

// NewAttempt returns the attempt naming email from the address ip at at.
func NewAttempt(email string, ip netip.Addr, at time.Time) Attempt {
	return Attempt{Email: strings.ToLower(strings.TrimSpace(email)), Source: SourceOf(ip), At: at}
}

// Reservation is what reserving a failure for an attempt before its password
// is verified found: whether the email or the source refuses the attempt,
// the failures counted with this one, and the lock it starts.
type Reservation struct {
	Attempt
	// LockedUntil is when an email locked before this attempt unlocks; zero
	// when it was not locked, and then the attempt counts as a failure.
	LockedUntil time.Time
	// Failures are the consecutive failures on the email, this one
	// included; 0 when the attempt was not counted against the email.
	Failures int
	// LocksUntil is when the lock this attempt's failure starts ends, as
	// stored; zero when it starts none.
	LocksUntil time.Time
	// SourceFailures are the failures of the source in the current window,
	// this one included.
	SourceFailures int
	// WindowStart is the start of the source's window the attempt counts in.
	WindowStart time.Time
}

// Refusal returns why the attempt is refused before its password is
// verified, judged at the time the attempt was reserved, or nil when it is
// verified: a throttled source first, then a locked email. A refused attempt
// takes no hashing slot.
func (r Reservation) Refusal() error {
	if r.SourceFailures > SourceFailureLimit {
		return apperrors.RateLimited("rate_limited", "too many failed attempts from this address; wait before trying again",
			r.WindowStart.Add(SourceWindow).Sub(r.At))
	}
	if r.LockedUntil.After(r.At) {
		return apperrors.RateLimited("locked", "too many failed sign-ins on this email; wait before trying again",
			r.LockedUntil.Sub(r.At))
	}
	return nil
}

// Locks reports whether this attempt, should it fail, locks the email, and
// for how long.
func (r Reservation) Locks() (bool, time.Duration) {
	if r.LocksUntil.IsZero() {
		return false, 0
	}
	return true, r.LocksUntil.Sub(r.At)
}
