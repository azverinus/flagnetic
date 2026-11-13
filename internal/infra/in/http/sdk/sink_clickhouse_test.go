package sdk_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// clickhouse is a ClickHouse sink of kinds to database on ch, creating its
// tables unless createTables says otherwise.
func clickhouse(ch *testkit.ClickHouse, database string, createTables bool, kinds ...string) management.SinkCreate {
	body := management.SinkCreate{
		Name: "Warehouse", Kind: "clickhouse",
		Clickhouse: &management.SinkClickHouseInput{
			Url: ptr(ch.URL), Database: ptr(database), Username: ptr(ch.Username), Password: ptr(ch.Password),
			CreateTables: ptr(createTables),
		},
	}
	if len(kinds) > 0 {
		body.EventKinds = &kinds
	}
	return body
}

// chInt reads a JSONEachRow integer, which ClickHouse quotes when it is 64 bits
// wide and leaves a bare number otherwise.
func chInt(t testing.TB, v any) int {
	t.Helper()
	n, err := strconv.Atoi(fmt.Sprint(v))
	if err != nil {
		t.Fatalf("read %#v as an integer: %v", v, err)
	}
	return n
}

// chCount is count() of table in database, FINAL when final.
func chCount(t testing.TB, ch *testkit.ClickHouse, database, table string, final bool) int {
	t.Helper()
	q := fmt.Sprintf("SELECT count() AS n FROM %s.%s", database, table)
	if final {
		q += " FINAL"
	}
	return chInt(t, ch.Query(t, q)[0]["n"])
}

// chStrings is column of table in database, each value once, sorted.
func chStrings(t testing.TB, ch *testkit.ClickHouse, database, table, column string) []string {
	t.Helper()
	var out []string
	for _, r := range ch.Query(t, fmt.Sprintf("SELECT DISTINCT %s AS v FROM %s.%s ORDER BY v", column, database, table)) {
		out = append(out, r["v"].(string))
	}
	return out
}

func chTables(t testing.TB, ch *testkit.ClickHouse, database string) []string {
	t.Helper()
	var out []string
	for _, r := range ch.Query(t, fmt.Sprintf("SELECT name FROM system.tables WHERE database = '%s' ORDER BY name", database)) {
		out = append(out, r["name"].(string))
	}
	return out
}

func containsText(list []string, text string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, text) })
}

func (f delivering) testSink(t *testing.T, sink management.Sink) management.SinkTestResult {
	t.Helper()
	res := do(f.p.admin.TestSinkWithResponse(t.Context(), f.p.key, sink.Id, management.SinkTest{}))
	if res.JSON200 == nil {
		t.Fatalf("POST test = %d: %s", res.StatusCode(), res.Body)
	}
	return *res.JSON200
}

