package governance

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// EventID identifies a catalogue event. Every delivery of the event, a
// redelivery included, carries it, so a receiver deduplicates on it.
type EventID = id.Of[Event]

// Derived is a catalogue event an audit entry causes: its type and its data.
type Derived struct {
	Type string
	Data any
}

// DeriveEvents returns the catalogue events the change e records causes,
// `audit.entry` last: an entry causes that one whatever its action, and
// zero or more others by its action, before and after. An entry whose
// action has no event of its own, such as an environment protected, still
// reaches a receiver as `audit.entry`.
func DeriveEvents(e Entry) []Derived {
	var out []Derived
	before, after := document(e.Before), document(e.After)
	beforeAfter := map[string]any{"before": before, "after": after}
	switch e.Action {
	case "flag.created":
		out = append(out, Derived{"flag.created", beforeAfter})
	case "flag.updated", "flag.restored":
		out = append(out, Derived{"flag.updated", beforeAfter})
	case "flag.archived", "flag.deleted":
		out = append(out, Derived{"flag.deleted", beforeAfter})
	case "flag.killed":
		// A kill switch is one event and never a switch off as well: a
		// receiver paging someone on it must not page twice.
		reason := mapOf(after)["reason"]
		out = append(out, Derived{"flag.kill_switch", map[string]any{"reason": reason, "before": before, "after": after}})
	case "flag.switched_on", "flag.switched_off", "flag.configuration_updated", "flag.configuration_copied",
		"flag.override_set", "flag.override_removed", "flag.salt_regenerated":
		out = append(out, configurationEvents(before, after)...)
	case "segment.created", "segment.updated", "segment.deleted":
		out = append(out, Derived{e.Action, withoutPrivateConditions(before, after)})
	case "flag.reverted":
		// A revert is a write of the configuration as any other, so a
		// receiver watching rules or the switch sees it too; it also says
		// which version the configuration returned to.
		reverted := map[string]any{"reverted_to": mapOf(after)["reverted_to"], "before": before, "after": after}
		out = append(append(out, Derived{"flag.reverted", reverted}), configurationEvents(before, after)...)
	case "grant.created", "grant.deleted", "sdk_key.created", "sdk_key.revoked":
		out = append(out, Derived{e.Action, beforeAfter})
	case "experiment.started", "experiment.stopped", "experiment.archived", "experiment.traffic_allocation_changed":
		out = append(out, Derived{e.Action, beforeAfter})
	case "experiment.alert_raised", "experiment.alert_cleared":
		if typ, ok := alertEventType(e.Action, before, after); ok {
			out = append(out, Derived{typ, beforeAfter})
		}
	case "change_request.opened", "change_request.approved", "change_request.rejected", "change_request.applied",
		"change_request.cancelled", "change_request.superseded", "change_request.schedule_failed":
		out = append(out, changeRequestEvents(e.Action, after)...)
	}
	return append(out, Derived{EventAuditEntry, EntryDocument(e)})
}

// alertEventTypes are the catalogue types of the alerts the checks raise,
// by the alert's kind. Only the sample ratio check has an event for its
// alert clearing.
var (
	alertRaisedTypes = map[string]string{
		"srm": "experiment.srm_detected", "no_exposures": "experiment.no_exposures",
		"exposure_conflicts": "experiment.exposure_conflicts", "past_expected_end": "experiment.past_expected_end",
	}
	alertClearedTypes = map[string]string{"srm": "experiment.srm_cleared"}
)

// alertEventType returns the catalogue type of an alert raised or cleared,
// from the alert's kind, which both sides of the entry record.
func alertEventType(action string, before, after any) (string, bool) {
	kind, _ := mapOf(after)["kind"].(string)
	if kind == "" {
		kind, _ = mapOf(before)["kind"].(string)
	}
	types := alertRaisedTypes
	if action == "experiment.alert_cleared" {
		types = alertClearedTypes
	}
	typ, ok := types[kind]
	return typ, ok
}

// EventChangeRequestApplied is the event of a change request applied. The
// audit entry it names is the first the application wrote beside the
// change request's own, so a receiver ties the two together.
const EventChangeRequestApplied = "change_request.applied"

// changeRequestEvents are the events of a change request's transition to
// the change request after, which they carry without its proposal, as the
// audit log records it. Approving with an apply time is also scheduling.
func changeRequestEvents(action string, after any) []Derived {
	data := without(mapOf(after), "proposed")
	if privateSegmentDefinition(mapOf(after)) {
		data["diff"] = withoutConditionOps(data["diff"])
	}
	out := []Derived{{action, data}}
	if action == "change_request.approved" && data["apply_at"] != nil {
		out = append(out, Derived{"change_request.scheduled", data})
	}
	return out
}

