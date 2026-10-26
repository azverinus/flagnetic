package governance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// LeaseToken names one claim of a replica on the deliveries it leased, so
// that a replica whose lease ran out finds out the delivery is no longer its
// own.
type LeaseToken = id.Of[lease]

type lease struct{}

// Leased is a delivery a replica holds the lease on, as its attempt needs
// it.
type Leased struct {
	// Delivery carries the attempts made so far, so the next is numbered
	// after them, whichever replica made them.
	Delivery governance.Delivery
	URL      string
	// SealedSecrets sign the attempt: the subscription's secret, then the
	// one a rotation replaced while its overlap lasts.
	SealedSecrets []string
}

// DeliveryStore is what the delivery worker reads and writes.
type DeliveryStore interface {
	// ClaimDeliveries leases to token, until until, up to limit deliveries
	// due by now of enabled subscriptions, the longest due first, taking of
	// each subscription only as many as fit beside the leases of every
	// replica still running at now, up to governance.MaxDeliveriesInFlight.
	// It runs in the unit of work ctx carries and returns the deliveries
	// leased.
	ClaimDeliveries(ctx context.Context, token LeaseToken, now, until time.Time, limit int) ([]governance.DeliveryID, error)
	// RenewLease pushes the lease token holds on delivery to until and
	// returns the delivery as its attempt needs it; false when the lease is
	// another's now, the delivery settled or its subscription is no longer
	// enabled.
	RenewLease(ctx context.Context, delivery governance.DeliveryID, token LeaseToken, now, until time.Time) (Leased, bool, error)
	// LockWebhook locks the subscription's row for the unit of work ctx
	// carries, before any of its deliveries, as the claim does.
	LockWebhook(ctx context.Context, webhook governance.WebhookID) error
	// RecordAttempt appends a to delivery and sets its status and next
	// attempt, releasing the lease, only while token holds it; it reports
	// whether it did.
	RecordAttempt(ctx context.Context, delivery governance.DeliveryID, token LeaseToken, a governance.DeliveryAttempt,
		status governance.DeliveryStatus, next *time.Time, now time.Time) (bool, error)
	// CountAttempt moves the subscription's counters by an attempt made at
	// at: a success sets its last success and clears its consecutive
	// failures, a failure adds one.
	CountAttempt(ctx context.Context, webhook governance.WebhookID, succeeded bool, at time.Time) error
}

// Sent is what came of one call to a receiver.
type Sent struct {
	// StatusCode is the receiver's answer; nil when none came, and Err says
	// why.
	StatusCode *int
	// Body is the start of the answer, at most
	// governance.ResponseSnippetLimit bytes.
	Body    []byte
	Latency time.Duration
	Err     error
}

// Sender makes one call to a receiver: a POST of body with headers to url,
// within governance.DeliveryTimeout, following no redirect and connecting
// to no address the operator does not allow, whatever the name resolves to
// at the time.
type Sender interface {
	Send(ctx context.Context, url string, headers map[string]string, body []byte) Sent
}

// Opener opens a value sealed under the instance's secret key.
type Opener interface {
	Open(purpose secret.Purpose, sealed string) ([]byte, error)
}

// deliveryBatch bounds the deliveries one claim leases. A round claims
// again once it has attempted them, so it bounds the transaction, not the
// round.
const deliveryBatch = 100

// leaseSlack is how far past an attempt's timeout a lease runs, so that no
// second replica takes a delivery while the first still waits for its
// answer.
const leaseSlack = time.Minute

// ErrLeaseLost answers an attempt whose lease ran out and was taken by
// another replica, or whose delivery settled meanwhile: what came of it is
// not this replica's to record. The receiver may see the event twice, and
// deduplicates on its id.
var ErrLeaseLost = errors.New("the lease on the webhook delivery was lost")

// DeliverDeps are what the delivery worker runs on.
type DeliverDeps struct {
	Tx     Transactor
	Store  DeliveryStore
	Sender Sender
	Opener Opener
	Clock  clock.Clock
	IDs    *id.Source
	// Random draws the jitter of each retry's delay.
	Random io.Reader
}

