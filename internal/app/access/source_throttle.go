package access

import (
	"context"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// SourceThrottleStore counts the failed attempts of sources.
type SourceThrottleStore interface {
	// ReserveSourceAttempt counts a as a failure of its source alone, before
	// what it presents is checked, and returns what it found.
	ReserveSourceAttempt(ctx context.Context, a access.Attempt) (access.Reservation, error)
	// ReleaseAttempt takes back the failure r reserved.
	ReleaseAttempt(ctx context.Context, r access.Reservation) error
}

// SourceThrottle lets a credential other than a password, such as a SCIM
// token, count its failed attempts against the throttle of the address they
// come from, which local sign-in, invitation acceptance and password reset
// share: guessing at one cannot be traded for guessing at another. A request
// that presents no credential but makes the instance work for anyone who
// sends it, such as asking for a password reset by mail, counts against it
// too and is never taken back, so one address cannot make the instance mail
// and audit at wire speed.
type SourceThrottle struct {
	tx    Transactor
	store SourceThrottleStore
}

// NewSourceThrottle returns the throttle.
func NewSourceThrottle(tx Transactor, store SourceThrottleStore) *SourceThrottle {
	return &SourceThrottle{tx: tx, store: store}
}

// Reserve counts one failure of the source ip comes from at at, before what
// is presented is checked. A throttled source is refused rate_limited, its
// failure taken back; otherwise the function returned takes the failure back
// for an attempt that guessed nothing, even if the request was cancelled.
func (t *SourceThrottle) Reserve(ctx context.Context, ip netip.Addr, at time.Time) (func(context.Context) error, error) {
	reserved, err := t.store.ReserveSourceAttempt(ctx, access.NewAttempt("", ip, at))
	if err != nil {
		return nil, err
	}
	release := func(ctx context.Context) error {
		return t.tx.Within(context.WithoutCancel(ctx), func(ctx context.Context) error {
			return t.store.ReleaseAttempt(ctx, reserved)
		})
	}
	if refusal := reserved.Refusal(); refusal != nil {
		return nil, releaseAttempt(ctx, t.tx, t.store, reserved, refusal)
	}
	return release, nil
}