func TestAClickHouseSinkCreatesTheDocumentedTablesAndDeliversRowsThatSurviveARetryAndAReplay(t *testing.T) {
	ch := testkit.StartClickHouse(t)
	db := ch.NewDatabase(t)
	f := startDelivering(t)
	created, _ := f.create(t, clickhouse(ch, db, true))
	if s := f.sink(t, created); s.BatchSize != 5000 || s.FlushInterval != 10 {
		t.Errorf("batching %d/%d, want the ClickHouse defaults 5000/10", s.BatchSize, s.FlushInterval)
	}

	// Testing the saved sink creates the tables.
	if r := f.testSink(t, created); !r.Ok || r.Class != nil || len(r.Warnings) != 0 {
		t.Fatalf("test = %+v, want ok with no warnings", r)
	}
	sortingKeys := map[string]string{
		"flagnetic_exposures":            "project_key, environment_key, experiment_key, unit_id, event_id",
		"flagnetic_aliases":              "project_key, environment_key, from_unit_type, from_unit_id, event_id",
		"flagnetic_evaluation_summaries": "project_key, environment_key, flag_key, report_id, variation_key, evaluation_reason",
	}
	for _, table := range sinks.ClickHouseTables() {
		name := table.Name("flagnetic")
		want := map[string]string{}
		for _, c := range table.Columns {
			want[c.Name] = c.Type
		}
		got := map[string]string{}
		for _, r := range ch.Query(t, fmt.Sprintf("SELECT name, type FROM system.columns WHERE database = '%s' AND table = '%s'", db, name)) {
			got[r["name"].(string)] = r["type"].(string)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s columns %v, want %v", name, got, want)
		}
		meta := ch.Query(t, fmt.Sprintf("SELECT engine, partition_key, sorting_key, create_table_query FROM system.tables WHERE database = '%s' AND name = '%s'", db, name))
		if len(meta) != 1 {
			t.Fatalf("%s: %d tables", name, len(meta))
		}
		if meta[0]["engine"] != "ReplacingMergeTree" || meta[0]["partition_key"] != "toYYYYMM(received_at)" || meta[0]["sorting_key"] != sortingKeys[name] {
			t.Errorf("%s engine, partition, sorting key = %v, %v, %v", name, meta[0]["engine"], meta[0]["partition_key"], meta[0]["sorting_key"])
		}
		if strings.Contains(meta[0]["create_table_query"].(string), "TTL") {
			t.Errorf("%s has a TTL: %s", name, meta[0]["create_table_query"])
		}
	}

	start := f.clock.Now()
	enabled := f.enable(t, created, management.SinkEnable{})
	e1, e2 := f.exposure("u-1", "true", f.clock.Now()), f.exposure("u-2", "false", f.clock.Now())
	a1 := f.alias(1)
	s1 := summaryEvent(eventID(f.clock.Now(), 6000), map[string]any{"flag": "checkout", "variation": "true", "reason": "rule", "count": 7})
	f.post(t, e1, e2, a1, s1)

	// The first delivery is acknowledged but its cursor commit is lost, as a
	// crash would leave it: once the lease lapses that batch is inserted again,
	// under the same query ID, and the sorting key folds the copies into one.
	f.srv.LoseNextSinkCommit()
	tick(t, f.srv)
	f.clock.Advance(5*time.Minute + time.Second)
	tick(t, f.srv)

	if got := chCount(t, ch, db, "flagnetic_exposures", true); got != 2 {
		t.Fatalf("exposures FINAL = %d, want 2", got)
	}
	ch.Exec(t, "SYSTEM FLUSH LOGS")
	written := func() int {
		rows := ch.Query(t, fmt.Sprintf(`SELECT sum(written_rows) AS n FROM system.query_log
			WHERE type = 'QueryFinish' AND query_kind = 'Insert' AND has(tables, '%s.flagnetic_exposures')`, db))
		return chInt(t, rows[0]["n"])
	}
	if got := written(); got != 4 {
		t.Errorf("rows inserted into exposures = %d, want the batch of two inserted twice", got)
	}
	inserts := ch.Query(t, fmt.Sprintf(`SELECT query_id FROM system.query_log
		WHERE type = 'QueryFinish' AND query_kind = 'Insert' AND has(tables, '%s.flagnetic_exposures')`, db))
	if len(inserts) != 2 || inserts[0]["query_id"] != inserts[1]["query_id"] || !strings.HasPrefix(inserts[0]["query_id"].(string), "dlv_") {
		t.Errorf("inserts %v, want the same delivery ID twice", inserts)
	}

	rows := ch.Query(t, fmt.Sprintf("SELECT * FROM %s.flagnetic_exposures FINAL WHERE event_id = '%s'", db, eventIDOf(e1)))
	if len(rows) != 1 {
		t.Fatalf("rows for %s: %v", eventIDOf(e1), rows)
	}
	for k, v := range map[string]string{
		"schema_version": "1", "project_key": f.p.key, "environment_key": "production", "experiment_key": "checkout_test",
		"flag_key": "checkout", "variation_key": "true", "unit_type": "user", "unit_id": "u-1", "override": "false",
		"received_at": start.UTC().Format("2006-01-02 15:04:05.000"),
	} {
		if fmt.Sprint(rows[0][k]) != v {
			t.Errorf("exposure %s = %#v, want %q", k, rows[0][k], v)
		}
	}
	if got := chStrings(t, ch, db, "flagnetic_aliases", "event_id"); !slices.Equal(got, []string{eventIDOf(a1)}) {
		t.Errorf("aliases %v, want [%s]", got, eventIDOf(a1))
	}
	summaries := ch.Query(t, fmt.Sprintf("SELECT report_id, flag_key, variation_key, evaluation_reason, count, source FROM %s.flagnetic_evaluation_summaries FINAL", db))
	if len(summaries) != 1 || summaries[0]["report_id"] != eventIDOf(s1) || chInt(t, summaries[0]["count"]) != 7 || summaries[0]["source"] != "sdk" {
		t.Errorf("summaries %v, want one row of report %s counting 7 from the sdk", summaries, eventIDOf(s1))
	}
	if s := f.sink(t, enabled); s.Health != management.SinkHealthHealthy {
		t.Errorf("health = %s, want healthy", s.Health)
	}

	// A replay from the start sends every row again; FINAL still counts what
	// was ingested.
	f.replay(t, created, management.SinkReplay{From: start})
	f.clock.Advance(11 * time.Second)
	tick(t, f.srv)
	ch.Exec(t, "SYSTEM FLUSH LOGS")
	if got := written(); got != 6 {
		t.Errorf("rows inserted into exposures = %d after the replay, want the two sent again as well", got)
	}
	if got := chCount(t, ch, db, "flagnetic_exposures", true); got != 2 {
		t.Errorf("exposures FINAL after a replay = %d, want 2", got)
	}
	if got := chCount(t, ch, db, "flagnetic_aliases", true); got != 1 {
		t.Errorf("aliases FINAL after a replay = %d, want 1", got)
	}

	// The password is sealed, read back masked and never logged.
	if s := f.sink(t, created); s.Clickhouse == nil || s.Clickhouse.Password != "***" {
		t.Errorf("password read back as %+v, want ***", s.Clickhouse)
	}
	for _, rec := range f.srv.Logs.Records() {
		if strings.Contains(fmt.Sprintf("%v", rec), ch.Password) {
			t.Fatalf("the server logged the ClickHouse password: %+v", rec)
		}
	}
}

