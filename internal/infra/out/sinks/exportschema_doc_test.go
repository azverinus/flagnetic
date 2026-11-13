package sinks_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "github.com/azverinus/flagnetic/internal/domain/sinks"
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
func jsonColumns(t *testing.T, row domain.Row) (names []string, kinds map[string]string) {
	t.Helper()
	b, err := domain.MarshalRow(row)
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
	for _, kind := range domain.EventKinds {
		documented := testkit.DocColumns(t, doc, string(kind))
		names, kinds := jsonColumns(t, domain.SampleRows(kind, "shop", time.Now())[0])
		table, ok := domain.ClickHouseTableOf(kind)
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
	for _, table := range domain.ClickHouseTables() {
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
	rows := domain.SampleRows(domain.EventExposure, "shop", time.Now())

	// The documented key template, filled in with one batch's values, is the
	// key the sink writes that batch under.
	template := regexp.MustCompile("(?m)^(\\{prefix\\}/\\{event_kind\\}/\\S+)$").FindStringSubmatch(doc)
	if template == nil {
		t.Fatal("exported-events.md has no object key template")
	}
	cfg := domain.ObjectStorage{Prefix: "lake", Format: "jsonl", Compression: "gzip"}
	ext, _ := cfg.FileType()
	documentedKey := strings.NewReplacer(
		"{prefix}", "lake", "{event_kind}", string(domain.EventExposure),
		"{YYYY-MM-DD}", rows[0].Received().UTC().Format("2006-01-02"), "{project_key}", "shop",
		"{first_seq}", strconv.FormatInt(rows[0].Sequence(), 10),
		"{last_seq}", strconv.FormatInt(rows[len(rows)-1].Sequence(), 10),
		"{batch_hash}", "3f1c9a0b7e2d4c6a8b0e1f2d3c", "{extension}", ext,
	).Replace(template[1])
	if got := cfg.BatchKey(domain.EventExposure, "shop", "dlv_3f1c9a0b7e2d4c6a8b0e1f2d3c", rows); got != documentedKey {
		t.Errorf("a batch is written under %s, the page documents %s", got, documentedKey)
	}
	if probe := (domain.ObjectStorage{Prefix: "{prefix}"}).ProbeKey(); !strings.Contains(doc, "`"+probe+"`") {
		t.Errorf("exported-events.md does not name the probe object %s", probe)
	}

	// The documented namespace derives the report IDs the platform exports.
	namespace := regexp.MustCompile("in the namespace `([0-9a-f-]{36})`").FindStringSubmatch(doc)
	if namespace == nil {
		t.Fatal("exported-events.md names no report ID namespace")
	}
	if want := uuid.NewSHA1(uuid.MustParse(namespace[1]), []byte("12:34")).String(); domain.ReportID(12, 34) != want {
		t.Errorf("ReportID(12, 34) = %s, the documented derivation gives %s", domain.ReportID(12, 34), want)
	}

	for _, c := range []struct {
		format, compression string
	}{{"jsonl", "gzip"}, {"jsonl", "zstd"}, {"jsonl", "none"}, {"parquet", "zstd"}} {
		ext, contentType := domain.ObjectStorage{Format: c.format, Compression: c.compression}.FileType()
		if !strings.Contains(doc, fmt.Sprintf("`%s` (`%s`)", ext, contentType)) {
			t.Errorf("exported-events.md does not pair `%s` with `%s`", ext, contentType)
		}
	}
	body, err := domain.WebhookBody(domain.ID{}, "shop", "dlv_x", "exposure", false, rows[:1])
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
