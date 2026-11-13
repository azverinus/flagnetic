package sinks_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// docType is the type exported-events.md gives a column that ClickHouse
// declares as chType.
func docType(t *testing.T, chType string) string {
	t.Helper()
	switch {
	case chType == "String" || chType == "LowCardinality(String)":
		return "string"
	case chType == "Bool":
		return "bool"
	case chType == "UInt16":
		return "int"
	case chType == "UInt64":
		return "int64"
	case strings.HasPrefix(chType, "DateTime64(3,"):
		return "timestamp"
	}
	t.Fatalf("no documented type for the ClickHouse type %s", chType)
	return ""
}

// jsonColumns are the keys of an exported row's JSON object in order, each
// with the kind of JSON value it holds.
func jsonColumns(t *testing.T, row sinks.Row) (names []string, kinds map[string]string) {
	t.Helper()
	b, err := sinks.MarshalRow(row)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	kinds = map[string]string{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		name := key.(string)
		names = append(names, name)
		switch v.(type) {
		case string:
			kinds[name] = "string"
		case bool:
			kinds[name] = "bool"
		case float64:
			kinds[name] = "number"
		default:
			t.Fatalf("%s holds %T", name, v)
		}
	}
	return names, kinds
}

func TestTheDocumentedColumnsAreTheExportedRowsColumns(t *testing.T) {
	doc := testkit.Doc(t, "exported-events.md")
	for _, kind := range sinks.EventKinds {
		documented := testkit.DocColumns(t, doc, string(kind))
		names, kinds := jsonColumns(t, sinks.SampleRows(kind, "shop", time.Now())[0])
		table, ok := sinks.ClickHouseTableOf(kind)
		if !ok {
			t.Fatalf("no ClickHouse table for %s", kind)
		}
		var want []testkit.DocColumn
		for _, name := range names {
			var chType string
			for _, c := range table.Columns {
				if c.Name == name {
					chType = c.Type
				}
			}
			if chType == "" {
				t.Errorf("%s: %s is exported but not a ClickHouse column", kind, name)
				continue
			}
			typ := docType(t, chType)
			if kinds[name] != typ && !(kinds[name] == "number" && slices.Contains([]string{"int", "int64", "timestamp"}, typ)) {
				t.Errorf("%s.%s is JSON %s and ClickHouse %s", kind, name, kinds[name], chType)
			}
			want = append(want, testkit.DocColumn{Name: name, Type: typ})
		}
		if !slices.Equal(documented, want) {
			t.Errorf("%s columns documented as\n  %v\nexported as\n  %v", kind, documented, want)
		}
	}
}

// normalizeSQL collapses whitespace, including around brackets and commas, so
// layout never decides whether two statements agree.
func normalizeSQL(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for _, p := range []string{"(", ")", ","} {
		s = strings.ReplaceAll(s, " "+p, p)
		s = strings.ReplaceAll(s, p+" ", p)
	}
	return strings.TrimSuffix(s, ";")
}

func TestTheDocumentedClickHouseDDLIsWhatThePlatformCreates(t *testing.T) {
	doc := testkit.Doc(t, "exported-events.md")
	for _, table := range sinks.ClickHouseTables() {
		// The statement the platform sends, but for its IF NOT EXISTS and the
		// database a sink's configuration names.
		want := table.CreateStatement("db", "flagnetic")
		want = strings.Replace(want, "IF NOT EXISTS ", "", 1)
		want = strings.Replace(want, table.Qualified("db", "flagnetic"), table.Name("flagnetic"), 1)
		got := testkit.DocSQL(t, doc, "ddl:"+string(table.Kind))
		if normalizeSQL(got) != normalizeSQL(want) {
			t.Errorf("ddl:%s documented as\n  %s\ncreated as\n  %s", table.Kind, normalizeSQL(got), normalizeSQL(want))
		}
		for _, c := range testkit.DocColumns(t, doc, string(table.Kind)) {
			found := false
			for _, col := range table.Columns {
				found = found || (col.Name == c.Name && docType(t, col.Type) == c.Type)
			}
			if !found {
				t.Errorf("%s.%s is documented as %s, which the table does not declare", table.Kind, c.Name, c.Type)
			}
		}
	}
}

func TestTheDocumentedObjectKeysAndWebhookEnvelopeAreWhatTheSinksWrite(t *testing.T) {
	doc := testkit.Doc(t, "exported-events.md")
	for _, want := range []string{
		"{prefix}/{event_kind}/dt={YYYY-MM-DD}/{project_key}-{first_seq}-{last_seq}-{batch_hash}.{extension}",
		"`jsonl.gz` (`application/gzip`)", "`jsonl.zst` (`application/zstd`)",
		"`jsonl` (`application/x-ndjson`)", "`parquet` (`application/vnd.apache.parquet`)",
		"{prefix}/_flagnetic_probe", "7d4f8c1e-2a6b-4e5d-9c3f-0b1a8e6d5c42",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("exported-events.md does not say %s", want)
		}
	}
	rows := sinks.SampleRows(sinks.EventExposure, "shop", time.Now())
	for _, c := range []struct {
		format, compression string
	}{{"jsonl", "gzip"}, {"jsonl", "zstd"}, {"jsonl", "none"}, {"parquet", "zstd"}} {
		ext, contentType := sinks.ObjectStorage{Format: c.format, Compression: c.compression}.FileType()
		if !strings.Contains(doc, fmt.Sprintf("`%s` (`%s`)", ext, contentType)) {
			t.Errorf("exported-events.md does not pair `%s` with `%s`", ext, contentType)
		}
	}
	body, err := sinks.WebhookBody(sinks.ID{}, "shop", "dlv_x", "exposure", false, rows[:1])
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	for key := range envelope {
		if !strings.Contains(doc, `"`+key+`":`) {
			t.Errorf("the documented webhook envelope has no %q", key)
		}
	}
}
