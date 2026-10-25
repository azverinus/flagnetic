package sdk_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// startVolume returns a summary fixture on an instance of its own: the gaps
// retention records are the instance's, so this test's are then the only
// ones. It returns the instance admin's token too.
func startVolume(t *testing.T) (*summaryFixture, string) {
	t.Helper()
	c := clock.NewFake(protocolTime)
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool), testkit.WithClock(c))
	admin := testkit.Bootstrap(t, pool)
	p := newProjectAs(t, srv, admin)
	p.setProtected(t, "production", false)
	return summariesOf(t, srv, c, p), admin
}

// alias returns an alias event numbered n, occurring a minute before now.
func (f *summaryFixture) alias(n int) map[string]any {
	at := f.clock.Now().Add(-time.Minute)
	return with(aliasEvent(eventID(at, n)), map[string]any{"occurred_at": at.Format(time.RFC3339Nano)})
}

func (f *summaryFixture) readVolume(t *testing.T, from, to *time.Time) *management.GetEventVolumeResponse {
	t.Helper()
	params := &management.GetEventVolumeParams{}
	if from != nil {
		params.From = &openapi_types.Date{Time: *from}
	}
	if to != nil {
		params.To = &openapi_types.Date{Time: *to}
	}
	return do(f.p.admin.GetEventVolumeWithResponse(t.Context(), f.p.key, params))
}

// volume reads the project's event volume over the days given, nil leaving an
// end to its default, as "day type events" lines and gaps.
func (f *summaryFixture) volume(t *testing.T, from, to *time.Time) (days []string, gaps []management.EventGap) {
	t.Helper()
	res := f.readVolume(t, from, to)
	if res.JSON200 == nil {
		t.Fatalf("GET …/events/volume = %d: %s", res.StatusCode(), res.Body)
	}
	days = []string{}
	for _, d := range res.JSON200.Days {
		days = append(days, fmt.Sprintf("%s %s %d", d.Day, d.Type, d.Events))
	}
	return days, res.JSON200.Gaps
}

func utcDay(at time.Time) time.Time { return at.UTC().Truncate(24 * time.Hour) }

func TestTheEventVolumeCountsEachDayAndTypeWithTheGapsRetentionLeft(t *testing.T) {
	f, admin := startVolume(t)
	day1 := f.clock.Now()
	f.post(t, f.summary(day1.Add(-time.Minute), counted("checkout", "true", "rule", 10)),
		f.summary(day1.Add(-time.Minute), counted("banner", "false", "fallthrough", 2)), f.alias(901))
	f.rollUp(t)
	// Nothing on the second day. On the third, two summary rows nobody reads
	// before retention drops them; on the fourth, an alias read after it.
	f.clock.Advance(48 * time.Hour)
	day3 := f.clock.Now()
	f.post(t, f.summary(day3.Add(-time.Minute), counted("checkout", "false", "fallthrough", 4), counted("banner", "true", "rule", 1)))
	f.clock.Advance(24 * time.Hour)
	day4 := f.clock.Now()
	f.post(t, f.alias(902))
	// Another project's events are not this one's.
	other := summariesOf(t, f.srv, f.clock, newProjectAs(t, f.srv, admin))
	other.p.setProtected(t, "production", false)
	other.post(t, other.alias(903))

	// The first three days past retention, the fourth kept.
	f.clock.Set(utcDay(day4).Add(retention + time.Hour))
	f.retain(t)
	f.rollUp(t)

	from, to := utcDay(day1), utcDay(day4)
	days, gaps := f.volume(t, &from, &to)
	want := []string{
		from.Format(time.DateOnly) + " alias 1",
		from.Format(time.DateOnly) + " evaluation_summary 2",
		to.Format(time.DateOnly) + " alias 1",
	}
	if fmt.Sprint(days) != fmt.Sprint(want) {
		t.Errorf("days = %v, want %v", days, want)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v, want one", gaps)
	}
	g := gaps[0]
	if g.Stream != "evaluation_summaries" || g.Type != management.EventTypeEvaluationSummary || g.Rows != 2 ||
		!g.PeriodStart.Equal(day3) || !g.PeriodEnd.Equal(day3) || !g.DroppedAt.Equal(f.clock.Now()) {
		t.Errorf("gap = %+v, want the 2 summary rows received on %s, dropped now", g, day3)
	}

	// A range the gap's period does not meet leaves it out.
	days, gaps = f.volume(t, &to, &to)
	if fmt.Sprint(days) != fmt.Sprint(want[2:]) || len(gaps) != 0 {
		t.Errorf("day 4 = %v and gaps %+v, want %v and no gap", days, gaps, want[2:])
	}
	// By default the last 30 days, here none with an event.
	if days, gaps = f.volume(t, nil, nil); len(days) != 0 || len(gaps) != 0 {
		t.Errorf("the last 30 days = %v and gaps %+v, want neither", days, gaps)
	}
}

func TestTheEventVolumeRefusesARangeItCannotRead(t *testing.T) {
	f := startSummaries(t)
	day := utcDay(f.clock.Now())
	for _, c := range []struct {
		name     string
		from, to time.Time
		want     int
	}{
		{"from after to", day.AddDate(0, 0, 1), day, http.StatusBadRequest},
		{"91 days", day.AddDate(0, 0, -90), day, http.StatusBadRequest},
		{"90 days", day.AddDate(0, 0, -89), day, http.StatusOK},
	} {
		if res := f.readVolume(t, &c.from, &c.to); res.StatusCode() != c.want {
			t.Errorf("%s: GET …/events/volume = %d, want %d: %s", c.name, res.StatusCode(), c.want, res.Body)
		}
	}
}
