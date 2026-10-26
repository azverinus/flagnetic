package governance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
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

// Redeliver returns the delivery of d's event again, as deliveryID, made at
// now and due at once: the same event, type and body byte for byte, with no
// attempts, so a receiver deduplicating on the event id recognises it and
// one that lost it takes it.
func (d Delivery) Redeliver(deliveryID DeliveryID, now time.Time) Delivery {
	return Delivery{
		ID: deliveryID, Webhook: d.Webhook, Event: d.Event, Type: d.Type, Body: d.Body, Status: DeliveryPending,
		Attempts: []DeliveryAttempt{}, NextAttemptAt: &now, CreatedAt: now,
	}
}

// The bounds of delivery.
const (
	// MaxDeliveryAttempts is how many attempts a delivery gets before it is
	// exhausted.
	MaxDeliveryAttempts = 8
	// DeliveryTimeout bounds one attempt, from dialling to the end of the
	// answer.
	DeliveryTimeout = 10 * time.Second
	// MaxDeliveriesInFlight is how many deliveries of one subscription are
	// attempted at once, counting every replica's, so a busy project never
	// floods a receiver and one slow receiver holds few of a replica's
	// attempts.
	MaxDeliveriesInFlight = 4
	// ResponseSnippetLimit is how much of a receiver's answer an attempt
	// keeps.
	ResponseSnippetLimit = 1024
	// retryJitter moves a retry either way by up to this fraction of its
	// delay, so deliveries failing on one outage do not come back in one
	// burst.
	retryJitter = 0.2
)

// retryDelays are the waits after each failed attempt, about 21 hours in
// all across the eight attempts.
var retryDelays = []time.Duration{
	10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour,
}

// RetryDelay returns how long a delivery waits after its attempt n, counted
// from 1, moved by up to 20 % either way by a draw from random. An attempt
// past the last delay waits the last one again.
func RetryDelay(n int, random io.Reader) (time.Duration, error) {
	var b [8]byte
	if _, err := io.ReadFull(random, b[:]); err != nil {
		return 0, fmt.Errorf("drawing a retry's jitter: %w", err)
	}
	// The top 53 bits make a fraction in [0, 1) every float64 holds exactly.
	u := float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53)
	base := retryDelays[min(max(n, 1), len(retryDelays))-1]
	return time.Duration(float64(base) * (1 - retryJitter + 2*retryJitter*u)), nil
}

// Succeeded reports whether the receiver accepted the call: a 2xx answer.
func (a DeliveryAttempt) Succeeded() bool {
	return a.StatusCode != nil && *a.StatusCode >= 200 && *a.StatusCode <= 299
}

// Retryable reports whether a failed in a way another attempt may change:
// no answer came, or the receiver answered 408, 429 or 5xx. Any other
// status is the receiver saying it understood the call and refuses it; a
// redirect is one too, since it would send the event to a target nobody
// reviewed.
func (a DeliveryAttempt) Retryable() bool {
	if a.StatusCode == nil {
		return true
	}
	c := *a.StatusCode
	return c == 408 || c == 429 || (c >= 500 && c <= 599)
}

// SettleAttempt returns what a delivers its delivery to: succeeded, failed
// without a retry, exhausted on the last attempt, or pending again after the
// backoff, which draws its jitter from random.
func SettleAttempt(a DeliveryAttempt, random io.Reader) (DeliveryStatus, *time.Time, error) {
	switch {
	case a.Succeeded():
		return DeliverySucceeded, nil, nil
	case !a.Retryable():
		return DeliveryFailed, nil, nil
	case a.N >= MaxDeliveryAttempts:
		return DeliveryExhausted, nil, nil
	}
	delay, err := RetryDelay(a.N, random)
	if err != nil {
		return "", nil, err
	}
	next := a.At.Add(delay)
	return DeliveryPending, &next, nil
}

// ResponseSnippet returns the start of a receiver's answer as an attempt
// keeps it: at most ResponseSnippetLimit bytes of valid UTF-8, so a binary
// body cannot break the JSON the delivery log answers; nil for an empty
// body.
func ResponseSnippet(body []byte) *string {
	if len(body) == 0 {
		return nil
	}
	s := strings.ToValidUTF8(string(body), "�")
	for len(s) > ResponseSnippetLimit {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return &s
}

// The headers every call to a receiver carries.
const (
	// HeaderEvent is the event's id, which a receiver deduplicates on.
	HeaderEvent = "X-Flagnetic-Event"
	// HeaderEventType is the event's catalogue type.
	HeaderEventType = "X-Flagnetic-Event-Type"
	// HeaderDelivery is the delivery's id, the same on each of its
	// attempts.
	HeaderDelivery = "X-Flagnetic-Delivery"
	// HeaderAttempt is the attempt's number, from 1.
	HeaderAttempt = "X-Flagnetic-Attempt"
	// HeaderTimestamp is when the call was signed, in Unix seconds, the
	// first half of what is signed.
	HeaderTimestamp = "X-Flagnetic-Timestamp"
	// HeaderSignature is one `v1=` signature per secret signing, comma
	// separated.
	HeaderSignature = "X-Flagnetic-Signature"
)

// SignatureVersion prefixes every signature, so a later scheme can be added
// without a receiver mistaking one for the other.
const SignatureVersion = "v1"

// SignatureTimestamp returns t as HeaderTimestamp carries it: Unix seconds.
func SignatureTimestamp(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

// Sign returns HeaderSignature for body sent at timestamp: for each secret
// in turn, `v1=` and the lower-case hex of HMAC-SHA256 over
// "{timestamp}.{body}" keyed by the secret's own bytes, comma separated.
// Outside a rotation's overlap one secret signs; during it both do, and a
// receiver holding either verifies.
func Sign(timestamp string, body []byte, secrets ...string) string {
	values := make([]string, len(secrets))
	for i, s := range secrets {
		mac := hmac.New(sha256.New, []byte(s))
		mac.Write([]byte(timestamp + "."))
		mac.Write(body)
		values[i] = SignatureVersion + "=" + hex.EncodeToString(mac.Sum(nil))
	}
	return strings.Join(values, ",")
}

// DeliveryHeaders returns the headers of attempt n at d, made at at and
// signed by secrets.
func DeliveryHeaders(d Delivery, n int, at time.Time, secrets []string) map[string]string {
	timestamp := SignatureTimestamp(at)
	return map[string]string{
		"Content-Type":  "application/json",
		HeaderEvent:     d.Event.String(),
		HeaderEventType: d.Type,
		HeaderDelivery:  d.ID.String(),
		HeaderAttempt:   strconv.Itoa(n),
		HeaderTimestamp: timestamp,
		HeaderSignature: Sign(timestamp, d.Body, secrets...),
	}
}

// PingEvent returns the test event a member sends to project's
// subscription: the project and the caller, no environment, entity or audit
// entry, and empty data.
func PingEvent(eventID EventID, project workspace.ProjectID, projectKey string, actor Actor, at time.Time) Event {
	return Event{
		ID: eventID, Type: EventPing, Project: project, ProjectKey: projectKey, OccurredAt: at, Actor: &actor,
		Data: map[string]any{},
	}
}
