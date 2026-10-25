package governance

import (
	"time"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

// DeliveryID identifies one delivery of an event to one subscription.
type DeliveryID = id.Of[Delivery]

// DeliveryStatus is where a delivery stands.
type DeliveryStatus string

// The delivery statuses.
const (
	// DeliveryPending is a delivery waiting for its next attempt.
	DeliveryPending DeliveryStatus = "pending"
	// DeliverySucceeded is a delivery the receiver accepted.
	DeliverySucceeded DeliveryStatus = "succeeded"
	// DeliveryFailed is a delivery the receiver refused in a way a retry
	// would not change.
	DeliveryFailed DeliveryStatus = "failed"
	// DeliveryExhausted is a delivery that ran out of attempts.
	DeliveryExhausted DeliveryStatus = "exhausted"
	// DeliveryDropped is a delivery left unsent because its subscription was
	// disabled by the platform.
	DeliveryDropped DeliveryStatus = "dropped"
)

// DeliveryStatuses are every status, in the order a delivery reaches them.
var DeliveryStatuses = []DeliveryStatus{DeliveryPending, DeliverySucceeded, DeliveryFailed, DeliveryExhausted, DeliveryDropped}

// Delivery is one event sent, or to be sent, to one subscription, with every
// attempt made.
type Delivery struct {
	ID      DeliveryID
	Webhook WebhookID
	Event   EventID
	Type    string
	// Body is the event's envelope as it is sent, encoded as JSON; a
	// redelivery sends the same bytes.
	Body     []byte
	Status   DeliveryStatus
	Attempts []DeliveryAttempt
	// NextAttemptAt is when a pending delivery is next attempted; nil once
	// it is no longer pending.
	NextAttemptAt *time.Time
	CreatedAt     time.Time
}

// DeliveryAttempt is one call made to a receiver.
type DeliveryAttempt struct {
	// N counts the delivery's attempts from 1.
	N  int
	At time.Time
	// StatusCode is the receiver's answer; nil when none came.
	StatusCode *int
	LatencyMS  int64
	// ResponseSnippet is the start of the receiver's answer.
	ResponseSnippet *string
	// Error is why no answer came, such as a timeout.
	Error *string
}

// NewDelivery returns the delivery of e to the subscription webhook, made at
// now and due at once.
func NewDelivery(deliveryID DeliveryID, webhook WebhookID, e QueuedEvent, now time.Time) Delivery {
	return Delivery{
		ID: deliveryID, Webhook: webhook, Event: e.ID, Type: e.Type, Body: e.Envelope, Status: DeliveryPending,
		Attempts: []DeliveryAttempt{}, NextAttemptAt: &now, CreatedAt: now,
	}
}
