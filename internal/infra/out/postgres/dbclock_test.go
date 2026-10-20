package postgres_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
)

// databaseClock matches every way SQL reads PostgreSQL's clock. Time comes from
// the application's clock alone, passed in as a parameter, so that a test
// moving the fake clock moves all time at once.
var databaseClock = regexp.MustCompile(`(?i)\b(?:now|clock_timestamp|transaction_timestamp|statement_timestamp)\s*\(|\b(?:current_timestamp|localtimestamp|current_date|current_time|localtime)\b`)

var sqlComment = regexp.MustCompile(`(?s)--[^\n]*|/\*.*?\*/`)

// databaseClockReads returns each read of the database clock in fsys: in .sql
// files, outside comments, and in the string literals of Go files other than
// tests. testdata directories are skipped.
func databaseClockReads(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var found []string
	check := func(where, sql string) {
		for _, m := range databaseClock.FindAllString(sqlComment.ReplaceAllString(sql, ""), -1) {
			found = append(found, where+": "+m)
		}
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" && p != "." {
				return fs.SkipDir
			}
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		switch {
		case path.Ext(p) == ".sql":
			check(p, string(src))
		case path.Ext(p) == ".go" && !strings.HasSuffix(p, "_test.go"):
			file, err := parser.ParseFile(token.NewFileSet(), p, src, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						check(p, s)
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestNoSQLReadsTheDatabaseClock(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"embedded migrations": postgres.Migrations(),
		"outbound adapters":   os.DirFS(".."),
	} {
		if found := databaseClockReads(t, fsys); len(found) > 0 {
			t.Errorf("%s read the database clock; pass the application clock's time as a parameter instead:\n%s",
				name, strings.Join(found, "\n"))
		}
	}
}

func TestTheDatabaseClockGuardFindsEveryRead(t *testing.T) {
	found := databaseClockReads(t, os.DirFS("testdata/dbclock"))
	if len(found) != 10 {
		t.Errorf("found %d reads of the database clock in the fixture, want 10:\n%s", len(found), strings.Join(found, "\n"))
	}
}
