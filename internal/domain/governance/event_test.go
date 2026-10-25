package governance_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	someProject     = id.MustParse[workspace.Project]("0190d2a0-0000-7000-8000-0000000000a1")
	someEnvironment = id.MustParse[workspace.Environment]("0190d2a0-0000-7000-8000-0000000000b1")
	occurred        = time.Date(2026, time.March, 2, 10, 0, 0, 0, time.UTC)
)

// recorded returns the entry of action with before and after, in the
// environment for a configuration action.
func recorded(action string, before, after any) governance.Entry {
	e := governance.Entry{
		ID: id.MustParse[governance.Entry]("0190d2a0-0000-7000-8000-0000000000c1"), OccurredAt: occurred,
		Actor: governance.Actor{Kind: governance.ActorMember, ID: "0190d2a0-0000-7000-8000-0000000000d1", Name: "Ada"},
		Record: governance.Record{
			Project: someProject, Action: action, Before: before, After: after,
			Entity: governance.Entity{Kind: "flag", ID: "0190d2a0-0000-7000-8000-0000000000e1", Key: "checkout"},
		},
	}
	return e
}

func configuration(on bool, rules ...map[string]any) map[string]any {
	if rules == nil {
		rules = []map[string]any{}
	}
	return map[string]any{
		"on": on, "off_variation": "off", "fallthrough": "off", "prerequisites": []map[string]any{},
		"overrides": []map[string]any{}, "rules": rules, "updated_by": "someone", "version": 3,
	}
}

func types(events []governance.Derived) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func dataOf(t *testing.T, events []governance.Derived, typ string) map[string]any {
	t.Helper()
	for _, e := range events {
		if e.Type == typ {
			data, ok := e.Data.(map[string]any)
			if !ok {
				t.Fatalf("%s data %T, want an object", typ, e.Data)
			}
			return data
		}
	}
	t.Fatalf("no %s among %v", typ, types(events))
	return nil
}

func TestAKillSwitchIsOneKillSwitchEventAndNoSwitchOff(t *testing.T) {
	after := configuration(false)
	after["reason"] = "checkout 500s"

	events := governance.DeriveEvents(recorded("flag.killed", configuration(true), after))

	if got := types(events); !slices.Equal(got, []string{"flag.kill_switch", governance.EventAuditEntry}) {
		t.Fatalf("events %v, want one kill switch and the entry", got)
	}
	data := dataOf(t, events, "flag.kill_switch")
	before, _ := data["before"].(map[string]any)
	afterData, _ := data["after"].(map[string]any)
	if data["reason"] != "checkout 500s" || before["on"] != true || afterData["on"] != false {
		t.Errorf("kill switch data %v", data)
	}
}

func TestSwitchingOnAndChangingRulesInOneWriteAreTwoEvents(t *testing.T) {
	rule := map[string]any{"id": "r1", "conditions": []any{}, "serve": map[string]any{"kind": "variation", "variation": "on"}}

	events := governance.DeriveEvents(recorded("flag.configuration_updated", configuration(false), configuration(true, rule)))

	want := []string{"flag.switched_on", "flag.rules_changed", governance.EventAuditEntry}
	if got := types(events); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	diff, _ := dataOf(t, events, "flag.rules_changed")["diff"].([]any)
	wantDiff := []any{map[string]any{"op": "add", "path": "/rules/0", "value": map[string]any{
		"id": "r1", "conditions": []any{}, "serve": map[string]any{"kind": "variation", "variation": "on"},
	}}}
	if !reflect.DeepEqual(diff, wantDiff) {
		t.Errorf("diff %v, want %v", diff, wantDiff)
	}
}

func TestSwitchingOffAloneIsOnlyASwitchOff(t *testing.T) {
	events := governance.DeriveEvents(recorded("flag.switched_off", configuration(true), configuration(false)))

	if got := types(events); !slices.Equal(got, []string{"flag.switched_off", governance.EventAuditEntry}) {
		t.Errorf("events %v", got)
	}
}

func TestAWriteChangingWhatNothingServesIsNoRulesChange(t *testing.T) {
	after := configuration(true)
	after["updated_by"], after["version"], after["experiment_stopped"] = "someone else", 4, "pricing"

	events := governance.DeriveEvents(recorded("flag.configuration_updated", configuration(true), after))

	if got := types(events); !slices.Equal(got, []string{governance.EventAuditEntry}) {
		t.Errorf("events %v, want the entry alone", got)
	}
}