func TestAClickHouseTableWithoutAnOptionalColumnIsAcceptedWithAWarningAndInsertsSkipIt(t *testing.T) {
	ch := testkit.StartClickHouse(t)
	db := ch.NewDatabase(t)
	f := startDelivering(t)
	ch.Exec(t, fmt.Sprintf(`CREATE TABLE %s.flagnetic_exposures (
		schema_version UInt16, event_id String, project_key String, environment_key String,
		experiment_key String, unit_id String, variation_key String, received_at DateTime64(3, 'UTC')
	) ENGINE = MergeTree ORDER BY event_id`, db))

	created, _ := f.create(t, clickhouse(ch, db, false, "exposure"))
	probe := f.testSink(t, created)
	if !probe.Ok || probe.Class != nil {
		t.Fatalf("test = %+v, want ok", probe)
	}
	if !containsText(probe.Warnings, "app_version") || containsText(probe.Warnings, "event_id") {
		t.Errorf("warnings %v, want app_version among them and not event_id", probe.Warnings)
	}
	if s := f.sink(t, created); !containsText(s.Warnings, "app_version") {
		t.Errorf("sink warnings %v, want app_version among them", s.Warnings)
	}
	if s := f.enable(t, created, management.SinkEnable{}); !containsText(s.Warnings, "app_version") {
		t.Errorf("warnings after enable %v, want app_version among them", s.Warnings)
	}

	e1 := f.exposure("u-1", "true", f.clock.Now())
	f.post(t, e1)
	tick(t, f.srv)
	rows := ch.Query(t, fmt.Sprintf("SELECT event_id, unit_id, variation_key FROM %s.flagnetic_exposures", db))
	if len(rows) != 1 || rows[0]["event_id"] != eventIDOf(e1) || rows[0]["unit_id"] != "u-1" || rows[0]["variation_key"] != "true" {
		t.Errorf("rows %v, want %s", rows, eventIDOf(e1))
	}
	if s := f.sink(t, created); s.Health != management.SinkHealthHealthy {
		t.Errorf("health = %s, want healthy", s.Health)
	}

	// The data engineer adds the column; the next insert fills it, with no
	// test or enable in between.
	ch.Exec(t, fmt.Sprintf("ALTER TABLE %s.flagnetic_exposures ADD COLUMN sdk_name String", db))
	e2 := f.exposure("u-2", "true", f.clock.Now())
	f.post(t, e2)
	f.clock.Advance(11 * time.Second)
	tick(t, f.srv)
	got := ch.Query(t, fmt.Sprintf("SELECT sdk_name FROM %s.flagnetic_exposures WHERE event_id = '%s'", db, eventIDOf(e2)))
	if len(got) != 1 || got[0]["sdk_name"] != "flagnetic-dart" {
		t.Errorf("sdk_name of the exposure after the column was added: %v, want flagnetic-dart", got)
	}
}

