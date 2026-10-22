package delivery_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
)

var received = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

const retention = 30 * 24 * time.Hour

// eventIDAt returns a UUIDv7 minted at t, numbered n.
func eventIDAt(t time.Time, n int) string {
	ms := fmt.Sprintf("%012x", t.UnixMilli())
	return fmt.Sprintf("%s-%s-7000-8000-%012x", ms[:8], ms[8:], n)
}

func judge(t *testing.T, event map[string]any) delivery.JudgedEvent {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return delivery.JudgeEvent(raw, received, retention)
}

func alias(id string) map[string]any {
	return map[string]any{
		"type": "alias", "event_id": id, "occurred_at": "2026-09-19T09:00:00Z",
		"from_unit_type": "anonymous", "from_unit_id": "d-8f1c", "to_unit_type": "user", "to_unit_id": "u-1001",
	}
}

func summary(id string, rows ...map[string]any) map[string]any {
	counts := make([]any, 0, len(rows))
	for _, r := range rows {
		counts = append(counts, r)
	}
	return map[string]any{
		"type": "evaluation_summary", "event_id": id, "interval_start": "2026-09-19T09:59:00Z",
		"interval_end": "2026-09-19T10:00:00Z", "ruleset_version": 1, "counts": counts,
	}
}

func row(flag, reason string, count int) map[string]any {
	return map[string]any{"flag": flag, "variation": "true", "reason": reason, "count": count}
}