func TestOverridesAndSaltsAreRulesChanges(t *testing.T) {
	after := configuration(true)
	after["overrides"] = []map[string]any{{"unit_type": "user", "unit_id": "u1", "variation": "on"}}

	for _, action := range []string{"flag.override_set", "flag.override_removed", "flag.salt_regenerated", "flag.configuration_copied"} {
		events := governance.DeriveEvents(recorded(action, configuration(true), after))
		if got := types(events); !slices.Equal(got, []string{"flag.rules_changed", governance.EventAuditEntry}) {
			t.Errorf("%s: events %v", action, got)
		}
	}
}

func TestAFlagsDefinitionEventsNameWhatHappenedToIt(t *testing.T) {
	for action, want := range map[string]string{
		"flag.created": "flag.created", "flag.updated": "flag.updated", "flag.restored": "flag.updated",
		"flag.archived": "flag.deleted", "flag.deleted": "flag.deleted",
		"segment.created": "segment.created", "segment.updated": "segment.updated", "segment.deleted": "segment.deleted",
		"grant.created": "grant.created", "grant.deleted": "grant.deleted",
		"sdk_key.created": "sdk_key.created", "sdk_key.revoked": "sdk_key.revoked",
	} {
		before, after := map[string]any{"key": "a"}, map[string]any{"key": "b"}
		events := governance.DeriveEvents(recorded(action, before, after))
		if got := types(events); !slices.Equal(got, []string{want, governance.EventAuditEntry}) {
			t.Errorf("%s: events %v, want %s and the entry", action, got, want)
			continue
		}
		data := dataOf(t, events, want)
		if !reflect.DeepEqual(data["before"], map[string]any{"key": "a"}) || !reflect.DeepEqual(data["after"], map[string]any{"key": "b"}) {
			t.Errorf("%s: data %v", action, data)
		}
	}
}

func TestAPrivateSegmentsConditionsAreNeverSent(t *testing.T) {
	conditions := []map[string]any{{"kind": "attribute", "attribute": "email", "op": "in", "value": []string{"a@x"}}}
	public := map[string]any{"key": "vips", "private": false, "conditions": conditions}
	private := map[string]any{"key": "vips", "private": true, "conditions": conditions}

	for name, tc := range map[string]struct {
		before, after any
		stripped      bool
	}{
		"created private":   {nil, private, true},
		"made private":      {public, private, true},
		"made public":       {private, public, true},
		"deleted private":   {private, nil, true},
		"public throughout": {public, public, false},
	} {
		events := governance.DeriveEvents(recorded("segment.updated", tc.before, tc.after))
		data := dataOf(t, events, "segment.updated")
		for _, side := range []string{"before", "after"} {
			values, _ := data[side].(map[string]any)
			if values == nil {
				continue
			}
			if _, has := values["conditions"]; has == tc.stripped {
				t.Errorf("%s: %s %v", name, side, values)
			}
		}
	}
}

func TestEveryEntryIsAnAuditEntryEventAsTheAPIReadsIt(t *testing.T) {
	e := recorded("environment.protected", map[string]any{"protected": false}, map[string]any{"protected": true})
	e.Environment, e.RequestID = someEnvironment, "req-1"

	events := governance.DeriveEvents(e)

	if got := types(events); !slices.Equal(got, []string{governance.EventAuditEntry}) {
		t.Fatalf("events %v, want the entry alone: protection has no event of its own", got)
	}
	want := map[string]any{
		"id": e.ID.String(), "occurred_at": "2026-03-02T10:00:00Z", "project_id": someProject.String(),
		"environment_id": someEnvironment.String(), "action": "environment.protected", "kill_switch": false,
		"actor":  map[string]any{"kind": "member", "id": e.Actor.ID, "name": "Ada", "member_id": nil},
		"entity": map[string]any{"kind": "flag", "id": e.Entity.ID, "key": "checkout"},
		"before": map[string]any{"protected": false}, "after": map[string]any{"protected": true},
		"request_id": "req-1", "change_request_id": nil,
	}
	if got := events[0].Data; !reflect.DeepEqual(got, want) {
		t.Errorf("audit.entry data\n%v\nwant\n%v", got, want)
	}
}

