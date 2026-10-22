package delivery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The bounds of an events batch: generous for an SDK flushing every few
// seconds, small enough that judging one stays cheap whoever posts it.
const (
	// MaxBatchEvents is how many events one batch holds at most.
	MaxBatchEvents = 500
	// MaxBatchBytes bounds a batch's body after decompression.
	MaxBatchBytes = 1 << 20
	// MaxSummaryRows is how many rows one evaluation summary holds at most.
	MaxSummaryRows = 1000
	// MaxEventString bounds every string of an event, in bytes.
	MaxEventString = 256
	// MaxEventAhead is how far ahead of the server clock an event's time
	// is taken as given; a later time is taken as the time it was received,
	// so a device whose clock runs fast cannot place counts in the future.
	MaxEventAhead = 24 * time.Hour
)

// EventType is the kind of an event an SDK posts.
type EventType string

// The event types.
const (
	EventEvaluationSummary EventType = "evaluation_summary"
	EventAlias             EventType = "alias"
	EventExposure          EventType = "exposure"
)

// RejectionCode says why an event of a batch was refused. The set is open:
// an SDK treats a code it does not know like any other rejection.
type RejectionCode string

// The rejection codes this server answers.
const (
	RejectEventInvalid       RejectionCode = "event_invalid"
	RejectEventTypeUnknown   RejectionCode = "event_type_unknown"
	RejectEventIDInvalid     RejectionCode = "event_id_invalid"
	RejectOutOfWindow        RejectionCode = "out_of_window"
	RejectUndeclaredUnitType RejectionCode = "undeclared_unit_type"
	RejectUnknownExperiment  RejectionCode = "unknown_experiment"
)

// Rejection is why one event was refused, for the SDK that sent it.
type Rejection struct {
	Code   RejectionCode
	Detail string
}

// Event marks the identifiers of events, chosen by the SDK.
type Event struct{}

// EventID identifies an event, a UUIDv7 the SDK chose so that a retry is
// recognised.
type EventID = id.Of[Event]

// SDKIdentity names the SDK that sent a request.
type SDKIdentity struct {
	Name     string
	Version  string
	Platform string
}

// SummaryRow counts the evaluations of one flag that served one variation
// for one reason.
type SummaryRow struct {
	Flag string
	// Variation is "" when no variation was served.
	Variation string
	Reason    string
	Count     int64
}

// EvaluationSummary is an evaluation summary as an SDK reports it: counts of
// the evaluations it made over an interval.
type EvaluationSummary struct {
	IntervalStart  time.Time
	IntervalEnd    time.Time
	RulesetVersion int64
	AppVersion     string
	Rows           []SummaryRow
}

// Alias links the unit ID in use before sign-up to the one after.
type Alias struct {
	OccurredAt   time.Time
	FromUnitType string
	FromUnitID   string
	ToUnitType   string
	ToUnitID     string
}

// Exposure is a unit encountering behaviour that depends on its experiment
// assignment.
type Exposure struct {
	OccurredAt       time.Time
	ExperimentKey    string
	FlagKey          string
	VariationKey     string
	UnitType         string
	UnitID           string
	Platform         string
	AppVersion       string
	EvaluationReason string
	Override         bool
	RulesetVersion   int64
}

// JudgedEvent is one event of a batch as judged on its own: of a known
// type with valid members, or rejected.
type JudgedEvent struct {
	// SentID is the event_id as sent, when it was a string, which a
	// rejection echoes.
	SentID string
	// ID is the event's identifier, when it is valid.
	ID   EventID
	Type EventType
	// One of these is set for an event not rejected, after its type.
	Summary  *EvaluationSummary
	Alias    *Alias
	Exposure *Exposure
	// Rejection is set when the event is refused.
	Rejection *Rejection
}

func (e JudgedEvent) reject(code RejectionCode, detail string) JudgedEvent {
	e.Summary, e.Alias, e.Exposure = nil, nil, nil
	e.Rejection = &Rejection{Code: code, Detail: detail}
	return e
}

// Reject returns e refused with code, for what only the stored state can
// tell, such as a unit type the project does not declare.
func (e JudgedEvent) Reject(code RejectionCode, detail string) JudgedEvent {
	return e.reject(code, detail)
}

// summaryReasons are the reasons a summary row may count: the true reasons
// an SDK evaluating a ruleset sees, the SDK's own fallback and local
// override, and targeting_match, which a client SDK counts from the masked
// answers it receives.
var summaryReasons = map[string]bool{
	string(eval.ReasonOff): true, string(eval.ReasonOverride): true, string(eval.ReasonRule): true,
	string(eval.ReasonRollout): true, string(eval.ReasonExperiment): true, string(eval.ReasonFallthrough): true,
	string(eval.ReasonPrerequisiteFailed): true, string(eval.ReasonError): true,
	"fallback": true, "local_override": true, string(ReasonTargetingMatch): true,
}