func with(event map[string]any, changes map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range event {
		out[k] = v
	}
	for k, v := range changes {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

func TestAValidAliasAndSummaryAreAccepted(t *testing.T) {
	id := eventIDAt(received, 1)
	a := judge(t, alias(id))
	if a.Rejection != nil || a.Alias == nil || a.ID.String() != id || a.Alias.FromUnitID != "d-8f1c" {
		t.Errorf("alias = %+v %+v", a, a.Rejection)
	}
	s := judge(t, summary(id, row("checkout", "targeting_match", 3), map[string]any{
		"flag": "banner_text", "variation": nil, "reason": "fallback", "count": 2,
	}))
	if s.Rejection != nil || s.Summary == nil || len(s.Summary.Rows) != 2 || s.Summary.Rows[1].Variation != "" ||
		s.Summary.Rows[0].Reason != "targeting_match" {
		t.Errorf("summary = %+v %+v", s.Summary, s.Rejection)
	}
}

func TestEachEventIsRejectedWithItsCode(t *testing.T) {
	id := eventIDAt(received, 1)
	old := eventIDAt(received.Add(-31*24*time.Hour), 2)
	rows1001 := make([]map[string]any, 1001)
	for i := range rows1001 {
		rows1001[i] = row("checkout", "rule", 1)
	}
	for name, c := range map[string]struct {
		event  map[string]any
		code   delivery.RejectionCode
		detail string
	}{
		"type missing":             {with(alias(id), map[string]any{"type": nil}), delivery.RejectEventInvalid, "type is missing"},
		"unknown type":             {with(alias(id), map[string]any{"type": "future_event"}), delivery.RejectEventTypeUnknown, `this server does not know events of type "future_event"`},
		"no event ID":              {with(alias(id), map[string]any{"event_id": nil}), delivery.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"not a UUID":               {alias("not-a-uuid"), delivery.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"upper case":               {alias(strings.ToUpper(eventIDAt(received, 0xa))), delivery.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"UUIDv4":                   {alias("01a0b91b-c500-4000-8000-000000000001"), delivery.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"member missing":           {with(alias(id), map[string]any{"from_unit_id": nil}), delivery.RejectEventInvalid, "from_unit_id is missing"},
		"member of the wrong type": {with(alias(id), map[string]any{"to_unit_id": 7}), delivery.RejectEventInvalid, "to_unit_id must be a string"},
		"string over 256 bytes":    {with(alias(id), map[string]any{"to_unit_id": strings.Repeat("u", 257)}), delivery.RejectEventInvalid, "to_unit_id is over 256 bytes"},
		"string with NUL":          {with(alias(id), map[string]any{"to_unit_id": "u\x00"}), delivery.RejectEventInvalid, "to_unit_id must not contain U+0000"},
		"old event ID":             {alias(old), delivery.RejectOutOfWindow, "the event_id time is older than the raw retention"},
		"old occurred_at":          {with(alias(id), map[string]any{"occurred_at": "2026-08-01T00:00:00Z"}), delivery.RejectOutOfWindow, "occurred_at is older than the raw retention"},
		"old interval_end": {with(summary(id, row("checkout", "rule", 1)), map[string]any{
			"interval_start": "2026-08-01T00:00:00Z", "interval_end": "2026-08-01T00:01:00Z",
		}), delivery.RejectOutOfWindow, "interval_end is older than the raw retention"},
		"summary over 1,000 rows": {summary(id, rows1001...), delivery.RejectEventInvalid, "counts holds 1001 rows, at most 1000"},
		"unknown reason":          {summary(id, row("checkout", "lucky", 1)), delivery.RejectEventInvalid, `counts[0].reason "lucky" is not an evaluation reason`},
		"count of 0":              {summary(id, row("checkout", "rule", 0)), delivery.RejectEventInvalid, "counts[0].count must be an integer of at least 1"},
		"interval backwards": {with(summary(id, row("checkout", "rule", 1)), map[string]any{
			"interval_start": "2026-09-19T10:00:00Z", "interval_end": "2026-09-19T09:00:00Z",
		}), delivery.RejectEventInvalid, "interval_start is after interval_end"},
		"not an object": {nil, delivery.RejectEventInvalid, "an event is a JSON object"},
		"empty string":  {with(alias(id), map[string]any{"from_unit_id": ""}), delivery.RejectEventInvalid, "from_unit_id must not be empty"},
		"many faults": {map[string]any{"type": "alias", "event_id": id}, delivery.RejectEventInvalid,
			"occurred_at is missing; from_unit_type is missing; from_unit_id is missing; to_unit_type is missing; to_unit_id is missing"},
		"occurred_at not a time": {with(alias(id), map[string]any{"occurred_at": "yesterday"}), delivery.RejectEventInvalid, "occurred_at must be an RFC 3339 time"},
		"exposure faults": {with(exposure(id), map[string]any{"override": "no", "ruleset_version": -1, "platform": 3}), delivery.RejectEventInvalid,
			"platform must be a string or null; override must be a boolean; ruleset_version must be a non-negative integer"},
		"more faults than shown": {map[string]any{"type": "exposure", "event_id": id}, delivery.RejectEventInvalid,
			"occurred_at is missing; experiment_key is missing; flag_key is missing; variation_key is missing; unit_type is missing; and 4 more"},
		"exposure override missing": {with(exposure(id), map[string]any{"override": nil}), delivery.RejectEventInvalid, "override is missing"},
		"old exposure":              {with(exposure(id), map[string]any{"occurred_at": "2026-08-01T00:00:00Z"}), delivery.RejectOutOfWindow, "occurred_at is older than the raw retention"},
		"summary without counts":    {with(summary(id), map[string]any{"counts": nil, "ruleset_version": nil}), delivery.RejectEventInvalid, "ruleset_version is missing; counts is missing"},
		"counts not a list":         {with(summary(id), map[string]any{"counts": "many"}), delivery.RejectEventInvalid, "counts must be a list of objects"},
		"a row not an object":       {with(summary(id), map[string]any{"counts": []any{"checkout"}}), delivery.RejectEventInvalid, "counts[0] must be an object"},
	} {
		t.Run(name, func(t *testing.T) {
			var e delivery.JudgedEvent
			if c.event == nil {
				e = delivery.JudgeEvent(json.RawMessage(`[1]`), received, retention)
			} else {
				e = judge(t, c.event)
			}
			if e.Rejection == nil {
				t.Fatalf("accepted, want %s", c.code)
			}
			if e.Rejection.Code != c.code || e.Rejection.Detail != c.detail {
				t.Errorf("rejection = %s %q, want %s %q", e.Rejection.Code, e.Rejection.Detail, c.code, c.detail)
			}
		})
	}
}

func exposure(id string) map[string]any {
	return map[string]any{
		"type": "exposure", "event_id": id, "occurred_at": "2026-09-19T09:00:00Z", "experiment_key": "onboarding_test",
		"flag_key": "onboarding", "variation_key": "guided", "unit_type": "user", "unit_id": "u-1001",
		"evaluation_reason": "experiment", "override": false, "ruleset_version": 1,
	}
}

func TestAValidExposureIsReadWhole(t *testing.T) {
	e := judge(t, with(exposure(eventIDAt(received, 1)), map[string]any{"occurred_at": "2026-09-23T00:00:00Z", "platform": "ios"}))
	if e.Rejection != nil || e.Exposure == nil || e.Exposure.ExperimentKey != "onboarding_test" || e.Exposure.Platform != "ios" ||
		e.Exposure.AppVersion != "" || !e.Exposure.OccurredAt.Equal(received) || e.Exposure.RulesetVersion != 1 {
		t.Errorf("exposure = %+v %+v", e.Exposure, e.Rejection)
	}
}

func TestASummaryOfExactly1000RowsIsAccepted(t *testing.T) {
	rows := make([]map[string]any, 1000)
	for i := range rows {
		rows[i] = row("checkout", "rule", 1)
	}
	if e := judge(t, summary(eventIDAt(received, 1), rows...)); e.Rejection != nil {
		t.Errorf("rejected: %+v", e.Rejection)
	}
}

func TestATimeFarAheadIsTakenAsTheTimeReceived(t *testing.T) {
	id := eventIDAt(received, 1)
	soon := judge(t, with(alias(id), map[string]any{"occurred_at": "2026-09-20T09:00:00Z"}))
	if !soon.Alias.OccurredAt.Equal(received.Add(23 * time.Hour)) {
		t.Errorf("23 hours ahead = %v, want kept", soon.Alias.OccurredAt)
	}
	far := judge(t, with(alias(id), map[string]any{"occurred_at": "2026-09-21T10:00:00Z"}))
	if !far.Alias.OccurredAt.Equal(received) {
		t.Errorf("2 days ahead = %v, want %v", far.Alias.OccurredAt, received)
	}
	s := judge(t, with(summary(id, row("checkout", "rule", 1)), map[string]any{
		"interval_start": "2026-09-22T10:00:00Z", "interval_end": "2026-09-22T10:01:00Z",
	}))
	if !s.Summary.IntervalStart.Equal(received) || !s.Summary.IntervalEnd.Equal(received) {
		t.Errorf("summary 3 days ahead = %v–%v, want both %v", s.Summary.IntervalStart, s.Summary.IntervalEnd, received)
	}
}
