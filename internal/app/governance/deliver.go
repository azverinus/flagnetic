package governance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// LeaseToken names one claim of a replica on the deliveries it leased, so
// that a replica whose lease ran out finds out the delivery is no longer its
// own.
type LeaseToken = id.Of[Lease]

// Lease is a replica's hold on the deliveries of one claim.
type Lease struct{}

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
	// returns the delivery as its attempt needs it; false when the lease ran
	// out by now or is another's, the delivery settled or its subscription
	// is no longer enabled.
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
	ProjectLocker
	// SubscriptionOf returns the subscription with webhook, of whichever
	// project, or ErrNotFound.
	SubscriptionOf(ctx context.Context, webhook governance.WebhookID) (governance.Webhook, error)
	// DisableWebhook stores w's disabling by the platform: its enabled flag,
	// reason and version.
	DisableWebhook(ctx context.Context, w governance.Webhook) error
	// DropPending drops every pending delivery of webhook at now, releasing
	// any lease, so an attempt in flight records nothing.
	DropPending(ctx context.Context, webhook governance.WebhookID, now time.Time) error
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
	// Audit records the platform disabling a subscription.
	Audit AuditLog
	// Outbox takes the webhook.disabled event to the project's other
	// subscriptions.
	Outbox Outbox
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
	c := Claim{Token: id.New[Lease](h.d.IDs)}
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
	// Disabled is set when the attempt was the failure that made the
	// platform disable the subscription.
	Disabled bool
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
	if a.Succeeded() {
		return Attempted{Status: status}, nil
	}
	disabled, err := h.disableIfFailing(ctx, l.Delivery.Webhook, a)
	return Attempted{Status: status, Disabled: disabled}, err
}

// disableIfFailing disables the subscription webhook when the failed attempt
// a made it fail persistently, and reports whether it did. The counters are
// judged first without a lock, so a failure far from the policy costs one
// read. The disabling takes the project's lock before the subscription's,
// as every edit of it does, in a transaction of its own after the one that
// recorded a: taking the project's lock while holding the subscription's
// would wait on an edit waiting on this.
func (h *Deliver) disableIfFailing(ctx context.Context, webhook governance.WebhookID, a governance.DeliveryAttempt) (bool, error) {
	now := h.now()
	w, err := h.d.Store.SubscriptionOf(ctx, webhook)
	if errors.Is(err, ErrNotFound) || (err == nil && !w.PersistentlyFailing(now)) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	disabled := false
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		var txErr error
		disabled, txErr = h.disableLocked(ctx, w.Project, webhook, a, now)
		return txErr
	})
	return disabled, err
}

// disableLocked takes the locks of project and webhook, reads the
// subscription again under them, since a success, an enable or another
// attempt's disabling may have come first, and disables it when it still
// fails persistently.
func (h *Deliver) disableLocked(ctx context.Context, project workspace.ProjectID, webhook governance.WebhookID,
	a governance.DeliveryAttempt, now time.Time,
) (bool, error) {
	if !project.IsZero() {
		// A project deleted meanwhile took the subscription with it.
		err := h.d.Store.LockProject(ctx, project)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	if err := h.d.Store.LockWebhook(ctx, webhook); err != nil {
		return false, err
	}
	before, err := h.d.Store.SubscriptionOf(ctx, webhook)
	if errors.Is(err, ErrNotFound) || (err == nil && !before.PersistentlyFailing(now)) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, h.disable(ctx, before, a, now)
}

// disable switches before off for persistent failure at now, after a failed:
// its pending deliveries are dropped, including those another replica is
// attempting, whose outcome then goes unrecorded; the audit log records it
// as the platform's doing; and the project's other subscriptions wanting it
// are sent webhook.disabled, which the subscription itself, no longer
// enabled, is not.
func (h *Deliver) disable(ctx context.Context, before governance.Webhook, a governance.DeliveryAttempt, now time.Time) error {
	after := before.DisableForFailure(now)
	if err := h.d.Store.DisableWebhook(ctx, after); err != nil {
		return err
	}
	if err := h.d.Store.DropPending(ctx, after.ID, now); err != nil {
		return err
	}
	req := governance.Request{Actor: governance.WebhookPolicyActor}
	entry, err := governance.NewEntry(id.New[governance.Entry](h.d.IDs), now, req, governance.Record{
		Project: after.Project, Action: "webhook.disabled",
		Entity: governance.Entity{Kind: "webhook", ID: after.ID.String(), Key: after.Name},
		Before: webhookValues(before), After: webhookValues(after),
	})
	if err != nil {
		return err
	}
	if err = h.d.Audit.Record(ctx, entry); err != nil {
		return err
	}
	if after.Project.IsZero() {
		// The instance's subscriptions take audit.entry alone, which the
		// entry above has already queued.
		return nil
	}
	wanted, err := h.d.Outbox.WantedEventTypes(ctx, after.Project)
	if err != nil || !governance.WantsEventType(wanted, governance.EventWebhookDisabled) {
		return err
	}
	projectKey, _, err := h.d.Outbox.ScopeKeys(ctx, after.Project, workspace.EnvironmentID{})
	if err != nil {
		return err
	}
	e, err := governance.WebhookDisabledEvent(id.New[governance.Event](h.d.IDs), after, projectKey, a, now).Queue()
	if err != nil {
		return err
	}
	return h.d.Outbox.Enqueue(ctx, []governance.QueuedEvent{e})
}
