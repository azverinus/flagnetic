package governance

// The sink health events: what a project's webhook subscriptions are told
// when one of its sinks stops delivering, is about to lose data, or has.
// Each is the audit entry of the same action, recorded once per episode by
// the system, so a receiver, the audit log and the console's change stream
// all hear of it through the one path every other event takes. There is no
// event for recovery: the sink's health says when it delivers again.
const (
	// EventSinkFailing tells that a sink's deliveries started failing.
	EventSinkFailing = "sink.failing"
	// EventSinkAtRisk tells that a sink's oldest undelivered row came
	// within the loss warning of the edge of retention.
	EventSinkAtRisk = "sink.at_risk"
	// EventSinkStopped tells that a sink stopped itself after consecutive
	// dead letters.
	EventSinkStopped = "sink.stopped"
	// EventSinkDataLost tells that retention dropped rows a sink had not
	// delivered.
	EventSinkDataLost = "sink.data_lost"
)

// SinkDeliveryActor is who the audit log and the receivers are told a sink
// started failing, stopped, or came near losing data: the platform itself,
// acting on its delivery attempts.
var SinkDeliveryActor = Actor{Kind: ActorSystem, ID: "sink_delivery", Name: "sink delivery"}

// EventRetentionActor is who the audit log and the receivers are told
// dropped the rows a sink had not delivered: the platform itself, dropping a
// partition.
var EventRetentionActor = Actor{Kind: ActorSystem, ID: "event_retention", Name: "event retention"}

// sinkEventFields are the fields of each sink health event's data, in the
// order the documentation lists them. The audit entry's `after` is the
// document the event is made from, and only these fields reach a receiver,
// each null where the entry did not record it, so one parser reads every
// occurrence of a type.
var sinkEventFields = map[string][]string{
	"sink.failing":   {"sink", "event_kind", "failure"},
	"sink.stopped":   {"sink", "event_kind", "consecutive_dead_letters", "failure"},
	"sink.at_risk":   {"sink", "event_kind", "lag_seconds", "loss_warning_hours"},
	"sink.data_lost": {"sink", "event_kind", "gap"},
}

// sinkEventObjects are the fields of the objects inside a sink event's data.
// A failure is told by its class, the HTTP status when there was one, the
// protocol's own error code when the target speaks one, and when it
// happened, never by the target's own words.
var sinkEventObjects = map[string][]string{
	"sink":    {"id", "name", "kind"},
	"failure": {"class", "status", "code", "at"},
}

// sinkEvent returns the sink health event typ made from the sink's entry
// document after.
func sinkEvent(typ string, after any) Derived {
	doc := mapOf(after)
	data := make(map[string]any, len(sinkEventFields[typ]))
	for _, field := range sinkEventFields[typ] {
		keys, object := sinkEventObjects[field]
		if !object {
			data[field] = doc[field]
			continue
		}
		// A field that should hold an object but holds anything else is
		// null, never passed through: whatever it held is not the object's
		// fields, so it is not the receiver's to read. An untyped nil keeps
		// it null to anyone reading data before it is encoded too.
		data[field] = nil
		if m := mapOf(doc[field]); m != nil {
			data[field] = completeObject(m, keys)
		}
	}
	return Derived{typ, data}
}

// completeObject returns the fields keys of m, null where m lacks one.
func completeObject(m map[string]any, keys []string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
