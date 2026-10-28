package governance_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
)

func digestTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDigestSlotCurrent(t *testing.T) {
	monday8 := governance.DigestSlot{Weekday: time.Monday, Hour: 8}
	for _, tc := range []struct {
		name  string
		slot  governance.DigestSlot
		now   string
		start string
		ok    bool
	}{
		{"first instant of the slot", monday8, "2026-10-26T08:00:00Z", "2026-10-26T08:00:00Z", true},
		{"inside the hour", monday8, "2026-10-26T08:59:59.999Z", "2026-10-26T08:00:00Z", true},
		{"the hour is over", monday8, "2026-10-26T09:00:00Z", "2026-10-26T08:00:00Z", false},
		{"before the slot of the day", monday8, "2026-10-26T07:59:59Z", "2026-10-19T08:00:00Z", false},
		{"another weekday", monday8, "2026-10-28T08:30:00Z", "2026-10-26T08:00:00Z", false},
		{"the day before the slot", monday8, "2026-10-25T08:30:00Z", "2026-10-19T08:00:00Z", false},
		{"a zone other than UTC is read in UTC", monday8, "2026-10-26T10:30:00+03:00", "2026-10-19T08:00:00Z", false},
		{"a zone other than UTC inside the hour", monday8, "2026-10-26T10:30:00+02:00", "2026-10-26T08:00:00Z", true},
		{"across a month boundary", governance.DigestSlot{Weekday: time.Thursday, Hour: 23}, "2026-11-01T01:00:00Z", "2026-10-29T23:00:00Z", false},
		{"across a year boundary", governance.DigestSlot{Weekday: time.Wednesday, Hour: 0}, "2027-01-02T12:00:00Z", "2026-12-30T00:00:00Z", false},
		{"last hour of the week", governance.DigestSlot{Weekday: time.Sunday, Hour: 23}, "2026-11-01T23:10:00Z", "2026-11-01T23:00:00Z", true},
		{"a leap day", governance.DigestSlot{Weekday: time.Thursday, Hour: 8}, "2028-03-01T00:00:00Z", "2028-02-24T08:00:00Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, ok := tc.slot.Current(digestTime(tc.now))
			if !start.Equal(digestTime(tc.start)) || ok != tc.ok {
				t.Errorf("Current(%s) = %s, %v; want %s, %v", tc.now, start.Format(time.RFC3339), ok, tc.start, tc.ok)
			}
			if start.Location() != time.UTC {
				t.Errorf("the slot starts in %s, want UTC", start.Location())
			}
		})
	}
}

func TestDigestSlotIsOneStartPerWeek(t *testing.T) {
	slot := governance.DigestSlot{Weekday: time.Friday, Hour: 17}
	starts := map[time.Time]bool{}
	for at := digestTime("2026-10-01T00:00:00Z"); at.Before(digestTime("2026-11-01T00:00:00Z")); at = at.Add(7 * time.Minute) {
		start, ok := slot.Current(at)
		if start.After(at) || at.Sub(start) >= 7*24*time.Hour {
			t.Fatalf("Current(%s) = %s, not the latest start at or before now", at, start)
		}
		if ok {
			starts[start] = true
		}
	}
	if len(starts) != 5 {
		t.Errorf("%d slots in October 2026's Fridays 17:00 seen, want 5: %v", len(starts), starts)
	}
}

func TestParseDigestWeekday(t *testing.T) {
	for in, want := range map[string]time.Weekday{"monday": time.Monday, "Sunday": time.Sunday, "SATURDAY": time.Saturday} {
		if got, ok := governance.ParseDigestWeekday(in); !ok || got != want {
			t.Errorf("ParseDigestWeekday(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "mon", "1", "someday"} {
		if _, ok := governance.ParseDigestWeekday(in); ok {
			t.Errorf("ParseDigestWeekday(%q) accepted", in)
		}
	}
}

func TestStaleFlagsEventEnvelope(t *testing.T) {
	e := governance.StaleFlagsEvent(someEvent, someProject, "shop", governance.DigestActor, digestTime("2026-10-26T08:01:00Z"), nil)
	env := e.Envelope()
	if env["type"] != "report.stale_flags" || env["environment"] != nil || env["entity"] != nil || env["audit_entry_id"] != nil {
		t.Errorf("envelope = %v", env)
	}
	data, _ := env["data"].(map[string]any)
	items, ok := data["items"].([]governance.StaleFlagItem)
	if !ok || items == nil || len(items) != 0 {
		t.Errorf("an empty digest's items = %#v, want a non-nil empty list", data["items"])
	}
	actor, _ := env["actor"].(map[string]any)
	if actor["kind"] != "system" {
		t.Errorf("actor = %v, want the system", actor)
	}
}