// JudgeEvent judges one event of a batch received at receivedAt, against a
// retention window of retention: its type, its event_id, its members, then
// its time. Whatever only the stored state can tell (duplicates, declared
// unit types, known flags and experiments) is left to the caller.
func JudgeEvent(raw json.RawMessage, receivedAt time.Time, retention time.Duration) JudgedEvent {
	var e JudgedEvent
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var members map[string]any
	if err := dec.Decode(&members); err != nil || members == nil {
		return e.reject(RejectEventInvalid, "an event is a JSON object")
	}
	r := &reader{members: members}
	if s, ok := members["event_id"].(string); ok {
		e.SentID = s
	}
	typ := r.requiredString("type")
	if len(r.faults) > 0 {
		return e.reject(RejectEventInvalid, r.detail())
	}
	e.Type = EventType(typ)
	switch e.Type {
	case EventEvaluationSummary, EventAlias, EventExposure:
	default:
		return e.reject(RejectEventTypeUnknown, fmt.Sprintf("this server does not know events of type %q", typ))
	}
	eventID, ok := ParseEventID(e.SentID)
	if !ok {
		return e.reject(RejectEventIDInvalid, "event_id must be a lowercase UUIDv7")
	}
	e.ID = eventID
	oldest := receivedAt.Add(-retention)
	if e.ID.Time().Before(oldest) {
		return e.reject(RejectOutOfWindow, "the event_id time is older than the raw retention")
	}
	switch e.Type {
	case EventEvaluationSummary:
		s := r.summary()
		if len(r.faults) > 0 {
			return e.reject(RejectEventInvalid, r.detail())
		}
		if s.IntervalEnd.Before(oldest) {
			return e.reject(RejectOutOfWindow, "interval_end is older than the raw retention")
		}
		if s.IntervalEnd.After(receivedAt.Add(MaxEventAhead)) {
			s.IntervalEnd = receivedAt
			if s.IntervalStart.After(receivedAt) {
				s.IntervalStart = receivedAt
			}
		}
		e.Summary = &s
	case EventAlias:
		a := Alias{
			OccurredAt:   r.time("occurred_at"),
			FromUnitType: r.requiredString("from_unit_type"), FromUnitID: r.requiredString("from_unit_id"),
			ToUnitType: r.requiredString("to_unit_type"), ToUnitID: r.requiredString("to_unit_id"),
		}
		if len(r.faults) > 0 {
			return e.reject(RejectEventInvalid, r.detail())
		}
		if a.OccurredAt.Before(oldest) {
			return e.reject(RejectOutOfWindow, "occurred_at is older than the raw retention")
		}
		a.OccurredAt = clampAhead(a.OccurredAt, receivedAt)
		e.Alias = &a
	case EventExposure:
		x := Exposure{
			OccurredAt: r.time("occurred_at"), ExperimentKey: r.requiredString("experiment_key"),
			FlagKey: r.requiredString("flag_key"), VariationKey: r.requiredString("variation_key"),
			UnitType: r.requiredString("unit_type"), UnitID: r.requiredString("unit_id"),
			Platform: r.optionalString("platform"), AppVersion: r.optionalString("app_version"),
			EvaluationReason: r.requiredString("evaluation_reason"), Override: r.boolean("override"),
			RulesetVersion: r.integer("ruleset_version", 0),
		}
		if len(r.faults) > 0 {
			return e.reject(RejectEventInvalid, r.detail())
		}
		if x.OccurredAt.Before(oldest) {
			return e.reject(RejectOutOfWindow, "occurred_at is older than the raw retention")
		}
		x.OccurredAt = clampAhead(x.OccurredAt, receivedAt)
		e.Exposure = &x
	}
	return e
}

// clampAhead returns at, or receivedAt when at is more than MaxEventAhead
// ahead of it.
func clampAhead(at, receivedAt time.Time) time.Time {
	if at.After(receivedAt.Add(MaxEventAhead)) {
		return receivedAt
	}
	return at
}

// ParseEventID reads an event ID: a UUIDv7 of RFC 9562 in its canonical,
// lowercase form, so the same event is never written two ways.
func ParseEventID(s string) (EventID, bool) {
	if s != strings.ToLower(s) {
		return EventID{}, false
	}
	v, err := id.Parse[Event](s)
	if err != nil {
		return EventID{}, false
	}
	b := v.Bytes()
	if b[6]>>4 != 7 || b[8]>>6 != 2 {
		return EventID{}, false
	}
	return v, true
}

// reader reads the members of one event, gathering a fault for each member
// missing or malformed.
type reader struct {
	members map[string]any
	// prefix names where the members read sit, such as "counts[2].".
	prefix string
	faults []string
}

// maxFaultsShown bounds the faults one rejection names, so its detail stays
// short however broken the event is.
const maxFaultsShown = 5

func (r *reader) detail() string {
	if len(r.faults) <= maxFaultsShown {
		return strings.Join(r.faults, "; ")
	}
	return strings.Join(r.faults[:maxFaultsShown], "; ") + "; and " + strconv.Itoa(len(r.faults)-maxFaultsShown) + " more"
}

