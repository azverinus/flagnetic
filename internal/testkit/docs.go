package testkit

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Doc returns the text of docs/name, so that a test can hold a page to the
// code it describes.
func Doc(t testing.TB, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the repository")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "docs", name)) //nolint:gosec // a test reads the page it holds to the code
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// DocAfterMarker is doc from the line after `<!-- marker -->` on.
func DocAfterMarker(t testing.TB, doc, marker string) string {
	t.Helper()
	line := "<!-- " + marker + " -->\n"
	i := strings.Index(doc, line)
	if i < 0 {
		t.Fatalf("the page has no %q", line)
	}
	return doc[i+len(line):]
}

// DocColumn is one row of a documented column table.
type DocColumn struct{ Name, Type string }

// DocColumns is the Column | Type | Meaning table following
// `<!-- columns:kind -->`.
func DocColumns(t testing.TB, doc, kind string) []DocColumn {
	t.Helper()
	var out []DocColumn
	for i, line := range strings.Split(DocAfterMarker(t, doc, "columns:"+kind), "\n") {
		if !strings.HasPrefix(line, "|") {
			break
		}
		if i < 2 { // the header and its rule
			continue
		}
		cells := strings.SplitN(strings.Trim(line, "|"), "|", 3)
		if len(cells) != 3 {
			t.Fatalf("columns:%s: %q is not a Column | Type | Meaning row", kind, line)
		}
		out = append(out, DocColumn{strings.Trim(strings.TrimSpace(cells[0]), "`"), strings.TrimSpace(cells[1])})
	}
	return out
}

// DocSQL is the sql block following `<!-- marker -->`, without its trailing
// semicolon.
func DocSQL(t testing.TB, doc, marker string) string {
	t.Helper()
	rest, ok := strings.CutPrefix(strings.TrimLeft(DocAfterMarker(t, doc, marker), "\n"), "```sql\n")
	end := strings.Index(rest, "```")
	if !ok || end < 0 {
		t.Fatalf("%s is not followed by a closed sql block", marker)
	}
	return strings.TrimSuffix(strings.TrimSpace(rest[:end]), ";")
}

// DocMetricLabels returns the labels of the row of metric in doc's metrics
// table, sorted, and whether doc has the row.
func DocMetricLabels(doc, metric string) ([]string, bool) {
	prefix := "| `" + metric + "` | "
	i := strings.Index(doc, prefix)
	if i < 0 {
		return nil, false
	}
	cell := doc[i+len(prefix):]
	cell = cell[:strings.Index(cell, " |")]
	var labels []string
	for _, l := range strings.Split(cell, ",") {
		if l = strings.Trim(strings.TrimSpace(l), "`"); l != "" {
			labels = append(labels, l)
		}
	}
	slices.Sort(labels)
	return labels, true
}
