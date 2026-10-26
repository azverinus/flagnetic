package governance

import (
	"strconv"
	"time"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// The disable policy: a receiver that keeps failing is not called forever.
const (
	// DisableAfterFailures is how many attempts in a row must fail before
	// the platform disables a subscription.
	DisableAfterFailures = 25
	// DisableAfterSilence is how long a subscription must have gone without
	// a success, counted from its last success or its last enable, before
	// the platform disables it. Twenty-five failures come fast when a busy
	// project's receiver is down for minutes; a day without one success is
	// what tells a dead receiver from an outage.
	DisableAfterSilence = 24 * time.Hour
	// DeliveryRetention is how long a delivery that is no longer pending is
	// kept in its subscription's log.
	DeliveryRetention = 30 * 24 * time.Hour
)

// DisabledPersistentFailure is the reason of a subscription the platform
// disabled for failing persistently.
const DisabledPersistentFailure DisabledReason = "persistent_failure"

// WebhookPolicyActor is who the audit log and the receivers are told
// disabled a subscription: the platform itself, acting on its delivery
// attempts.
var WebhookPolicyActor = Actor{Kind: ActorSystem, ID: "webhook_delivery", Name: "webhook delivery"}

// ErrPlatformDisabled refuses an edit enabling a subscription the platform
// disabled: enabling it again is a decision of its own, which resets its
// failures, and an edit that happens to name enabled must not make it in
// passing.
var ErrPlatformDisabled = apperrors.Conflict("webhook_disabled",
	"the platform disabled the webhook subscription for failing persistently; enable it with POST .../enable")

// PersistentlyFailing reports whether the platform disables w at now: it is
// enabled, its last DisableAfterFailures attempts or more failed, and it has
// had no success for DisableAfterSilence since it was last enabled.
func (w Webhook) PersistentlyFailing(now time.Time) bool {
	alive := w.EnabledAt
	if w.LastSuccessAt != nil && w.LastSuccessAt.After(alive) {
		alive = *w.LastSuccessAt
	}
	return w.Enabled && w.ConsecutiveFailures >= DisableAfterFailures && now.Sub(alive) >= DisableAfterSilence
}

// DisableForFailure returns w disabled by the platform at now, as its next
// version, its failures kept for whoever looks into why.
func (w Webhook) DisableForFailure(now time.Time) Webhook {
	w.Enabled, w.DisabledReason = false, DisabledPersistentFailure
	w.UpdatedAt, w.Version = now, w.Version+1
	return w
}

// Enable returns w enabled at now, with no reason, no failures and its
// window starting again, and whether that changed it: an enabled
// subscription is left as it is.
func (w Webhook) Enable(now time.Time) (Webhook, bool) {
	if w.Enabled {
		return w, false
	}
	w.Enabled, w.DisabledReason, w.ConsecutiveFailures, w.EnabledAt = true, "", 0, now
	w.UpdatedAt, w.Version = now, w.Version+1
	return w, true
}

// CheckChange refuses an edit enabling a subscription the platform disabled.
func (w Webhook) CheckChange(change WebhookChange) error {
	if w.DisabledReason != "" && change.Enabled != nil && *change.Enabled {
		return ErrPlatformDisabled
	}
	return nil
}

// RotateSecret returns w signing under sealed from now, the secret it
// replaces signing beside it for overlap so a receiver can take the new one
// without losing an event. Only two secrets sign at a time: one an earlier
// rotation left overlapping is dropped.
func (w Webhook) RotateSecret(sealed string, now time.Time, overlap time.Duration) Webhook {
	expires := now.Add(overlap)
	w.PreviousSealedSecret, w.PreviousSecretExpiresAt, w.SealedSecret = w.SealedSecret, &expires, sealed
	w.UpdatedAt, w.Version = now, w.Version+1
	return w
}

// WebhookDisabledEvent returns the event telling a project's other
// subscriptions that the platform disabled w at at, after last failed. It
// names the subscription and why its last attempt failed, never its target,
// which may carry a credential and is not theirs to see.
func WebhookDisabledEvent(eventID EventID, w Webhook, projectKey string, last DeliveryAttempt, at time.Time) Event {
	actor := WebhookPolicyActor
	return Event{
		ID: eventID, Type: "webhook.disabled", Project: w.Project, ProjectKey: projectKey, OccurredAt: at,
		Actor: &actor, Entity: &Entity{Kind: "webhook", ID: w.ID.String(), Key: w.Name},
		Data: map[string]any{"subscription_id": w.ID.String(), "name": w.Name, "last_error": attemptError(last)},
	}
}

// attemptError is why a failed attempt failed: the receiver's status, or
// why no answer came.
func attemptError(a DeliveryAttempt) string {
	if a.StatusCode != nil {
		return "the receiver answered " + strconv.Itoa(*a.StatusCode)
	}
	if a.Error != nil {
		return *a.Error
	}
	return ""
}