func TestTheEnvelopeNamesEverythingAReceiverParses(t *testing.T) {
	e := recorded("flag.switched_on", configuration(false), configuration(true))
	e.Environment = someEnvironment
	eventID := id.MustParse[governance.Event]("0190d2a0-0000-7000-8000-0000000000f1")

	queued, err := governance.EntryEvent(eventID, e, governance.DeriveEvents(e)[0], "shop", "production").Queue()
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(queued.Envelope, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id": eventID.String(), "type": "flag.switched_on", "timestamp": "2026-03-02T10:00:00Z",
		"project":        map[string]any{"id": someProject.String(), "key": "shop"},
		"environment":    map[string]any{"id": someEnvironment.String(), "key": "production"},
		"actor":          map[string]any{"kind": "member", "id": e.Actor.ID, "name": "Ada"},
		"entity":         map[string]any{"kind": "flag", "id": e.Entity.ID, "key": "checkout"},
		"audit_entry_id": e.ID.String(),
	}
	data := got["data"]
	delete(got, "data")
	if !reflect.DeepEqual(got, want) || data == nil {
		t.Errorf("envelope\n%v\nwant\n%v with data", got, want)
	}
	if queued.ID != eventID || queued.Type != "flag.switched_on" || queued.Project != someProject ||
		queued.EnvironmentKey != "production" || !queued.CreatedAt.Equal(occurred) {
		t.Errorf("queued %+v", queued)
	}
}

func TestASubscriptionIsSentOnlyTheEventsItWants(t *testing.T) {
	sub := governance.Webhook{
		Project: someProject, CreatedAt: occurred,
		WebhookInput: governance.WebhookInput{Events: []string{governance.EventAll}, Enabled: true},
	}
	event := governance.QueuedEvent{Type: "flag.kill_switch", Project: someProject, EnvironmentKey: "production", CreatedAt: occurred}
	with := func(change func(w *governance.Webhook, e *governance.QueuedEvent)) bool {
		w, e := sub, event
		change(&w, &e)
		return w.Wants(e)
	}

	for name, tc := range map[string]struct {
		change func(w *governance.Webhook, e *governance.QueuedEvent)
		want   bool
	}{
		"everything":                   {func(*governance.Webhook, *governance.QueuedEvent) {}, true},
		"its one type":                 {func(w *governance.Webhook, _ *governance.QueuedEvent) { w.Events = []string{"flag.kill_switch"} }, true},
		"another type":                 {func(w *governance.Webhook, _ *governance.QueuedEvent) { w.Events = []string{"flag.created"} }, false},
		"audit.entry under everything": {func(_ *governance.Webhook, e *governance.QueuedEvent) { e.Type = governance.EventAuditEntry }, false},
		"audit.entry asked by name": {func(w *governance.Webhook, e *governance.QueuedEvent) {
			w.Events, e.Type = []string{governance.EventAuditEntry}, governance.EventAuditEntry
		}, true},
		"its environment":     {func(w *governance.Webhook, _ *governance.QueuedEvent) { w.Environments = []string{"production"} }, true},
		"another environment": {func(w *governance.Webhook, _ *governance.QueuedEvent) { w.Environments = []string{"staging"} }, false},
		"an event of no environment": {func(w *governance.Webhook, e *governance.QueuedEvent) {
			w.Environments, e.EnvironmentKey = []string{"staging"}, ""
		}, true},
		"disabled":                 {func(w *governance.Webhook, _ *governance.QueuedEvent) { w.Enabled = false }, false},
		"another project":          {func(w *governance.Webhook, _ *governance.QueuedEvent) { w.Project = workspace.ProjectID{} }, false},
		"created after the event":  {func(w *governance.Webhook, _ *governance.QueuedEvent) { w.CreatedAt = occurred.Add(time.Microsecond) }, false},
		"created before the event": {func(w *governance.Webhook, _ *governance.QueuedEvent) { w.CreatedAt = occurred.Add(-time.Hour) }, true},
	} {
		if got := with(tc.change); got != tc.want {
			t.Errorf("%s: Wants = %v, want %v", name, got, tc.want)
		}
	}
}
