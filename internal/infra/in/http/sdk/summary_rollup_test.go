package sdk_test

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// summaryFixture is a project with the boolean flags checkout and banner, a
// key of each kind in production, and a counter numbering its events.
type summaryFixture struct {
	srv    *testkit.Server
	clock  *clock.Fake
	p      project
	server string
	client string
	mu     sync.Mutex
	n      int
}

func startSummaries(t *testing.T) *summaryFixture {
	t.Helper()
	c := clock.NewFake(protocolTime)
	srv := testkit.StartServer(t, testkit.WithClock(c))
	return summariesOf(t, srv, c, newProject(t, srv))
}

// summariesOf returns the summary fixture of p on srv, timed by c.
func summariesOf(t *testing.T, srv *testkit.Server, c *clock.Fake, p project) *summaryFixture {
	t.Helper()
	p.flag(t, "checkout")
	p.flag(t, "banner")
	f := &summaryFixture{srv: srv, clock: c, p: p}
	_, f.server = p.sdkKey(t, "production", management.SdkKeyKindServer)
	_, f.client = p.sdkKey(t, "production", management.SdkKeyKindClient)
	return f
}

// counted is one row of an evaluation summary; a variation of "" is none.
func counted(flag, variation, reason string, n int64) map[string]any {
	row := map[string]any{"flag": flag, "variation": nil, "reason": reason, "count": n}
	if variation != "" {
		row["variation"] = variation
	}
	return row
}

// summary is an evaluation summary over the minute before end, with a new
// event ID.
func (f *summaryFixture) summary(end time.Time, rows ...map[string]any) map[string]any {
	f.mu.Lock()
	f.n++
	n := f.n
	f.mu.Unlock()
	return map[string]any{
		"type": "evaluation_summary", "event_id": eventID(end, n), "interval_start": end.Add(-time.Minute).Format(time.RFC3339Nano),
		"interval_end": end.Format(time.RFC3339Nano), "ruleset_version": 1, "counts": rows,
	}
}

// post sends events with the server key, sent now by the server's clock, and
// wants each accepted.
func (f *summaryFixture) post(t *testing.T, evs ...any) {
	t.Helper()
	f.postAs(t, f.srv, f.server, evs...)
}

func (f *summaryFixture) postAs(t *testing.T, srv *testkit.Server, key string, evs ...any) {
	t.Helper()
	if res := batchResult(t, srv, key, f.envelope(t, evs...)); res.Accepted != len(evs) {
		t.Fatalf("batch = %+v, want all %d accepted", res, len(evs))
	}
}