// Deliver attempts the webhook deliveries that are due. A delivery is
// leased, not held under a transaction: the claim writes a token and a time
// onto it in a short transaction, the attempt renews the lease against the
// token immediately before the call, the call happens with no transaction
// open, and its outcome is recorded only while the token still holds. A
// replica dying mid-attempt therefore leaves the delivery to be attempted
// again once its lease runs out, which is where at-least-once comes from;
// one that comes back from a slow receiver after its lease was taken
// records nothing over the replica that took it.
type Deliver struct{ d DeliverDeps }

// NewDeliver returns the use case.
func NewDeliver(d DeliverDeps) *Deliver { return &Deliver{d: d} }

// Claim is a batch of deliveries a replica leased, each to be attempted.
type Claim struct {
	Token      LeaseToken
	Deliveries []governance.DeliveryID
}

func (h *Deliver) now() time.Time { return h.d.Clock.Now().UTC().Truncate(time.Microsecond) }

// leaseUntil is when a lease taken at now runs out.
func leaseUntil(now time.Time) time.Time { return now.Add(governance.DeliveryTimeout + leaseSlack) }

// Claim leases a batch of the deliveries due, empty when none is.
func (h *Deliver) Claim(ctx context.Context) (Claim, error) {
	c := Claim{Token: id.New[lease](h.d.IDs)}
	now := h.now()
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var err error
		c.Deliveries, err = h.d.Store.ClaimDeliveries(ctx, c.Token, now, leaseUntil(now), deliveryBatch)
		return err
	})
	return c, err
}

// Attempted is what became of one attempt.
type Attempted struct {
	// Status is what the delivery stands at after it; empty when there was
	// no attempt, the lease having gone before it.
	Status governance.DeliveryStatus
}

// Attempt makes the next attempt at delivery under token and records it.
// A batch is claimed at once but attempted a few at a time, so the lease is
// renewed immediately before the call; a delivery whose lease went
// meanwhile is not attempted, and one whose lease went during the call
// answers ErrLeaseLost.
func (h *Deliver) Attempt(ctx context.Context, token LeaseToken, delivery governance.DeliveryID) (Attempted, error) {
	now := h.now()
	l, held, err := h.d.Store.RenewLease(ctx, delivery, token, now, leaseUntil(now))
	if err != nil || !held {
		return Attempted{}, err
	}
	secrets := make([]string, len(l.SealedSecrets))
	for i, sealed := range l.SealedSecrets {
		value, openErr := h.d.Opener.Open(secret.PurposeWebhookSecret, sealed)
		if openErr != nil {
			// The lease runs out and another round tries again: a key the
			// operator restores lets it through.
			return Attempted{}, fmt.Errorf("opening a webhook subscription's secret: %w", openErr)
		}
		secrets[i] = string(value)
	}
	a := governance.DeliveryAttempt{N: len(l.Delivery.Attempts) + 1, At: now}
	sent := h.d.Sender.Send(ctx, l.URL, governance.DeliveryHeaders(l.Delivery, a.N, now, secrets), l.Delivery.Body)
	if ctx.Err() != nil {
		// The replica is stopping or the round ran out: the call was cut
		// short by this side, which says nothing about the receiver. The
		// lease runs out and the delivery is attempted again.
		return Attempted{}, ctx.Err()
	}
	a.LatencyMS = sent.Latency.Milliseconds()
	if sent.Err != nil {
		reason := sent.Err.Error()
		a.Error = &reason
	} else {
		a.StatusCode, a.ResponseSnippet = sent.StatusCode, governance.ResponseSnippet(sent.Body)
	}
	status, next, err := governance.SettleAttempt(a, h.d.Random)
	if err != nil {
		return Attempted{}, err
	}
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		// The subscription is locked before its delivery, as the claim locks
		// them, so two attempts recorded at once never wait on each other
		// in turn.
		if txErr := h.d.Store.LockWebhook(ctx, l.Delivery.Webhook); txErr != nil {
			return txErr
		}
		recorded, txErr := h.d.Store.RecordAttempt(ctx, delivery, token, a, status, next, h.now())
		if txErr != nil {
			return txErr
		}
		if !recorded {
			return ErrLeaseLost
		}
		return h.d.Store.CountAttempt(ctx, l.Delivery.Webhook, a.Succeeded(), a.At)
	})
	if err != nil {
		return Attempted{}, err
	}
	return Attempted{Status: status}, nil
}
