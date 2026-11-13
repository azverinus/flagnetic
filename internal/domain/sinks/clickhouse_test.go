package sinks_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

func TestEachEventKindHasATableWhoseColumnsAreItsRowsColumnsInOrder(t *testing.T) {
	rows := map[sinks.EventKind]sinks.Row{
		sinks.EventExposure:          exposureRow(),
		sinks.EventAlias:             sinks.SampleRows(sinks.EventAlias, "shop", rowTime)[0],
		sinks.EventEvaluationSummary: sinks.SampleRows(sinks.EventEvaluationSummary, "shop", rowTime)[0],
	}
	for _, kind := range sinks.EventKinds {
		table, ok := sinks.ClickHouseTableOf(kind)
		if !ok {
			t.Fatalf("no table for %s", kind)
		}
		encoded, err := sinks.MarshalRow(rows[kind])
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range table.Columns {
			names = append(names, c.Name)
		}
		got := decode(t, encoded)
		if len(got) != len(names) {
			t.Errorf("%s: row has %d columns, table %d", kind, len(got), len(names))
		}
		for _, n := range names {
			if _, ok := got[n]; !ok {
				t.Errorf("%s: row has no column %s", kind, n)
			}
		}
		if !slices.Contains(names, table.IDColumn) {
			t.Errorf("%s: no column %s", kind, table.IDColumn)
		}
		for _, k := range table.OrderBy {
			if !slices.Contains(names, k) {
				t.Errorf("%s: sorting key %s is no column", kind, k)
			}
		}
	}
	if _, ok := sinks.ClickHouseTableOf("audit"); ok {
		t.Error("a table for a kind no sink exports")
	}
	if got := len(sinks.ClickHouseTables()); got != 3 {
		t.Errorf("%d tables, want 3", got)
	}
}

func TestATablesStatementsNameItQuotedAndKeepTheDocumentedShape(t *testing.T) {
	table, _ := sinks.ClickHouseTableOf(sinks.EventAlias)
	if got, want := table.Qualified("my`db", "flagnetic"), "`my\\`db`.`flagnetic_aliases`"; got != want {
		t.Errorf("Qualified = %s, want %s", got, want)
	}
	create := table.CreateStatement("warehouse", "x")
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `warehouse`.`x_aliases` (schema_version UInt16, event_id String, project_key LowCardinality(String)",
		"occurred_at DateTime64(3, 'UTC')",
		") ENGINE = ReplacingMergeTree PARTITION BY toYYYYMM(received_at) ORDER BY (project_key, environment_key, from_unit_type, from_unit_id, event_id)",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("create statement %s lacks %s", create, want)
		}
	}
	if strings.Contains(create, "TTL") {
		t.Errorf("create statement has a TTL: %s", create)
	}
	if got, want := table.InsertStatement("warehouse", "x", []string{"event_id", "project_key"}),
		"INSERT INTO `warehouse`.`x_aliases` (event_id, project_key) FORMAT JSONEachRow"; got != want {
		t.Errorf("insert = %s, want %s", got, want)
	}
}

func TestTheSummaryTableSortsByWhatMakesOneRowUniqueAndRequiresItsReportID(t *testing.T) {
	table, _ := sinks.ClickHouseTableOf(sinks.EventEvaluationSummary)
	if want := []string{"project_key", "environment_key", "flag_key", "report_id", "variation_key", "evaluation_reason"}; !slices.Equal(table.OrderBy, want) {
		t.Errorf("sorting key %v, want %v", table.OrderBy, want)
	}
	have := table.ColumnSet()
	delete(have, "report_id")
	delete(have, "app_version")
	delete(have, "source")
	if got, want := table.MissingRequired(have), []string{"report_id"}; !slices.Equal(got, want) {
		t.Errorf("missing required %v, want %v", got, want)
	}
	if got, want := table.MissingOptional(have), []string{"app_version", "source"}; !slices.Equal(got, want) {
		t.Errorf("missing optional %v, want %v", got, want)
	}
	if got := table.Intersection(have); slices.Contains(got, "source") || got[0] != "schema_version" {
		t.Errorf("intersection %v, want the shared columns in export order", got)
	}
}

func TestJSONEachRowHoldsOnlyTheColumnsAskedForWithTimestampsAsUTCText(t *testing.T) {
	r := exposureRow()
	r.UnitID = "<u&1>"
	var b bytes.Buffer
	if err := sinks.WriteJSONEachRow(&b, []sinks.Row{r, r}, []string{"event_id", "unit_id", "override", "ruleset_version", "received_at"}); err != nil {
		t.Fatal(err)
	}
	want := `{"event_id":"0195f6b0-0000-7000-8000-000000000001","unit_id":"<u&1>","override":true,"ruleset_version":7,"received_at":"2026-03-04T05:06:08.891Z"}` + "\n"
	if got := b.String(); got != want+want {
		t.Errorf("rows\n%s\nwant\n%s", got, want+want)
	}
	if err := sinks.WriteJSONEachRow(&b, []sinks.Row{r}, []string{"window_start"}); err == nil {
		t.Error("a column the row's schema lacks was accepted")
	}
}

func TestASchemaFailureNamesTheExceptionClickHouseWouldAnswer(t *testing.T) {
	for f, code := range map[sinks.Failure]string{sinks.NoTableFailure(): "60 UNKNOWN_TABLE", sinks.NoColumnFailure(): "16 NO_SUCH_COLUMN_IN_TABLE"} {
		if f.Class != sinks.ClassRejected || f.Status != 0 || f.Code != code {
			t.Errorf("failure %+v, want rejected with %s", f, code)
		}
	}
}