// privateSegmentDefinition reports whether cr, a change request as its
// audit entry records it, proposes a segment's definition that is private
// on either side: private as proposed, or private before when the diff
// changes the switch. Its diff then names who is in the segment, which the
// segment's own events keep from receivers too.
func privateSegmentDefinition(cr map[string]any) bool {
	if cr["segment"] == nil {
		return false
	}
	if mapOf(cr["proposed"])["private"] == true {
		return true
	}
	ops, _ := cr["diff"].([]any)
	for _, op := range ops {
		if mapOf(op)["path"] == "/private" {
			return true
		}
	}
	return false
}

// withoutConditionOps returns diff without the operations on a segment's
// conditions.
func withoutConditionOps(diff any) []any {
	ops, _ := diff.([]any)
	out := []any{}
	for _, op := range ops {
		path, _ := mapOf(op)["path"].(string)
		if path != "/conditions" && !strings.HasPrefix(path, "/conditions/") {
			out = append(out, op)
		}
	}
	return out
}

// servedFields are the fields of a configuration that decide what it
// serves, other than whether it is on: who wrote it, when, its version and
// what a write adds beside it are no change of rules.
var servedFields = []string{"off_variation", "fallthrough", "prerequisites", "overrides", "rules"}

// configurationEvents are the events of a write to a flag's configuration
// in an environment: switched on or off when that moved, and rules changed,
// with the patch from the rules before to those after, when anything else
// that decides what it serves did. One write may cause both.
func configurationEvents(before, after any) []Derived {
	b, a := mapOf(before), mapOf(after)
	if b == nil || a == nil {
		return nil
	}
	var out []Derived
	if on, _ := a["on"].(bool); b["on"] != a["on"] {
		typ := "flag.switched_off"
		if on {
			typ = "flag.switched_on"
		}
		out = append(out, Derived{typ, map[string]any{"before": before, "after": after}})
	}
	diff := Diff(pick(b, servedFields), pick(a, servedFields))
	if len(diff) > 0 {
		ops := make([]any, len(diff))
		for i, op := range diff {
			ops[i] = op.Values()
		}
		out = append(out, Derived{"flag.rules_changed", map[string]any{"before": before, "after": after, "diff": ops}})
	}
	return out
}

// withoutPrivateConditions returns a segment's values around a change, with
// its conditions left out on both sides when it is private on either: a
// private segment's conditions name who is in it, which its members read
// and no receiver does.
func withoutPrivateConditions(before, after any) map[string]any {
	b, a := mapOf(before), mapOf(after)
	if b["private"] == true || a["private"] == true {
		b, a = without(b, "conditions"), without(a, "conditions")
	}
	out := map[string]any{"before": nil, "after": nil}
	if b != nil {
		out["before"] = b
	}
	if a != nil {
		out["after"] = a
	}
	return out
}

// EntryDocument returns e as the audit log's API answers an entry, the data
// of `audit.entry`, so one parser reads both.
func EntryDocument(e Entry) map[string]any {
	actor := map[string]any{"kind": string(e.Actor.Kind), "id": e.Actor.ID, "name": e.Actor.Name, "member_id": nil}
	if m, err := id.Parse[struct{}](e.Actor.MemberID); err == nil {
		actor["member_id"] = m.String()
	}
	return map[string]any{
		"id": e.ID.String(), "occurred_at": e.OccurredAt.UTC().Format(time.RFC3339Nano),
		"project_id": optionalID(e.Project), "environment_id": optionalID(e.Environment), "actor": actor,
		"action": e.Action, "entity": map[string]any{"kind": e.Entity.Kind, "id": e.Entity.ID, "key": e.Entity.Key},
		"before": document(e.Before), "after": document(e.After), "kill_switch": e.KillSwitch,
		"request_id": optionalString(e.RequestID), "change_request_id": optionalID(e.ChangeRequest),
	}
}

// Event is a catalogue event as a receiver reads it.
type Event struct {
	ID   EventID
	Type string
	// Project is the project the event belongs to, zero for the instance;
	// Environment the environment it happened in, zero for none.
	Project        workspace.ProjectID
	ProjectKey     string
	Environment    workspace.EnvironmentID
	EnvironmentKey string
	OccurredAt     time.Time
	// Actor is who caused the event; nil when no audited change did.
	Actor  *Actor
	Entity *Entity
	Data   any
	// AuditEntry is the entry the event was derived from; zero for an event
	// no audited change caused.
	AuditEntry EntryID
}

// EntryEvent returns the event derived from e with id, in a project keyed
// projectKey and an environment keyed environmentKey.
func EntryEvent(eventID EventID, e Entry, d Derived, projectKey, environmentKey string) Event {
	actor, entity := e.Actor, e.Entity
	return Event{
		ID: eventID, Type: d.Type, Project: e.Project, ProjectKey: projectKey, Environment: e.Environment,
		EnvironmentKey: environmentKey, OccurredAt: e.OccurredAt, Actor: &actor, Entity: &entity, Data: d.Data,
		AuditEntry: e.ID,
	}
}

