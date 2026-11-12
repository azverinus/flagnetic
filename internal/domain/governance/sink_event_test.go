package governance_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
)

const someSink = "0190d2a0-0000-7000-8000-0000000000f5"

// sinkEntry returns the entry recording action on a sink, by actor, with the
// document after as the sink's health events record it.
func sinkEntry(action string, actor governance.Actor, after map[string]any) governance.Entry {
	e := recorded(action, nil, after)
	e.Actor = actor
	e.Entity = governance.Entity{Kind: "sink", ID: someSink, Key: "warehouse"}
	return e
}

func sinkRef(kind string) map[string]any {
	return map[string]any{"id": someSink, "name": "warehouse", "kind": kind}
}

func TestEachSinkHealthActionCausesItsEventAndTheEntry(t *testing.T) {
	failure := map[string]any{"class": "unreachable", "status": nil, "code": nil, "at": "2026-03-02T10:00:00Z"}
	for _, tc := range []struct {
		action, typ string
		actor       governance.Actor
		after       map[string]any
		want        map[string]any
	}{
		{
			"sink.failing", "sink.failing", governance.SinkDeliveryActor,
			map[string]any{"sink": sinkRef("webhook"), "event_kind": "exposure", "failure": failure},
			map[string]any{"sink": sinkRef("webhook"), "event_kind": "exposure", "failure": failure},
		},
		{
			"sink.stopped", "sink.stopped", governance.SinkDeliveryActor,
			map[string]any{"sink": sinkRef("clickhouse"), "event_kind": "alias", "consecutive_dead_letters": float64(3), "failure": failure},
			map[string]any{"sink": sinkRef("clickhouse"), "event_kind": "alias", "consecutive_dead_letters": float64(3), "failure": failure},
		},
		{
			"sink.at_risk", "sink.at_risk", governance.SinkDeliveryActor,
			map[string]any{"sink": sinkRef("object_storage"), "event_kind": "evaluation_summary", "lag_seconds": float64(2400000), "loss_warning_hours": float64(72)},
			map[string]any{"sink": sinkRef("object_storage"), "event_kind": "evaluation_summary", "lag_seconds": float64(2400000), "loss_warning_hours": float64(72)},
		},
		{
			"sink.data_lost", "sink.data_lost", governance.EventRetentionActor,
			map[string]any{"sink": sinkRef("webhook"), "event_kind": "exposure", "gap": map[string]any{"rows": float64(120)}},
			map[string]any{"sink": sinkRef("webhook"), "event_kind": "exposure", "gap": map[string]any{"rows": float64(120)}},
		},
	} {
		events := governance.DeriveEvents(sinkEntry(tc.action, tc.actor, tc.after))

		if got := types(events); !slices.Equal(got, []string{tc.typ, governance.EventAuditEntry}) {
			t.Errorf("%s: events %v, want %s and the entry", tc.action, got, tc.typ)
			continue
		}
		if data := dataOf(t, events, tc.typ); !reflect.DeepEqual(data, tc.want) {
			t.Errorf("%s: data %v, want %v", tc.action, data, tc.want)
		}
	}
}

func TestASinkEventsDataIsWhatItsTypeNamesAndNothingTheEntryHappenedToCarry(t *testing.T) {
	events := governance.DeriveEvents(sinkEntry("sink.stopped", governance.SinkDeliveryActor, map[string]any{
		"sink": sinkRef("webhook"), "event_kind": "exposure", "consecutive_dead_letters": float64(3),
		"failure":  map[string]any{"class": "rejected", "status": float64(400), "code": nil, "at": "2026-03-02T10:00:00Z", "body": "the target's own words"},
		"password": "hunter2",
	}))

	data := dataOf(t, events, "sink.stopped")

	want := map[string]any{
		"sink": sinkRef("webhook"), "event_kind": "exposure", "consecutive_dead_letters": float64(3),
		"failure": map[string]any{"class": "rejected", "status": float64(400), "code": nil, "at": "2026-03-02T10:00:00Z"},
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("data %v, want %v", data, want)
	}
}

func TestASinkEventsObjectThatIsNotAnObjectIsNull(t *testing.T) {
	events := governance.DeriveEvents(sinkEntry("sink.failing", governance.SinkDeliveryActor, map[string]any{
		"sink": "warehouse", "event_kind": "exposure", "failure": "the target's own words",
	}))

	data := dataOf(t, events, "sink.failing")

	if data["sink"] != nil || data["failure"] != nil {
		t.Errorf("sink %#v, failure %#v, want both null", data["sink"], data["failure"])
	}
}

func TestASinkEventsAbsentObjectIsNull(t *testing.T) {
	events := governance.DeriveEvents(sinkEntry("sink.stopped", governance.SinkDeliveryActor, map[string]any{"sink": sinkRef("webhook")}))

	data := dataOf(t, events, "sink.stopped")

	if failure, present := data["failure"]; !present || failure != nil {
		t.Errorf("failure %#v (present %v), want null", failure, present)
	}
}

func TestASinkEventsDataHasEveryFieldOfItsTypeEvenWhenTheEntryLacksOne(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		keys []string
	}{
		{"sink.failing", []string{"sink", "event_kind", "failure"}},
		{"sink.stopped", []string{"sink", "event_kind", "consecutive_dead_letters", "failure"}},
		{"sink.at_risk", []string{"sink", "event_kind", "lag_seconds", "loss_warning_hours"}},
		{"sink.data_lost", []string{"sink", "event_kind", "gap"}},
	} {
		events := governance.DeriveEvents(sinkEntry(tc.typ, governance.SinkDeliveryActor, map[string]any{"sink": sinkRef("webhook")}))

		data := dataOf(t, events, tc.typ)

		got := make([]string, 0, len(data))
		for k := range data {
			got = append(got, k)
		}
		slices.Sort(got)
		if want := slices.Sorted(slices.Values(tc.keys)); !slices.Equal(got, want) {
			t.Errorf("%s: data keys %v, want %v", tc.typ, got, want)
		}
	}
}

func TestEveryOtherSinkActionIsOnlyAnAuditEntry(t *testing.T) {
	for _, action := range []string{"sink.created", "sink.updated", "sink.deleted", "sink.secret_rotated", "sink.enabled", "sink.disabled", "sink.tested", "sink.config_tested", "sink.resumed", "sink.dead_lettered", "sink.dead_letter_redelivered", "sink.dead_letter_discarded", "sink.replayed", "sink.sampled"} {
		e := sinkEntry(action, governance.SinkDeliveryActor, map[string]any{"sink": sinkRef("webhook")})

		if got := types(governance.DeriveEvents(e)); !slices.Equal(got, []string{governance.EventAuditEntry}) {
			t.Errorf("%s: events %v, want only the entry", action, got)
		}
	}
}

func TestTheCatalogueNamesTheFourSinkEvents(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range governance.EventCatalogue() {
		seen[e.Type] = true
	}
	for _, typ := range []string{"sink.failing", "sink.at_risk", "sink.stopped", "sink.data_lost"} {
		if !seen[typ] {
			t.Errorf("the catalogue lacks %s", typ)
		}
	}
}

func TestTheSinkEventsAreAttributedToTheSystem(t *testing.T) {
	for _, a := range []governance.Actor{governance.SinkDeliveryActor, governance.EventRetentionActor} {
		if a.Kind != governance.ActorSystem || a.ID == "" || a.Name == "" {
			t.Errorf("actor %+v, want a named system actor", a)
		}
	}
	if governance.SinkDeliveryActor.ID == governance.EventRetentionActor.ID {
		t.Error("delivery and retention share an actor")
	}
}
