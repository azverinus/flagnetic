package sinks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// ClickHouseColumn is one column of the export schema as a table the platform
// creates declares it.
type ClickHouseColumn struct{ Name, Type string }

const (
	chLowCardinality = "LowCardinality(String)"
	chTimestamp      = "DateTime64(3, 'UTC')"
)

// ClickHouseTable is the table one event kind is exported to: its name after
// the sink's table prefix, the export schema's columns in export order, and
// the sorting key a table the platform creates is sorted, and deduplicated, by.
type ClickHouseTable struct {
	Kind EventKind
	// Suffix follows the table prefix and an underscore in the table's name.
	Suffix string
	// IDColumn is the column holding the row's ID: event_id, or a summary
	// row's report_id.
	IDColumn string
	// Columns are the export schema's columns, in export order.
	Columns []ClickHouseColumn
	// OrderBy is the sorting key. The tables are ReplacingMergeTree, so it is
	// also the deduplication key: it ends in the columns that make one
	// exported row unique, so that a retried or replayed row folds into the
	// copy already stored and a summary's other rows of the same report are
	// not folded into it.
	OrderBy []string
}

var clickHouseTables = []ClickHouseTable{
	{
		Kind: EventExposure, Suffix: "exposures", IDColumn: "event_id",
		Columns: []ClickHouseColumn{
			{"schema_version", "UInt16"}, {"event_id", "String"},
			{"project_key", chLowCardinality}, {"environment_key", chLowCardinality},
			{"experiment_key", chLowCardinality}, {"flag_key", chLowCardinality},
			{"variation_key", chLowCardinality}, {"unit_type", chLowCardinality}, {"unit_id", "String"},
			{"evaluation_reason", chLowCardinality}, {"override", "Bool"},
			{"platform", chLowCardinality}, {"app_version", "String"},
			{"sdk_name", chLowCardinality}, {"sdk_version", chLowCardinality}, {"ruleset_version", "UInt64"},
			{"occurred_at", chTimestamp}, {"received_at", chTimestamp},
		},
		OrderBy: []string{"project_key", "environment_key", "experiment_key", "unit_id", "event_id"},
	},
	{
		Kind: EventAlias, Suffix: "aliases", IDColumn: "event_id",
		Columns: []ClickHouseColumn{
			{"schema_version", "UInt16"}, {"event_id", "String"},
			{"project_key", chLowCardinality}, {"environment_key", chLowCardinality},
			{"from_unit_type", chLowCardinality}, {"from_unit_id", "String"},
			{"to_unit_type", chLowCardinality}, {"to_unit_id", "String"},
			{"platform", chLowCardinality}, {"sdk_name", chLowCardinality}, {"sdk_version", chLowCardinality},
			{"occurred_at", chTimestamp}, {"received_at", chTimestamp},
		},
		OrderBy: []string{"project_key", "environment_key", "from_unit_type", "from_unit_id", "event_id"},
	},
	{
		Kind: EventEvaluationSummary, Suffix: "evaluation_summaries", IDColumn: "report_id",
		Columns: []ClickHouseColumn{
			{"schema_version", "UInt16"}, {"report_id", "String"},
			{"project_key", chLowCardinality}, {"environment_key", chLowCardinality},
			{"flag_key", chLowCardinality}, {"variation_key", chLowCardinality},
			{"evaluation_reason", chLowCardinality}, {"count", "UInt64"},
			{"window_start", chTimestamp}, {"window_end", chTimestamp},
			{"sdk_name", chLowCardinality}, {"sdk_version", chLowCardinality},
			{"platform", chLowCardinality}, {"app_version", "String"}, {"ruleset_version", "UInt64"},
			{"received_at", chTimestamp}, {"source", chLowCardinality},
		},
		OrderBy: []string{"project_key", "environment_key", "flag_key", "report_id", "variation_key", "evaluation_reason"},
	},
}

// ClickHouseTables returns the three tables, one per event kind. The result is
// a copy.
func ClickHouseTables() []ClickHouseTable { return slices.Clone(clickHouseTables) }

// ClickHouseTableOf returns the table the rows of kind are exported to.
func ClickHouseTableOf(kind EventKind) (ClickHouseTable, bool) {
	for _, t := range clickHouseTables {
		if t.Kind == kind {
			return t, true
		}
	}
	return ClickHouseTable{}, false
}

// Name is the table's name under prefix.
func (t ClickHouseTable) Name(prefix string) string { return prefix + "_" + t.Suffix }

// Qualified is the table's name under database and prefix, quoted as a
// statement names it.
func (t ClickHouseTable) Qualified(database, prefix string) string {
	return QuoteIdentifier(database) + "." + QuoteIdentifier(t.Name(prefix))
}

