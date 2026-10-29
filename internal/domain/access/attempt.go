package access

import (
	"net/netip"
	"strings"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/subnet"
)

// The lockout of an email: from the fifth consecutive failure on, each
// failure locks the email, for a minute at first and twice as long with each
// further failure, up to an hour. Guessing one member's password is slow,
// and since unknown emails lock the same way, enumeration learns nothing.
//
// The lock holds against every source but the member's known ones, those
// they signed in from within KnownSourceAge: a known source's attempts are
// counted under the email and that source alone, and locked by the same
// schedule. Anyone can lock an email by guessing at it, so a lock meeting
// every source would let them keep its member, an instance admin included,
// out for as long as they keep guessing; a known source is one an attacker
// rarely shares with the member.
const (
	LockoutThreshold = 5
	firstLockout     = time.Minute
	longestLockout   = time.Hour
)

// KnownSourceAge is how long a source stays known to a member after they
// last signed in from it: long enough to span a holiday away from the desk,
// short enough that an address the member gave up, which someone else may
// hold now, stops being theirs.
const KnownSourceAge = 30 * 24 * time.Hour

// KnownSourceOf returns the source a sign-in from ip makes known to its
// member, and false when there is no address: every attempt without one
// shares the unspecified source, which is nobody's own.
func KnownSourceOf(ip netip.Addr) (netip.Prefix, bool) {
	if !ip.Unmap().IsValid() {
		return netip.Prefix{}, false
	}
	return SourceOf(ip), true
}

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

// EmailFailureRetention is how long an email's failures are kept after its
// last one once no lock holds it: long enough that a guesser pausing between
// bursts still meets the lockout, short enough that a member's forgotten
// mistakes stop counting against them.
const EmailFailureRetention = 24 * time.Hour

// FailureCutoffs are the instants before which counted failures no longer
// matter, as the retention worker selects them in bulk.
type FailureCutoffs struct {
	// Now is when the cutoffs are judged: a lock running past it keeps its
	// email's failures.
	Now time.Time
	// LastEmailFailureBy ends an unlocked email's failures when its last one
	// is at or before it.
	LastEmailFailureBy time.Time
	// SourceWindowStartBy ends a source's failures when its window opened at
	// or before it: the window is over, and the next failure opens another.
	SourceWindowStartBy time.Time
	// KnownSourceBy ends a member's known source when they last signed in
	// from it at or before it.
	KnownSourceBy time.Time
}

// AttemptCutoffs returns the cutoffs at now.
func AttemptCutoffs(now time.Time) FailureCutoffs {
	return FailureCutoffs{
		Now: now, LastEmailFailureBy: now.Add(-EmailFailureRetention), SourceWindowStartBy: now.Add(-SourceWindow),
		KnownSourceBy: now.Add(-KnownSourceAge),
	}
}

// SourceOf returns the source an address's failures are counted against:
// an IPv4 address alone, an IPv6 address with the rest of its /64, which one
// subscriber usually holds whole. Without an address, the source is the
// unspecified one.
func SourceOf(ip netip.Addr) netip.Prefix { return subnet.Of(ip) }

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

// KnownAfter returns the instant after which the member the attempt's email
// names must have signed in from its source for the source to be known.
func (a Attempt) KnownAfter() time.Time { return a.At.Add(-KnownSourceAge) }

// Reservation is what reserving a failure for an attempt before its password
// is verified found: whether the email or the source refuses the attempt,
// the failures counted with this one, and the lock it starts.
type Reservation struct {
	Attempt
	// KnownSource is whether the attempt comes from a source known to the
	// member its email names: its failures are then counted, and locked,
	// under the email and that source alone, and the email's lock does not
	// refuse it. An email no member has knows no source.
	KnownSource bool
	// LockedUntil is when the failures the attempt is counted under, locked
	// before this attempt, unlock; zero when they were not locked, and then
	// the attempt counts as a failure.
	LockedUntil time.Time
	// Failures are the consecutive failures the attempt is counted under,
	// this one included; 0 when the attempt was not counted against the
	// email.
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
// verified: a throttled source first, then a locked email, or, from a source
// known to its member, the lock of that source's own failures on it, which
// answers the same. A refused attempt takes no hashing slot.
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
