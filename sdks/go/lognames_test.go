package flagnetic_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// logNamesDoc is the SDK documentation whose "Log names" table lists the
// log event names every SDK shares, so that an operator's alert works
// whichever SDK a service uses.
const logNamesDoc = "../../docs/sdks/conformance.md"

// replacedLogNames are names this SDK once logged for a concept the table
// now names otherwise: an alert still written against one would never fire.
var replacedLogNames = []string{
	"ingest_failed",
	"exposures_disabled",
	"sse_connected",
	"sse_dropped",
	"window_older_than_one_hour",
	"deprecation_received",
	"deprecated_operation",
}

// documentedLogNames reads the first column of the "Log names" table.
func documentedLogNames(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(logNamesDoc)
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(raw), "\n## Log names\n")
	if !found {
		t.Fatalf("%s has no Log names section", logNamesDoc)
	}
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")
	var names []string
	for _, m := range row.FindAllStringSubmatch(section, -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatalf("the Log names table of %s lists no name", logNamesDoc)
	}
	return names
}

// packageSource parses the package's own files, tests left out, and returns
// the values of its Event constants and every string literal it holds.
func packageSource(t *testing.T) (events map[string]string, literals []string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	events = map[string]string{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				typ, ok := n.Type.(*ast.Ident)
				if !ok || typ.Name != "Event" {
					return true
				}
				for i, id := range n.Names {
					if lit, ok := n.Values[i].(*ast.BasicLit); ok && id.IsExported() {
						v, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatal(err)
						}
						events[v] = id.Name
					}
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					if v, err := strconv.Unquote(n.Value); err == nil {
						literals = append(literals, v)
					}
				}
			}
			return true
		})
	}
	return events, literals
}

func TestLogNamesAreTheDocumentedOnes(t *testing.T) {
	events, literals := packageSource(t)
	for _, name := range documentedLogNames(t) {
		if _, ok := events[name]; !ok {
			t.Errorf("%s is documented but no exported Event constant has that value", name)
		}
	}
	if !slices.Contains(literals, "window_too_old") {
		t.Error("window_too_old is documented as a queue_dropped cause but never logged")
	}
	for _, old := range replacedLogNames {
		if c, ok := events[old]; ok {
			t.Errorf("%s still has the replaced name %s", c, old)
		}
		if slices.Contains(literals, old) {
			t.Errorf("the replaced name %s is still logged", old)
		}
	}
}