// Required are the columns a table must have for a row to mean anything
// downstream; any other column it lacks is a warning, and inserts skip it.
func (t ClickHouseTable) Required() []string {
	return []string{"schema_version", t.IDColumn, "project_key", "environment_key", "received_at"}
}

// MissingRequired returns the required columns that have lacks.
func (t ClickHouseTable) MissingRequired(have map[string]bool) []string {
	var missing []string
	for _, name := range t.Required() {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// MissingOptional returns the columns that have lacks and are not required, in
// export order.
func (t ClickHouseTable) MissingOptional(have map[string]bool) []string {
	required := t.Required()
	var missing []string
	for _, c := range t.Columns {
		if !have[c.Name] && !slices.Contains(required, c.Name) {
			missing = append(missing, c.Name)
		}
	}
	return missing
}

// Intersection returns the columns of the export schema that have holds, in
// export order: the columns an insert names.
func (t ClickHouseTable) Intersection(have map[string]bool) []string {
	var out []string
	for _, c := range t.Columns {
		if have[c.Name] {
			out = append(out, c.Name)
		}
	}
	return out
}

// ColumnSet returns the names of all the table's columns.
func (t ClickHouseTable) ColumnSet() map[string]bool {
	set := make(map[string]bool, len(t.Columns))
	for _, c := range t.Columns {
		set[c.Name] = true
	}
	return set
}

// CreateStatement is the statement that creates the table. It is partitioned
// by month of receipt and has no TTL, since retention in ClickHouse is the data
// engineer's. IF NOT EXISTS, since another replica may create it first.
func (t ClickHouseTable) CreateStatement(database, prefix string) string {
	defs := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		defs[i] = c.Name + " " + c.Type
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s) ENGINE = ReplacingMergeTree PARTITION BY toYYYYMM(received_at) ORDER BY (%s)",
		t.Qualified(database, prefix), strings.Join(defs, ", "), strings.Join(t.OrderBy, ", "))
}

// InsertStatement is the statement that inserts rows with columns.
func (t ClickHouseTable) InsertStatement(database, prefix string, columns []string) string {
	return fmt.Sprintf("INSERT INTO %s (%s) FORMAT JSONEachRow", t.Qualified(database, prefix), strings.Join(columns, ", "))
}

// QuoteIdentifier quotes name for a ClickHouse statement.
func QuoteIdentifier(name string) string {
	return "`" + strings.NewReplacer(`\`, `\\`, "`", "\\`").Replace(name) + "`"
}

// clickHouseTimestamps are the export schema's timestamps. Rows carry them as
// Unix milliseconds; ClickHouse is sent RFC 3339 in UTC, so that a DateTime64
// column of any time zone reads the same instant.
var clickHouseTimestamps = []string{"occurred_at", "received_at", "window_start", "window_end"}

// WriteJSONEachRow writes rows to w as JSONEachRow holding columns only, in
// that order, every timestamp as RFC 3339 UTC with milliseconds and no HTML
// escaping. A column a row's schema does not have is an error.
func WriteJSONEachRow(w io.Writer, rows []Row, columns []string) error {
	var line bytes.Buffer
	for _, r := range rows {
		encoded, err := MarshalRow(r)
		if err != nil {
			return err
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &values); err != nil {
			return err
		}
		line.Reset()
		line.WriteByte('{')
		for i, col := range columns {
			v, ok := values[col]
			if !ok {
				return fmt.Errorf("the row has no %s", col)
			}
			if slices.Contains(clickHouseTimestamps, col) {
				var ms int64
				if err := json.Unmarshal(v, &ms); err != nil {
					return fmt.Errorf("%s: %w", col, err)
				}
				v = json.RawMessage(`"` + time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") + `"`)
			}
			if i > 0 {
				line.WriteByte(',')
			}
			line.WriteString(`"` + col + `":`)
			line.Write(v)
		}
		line.WriteString("}\n")
		if _, err := w.Write(line.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// ClickHouse exception codes a sink names when the check it makes itself finds
// the schema wrong, the codes ClickHouse would answer an insert into such a
// table with.
const (
	exceptionNoSuchColumn = "16"
	exceptionUnknownTable = "60"
)

// UnknownTableCode is the exception code of a table that does not exist, as
// X-ClickHouse-Exception-Code carries it.
const UnknownTableCode = exceptionUnknownTable

// NoTableFailure is the failure of a table that does not exist and may not be
// created.
func NoTableFailure() Failure { return schemaFailure(exceptionUnknownTable) }

// NoColumnFailure is the failure of a table that lacks a required column.
func NoColumnFailure() Failure { return schemaFailure(exceptionNoSuchColumn) }

func schemaFailure(exception string) Failure {
	code, _ := clickHouseCode(exception)
	return Failure{Class: ClassRejected, Code: code}
}
