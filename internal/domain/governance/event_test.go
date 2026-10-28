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

// changeRequest is the values an audit entry records of a change request in
// state, with a proposal that no event carries.
func changeRequest(state string, extra map[string]any) map[string]any {
	cr := map[string]any{
		"kind": "flag_configuration", "state": state, "title": "Ship checkout", "description": "",
		"base_version": 3, "proposed": map[string]any{"on": true}, "diff": []any{map[string]any{"op": "replace", "path": "/on", "value": true}},
		"author": map[string]any{"kind": "member", "id": "someone"}, "version": 2, "flag": "checkout", "environment": "production",
	}
	for k, v := range extra {
		cr[k] = v
	}
	return cr
}

func transitioned(action string, before, after any) governance.Entry {
	e := recorded(action, before, after)
	e.Entity = governance.Entity{Kind: "change_request", ID: "0190d2a0-0000-7000-8000-0000000000f1", Key: "Ship checkout"}
	return e
}

func TestEveryChangeRequestTransitionHasAnEventOfItsOwn(t *testing.T) {
	for action, want := range map[string]string{
		"change_request.opened": "change_request.opened", "change_request.approved": "change_request.approved",
		"change_request.rejected": "change_request.rejected", "change_request.applied": "change_request.applied",
		"change_request.cancelled": "change_request.cancelled", "change_request.superseded": "change_request.superseded",
		"change_request.schedule_failed": "change_request.schedule_failed",
	} {
		events := governance.DeriveEvents(transitioned(action, changeRequest("open", nil), changeRequest("x", nil)))

		if got := types(events); !slices.Equal(got, []string{want, governance.EventAuditEntry}) {
			t.Errorf("%s: events %v, want %s and the entry", action, got, want)
		}
	}
}

func TestAChangeRequestEventCarriesTheChangeRequestWithoutItsProposal(t *testing.T) {
	events := governance.DeriveEvents(transitioned("change_request.opened", changeRequest("draft", nil), changeRequest("open", nil)))

	data := dataOf(t, events, "change_request.opened")
	if _, has := data["proposed"]; has {
		t.Errorf("data %v carries the proposal", data)
	}
	if data["state"] != "open" || data["title"] != "Ship checkout" || data["diff"] == nil || data["flag"] != "checkout" {
		t.Errorf("data %v, want the change request as it is after the transition, with its diff", data)
	}
	if entry := dataOf(t, events, governance.EventAuditEntry); entry["after"].(map[string]any)["proposed"] == nil {
		t.Error("audit.entry lost the proposal: it is the entry as the log reads it")
	}
}

func TestApprovingWithAnApplyTimeIsAlsoScheduled(t *testing.T) {
	at := "2026-03-03T09:00:00Z"
	scheduled := governance.DeriveEvents(transitioned("change_request.approved", changeRequest("open", nil),
		changeRequest("approved", map[string]any{"apply_at": at})))
	immediate := governance.DeriveEvents(transitioned("change_request.approved", changeRequest("open", nil), changeRequest("approved", nil)))

	if got := types(scheduled); !slices.Equal(got, []string{"change_request.approved", "change_request.scheduled", governance.EventAuditEntry}) {
		t.Errorf("approved with an apply time: events %v", got)
	}
	if data := dataOf(t, scheduled, "change_request.scheduled"); data["apply_at"] != at {
		t.Errorf("scheduled data %v, want apply_at %s", data, at)
	}
	if got := types(immediate); !slices.Equal(got, []string{"change_request.approved", governance.EventAuditEntry}) {
		t.Errorf("approved without one: events %v", got)
	}
}

func TestADraftEditAndACopyOfAChangeRequestAreOnlyAuditEntries(t *testing.T) {
	for _, action := range []string{"change_request.drafted", "change_request.updated", "change_request.rebased", "change_request.copied"} {
		events := governance.DeriveEvents(transitioned(action, nil, changeRequest("draft", nil)))

		if got := types(events); !slices.Equal(got, []string{governance.EventAuditEntry}) {
			t.Errorf("%s: events %v, want only the entry", action, got)
		}
	}
}

func TestAScheduleFailureEventCarriesTheFailure(t *testing.T) {
	failure := map[string]any{"at": "2026-03-03T09:05:00Z", "code": "invalid_configuration", "attempts": 5, "stopped": true}

	events := governance.DeriveEvents(transitioned("change_request.schedule_failed", changeRequest("approved", nil),
		changeRequest("approved", map[string]any{"schedule_failure": failure})))

	if data := dataOf(t, events, "change_request.schedule_failed"); data["schedule_failure"] == nil {
		t.Errorf("data %v, want schedule_failure", data)
	}
}