// Envelope returns the document every event is sent as: its identifier,
// type, time, project, environment, actor, entity, data and the audit entry
// it came from, each null where it does not apply, so one parser reads every
// type.
func (e Event) Envelope() map[string]any {
	out := map[string]any{
		"id": e.ID.String(), "type": e.Type, "timestamp": e.OccurredAt.UTC().Format(time.RFC3339Nano),
		"project": nil, "environment": nil, "actor": nil, "entity": nil, "data": e.Data,
		"audit_entry_id": optionalID(e.AuditEntry),
	}
	if !e.Project.IsZero() {
		out["project"] = map[string]any{"id": e.Project.String(), "key": e.ProjectKey}
	}
	if !e.Environment.IsZero() {
		out["environment"] = map[string]any{"id": e.Environment.String(), "key": e.EnvironmentKey}
	}
	if a := e.Actor; a != nil {
		out["actor"] = map[string]any{"kind": string(a.Kind), "id": a.ID, "name": a.Name}
	}
	if en := e.Entity; en != nil {
		out["entity"] = map[string]any{"kind": en.Kind, "id": en.ID, "key": en.Key}
	}
	return out
}

// QueuedEvent is an event waiting in the outbox to be fanned out to the
// subscriptions that want it.
type QueuedEvent struct {
	ID      EventID
	Type    string
	Project workspace.ProjectID
	// EnvironmentKey is the key of the environment the event happened in,
	// "" for none.
	EnvironmentKey string
	// Envelope is the event as it is sent, encoded as JSON.
	Envelope []byte
	// CreatedAt is when the event happened.
	CreatedAt time.Time
}

// Queue returns e as it waits in the outbox.
func (e Event) Queue() (QueuedEvent, error) {
	envelope, err := json.Marshal(e.Envelope())
	if err != nil {
		return QueuedEvent{}, fmt.Errorf("encoding the envelope of a %s event: %w", e.Type, err)
	}
	return QueuedEvent{
		ID: e.ID, Type: e.Type, Project: e.Project, EnvironmentKey: e.EnvironmentKey, Envelope: envelope,
		CreatedAt: e.OccurredAt,
	}, nil
}

// Wants reports whether the subscription w is sent the event e: it is
// enabled, of e's project (or both of the instance), names e's type, names
// no environment or e's (an event of no environment passes every filter),
// and existed when e happened, so a subscription never starts with a
// backlog.
func (w Webhook) Wants(e QueuedEvent) bool {
	return w.Enabled && w.Project == e.Project && !w.CreatedAt.After(e.CreatedAt) && WantsEventType(w.Events, e.Type) &&
		(len(w.Environments) == 0 || e.EnvironmentKey == "" || slices.Contains(w.Environments, e.EnvironmentKey))
}

// document returns v as encoding/json decodes its encoding into an any, so
// the values a context recorded as maps of its own types compare, diff and
// strip like the stored entry the API answers. A value that cannot be
// encoded is answered as nil; every recorded value is encoded when the entry
// is stored, which refuses such a value first.
func document(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func pick(m map[string]any, keys []string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

func without(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	out := maps.Clone(m)
	delete(out, key)
	return out
}

func optionalID[T any](v id.Of[T]) any {
	if v.IsZero() {
		return nil
	}
	return v.String()
}

func optionalString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// entryOnlyActions are the audit actions that cause no catalogue event of
// their own: a receiver learns of them as `audit.entry` alone.
var entryOnlyActions = []string{
	"api_token.created",
	"api_token.revoked",
	"attribute.created",
	"attribute.deleted",
	"attribute.updated",
	"auth.locked_out",
	"auth.sign_in_failed",
	"auth.signed_in",
	"auth.signed_out",
	"auth_provider.created",
	"auth_provider.deleted",
	"auth_provider.disabled",
	"auth_provider.enabled",
	"auth_provider.updated",
	"auth_settings.updated",
	"change_request.copied",
	"change_request.drafted",
	"change_request.rebased",
	"change_request.updated",
	"environment.created",
	"environment.deleted",
	"environment.protected",
	"environment.unprotected",
	"environment.updated",
	"experiment.alert_acknowledged",
	"experiment.cloned",
	"experiment.created",
	"experiment.deleted",
	"experiment.salts_regenerated",
	"experiment.updated",
	"member.created",
	"member.deactivated",
	"member.demoted",
	"member.invitation_accepted",
	"member.invitation_reissued",
	"member.invited",
	"member.promoted",
	"member.reactivated",
	"member.updated",
	"password.changed",
	"project.created",
	"project.deleted",
	"project.updated",
	"sdk_key.rotated",
	"sdk_key.updated",
	"session.revoked",
	"sink.created",
	"sink.deleted",
	"sink.secret_rotated",
	"sink.updated",
	"unit_type.created",
	"unit_type.deleted",
	"unit_type.updated",
	"webhook.created",
	"webhook.deleted",
	"webhook.enabled",
	"webhook.secret_rotated",
	"webhook.updated",
}

// EntryOnlyActions returns the audit actions whose entry is the only event
// they cause.
func EntryOnlyActions() []string { return slices.Clone(entryOnlyActions) }
