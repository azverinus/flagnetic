package sinks_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var rowTime = time.Date(2026, 3, 4, 5, 6, 7, 891_000_000, time.UTC)

func exposureRow() sinks.ExposureRow {
	return sinks.ExposureRow{
		EventID: "0195f6b0-0000-7000-8000-000000000001", ProjectKey: "shop", EnvironmentKey: "production",
		ExperimentKey: "checkout_test", FlagKey: "checkout", VariationKey: "on", UnitType: "user", UnitID: "u-1",
		EvaluationReason: "experiment", Override: true, SDKName: "flagnetic-go", SDKVersion: "1.4.0", RulesetVersion: 7,
		OccurredAt: rowTime, ReceivedAt: rowTime.Add(time.Second),
	}
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

// The remote evaluation rows have no event ID, so the ID that stands for one
// is derived from where the row sits, and never changes; these vectors are
// the contract an analyst can recompute.
func TestAReportIDIsAUUIDv5OfThePositionOfItsRow(t *testing.T) {
	for _, tc := range []struct {
		xid  uint64
		seq  int64
		want string
	}{
		{1, 1, "8339fb84-daea-5a00-8334-75137623d9f2"},
		{4_294_967_296, 12, "82e0ced0-cc58-5b06-976c-35cf80af86ea"},
		{42, 9_000_000_000, "93cad210-1e40-5f81-9021-34cc5c886826"},
	} {
		got := sinks.ReportID(tc.xid, tc.seq)
		if got != tc.want {
			t.Errorf("ReportID(%d, %d) = %s, want %s", tc.xid, tc.seq, got, tc.want)
		}
	}
}

func TestAReportIDIsStableAndOtherPositionsGetOtherIDs(t *testing.T) {
	seen := map[string]string{}
	for _, p := range [][2]int64{{1, 1}, {1, 2}, {2, 1}, {11, 1}, {1, 11}} {
		got := sinks.ReportID(uint64(p[0]), p[1])
		if again := sinks.ReportID(uint64(p[0]), p[1]); again != got {
			t.Errorf("ReportID(%v) = %s then %s, want the same", p, got, again)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("ReportID(%v) = %s, the same as %s", p, got, other)
		}
		seen[got] = "position"
		if len(got) != 36 || got[14] != '5' {
			t.Errorf("ReportID(%v) = %s, want a version 5 UUID", p, got)
		}
	}
}

func TestADeliveryIDNamesTheSameRowsTheSameWayEveryTime(t *testing.T) {
	sink := id.MustParse[sinks.Sink]("0195f6b0-0000-7000-8000-0000000000aa")
	from, to := events.Cursor{XID: 10, Seq: 100}, events.Cursor{XID: 12, Seq: 130}
	got := sinks.DeliveryID(sink, sinks.EventExposure, from, to, []string{"a", "b", "c"})
	if len(got) != len("dlv_")+26 || !strings.HasPrefix(got, "dlv_") {
		t.Fatalf("DeliveryID = %q, want dlv_ and 26 hex digits", got)
	}
	if again := sinks.DeliveryID(sink, sinks.EventExposure, from, to, []string{"a", "b", "c"}); again != got {
		t.Errorf("the same rows = %s then %s, want the same ID", got, again)
	}
	for name, other := range map[string]string{
		"other rows over the same range": sinks.DeliveryID(sink, sinks.EventExposure, from, to, []string{"a", "b", "d"}),
		"the rows in another order":      sinks.DeliveryID(sink, sinks.EventExposure, from, to, []string{"b", "a", "c"}),
		"fewer rows":                     sinks.DeliveryID(sink, sinks.EventExposure, from, to, []string{"a", "b"}),
		"another range":                  sinks.DeliveryID(sink, sinks.EventExposure, from, events.Cursor{XID: 12, Seq: 131}, []string{"a", "b", "c"}),
		"another start":                  sinks.DeliveryID(sink, sinks.EventExposure, events.Cursor{XID: 9, Seq: 100}, to, []string{"a", "b", "c"}),
		"another event kind":             sinks.DeliveryID(sink, sinks.EventAlias, from, to, []string{"a", "b", "c"}),
		"another sink":                   sinks.DeliveryID(id.MustParse[sinks.Sink]("0195f6b0-0000-7000-8000-0000000000ab"), sinks.EventExposure, from, to, []string{"a", "b", "c"}),
		"ids that run together":          sinks.DeliveryID(sink, sinks.EventExposure, from, to, []string{"ab", "c"}),
	} {
		if other == got {
			t.Errorf("%s = %s, want another ID than %s", name, other, got)
		}
	}
}

// The ID is a promise to every receiver deduplicating on it: it is fixed here
// so that a change of its inputs shows up as a failing test, not as every
// retry after an upgrade arriving as new rows.
func TestADeliveryIDHasAFixedValue(t *testing.T) {
	sink := id.MustParse[sinks.Sink]("0195f6b0-0000-7000-8000-0000000000aa")
	got := sinks.DeliveryID(sink, sinks.EventExposure, events.Cursor{XID: 10, Seq: 100}, events.Cursor{XID: 12, Seq: 130}, []string{"a", "b", "c"})
	if want := "dlv_e68b395ff7b40c042f9e7a64ec"; got != want {
		t.Errorf("DeliveryID = %s, want %s", got, want)
	}
}

func TestAnExposureIsOneFlatObjectWithTimestampsInMilliseconds(t *testing.T) {
	b, err := sinks.MarshalRow(exposureRow())
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema_version":1,"event_id":"0195f6b0-0000-7000-8000-000000000001","project_key":"shop",` +
		`"environment_key":"production","experiment_key":"checkout_test","flag_key":"checkout","variation_key":"on",` +
		`"unit_type":"user","unit_id":"u-1","evaluation_reason":"experiment","override":true,"platform":"",` +
		`"app_version":"","sdk_name":"flagnetic-go","sdk_version":"1.4.0","ruleset_version":7,` +
		`"occurred_at":1772600767891,"received_at":1772600768891}`
	if string(b) != want {
		t.Errorf("exposure =\n%s\nwant\n%s", b, want)
	}
}

func TestAnAliasIsOneFlatObjectWithTimestampsInMilliseconds(t *testing.T) {
	b, err := sinks.MarshalRow(sinks.AliasRow{
		EventID: "e", ProjectKey: "shop", EnvironmentKey: "production", FromUnitType: "anon", FromUnitID: "a-1",
		ToUnitType: "user", ToUnitID: "u-1", Platform: "ios", SDKName: "flagnetic-dart", SDKVersion: "1.0.0",
		OccurredAt: rowTime, ReceivedAt: rowTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema_version":1,"event_id":"e","project_key":"shop","environment_key":"production",` +
		`"from_unit_type":"anon","from_unit_id":"a-1","to_unit_type":"user","to_unit_id":"u-1","platform":"ios",` +
		`"sdk_name":"flagnetic-dart","sdk_version":"1.0.0","occurred_at":1772600767891,"received_at":1772600767891}`
	if string(b) != want {
		t.Errorf("alias =\n%s\nwant\n%s", b, want)
	}
}

func TestASummaryCarriesItsSourceLast(t *testing.T) {
	b, err := sinks.MarshalRow(sinks.SummaryRow{
		ReportID: "r", ProjectKey: "shop", EnvironmentKey: "production", FlagKey: "checkout", EvaluationReason: "off",
		Count: 3, WindowStart: rowTime.Add(-time.Minute), WindowEnd: rowTime, SDKName: "flagnetic-go", SDKVersion: "1.4.0",
		RulesetVersion: 2, ReceivedAt: rowTime, Source: "remote_evaluation",
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema_version":1,"report_id":"r","project_key":"shop","environment_key":"production",` +
		`"flag_key":"checkout","variation_key":"","evaluation_reason":"off","count":3,"window_start":1772600707891,` +
		`"window_end":1772600767891,"sdk_name":"flagnetic-go","sdk_version":"1.4.0","platform":"","app_version":"",` +
		`"ruleset_version":2,"received_at":1772600767891,"source":"remote_evaluation"}`
	if string(b) != want {
		t.Errorf("summary =\n%s\nwant\n%s", b, want)
	}
}

func TestJSONLinesIsOneRowPerLineAndLeavesHTMLAlone(t *testing.T) {
	a, b := exposureRow(), exposureRow()
	a.UnitID, b.UnitID = "<a&b>", "line\nbreak"
	var out bytes.Buffer
	if err := sinks.WriteJSONLines(&out, []sinks.Row{a, b}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("lines = %q, want two, each ended by a newline", out.String())
	}
	if !strings.Contains(lines[0], `"unit_id":"<a&b>"`) {
		t.Errorf("first line = %s, want < & > written as they are", lines[0])
	}
	if got := decode(t, []byte(lines[1]))["unit_id"]; got != "line\nbreak" {
		t.Errorf("second unit_id = %q, want the line break escaped and read back", got)
	}
	one, err := sinks.MarshalRow(a)
	if err != nil || string(one) != lines[0] {
		t.Errorf("a row alone = %s (%v), want the line %s", one, err, lines[0])
	}
}

func TestTenSampleRowsOfEachKindAreMarkedAsSamples(t *testing.T) {
	for _, kind := range sinks.EventKinds {
		rows := sinks.SampleRows(kind, "shop", rowTime)
		if len(rows) != sinks.SampleCount || sinks.SampleCount != 10 {
			t.Fatalf("%s samples = %d, want 10", kind, len(rows))
		}
		for i, r := range rows {
			if want := "sample-" + string(rune('1'+i)); i < 9 && r.ID() != want {
				t.Errorf("%s sample %d ID = %s, want %s", kind, i, r.ID(), want)
			}
			m := decode(t, mustMarshal(t, r))
			if m["schema_version"] != float64(1) || m["project_key"] != "shop" || m["environment_key"] != "sample" {
				t.Errorf("%s sample = %v, want version 1, the project's key and the environment sample", kind, m)
			}
		}
		if got := rows[9].ID(); got != "sample-10" {
			t.Errorf("%s last sample ID = %s, want sample-10", kind, got)
		}
	}
	m := decode(t, mustMarshal(t, sinks.SampleRows(sinks.EventExposure, "shop", rowTime)[0]))
	if m["experiment_key"] != "sample" || m["unit_id"] != "sample" || m["evaluation_reason"] != "experiment" || m["occurred_at"] != float64(rowTime.UnixMilli()) {
		t.Errorf("exposure sample = %v", m)
	}
	m = decode(t, mustMarshal(t, sinks.SampleRows(sinks.EventEvaluationSummary, "shop", rowTime)[0]))
	if m["source"] != "sdk" || m["report_id"] != "sample-1" || m["count"] != float64(1) {
		t.Errorf("summary sample = %v", m)
	}
}

func mustMarshal(t *testing.T, r sinks.Row) []byte {
	t.Helper()
	b, err := sinks.MarshalRow(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRowIDsListsTheIDsInOrder(t *testing.T) {
	rows := []sinks.Row{sinks.ExposureRow{EventID: "x"}, sinks.AliasRow{EventID: "y"}, sinks.SummaryRow{ReportID: "z"}}
	got := sinks.RowIDs(rows)
	if strings.Join(got, ",") != "x,y,z" {
		t.Errorf("RowIDs = %v, want x,y,z", got)
	}
}