func (f *summaryFixture) envelope(t *testing.T, evs ...any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"schema_version": 1, "sent_at": f.clock.Now().Format(time.RFC3339Nano),
		"sdk": map[string]any{"name": "flagnetic-go", "version": "1.4.0", "platform": "linux"}, "events": evs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rollUp runs the rollups of the streams reading what the fixture posts.
func (f *summaryFixture) rollUp(t *testing.T) {
	t.Helper()
	f.srv.TickRollup(t, worker.SummaryRollupName)
	f.srv.TickRollup(t, worker.AliasRollupName)
}

// evaluations reads flag's evaluations in production over the days given, nil
// leaving an end to its default.
func (f *summaryFixture) evaluations(t *testing.T, flag string, from, to *time.Time) []management.FlagEvaluationDay {
	t.Helper()
	res := f.readEvaluations(t, flag, "production", from, to)
	if res.JSON200 == nil {
		t.Fatalf("GET …/evaluations = %d: %s", res.StatusCode(), res.Body)
	}
	return res.JSON200.Items
}

func (f *summaryFixture) readEvaluations(t *testing.T, flag, environment string, from, to *time.Time) *management.GetFlagEvaluationsResponse {
	t.Helper()
	params := &management.GetFlagEvaluationsParams{}
	if from != nil {
		params.From = &openapi_types.Date{Time: *from}
	}
	if to != nil {
		params.To = &openapi_types.Date{Time: *to}
	}
	return do(f.p.admin.GetFlagEvaluationsWithResponse(t.Context(), f.p.key, flag, environment, params))
}

// row is an evaluations row as the test writes it: day, source, variation,
// reason, count.
func row(d management.FlagEvaluationDay) string {
	v := "-"
	if d.Variation != nil {
		v = *d.Variation
	}
	return fmt.Sprintf("%s %s %s %s %d", d.Day.Format(time.DateOnly), d.Source, v, d.Reason, d.Evaluations)
}

func rows(days []management.FlagEvaluationDay) []string {
	out := make([]string, len(days))
	for i, d := range days {
		out[i] = row(d)
	}
	return out
}

// lastEvaluated reads flag through GET and through the list, wants both to
// agree, and returns when it was last evaluated by environment, "" for
// never.
func (f *summaryFixture) lastEvaluated(t *testing.T, flag string) map[string]string {
	t.Helper()
	return lastEvaluatedOf(t, f.p, flag)
}

func lastEvaluatedOf(t *testing.T, p project, flag string) map[string]string {
	t.Helper()
	text := func(at *time.Time) string {
		if at == nil {
			return ""
		}
		return at.UTC().Format(time.RFC3339Nano)
	}
	got := do(p.admin.GetFlagWithResponse(t.Context(), p.key, flag))
	if got.JSON200 == nil {
		t.Fatalf("GET …/flags/%s = %d: %s", flag, got.StatusCode(), got.Body)
	}
	out := map[string]string{}
	for _, l := range got.JSON200.LastEvaluated {
		out[l.Environment] = text(l.LastEvaluatedAt)
	}
	if len(out) != len(got.JSON200.Environments) {
		t.Errorf("last_evaluated = %+v, want one per environment", got.JSON200.LastEvaluated)
	}
	list := do(p.admin.ListFlagsWithResponse(t.Context(), p.key, &management.ListFlagsParams{}))
	if list.JSON200 == nil {
		t.Fatalf("GET …/flags = %d: %s", list.StatusCode(), list.Body)
	}
	for _, item := range list.JSON200.Items {
		if item.Key != flag {
			continue
		}
		for _, s := range item.States {
			if text(s.LastEvaluatedAt) != out[s.Environment] {
				t.Errorf("listed %s in %s last evaluated %q, read %q", flag, s.Environment, text(s.LastEvaluatedAt), out[s.Environment])
			}
		}
	}
	return out
}

func TestSummariesAreAddedUpPerDayVariationReasonAndSourceAfterARound(t *testing.T) {
	f := startSummaries(t)
	end := f.clock.Now().Add(-time.Minute)
	today := end.Format(time.DateOnly)
	f.post(t, f.summary(end,
		counted("checkout", "true", "rule", 10), counted("checkout", "false", "fallthrough", 5),
		counted("checkout", "", "error", 2), counted("banner", "true", "targeting_match", 1)))

	if got := f.evaluations(t, "checkout", nil, nil); len(got) != 0 {
		t.Fatalf("before the round = %v, want nothing rolled up", rows(got))
	}
	if got := f.lastEvaluated(t, "checkout"); got["production"] != "" {
		t.Fatalf("last evaluated before the round = %v, want never", got)
	}
	f.rollUp(t)
	want := []string{today + " sdk - error 2", today + " sdk false fallthrough 5", today + " sdk true rule 10"}
	if got := rows(f.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after the round = %v, want %v", got, want)
	}

	// A second summary adds to the same day; the last evaluation is the
	// latest interval end, whichever summary arrived last.
	later := end.Add(30 * time.Second)
	f.post(t, f.summary(later, counted("checkout", "true", "rule", 3)))
	f.post(t, f.summary(end.Add(-10*time.Second), counted("checkout", "true", "rule", 1)))
	f.rollUp(t)
	want[2] = today + " sdk true rule 14"
	if got := rows(f.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after the second round = %v, want %v", got, want)
	}
	if got := f.lastEvaluated(t, "checkout"); got["production"] != later.Format(time.RFC3339Nano) || got["staging"] != "" {
		t.Errorf("checkout last evaluated = %v, want %v in production only", got, later)
	}
	if got := f.lastEvaluated(t, "banner"); got["production"] != end.Format(time.RFC3339Nano) {
		t.Errorf("banner last evaluated = %v, want %v", got, end)
	}

	// The next day's summaries count on the next day.
	f.clock.Advance(24 * time.Hour)
	f.post(t, f.summary(f.clock.Now(), counted("checkout", "true", "rule", 4)))
	f.rollUp(t)
	tomorrow := f.clock.Now().Format(time.DateOnly)
	want = append(want, tomorrow+" sdk true rule 4")
	if got := rows(f.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("a day later = %v, want %v", got, want)
	}
}

func TestRemoteEvaluationsAreASeriesOfTheirOwnAndNeverMakeAFlagEvaluated(t *testing.T) {
	f := startSummaries(t)
	today := f.clock.Now().Format(time.DateOnly)
	for range 2 {
		evaluationWith(t, f.srv, f.client, `{"context":{"units":{"user":"u-1"}}}`, "User-Agent", "flagnetic-js/1.2.0 (web)")
	}
	if err := f.srv.Tick(t, worker.RemoteEvaluationCountsName); err != nil {
		t.Fatal(err)
	}
	f.post(t, f.summary(f.clock.Now(), counted("checkout", "false", "targeting_match", 7)))
	f.rollUp(t)

	want := []string{today + " remote_evaluation false off 2", today + " sdk false targeting_match 7"}
	if got := rows(f.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("checkout = %v, want %v", got, want)
	}
	if got := rows(f.evaluations(t, "banner", nil, nil)); fmt.Sprint(got) != fmt.Sprint([]string{today + " remote_evaluation false off 2"}) {
		t.Errorf("banner = %v, want the remote evaluations only", got)
	}
	if got := f.lastEvaluated(t, "checkout"); got["production"] != f.clock.Now().Format(time.RFC3339Nano) {
		t.Errorf("checkout last evaluated = %v, want the summary's interval end", got)
	}
	if got := f.lastEvaluated(t, "banner"); got["production"] != "" {
		t.Errorf("banner last evaluated = %v, want never: only the server answered it", got)
	}
}

func TestAnExposureMovesWhenItsFlagWasLastEvaluated(t *testing.T) {
	f := startRollup(t)
	at := f.clock.Now().Add(-time.Hour)
	f.post(t, f.exposure("u", "true", at), f.override("v", "false", at.Add(-time.Minute)))
	f.rollUp(t)
	if got := lastEvaluatedOf(t, f.p, "checkout"); got["production"] != at.Format(time.RFC3339Nano) || got["staging"] != "" {
		t.Errorf("checkout last evaluated = %v, want the latest exposure in production only", got)
	}
}

func TestAnOpenTransactionHoldsTheSummaryRollupBackUntilItCommits(t *testing.T) {
	f := startSummaries(t)
	now := f.clock.Now()
	written, release := f.srv.HoldNextIngest(t)
	body := f.envelope(t, f.summary(now, counted("checkout", "true", "rule", 10)))
	held := make(chan answer, 1)
	go func() { held <- postEvents(t, f.srv, f.server, body) }()
	<-written
	// A summary committed after the held one, while it is still open.
	f.post(t, f.summary(now, counted("checkout", "true", "rule", 5)))

	f.rollUp(t)
	if got := f.evaluations(t, "checkout", nil, nil); len(got) != 0 {
		t.Fatalf("while a transaction is open = %v, want nothing past it rolled up", rows(got))
	}
	f.clock.Advance(30 * time.Second)
	f.rollUp(t)
	if lag := testkit.Metric(t, f.srv.Scrape(t), `flagnetic_rollup_lag_seconds{stream="evaluation_summaries"}`); lag < 30 {
		t.Errorf("rollup lag = %v while held back, want at least 30", lag)
	}

	release()
	if a := <-held; a.status != http.StatusAccepted {
		t.Fatalf("the held batch = %d: %s", a.status, a.body)
	}
	f.rollUp(t)
	if got := rows(f.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint([]string{now.Format(time.DateOnly) + " sdk true rule 15"}) {
		t.Errorf("after the commit = %v, want both summaries", got)
	}
	if lag := testkit.Metric(t, f.srv.Scrape(t), `flagnetic_rollup_lag_seconds{stream="evaluation_summaries"}`); lag != 0 {
		t.Errorf("rollup lag = %v once caught up, want 0", lag)
	}
}

func TestTwoReplicasRollingSummariesUpAtOnceCountEachOnce(t *testing.T) {
	f := startSummaries(t)
	other := testkit.StartServer(t, testkit.WithClock(f.clock))
	const rounds, summaries = 3, 20
	for round := range rounds {
		var evs []any
		for range summaries {
			evs = append(evs, f.summary(f.clock.Now(), counted("checkout", "true", "rule", 1)))
		}
		f.post(t, evs...)
		var wg sync.WaitGroup
		for _, srv := range []*testkit.Server{f.srv, other, f.srv, other} {
			wg.Go(func() { srv.TickRollup(t, worker.SummaryRollupName) })
		}
		wg.Wait()
		want := fmt.Sprintf("%s sdk true rule %d", f.clock.Now().Format(time.DateOnly), (round+1)*summaries)
		if got := rows(f.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint([]string{want}) {
			t.Fatalf("round %d = %v, want %s", round, got, want)
		}
	}
}

func TestADailyCountStopsAtTheLargestInt64RatherThanFailTheRollup(t *testing.T) {
	f, other := startSummaries(t), startSummaries(t)
	today := f.clock.Now().Format(time.DateOnly)
	// Two rows of one summary overflow within the batch; another project's
	// summary is rolled up with them.
	f.post(t, f.summary(f.clock.Now(), counted("checkout", "true", "rule", math.MaxInt64), counted("checkout", "true", "targeting_match", 1)),
		f.summary(f.clock.Now(), counted("checkout", "true", "rule", 1)))
	other.post(t, other.summary(other.clock.Now(), counted("checkout", "true", "rule", 2)))
	f.rollUp(t)
	saturated := fmt.Sprintf("%s sdk true rule %d", today, int64(math.MaxInt64))
	want := []string{saturated, today + " sdk true targeting_match 1"}
	if got := rows(f.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after an overflowing batch = %v, want %v", got, want)
	}
	if got := rows(other.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint([]string{today + " sdk true rule 2"}) {
		t.Errorf("the other project = %v, want its own count", got)
	}

	// Adding to the stopped count keeps it there and the rollup moving.
	f.post(t, f.summary(f.clock.Now(), counted("checkout", "true", "rule", 5)))
	other.post(t, other.summary(other.clock.Now(), counted("checkout", "true", "rule", 3)))
	f.rollUp(t)
	if got := rows(f.evaluations(t, "checkout", nil, nil)); got[0] != saturated {
		t.Errorf("after adding to it = %v, want %s", got, saturated)
	}
	if got := rows(other.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint([]string{today + " sdk true rule 5"}) {
		t.Errorf("the other project = %v, want 5", got)
	}
}

func TestEvaluationsAreReadOverADefaultMonthOrTheDaysGiven(t *testing.T) {
	f := startSummaries(t)
	start := f.clock.Now()
	for _, days := range []int{0, 10, 40} {
		at := start.AddDate(0, 0, days)
		f.clock.Set(at)
		f.post(t, f.summary(at, counted("checkout", "true", "rule", int64(days+1))))
	}
	f.rollUp(t)
	day := func(days int) time.Time { return start.AddDate(0, 0, days) }
	ds := func(days int) string { return day(days).Format(time.DateOnly) }

	// Today is day 40: the last 30 days hold day 40 only.
	if got := rows(f.evaluations(t, "checkout", nil, nil)); fmt.Sprint(got) != fmt.Sprint([]string{ds(40) + " sdk true rule 41"}) {
		t.Errorf("by default = %v, want day 40 only", got)
	}
	to := day(10)
	if got := rows(f.evaluations(t, "checkout", nil, &to)); fmt.Sprint(got) != fmt.Sprint([]string{ds(0) + " sdk true rule 1", ds(10) + " sdk true rule 11"}) {
		t.Errorf("to day 10 = %v, want days 0 and 10", got)
	}
	from := day(5)
	if got := rows(f.evaluations(t, "checkout", &from, nil)); len(got) != 2 {
		t.Errorf("from day 5 = %v, want days 10 and 40", got)
	}
	if got := f.evaluations(t, "checkout", &to, &to); len(got) != 1 {
		t.Errorf("day 10 alone = %v, want its row", rows(got))
	}

	tooEarly, reversed := day(-50), day(11)
	for name, res := range map[string]*management.GetFlagEvaluationsResponse{
		"more than 90 days": f.readEvaluations(t, "checkout", "production", &tooEarly, nil),
		"from after to":     f.readEvaluations(t, "checkout", "production", &reversed, &to),
	} {
		refused(t, name, res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
	ninety := day(-49)
	if res := f.readEvaluations(t, "checkout", "production", &ninety, nil); res.JSON200 == nil {
		t.Errorf("90 days = %d: %s", res.StatusCode(), res.Body)
	}
	for name, res := range map[string]*management.GetFlagEvaluationsResponse{
		"flag":        f.readEvaluations(t, "nothing", "production", nil, nil),
		"environment": f.readEvaluations(t, "checkout", "nowhere", nil, nil),
	} {
		refused(t, "unknown "+name, res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
	}
}

func TestEverySummaryIsOneEventReceivedWhatAnyOfItsFlagsBecame(t *testing.T) {
	f := startSummaries(t)
	f.p.flag(t, "retired")
	f.post(t, f.summary(f.clock.Now(), counted("checkout", "true", "rule", 1), counted("banner", "true", "rule", 1),
		counted("retired", "true", "rule", 9)))
	f.post(t, f.summary(f.clock.Now(), counted("retired", "true", "rule", 1)))
	evaluationWith(t, f.srv, f.server, `{"context":{"units":{"user":"u-1"}}}`, "User-Agent", "flagnetic-go/1.4.0 (linux)")
	if err := f.srv.Tick(t, worker.RemoteEvaluationCountsName); err != nil {
		t.Fatal(err)
	}
	// A flag deleted after its summaries were stored is left out of the
	// counts, while its summaries still count as received.
	got := do(f.p.admin.GetFlagWithResponse(t.Context(), f.p.key, "retired"))
	etag := got.HTTPResponse.Header.Get("ETag")
	archived := do(f.p.admin.ArchiveFlagWithResponse(t.Context(), f.p.key, "retired", &management.ArchiveFlagParams{IfMatch: &etag}))
	if archived.JSON200 == nil {
		t.Fatalf("archive = %d: %s", archived.StatusCode(), archived.Body)
	}
	etag = archived.HTTPResponse.Header.Get("ETag")
	if res := do(f.p.admin.DeleteFlagWithResponse(t.Context(), f.p.key, "retired", &management.DeleteFlagParams{IfMatch: &etag})); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", res.StatusCode(), res.Body)
	}
	f.rollUp(t)

	project := f.p.projectID(t)
	counts, err := pgevents.NewStore(testkit.Pool(t)).EventCounts(t.Context(), project, f.clock.Now(), f.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	received := events.EventDay{Project: project, Day: f.clock.Now().UTC().Truncate(24 * time.Hour), Type: events.EventEvaluationSummary}
	if counts[received] != 2 || len(counts) != 1 {
		t.Errorf("event counts = %+v, want the 2 summaries and not the remote evaluations", counts)
	}
	if got := rows(f.evaluations(t, "checkout", nil, nil)); len(got) != 2 {
		t.Errorf("checkout = %v, want its summary row and its remote evaluation", got)
	}
}