func (r *reader) fault(name, format string, args ...any) {
	r.faults = append(r.faults, r.prefix+name+" "+fmt.Sprintf(format, args...))
}

// checkString faults a string over MaxEventString bytes or holding U+0000.
func (r *reader) checkString(name, s string) {
	switch {
	case len(s) > MaxEventString:
		r.fault(name, "is over %d bytes", MaxEventString)
	case strings.ContainsRune(s, 0):
		r.fault(name, "must not contain U+0000")
	}
}

func (r *reader) requiredString(name string) string {
	v, ok := r.members[name]
	if !ok || v == nil {
		r.fault(name, "is missing")
		return ""
	}
	s, ok := v.(string)
	switch {
	case !ok:
		r.fault(name, "must be a string")
	case s == "":
		r.fault(name, "must not be empty")
	default:
		r.checkString(name, s)
	}
	return s
}

// optionalString reads a string that may be absent or null, as "".
func (r *reader) optionalString(name string) string {
	v, ok := r.members[name]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		r.fault(name, "must be a string or null")
		return ""
	}
	r.checkString(name, s)
	return s
}

func (r *reader) boolean(name string) bool {
	v, ok := r.members[name]
	if !ok || v == nil {
		r.fault(name, "is missing")
		return false
	}
	b, ok := v.(bool)
	if !ok {
		r.fault(name, "must be a boolean")
	}
	return b
}

// integer reads a whole number of at least least.
func (r *reader) integer(name string, least int64) int64 {
	v, ok := r.members[name]
	if !ok || v == nil {
		r.fault(name, "is missing")
		return 0
	}
	n, ok := v.(json.Number)
	var i int64
	var err error
	if ok {
		i, err = n.Int64()
	}
	if !ok || err != nil || i < least {
		if least == 0 {
			r.fault(name, "must be a non-negative integer")
		} else {
			r.fault(name, "must be an integer of at least %d", least)
		}
		return 0
	}
	return i
}

func (r *reader) time(name string) time.Time {
	v, ok := r.members[name]
	if !ok || v == nil {
		r.fault(name, "is missing")
		return time.Time{}
	}
	s, _ := v.(string)
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		r.fault(name, "must be an RFC 3339 time")
	}
	return t
}

// summary reads the members of an evaluation summary.
func (r *reader) summary() EvaluationSummary {
	s := EvaluationSummary{
		IntervalStart: r.time("interval_start"), IntervalEnd: r.time("interval_end"),
		RulesetVersion: r.integer("ruleset_version", 0), AppVersion: r.optionalString("app_version"),
	}
	if len(r.faults) == 0 && s.IntervalStart.After(s.IntervalEnd) {
		r.fault("interval_start", "is after interval_end")
	}
	v, ok := r.members["counts"]
	if !ok || v == nil {
		r.fault("counts", "is missing")
		return s
	}
	rows, ok := v.([]any)
	if !ok {
		r.fault("counts", "must be a list of objects")
		return s
	}
	if len(rows) > MaxSummaryRows {
		r.fault("counts", "holds %d rows, at most %d", len(rows), MaxSummaryRows)
		return s
	}
	for i, raw := range rows {
		members, ok := raw.(map[string]any)
		if !ok {
			r.fault("counts["+strconv.Itoa(i)+"]", "must be an object")
			continue
		}
		row := &reader{members: members, prefix: "counts[" + strconv.Itoa(i) + "]."}
		sr := SummaryRow{
			Flag: row.requiredString("flag"), Variation: row.optionalString("variation"),
			Reason: row.requiredString("reason"), Count: row.integer("count", 1),
		}
		if sr.Reason != "" && !summaryReasons[sr.Reason] && len(row.faults) == 0 {
			row.fault("reason", "%q is not an evaluation reason", sr.Reason)
		}
		r.faults = append(r.faults, row.faults...)
		s.Rows = append(s.Rows, sr)
	}
	return s
}

// SummarySource says who counted the evaluations of a summary row.
type SummarySource string

// The summary sources.
const (
	// SourceSDK is an SDK reporting the evaluations it made.
	SourceSDK SummarySource = "sdk"
	// SourceRemoteEvaluation is the server counting the remote evaluations
	// it answered, which a client SDK may count again from the answers it
	// received: reports keep the two apart so neither is added twice.
	SourceRemoteEvaluation SummarySource = "remote_evaluation"
)

// SummaryRecord is one row of an evaluation summary as stored: what was
// counted, by whom, and where.
type SummaryRecord struct {
	Source SummarySource
	// EventID is the summary's, zero for the server's own counts.
	EventID        EventID
	Key            SDKKey
	SDK            SDKIdentity
	AppVersion     string
	RulesetVersion int64
	PeriodStart    time.Time
	PeriodEnd      time.Time
	Row            SummaryRow
	ReceivedAt     time.Time
}

// AliasRecord is an alias as stored.
type AliasRecord struct {
	EventID    EventID
	Key        SDKKey
	SDK        SDKIdentity
	Alias      Alias
	ReceivedAt time.Time
}