func TestAClickHouseTableMissingARequiredColumnOrMissingAltogetherFailsTheTestAndTheEnable(t *testing.T) {
	ch := testkit.StartClickHouse(t)
	db := ch.NewDatabase(t)
	f := startDelivering(t)
	ch.Exec(t, fmt.Sprintf(`CREATE TABLE %s.flagnetic_exposures (
		schema_version UInt16, project_key String, environment_key String, received_at DateTime64(3, 'UTC')
	) ENGINE = MergeTree ORDER BY project_key`, db))
	created, _ := f.create(t, clickhouse(ch, db, false, "exposure", "alias"))

	probe := f.testSink(t, created)
	if probe.Ok || probe.Class == nil || *probe.Class != "rejected" || probe.Code == nil || *probe.Code != "16 NO_SUCH_COLUMN_IN_TABLE" {
		t.Fatalf("test = %+v, want not ok, rejected, 16 NO_SUCH_COLUMN_IN_TABLE", probe)
	}
	for _, text := range []string{"event_id", "flagnetic_aliases"} {
		if !containsText(probe.Warnings, text) {
			t.Errorf("warnings %v do not name %s", probe.Warnings, text)
		}
	}

	res := do(f.p.admin.EnableSinkWithResponse(t.Context(), f.p.key, created.Id, management.SinkEnable{}))
	var problem map[string]any
	if err := json.Unmarshal(res.Body, &problem); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusUnprocessableEntity || problem["code"] != "sink_not_ready" || problem["failure"] != "rejected" ||
		problem["failure_code"] != "16 NO_SUCH_COLUMN_IN_TABLE" {
		t.Fatalf("POST enable = %d: %s, want 422 sink_not_ready, rejected, 16 NO_SUCH_COLUMN_IN_TABLE", res.StatusCode(), res.Body)
	}
	if s := f.sink(t, created); s.Enabled || len(s.Streams) != 0 {
		t.Errorf("sink after a refused enable: enabled %v, %d streams", s.Enabled, len(s.Streams))
	}
	if got := chTables(t, ch, db); !slices.Equal(got, []string{"flagnetic_exposures"}) {
		t.Errorf("tables %v, want only the data engineer's own: create_tables is false", got)
	}

	// Only a missing table, with create_tables off, is its own code.
	other := ch.NewDatabase(t)
	missing, _ := f.create(t, clickhouse(ch, other, false, "alias"))
	if r := f.testSink(t, missing); r.Ok || r.Code == nil || *r.Code != "60 UNKNOWN_TABLE" || !containsText(r.Warnings, "flagnetic_aliases") {
		t.Errorf("test of a sink whose table is missing = %+v, want 60 UNKNOWN_TABLE naming the table", r)
	}
}

