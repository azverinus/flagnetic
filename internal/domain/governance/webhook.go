package governance

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// WebhookID identifies a webhook subscription.
type WebhookID = id.Of[Webhook]

// The event types with a meaning of their own.
const (
	// EventAuditEntry is every audit entry itself, for a copy of the log
	// kept outside the platform. It doubles every change, so `*` leaves it
	// out and a subscription asks for it by name.
	EventAuditEntry = "audit.entry"
	// EventPing is the test call a member sends to check a receiver.
	EventPing = "ping"
	// EventAll in a subscription's event types matches every type but
	// EventAuditEntry.
	EventAll = "*"
)

// EventType is one type of the event catalogue and what emits it.
type EventType struct {
	Type        string
	Description string
}

// catalogue is every event type a subscription may name, in the order the
// API lists them.
var catalogue = []EventType{
	{"flag.created", "A flag was created."},
	{"flag.updated", "A flag's definition changed, or the flag was restored."},
	{"flag.deleted", "A flag was archived or deleted."},
	{"flag.switched_on", "A flag was switched on in an environment."},
	{"flag.switched_off", "A flag was switched off in an environment, other than by the kill switch."},
	{"flag.kill_switch", "A flag was switched off in an environment with the kill switch."},
	{"flag.rules_changed", "A flag's rules, rollouts, overrides, prerequisites, fallthrough or off variation changed in an environment."},
	{"flag.reverted", "A flag's configuration in an environment was reverted to an earlier version."},
	{"segment.created", "A segment was created."},
	{"segment.updated", "A segment changed."},
	{"segment.deleted", "A segment was deleted."},
	{"experiment.started", "An experiment started."},
	{"experiment.stopped", "An experiment stopped."},
	{"experiment.archived", "An experiment was archived."},
	{"experiment.traffic_allocation_changed", "A running experiment's traffic allocation grew."},
	{"experiment.srm_detected", "The sample ratio check raised an alert for an experiment in an environment."},
	{"experiment.srm_cleared", "The sample ratio check cleared its alert."},
	{"experiment.no_exposures", "A running experiment saw no exposures in an environment for a day."},
	{"experiment.exposure_conflicts", "Units were exposed to more than one variation of an experiment."},
	{"experiment.past_expected_end", "A running experiment passed its expected end."},
	{"change_request.opened", "A change request was opened for review."},
	{"change_request.approved", "A change request was approved."},
	{"change_request.rejected", "A change request was rejected."},
	{"change_request.applied", "A change request was applied."},
	{"change_request.cancelled", "A change request was cancelled."},
	{"change_request.superseded", "A change request was superseded by a change to what it was based on."},
	{"change_request.scheduled", "A change request was approved with a time to apply it at."},
	{"change_request.schedule_failed", "The scheduler stopped retrying a scheduled change request that failed to apply."},
	{"grant.created", "A member was granted a role in the project."},
	{"grant.deleted", "A member's grant of a role in the project was removed."},
	{"sdk_key.created", "An SDK key was created; its value is never sent."},
	{"sdk_key.revoked", "An SDK key was revoked."},
	{"webhook.disabled", "Another subscription of the project was disabled by the platform after failing persistently."},
	{"report.stale_flags", "The stale flags digest, sent weekly or on demand."},
	{EventAuditEntry, "Every audit entry, for export. `*` does not match it."},
	{EventPing, "A test call sent on demand to check a receiver."},
}

// EventCatalogue returns every event type a subscription may name.
func EventCatalogue() []EventType { return slices.Clone(catalogue) }

// knownEventType reports whether t is a catalogue type.
func knownEventType(t string) bool {
	return slices.ContainsFunc(catalogue, func(e EventType) bool { return e.Type == t })
}

// WantsEventType reports whether a subscription naming events wants an event
// of typ.
func WantsEventType(events []string, typ string) bool {
	if slices.Contains(events, typ) {
		return true
	}
	return typ != EventAuditEntry && slices.Contains(events, EventAll)
}

// DisabledReason is why the platform disabled a subscription; empty when a
// member did, or when it is enabled.
type DisabledReason string

