package testkit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
)

var (
	createdTable = regexp.MustCompile(`(?im)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	droppedTable = regexp.MustCompile(`(?im)^\s*DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	// statement is how SQL that reads or writes rows starts.
	statement = regexp.MustCompile(`(?is)^\s*(?:--[^\n]*\n\s*)*(?:select|insert|update|delete|with|truncate|copy)\b`)
	// tableReference is a name following the words that take a table.
	tableReference = regexp.MustCompile(`(?i)\b(?:from|join|into|update|truncate(?:\s+table)?|copy)\s+(?:only\s+)?([a-z_][a-z0-9_]*)`)
)

// faultHelpers are the files of the test kit allowed to run a statement on
// an application table: each injects a state no API reaches, and says which.
var faultHelpers = []string{"faults.go", "restore.go"}

// applicationTables returns the tables the migrations leave in the schema.
func applicationTables(t *testing.T) map[string]bool {
	t.Helper()
	migrations := postgres.Migrations()
	files, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	tables := map[string]bool{}
	for _, f := range files {
		src, err := fs.ReadFile(migrations, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range createdTable.FindAllStringSubmatch(string(src), -1) {
			tables[strings.ToLower(m[1])] = true
		}
		for _, m := range droppedTable.FindAllStringSubmatch(string(src), -1) {
			delete(tables, strings.ToLower(m[1]))
		}
	}
	return tables
}

// tableStatements returns each SQL string of the test files of fsys, and of
// the test kit's helpers other than faultHelpers, that names a table of
// tables.
func tableStatements(t *testing.T, fsys fs.FS, tables map[string]bool) []string {
	t.Helper()
	var found []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		isTest := strings.HasSuffix(p, "_test.go")
		isKit := path.Dir(p) == "testkit" && strings.HasSuffix(p, ".go") && !slices.Contains(faultHelpers, path.Base(p))
		if !isTest && !isKit {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			sql, err := strconv.Unquote(lit.Value)
			if err != nil || !statement.MatchString(sql) {
				return true
			}
			for _, m := range tableReference.FindAllStringSubmatch(sql, -1) {
				if tables[strings.ToLower(m[1])] {
					found = append(found, fset.Position(lit.Pos()).String()+": "+m[1])
					break
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// A test asserts on what the API, or a repository's own query, answers; a
// table is not part of what a client or the next minor sees, so a test
// reading one breaks on a schema change that alters no behaviour and stays
// green when the column the server reads is no longer the one it asserts on.
// What no API reaches, a fault, is injected by a helper of faults.go or
// restore.go that names what it stands for.
func TestNoTestRunsAStatementOnAnApplicationTable(t *testing.T) {
	tables := applicationTables(t)
	if len(tables) < 50 || !tables["alias_events"] || !tables["scim_tokens"] {
		t.Fatalf("read %d tables from the migrations, want the schema's", len(tables))
	}
	// The directories holding Go tests and the test kit, from the root of
	// the module.
	for _, dir := range []string{"../../internal", "../../cmd", "../../e2e"} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		if found := tableStatements(t, os.DirFS(dir), tables); len(found) > 0 {
			t.Errorf("tests run SQL on application tables; observe through the API or a repository's own query, or name a fault helper in the test kit:\n%s",
				strings.Join(found, "\n"))
		}
	}
}

func TestTheTableGuardFindsEveryStatementOnATable(t *testing.T) {
	tables := map[string]bool{"scim_tokens": true, "alias_events": true}
	fsys := fstest.MapFS{
		"a/a_test.go": {Data: []byte("package a\nvar _ = []string{\n" +
			"`UPDATE scim_tokens SET x = 1`,\n" +
			"`SELECT 1 FROM alias_events`,\n" +
			"`insert into alias_events (a) values (1)`,\n" +
			"`WITH x AS (SELECT 1) SELECT * FROM scim_tokens`,\n" +
			"`SELECT count(*) FROM pg_stat_activity`,\n" +
			"\"reading rows from alias_events failed\",\n" +
			"}\n")},
		"testkit/server.go":  {Data: []byte("package testkit\nvar _ = `DELETE FROM scim_tokens`\n")},
		"testkit/faults.go":  {Data: []byte("package testkit\nvar _ = `DELETE FROM scim_tokens`\n")},
		"testkit/restore.go": {Data: []byte("package testkit\nvar _ = `UPDATE alias_events SET a = 1`\n")},
		"other/helper.go":    {Data: []byte("package other\nvar _ = `DELETE FROM scim_tokens`\n")},
		"testdata/x_test.go": {Data: []byte("package x\nvar _ = `DELETE FROM scim_tokens`\n")},
	}
	found := tableStatements(t, fsys, tables)
	if len(found) != 5 {
		t.Errorf("found %d statements, want the 4 of a_test.go and the one of testkit/server.go:\n%s", len(found), strings.Join(found, "\n"))
	}
}