func TestAnExperimentsLifecycleEventsNameItBeforeAndAfter(t *testing.T) {
	before, after := map[string]any{"status": "draft"}, map[string]any{"status": "running"}
	for action, want := range map[string]string{
		"experiment.started": "experiment.started", "experiment.stopped": "experiment.stopped",
		"experiment.archived": "experiment.archived", "experiment.traffic_allocation_changed": "experiment.traffic_allocation_changed",
	} {
		e := recorded(action, before, after)
		e.Entity = governance.Entity{Kind: "experiment", ID: "0190d2a0-0000-7000-8000-0000000000f2", Key: "new-checkout"}

		events := governance.DeriveEvents(e)

		if got := types(events); !slices.Equal(got, []string{want, governance.EventAuditEntry}) {
			t.Errorf("%s: events %v", action, got)
		}
		if data := dataOf(t, events, want); !reflect.DeepEqual(data["before"], before) || !reflect.DeepEqual(data["after"], after) {
			t.Errorf("%s: data %v, want the experiment before and after", action, data)
		}
	}
}

func TestAnExperimentsOtherWritesAreOnlyAuditEntries(t *testing.T) {
	for _, action := range []string{"experiment.created", "experiment.cloned", "experiment.updated", "experiment.deleted",
		"experiment.salts_regenerated", "experiment.alert_acknowledged"} {
		e := recorded(action, nil, map[string]any{"status": "draft"})
		e.Entity = governance.Entity{Kind: "experiment", ID: "0190d2a0-0000-7000-8000-0000000000f2", Key: "new-checkout"}

		if got := types(governance.DeriveEvents(e)); !slices.Equal(got, []string{governance.EventAuditEntry}) {
			t.Errorf("%s: events %v, want only the entry", action, got)
		}
	}
}

func TestEachAlertKindRaisedAndClearedHasItsEvent(t *testing.T) {
	alert := func(kind string) map[string]any { return map[string]any{"kind": kind, "environment": "x"} }
	for _, tc := range []struct {
		action, kind string
		want         []string
	}{
		{"experiment.alert_raised", "srm", []string{"experiment.srm_detected"}},
		{"experiment.alert_raised", "no_exposures", []string{"experiment.no_exposures"}},
		{"experiment.alert_raised", "exposure_conflicts", []string{"experiment.exposure_conflicts"}},
		{"experiment.alert_raised", "past_expected_end", []string{"experiment.past_expected_end"}},
		{"experiment.alert_cleared", "srm", []string{"experiment.srm_cleared"}},
		// The catalogue has a cleared event for the sample ratio check alone.
		{"experiment.alert_cleared", "no_exposures", nil},
		{"experiment.alert_cleared", "exposure_conflicts", nil},
	} {
		e := recorded(tc.action, nil, alert(tc.kind))
		e.Entity = governance.Entity{Kind: "experiment", ID: "0190d2a0-0000-7000-8000-0000000000f2", Key: "new-checkout"}
		if tc.action == "experiment.alert_cleared" {
			e.Before = alert(tc.kind)
		}

		events := governance.DeriveEvents(e)

		if got := types(events); !slices.Equal(got, append(slices.Clone(tc.want), governance.EventAuditEntry)) {
			t.Errorf("%s of %s: events %v, want %v and the entry", tc.action, tc.kind, got, tc.want)
		}
		if len(tc.want) == 1 {
			if data := dataOf(t, events, tc.want[0]); !reflect.DeepEqual(data["after"], alert(tc.kind)) {
				t.Errorf("%s: data %v, want the alert", tc.want[0], data)
			}
		}
	}
}

func TestARevertIsRevertedAndAlsoWhatItChanged(t *testing.T) {
	after := configuration(true, map[string]any{"id": "r1"})
	after["reverted_to"] = 2

	events := governance.DeriveEvents(recorded("flag.reverted", configuration(false), after))

	want := []string{"flag.reverted", "flag.switched_on", "flag.rules_changed", governance.EventAuditEntry}
	if got := types(events); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	if data := dataOf(t, events, "flag.reverted"); data["reverted_to"] != float64(2) || data["before"] == nil || data["after"] == nil {
		t.Errorf("flag.reverted data %v, want reverted_to, before and after", data)
	}
}