// Webhook is a webhook subscription: a standing request to be sent chosen
// events over signed HTTP.
type Webhook struct {
	ID WebhookID
	// Project is the project subscribed to; zero for the instance.
	Project workspace.ProjectID
	WebhookInput
	DisabledReason DisabledReason
	// SealedSecret is the secret signing every call, sealed under the
	// instance's secret key; the value itself is shown once.
	SealedSecret string
	// PreviousSealedSecret is the secret a rotation replaced, which signs
	// too until PreviousSecretExpiresAt; empty outside a rotation's overlap.
	PreviousSealedSecret    string
	PreviousSecretExpiresAt *time.Time
	// CreatedBy is the member or the project token that created the
	// subscription; nil when neither did.
	CreatedBy           *Author
	LastSuccessAt       *time.Time
	ConsecutiveFailures int
	// EnabledAt is when the subscription was last enabled, from which its
	// failures are judged.
	EnabledAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// WebhookInput is what a member chooses of a subscription.
type WebhookInput struct {
	Name string
	URL  string
	// Events are the types wanted, EventAll among them.
	Events []string
	// Environments are the keys of the environments whose events are
	// wanted; empty for every one.
	Environments []string
	Enabled      bool
}

// WebhookChange is an edit of a subscription; a nil field is left as it is.
type WebhookChange struct {
	Name         *string
	URL          *string
	Events       *[]string
	Environments *[]string
	Enabled      *bool
}

// With returns in with change applied.
func (in WebhookInput) With(change WebhookChange) WebhookInput {
	if change.Name != nil {
		in.Name = *change.Name
	}
	if change.URL != nil {
		in.URL = *change.URL
	}
	if change.Events != nil {
		in.Events = *change.Events
	}
	if change.Environments != nil {
		in.Environments = *change.Environments
	}
	if change.Enabled != nil {
		in.Enabled = *change.Enabled
	}
	return in
}

// Same reports whether in and other choose the same subscription.
func (in WebhookInput) Same(other WebhookInput) bool {
	return in.Name == other.Name && in.URL == other.URL && in.Enabled == other.Enabled &&
		slices.Equal(in.Events, other.Events) && slices.Equal(in.Environments, other.Environments)
}

// TargetPolicy is what the operator allows a subscription's target to be.
type TargetPolicy struct {
	// AllowHTTP accepts plain http targets.
	AllowHTTP bool
	// AllowPrivateNetworks accepts targets on loopback, private,
	// link-local, unspecified and multicast addresses.
	AllowPrivateNetworks bool
}

const maxWebhookNameLength = 200

var (
	errInsecureURL = apperrors.Invalid("insecure_url",
		"the target must be https; the operator allows http with FLAGNETIC_WEBHOOK_ALLOW_HTTP",
		apperrors.Violation{Pointer: "/url", Code: "insecure_url", Detail: "the target must be https"})
	errPrivateNetworkTarget = apperrors.Invalid("private_network_target",
		"the target resolves to a loopback, private, link-local, unspecified or multicast address; "+
			"the operator allows those with FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS",
		apperrors.Violation{Pointer: "/url", Code: "private_network_target",
			Detail: "the target is not on the public internet"})
)

// CheckWebhook returns in trimmed and without repeated event types or
// environments, or the refusal of the first rule it breaks, in this order:
// a malformed field (invalid_webhook, naming every one), an event type
// outside the catalogue, a target that is not https unless policy allows
// it. environments are the keys of the project's environments. Where the
// target's name resolves to is judged apart, by CheckTargetAddresses.
func CheckWebhook(in WebhookInput, environments []string, policy TargetPolicy) (WebhookInput, error) {
	var violations []apperrors.Violation
	add := func(pointer, code, detail string, params map[string]any) {
		violations = append(violations, apperrors.Violation{Pointer: pointer, Code: code, Detail: detail, Params: params})
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxWebhookNameLength {
		add("/name", "invalid_name", "a name has 1 to 200 characters and is not blank",
			map[string]any{"min": 1, "max": maxWebhookNameLength})
	}
	target, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || !target.IsAbs() || target.Host == "" || target.Hostname() == "" || target.User != nil ||
		target.Fragment != "" || (target.Scheme != "https" && target.Scheme != "http") {
		add("/url", "invalid_url", "the target is an absolute http or https URL without credentials or a fragment", nil)
		target = nil
	} else {
		in.URL = target.String()
	}
	if len(in.Events) == 0 {
		add("/events", "required", "a subscription names at least one event type", nil)
	}
	// Pointers index the list as the request wrote it, so they are judged
	// before repeats are dropped.
	for i, e := range in.Environments {
		if !slices.Contains(environments, e) && !slices.Contains(in.Environments[:i], e) {
			add(fmt.Sprintf("/environments/%d", i), "not_found", "the project has no environment with this key", nil)
		}
	}
	in.Environments = distinct(in.Environments)
	if len(violations) > 0 {
		return in, apperrors.Invalid("invalid_webhook", "the webhook subscription is not valid", violations...)
	}
	for i, e := range in.Events {
		if e != EventAll && !knownEventType(e) {
			return in, apperrors.Invalid("unknown_event_type", "an event type is not in the catalogue", apperrors.Violation{
				Pointer: fmt.Sprintf("/events/%d", i), Code: "unknown_event_type",
				Detail: "GET /webhook-event-types lists the catalogue",
			})
		}
	}
	in.Events = distinct(in.Events)
	if target.Scheme == "http" && !policy.AllowHTTP {
		return in, errInsecureURL
	}
	return in, nil
}

// distinct returns values without repeats, the first of each kept in place.
func distinct(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// TargetHost returns the host name of a URL CheckWebhook accepted.
func TargetHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// CheckTargetAddresses refuses a target any of whose addresses is outside
// the public internet, unless policy allows private networks: the
// deployment's own host, its links and private networks are reached by no
// subscription the operator did not allow.
func CheckTargetAddresses(addrs []netip.Addr, policy TargetPolicy) error {
	if policy.AllowPrivateNetworks {
		return nil
	}
	for _, a := range addrs {
		if TargetAddressRefused(a) {
			return errPrivateNetworkTarget
		}
	}
	return nil
}

// TargetAddressRefused reports whether a is an address no subscription may
// reach without the operator's leave: loopback, private, link-local,
// unspecified or multicast, written as IPv4 or IPv4-mapped IPv6 alike.
func TargetAddressRefused(a netip.Addr) bool {
	a = a.Unmap()
	return !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsUnspecified() || a.IsLinkLocalUnicast() ||
		a.IsMulticast()
}

// webhookSecretLength is the bytes of randomness in a subscription's secret.
const webhookSecretLength = 32

// NewWebhookSecret draws a subscription's secret from random: 32 bytes,
// written as unpadded base64url.
func NewWebhookSecret(random io.Reader) (secret.String, error) {
	b := make([]byte, webhookSecretLength)
	if _, err := io.ReadFull(random, b); err != nil {
		return secret.String{}, fmt.Errorf("drawing a webhook secret: %w", err)
	}
	return secret.New(base64.RawURLEncoding.EncodeToString(b)), nil
}