func TestTestingAnUnsavedClickHouseConfigCreatesNothingAndAWrongPasswordFailsWithItsCodeAndWithoutLeakingIt(t *testing.T) {
	ch := testkit.StartClickHouse(t)
	db := ch.NewDatabase(t)
	f := startDelivering(t)

	unsaved := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, clickhouse(ch, db, true)))
	if unsaved.JSON200 == nil || !unsaved.JSON200.Ok {
		t.Fatalf("unsaved test = %d: %s", unsaved.StatusCode(), unsaved.Body)
	}
	for _, table := range sinks.ClickHouseTables() {
		if !containsText(unsaved.JSON200.Warnings, table.Name("flagnetic")) {
			t.Errorf("warnings %v do not say %s would be created", unsaved.JSON200.Warnings, table.Name("flagnetic"))
		}
	}
	if got := chTables(t, ch, db); len(got) != 0 {
		t.Errorf("an unsaved test created %v", got)
	}
	if list := do(f.p.admin.ListSinksWithResponse(t.Context(), f.p.key)); list.JSON200 == nil || len(list.JSON200.Items) != 0 {
		t.Errorf("sinks after an unsaved test %s, want none", list.Body)
	}

	const wrong = "not-the-clickhouse-password-7731"
	testkit.RecordSecret(wrong)
	bad := clickhouse(ch, db, true)
	bad.Clickhouse.Password = ptr(wrong)
	res := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, bad))
	if res.JSON200 == nil || res.JSON200.Ok || res.JSON200.Class == nil || *res.JSON200.Class != "rejected" || res.JSON200.Status == nil ||
		*res.JSON200.Status != http.StatusForbidden || res.JSON200.Code == nil || *res.JSON200.Code != "516 AUTHENTICATION_FAILED" {
		t.Fatalf("a wrong password = %d: %s, want not ok, rejected, 403, 516 AUTHENTICATION_FAILED", res.StatusCode(), res.Body)
	}
	if strings.Contains(string(res.Body), wrong) {
		t.Error("the wrong password is echoed back")
	}
	for _, rec := range f.srv.Logs.Records() {
		if strings.Contains(fmt.Sprintf("%v", rec), wrong) {
			t.Fatalf("the server logged the wrong password: %+v", rec)
		}
	}

	for name, mutate := range map[string]func(*management.SinkClickHouseInput){
		"a URL with credentials": func(c *management.SinkClickHouseInput) {
			c.Url = ptr(strings.Replace(ch.URL, "http://", "http://user:pw@", 1))
		},
		"a reserved setting": func(c *management.SinkClickHouseInput) { c.Settings = &map[string]string{"query_id": "mine"} },
	} {
		body := clickhouse(ch, db, true)
		mutate(body.Clickhouse)
		if got := do(f.p.admin.TestSinkConfigWithResponse(t.Context(), f.p.key, body)); got.StatusCode() != http.StatusUnprocessableEntity {
			t.Errorf("test with %s = %d: %s, want 422", name, got.StatusCode(), got.Body)
		}
	}
}

func TestAClickHouseBatchTheTableCannotTakeIsRejectedAndCounted(t *testing.T) {
	ch := testkit.StartClickHouse(t)
	db := ch.NewDatabase(t)
	f := startDelivering(t)
	// unit_id is a number here, so a row whose unit is "u-1" cannot be parsed:
	// ClickHouse refuses the insert, which a retry cannot cure.
	ch.Exec(t, fmt.Sprintf(`CREATE TABLE %s.flagnetic_exposures (
		schema_version UInt16, event_id String, project_key String, environment_key String,
		unit_id UInt64, received_at DateTime64(3, 'UTC')
	) ENGINE = MergeTree ORDER BY event_id`, db))
	created, _ := f.enabled(t, clickhouse(ch, db, false, "exposure"))

	f.post(t, f.exposure("u-1", "true", f.clock.Now()))
	tick(t, f.srv)

	s := f.sink(t, created)
	stream := streamOf(s, "exposure")
	if s.Health != management.SinkHealthFailing || stream == nil || stream.LastFailure == nil ||
		stream.LastFailure.Class != "rejected" || stream.LastFailure.Status == nil || *stream.LastFailure.Status != http.StatusBadRequest ||
		stream.LastFailure.Code == nil || *stream.LastFailure.Code == "" {
		t.Fatalf("sink after a batch ClickHouse refused %+v, want failing with class rejected, 400 and ClickHouse's code", s)
	}
	series := `flagnetic_sink_batches_total{event_kind="exposure",result="rejected",sink="` + created.Id.String() + `"}`
	if got := testkit.Metric(t, f.srv.Scrape(t), series); got != 1 {
		t.Errorf("%s = %v, want 1", series, got)
	}
	if got := chCount(t, ch, db, "flagnetic_exposures", false); got != 0 {
		t.Errorf("%d rows stored, want none", got)
	}
}
