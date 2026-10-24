package events_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
)

var received = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

const retention = 30 * 24 * time.Hour

// eventIDAt returns a UUIDv7 minted at t, numbered n.
func eventIDAt(t time.Time, n int) string {
	ms := fmt.Sprintf("%012x", t.UnixMilli())
	return fmt.Sprintf("%s-%s-7000-8000-%012x", ms[:8], ms[8:], n)
}

func judge(t *testing.T, event map[string]any) events.JudgedEvent {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return events.JudgeEvent(raw, received, retention, 0)
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
		code   events.RejectionCode
		detail string
	}{
		"type missing":             {with(alias(id), map[string]any{"type": nil}), events.RejectEventInvalid, "type is missing"},
		"unknown type":             {with(alias(id), map[string]any{"type": "future_event"}), events.RejectEventTypeUnknown, `this server does not know events of type "future_event"`},
		"no event ID":              {with(alias(id), map[string]any{"event_id": nil}), events.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"not a UUID":               {alias("not-a-uuid"), events.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"upper case":               {alias(strings.ToUpper(eventIDAt(received, 0xa))), events.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"UUIDv4":                   {alias("01a0b91b-c500-4000-8000-000000000001"), events.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"member missing":           {with(alias(id), map[string]any{"from_unit_id": nil}), events.RejectEventInvalid, "from_unit_id is missing"},
		"member of the wrong type": {with(alias(id), map[string]any{"to_unit_id": 7}), events.RejectEventInvalid, "to_unit_id must be a string"},
		"string over 256 bytes":    {with(alias(id), map[string]any{"to_unit_id": strings.Repeat("u", 257)}), events.RejectEventInvalid, "to_unit_id is over 256 bytes"},
		"string with NUL":          {with(alias(id), map[string]any{"to_unit_id": "u\x00"}), events.RejectEventInvalid, "to_unit_id must not contain U+0000"},
		"old event ID":             {alias(old), events.RejectOutOfWindow, "the event_id time is older than the raw retention"},
		"old occurred_at":          {with(alias(id), map[string]any{"occurred_at": "2026-08-01T00:00:00Z"}), events.RejectOutOfWindow, "occurred_at is older than the raw retention"},
		"old interval_end": {with(summary(id, row("checkout", "rule", 1)), map[string]any{
			"interval_start": "2026-08-01T00:00:00Z", "interval_end": "2026-08-01T00:01:00Z",
		}), events.RejectOutOfWindow, "interval_end is older than the raw retention"},
		"summary over 1,000 rows": {summary(id, rows1001...), events.RejectEventInvalid, "counts holds 1001 rows, at most 1000"},
		"unknown reason":          {summary(id, row("checkout", "lucky", 1)), events.RejectEventInvalid, `counts[0].reason "lucky" is not an evaluation reason`},
		"count of 0":              {summary(id, row("checkout", "rule", 0)), events.RejectEventInvalid, "counts[0].count must be an integer of at least 1"},
		"interval backwards": {with(summary(id, row("checkout", "rule", 1)), map[string]any{
			"interval_start": "2026-09-19T10:00:00Z", "interval_end": "2026-09-19T09:00:00Z",
		}), events.RejectEventInvalid, "interval_start is after interval_end"},
		"not an object": {nil, events.RejectEventInvalid, "an event is a JSON object"},
		"empty string":  {with(alias(id), map[string]any{"from_unit_id": ""}), events.RejectEventInvalid, "from_unit_id must not be empty"},
		"many faults": {map[string]any{"type": "alias", "event_id": id}, events.RejectEventInvalid,
			"occurred_at is missing; from_unit_type is missing; from_unit_id is missing; to_unit_type is missing; to_unit_id is missing"},
		"occurred_at not a time": {with(alias(id), map[string]any{"occurred_at": "yesterday"}), events.RejectEventInvalid, "occurred_at must be an RFC 3339 time"},
		"more faults than shown": {map[string]any{"type": "evaluation_summary", "event_id": id, "counts": []any{map[string]any{}}},
			events.RejectEventInvalid,
			"interval_start is missing; interval_end is missing; ruleset_version is missing; counts[0].flag is missing; counts[0].reason is missing; and 1 more"},
		"summary without counts": {with(summary(id), map[string]any{"counts": nil, "ruleset_version": nil}), events.RejectEventInvalid, "ruleset_version is missing; counts is missing"},
		"counts not a list":      {with(summary(id), map[string]any{"counts": "many"}), events.RejectEventInvalid, "counts must be a list of objects"},
		"a row not an object":    {with(summary(id), map[string]any{"counts": []any{"checkout"}}), events.RejectEventInvalid, "counts[0] must be an object"},
	} {
		t.Run(name, func(t *testing.T) {
			var e events.JudgedEvent
			if c.event == nil {
				e = events.JudgeEvent(json.RawMessage(`[1]`), received, retention, 0)
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

func TestAValidExposureIsAccepted(t *testing.T) {
	id := eventIDAt(received, 1)
	e := judge(t, with(exposure(id), map[string]any{"platform": "ios", "app_version": "2.4.0", "override": true, "evaluation_reason": "override"}))
	if e.Rejection != nil || e.Exposure == nil || e.ID.String() != id {
		t.Fatalf("exposure = %+v %+v", e.Exposure, e.Rejection)
	}
	want := events.Exposure{
		OccurredAt: time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC), ExperimentKey: "onboarding_test", FlagKey: "onboarding",
		VariationKey: "guided", UnitType: "user", UnitID: "u-1001", Platform: "ios", AppVersion: "2.4.0",
		Reason: "override", Override: true, RulesetVersion: 1,
	}
	if *e.Exposure != want {
		t.Errorf("exposure = %+v, want %+v", *e.Exposure, want)
	}
}

func TestAnExposureIsJudgedLikeAnyOtherEvent(t *testing.T) {
	id := eventIDAt(received, 1)
	for name, c := range map[string]struct {
		event  map[string]any
		code   events.RejectionCode
		detail string
	}{
		"member missing":      {with(exposure(id), map[string]any{"unit_id": nil}), events.RejectEventInvalid, "unit_id is missing"},
		"override not a bool": {with(exposure(id), map[string]any{"override": "no"}), events.RejectEventInvalid, "override must be a boolean"},
		"negative ruleset":    {with(exposure(id), map[string]any{"ruleset_version": -1}), events.RejectEventInvalid, "ruleset_version must be a non-negative integer"},
		"platform not text":   {with(exposure(id), map[string]any{"platform": 3}), events.RejectEventInvalid, "platform must be a string or null"},
		"unit ID too long":    {with(exposure(id), map[string]any{"unit_id": strings.Repeat("u", 300)}), events.RejectEventInvalid, "unit_id is over 256 bytes"},
		"event ID not UUIDv7": {exposure("not-a-uuid"), events.RejectEventIDInvalid, "event_id must be a lowercase UUIDv7"},
		"old event ID":        {exposure(eventIDAt(received.Add(-31*24*time.Hour), 2)), events.RejectOutOfWindow, "the event_id time is older than the raw retention"},
		"old occurred_at":     {with(exposure(id), map[string]any{"occurred_at": "2026-08-01T00:00:00Z"}), events.RejectOutOfWindow, "occurred_at is older than the raw retention"},
	} {
		t.Run(name, func(t *testing.T) {
			e := judge(t, c.event)
			if e.Rejection == nil || e.Rejection.Code != c.code || e.Rejection.Detail != c.detail {
				t.Errorf("rejection = %+v, want %s %q", e.Rejection, c.code, c.detail)
			}
		})
	}
}

func TestAnExposureAheadOfTheServerIsTakenAsReceived(t *testing.T) {
	e := judge(t, with(exposure(eventIDAt(received, 1)), map[string]any{"occurred_at": "2026-09-22T10:00:00Z"}))
	if !e.Exposure.OccurredAt.Equal(received) {
		t.Errorf("occurred_at = %v, want %v", e.Exposure.OccurredAt, received)
	}
}

func TestAClockIsSkewedOnlyBeyondAMinute(t *testing.T) {
	sent := received
	for name, c := range map[string]struct {
		receivedAt time.Time
		want       time.Duration
	}{
		"in step":               {sent, 0},
		"59 seconds behind":     {sent.Add(59 * time.Second), 0},
		"59 seconds ahead":      {sent.Add(-59 * time.Second), 0},
		"exactly a minute":      {sent.Add(time.Minute), 0},
		"a minute and a second": {sent.Add(61 * time.Second), 61 * time.Second},
		"a phone a day slow":    {sent.Add(24 * time.Hour), 24 * time.Hour},
		"a phone an hour fast":  {sent.Add(-time.Hour), -time.Hour},
		"61 seconds fast":       {sent.Add(-61 * time.Second), -61 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			if got := events.ClockSkew(sent, c.receivedAt); got != c.want {
				t.Errorf("ClockSkew = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSkewShiftsEveryTimeOfEveryEventBeforeTheWindowCheck(t *testing.T) {
	// A phone two days slow: everything it stamps is two days early.
	skew := 48 * time.Hour
	judgeSkewed := func(event map[string]any) events.JudgedEvent {
		raw, _ := json.Marshal(event)
		return events.JudgeEvent(raw, received, retention, skew)
	}
	id := eventIDAt(received, 1)
	slow := func(t time.Time) string { return t.Add(-skew).Format(time.RFC3339) }

	x := judgeSkewed(with(exposure(id), map[string]any{"occurred_at": slow(received.Add(-time.Hour))}))
	if x.Exposure == nil || !x.Exposure.OccurredAt.Equal(received.Add(-time.Hour)) {
		t.Errorf("exposure = %+v %+v, want occurred an hour ago", x.Exposure, x.Rejection)
	}
	a := judgeSkewed(with(alias(id), map[string]any{"occurred_at": slow(received.Add(-time.Hour))}))
	if a.Alias == nil || !a.Alias.OccurredAt.Equal(received.Add(-time.Hour)) {
		t.Errorf("alias = %+v %+v, want occurred an hour ago", a.Alias, a.Rejection)
	}
	s := judgeSkewed(with(summary(id, row("checkout", "rule", 1)), map[string]any{
		"interval_start": slow(received.Add(-time.Minute)), "interval_end": slow(received),
	}))
	if s.Summary == nil || !s.Summary.IntervalStart.Equal(received.Add(-time.Minute)) || !s.Summary.IntervalEnd.Equal(received) {
		t.Errorf("summary = %+v %+v, want the last minute", s.Summary, s.Rejection)
	}

	// An event of a phone 29 days slow is not lost to the window: shifted, it
	// is recent. Unshifted, it would be out of the window.
	old := events.JudgeEvent(mustJSON(with(exposure(id), map[string]any{
		"occurred_at": received.Add(-29 * 24 * time.Hour).Add(-time.Hour).Format(time.RFC3339),
	})), received, retention, 29*24*time.Hour)
	if old.Rejection != nil {
		t.Errorf("a slow phone's exposure = %+v, want accepted", old.Rejection)
	}
	// A time still older than the window after the shift is out of it.
	tooOld := judgeSkewed(with(exposure(id), map[string]any{"occurred_at": slow(received.Add(-31 * 24 * time.Hour))}))
	if tooOld.Rejection == nil || tooOld.Rejection.Code != events.RejectOutOfWindow {
		t.Errorf("a shifted time past retention = %+v, want out_of_window", tooOld.Rejection)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func onboarding() *events.ExperimentFacts {
	return &events.ExperimentFacts{FlagKey: "onboarding", UnitType: "user", Variations: []string{"control", "guided"}}
}

func exposed(t *testing.T, changes map[string]any) events.Exposure {
	t.Helper()
	e := judge(t, with(exposure(eventIDAt(received, 1)), changes))
	if e.Exposure == nil {
		t.Fatalf("exposure rejected: %+v", e.Rejection)
	}
	return *e.Exposure
}

func TestAnExposureIsCheckedAgainstItsExperimentInOrder(t *testing.T) {
	for name, c := range map[string]struct {
		x          events.Exposure
		experiment *events.ExperimentFacts
		code       events.RejectionCode
		server     string
		client     string
	}{
		"no such experiment": {exposed(t, nil), nil, events.RejectUnknownExperiment,
			`no experiment is keyed "onboarding_test"`, `no experiment is keyed "onboarding_test"`},
		"another unit type": {exposed(t, map[string]any{"unit_type": "anonymous"}), onboarding(), events.RejectUnitTypeMismatch,
			`experiment "onboarding_test" assigns units of type "user", not "anonymous"`,
			"the unit type is not the experiment's"},
		"a variation it lacks": {exposed(t, map[string]any{"variation_key": "wizard"}), onboarding(), events.RejectUnknownVariation,
			`experiment "onboarding_test" has no variation "wizard"`, "the variation is not one of the experiment's"},
		"another flag": {exposed(t, map[string]any{"flag_key": "pricing"}), onboarding(), events.RejectEventInvalid,
			`flag_key is "pricing", but experiment "onboarding_test" is on flag "onboarding"`, "the flag is not the experiment's"},
		"a reason that is not an experiment's": {exposed(t, map[string]any{"evaluation_reason": "rule"}), onboarding(), events.RejectEventInvalid,
			`evaluation_reason is "rule", not "experiment" or "override"`, `evaluation_reason is "rule", not "experiment" or "override"`},
		// The unit type is judged before the variation, the variation before
		// the flag and the reason.
		"unit type before variation": {exposed(t, map[string]any{"unit_type": "device", "variation_key": "wizard", "flag_key": "x"}), onboarding(),
			events.RejectUnitTypeMismatch, `experiment "onboarding_test" assigns units of type "user", not "device"`,
			"the unit type is not the experiment's"},
		"variation before flag": {exposed(t, map[string]any{"variation_key": "wizard", "flag_key": "pricing", "evaluation_reason": "rule"}), onboarding(),
			events.RejectUnknownVariation, `experiment "onboarding_test" has no variation "wizard"`, "the variation is not one of the experiment's"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, detailed := range []bool{true, false} {
				got := events.CheckExposure(c.x, c.experiment, detailed)
				want := c.client
				if detailed {
					want = c.server
				}
				if got == nil || got.Code != c.code || got.Detail != want {
					t.Errorf("detailed %t: rejection = %+v, want %s %q", detailed, got, c.code, want)
				}
			}
		})
	}
}

func TestAnOverrideExposureMayNameAnyVariationOfTheFlag(t *testing.T) {
	x := exposed(t, map[string]any{"variation_key": "legacy", "override": true, "evaluation_reason": "override"})
	if got := events.CheckExposure(x, onboarding(), true); got != nil {
		t.Errorf("override exposure of a variation the experiment does not weigh = %+v, want accepted", got)
	}
	plain := exposed(t, map[string]any{"variation_key": "legacy"})
	if got := events.CheckExposure(plain, onboarding(), true); got == nil || got.Code != events.RejectUnknownVariation {
		t.Errorf("the same without override = %+v, want unknown_variation", got)
	}
}

func TestAnExposureThatAgreesIsNotRejected(t *testing.T) {
	for _, changes := range []map[string]any{nil, {"variation_key": "control"}, {"override": true, "evaluation_reason": "override"}} {
		if got := events.CheckExposure(exposed(t, changes), onboarding(), false); got != nil {
			t.Errorf("%v = %+v, want accepted", changes, got)
		}
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
