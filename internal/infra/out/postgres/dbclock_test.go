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

// databaseClock matches every way SQL reads PostgreSQL's clock: the functions,
// the keywords, timeofday(), and the special date/time inputs 'now', 'today',
// 'tomorrow' and 'yesterday' (DEFAULT 'now', 'now'::timestamptz), which
// PostgreSQL resolves to the current time when the statement runs. Time comes
// from the application's clock alone, passed in as a parameter, so that a test
// moving the fake clock moves all time at once.
var databaseClock = regexp.MustCompile(`(?i)\b(?:now|timeofday|clock_timestamp|transaction_timestamp|statement_timestamp)\s*\(|\b(?:current_timestamp|localtimestamp|current_date|current_time|localtime)\b|'\s*(?:now|today|tomorrow|yesterday)\s*'`)

// withoutSQLComments removes the comments of sql, `--` to the end of the line
// and `/* */` (which nest in PostgreSQL), and leaves string literals and quoted
// identifiers whole: a `--` inside a literal starts no comment, so
// `'--' || now()` still shows its read.
func withoutSQLComments(sql string) string {
	var out strings.Builder
	for i := 0; i < len(sql); {
		switch {
		case strings.HasPrefix(sql[i:], "--"):
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
		case strings.HasPrefix(sql[i:], "/*"):
			for depth := 0; i < len(sql); {
				switch {
				case strings.HasPrefix(sql[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(sql[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
				if depth == 0 {
					break
				}
			}
		case sql[i] == '\'' || sql[i] == '"':
			// A quote inside is doubled, which reads as the literal ending and
			// starting again, so copying quote to quote is enough.
			quote := sql[i]
			j := i + 1
			for j < len(sql) && sql[j] != quote {
				j++
			}
			j = min(j+1, len(sql))
			out.WriteString(sql[i:j])
			i = j
		default:
			out.WriteByte(sql[i])
			i++
		}
	}
	return out.String()
}

// databaseClockReads returns each read of the database clock in fsys: in .sql
// files, outside comments, and in the string literals of Go files other than
// tests. testdata directories are skipped.
func databaseClockReads(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var found []string
	check := func(where, sql string) {
		for _, m := range databaseClock.FindAllString(withoutSQLComments(sql), -1) {
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
		// pglisten and the other packages under internal/pkg send SQL too.
		"shared packages": os.DirFS("../../../pkg"),
	} {
		if found := databaseClockReads(t, fsys); len(found) > 0 {
			t.Errorf("%s read the database clock; pass the application clock's time as a parameter instead:\n%s",
				name, strings.Join(found, "\n"))
		}
	}
}

func TestTheDatabaseClockGuardFindsEveryRead(t *testing.T) {
	found := databaseClockReads(t, os.DirFS("testdata/dbclock"))
	if len(found) != 16 {
		t.Errorf("found %d reads of the database clock in the fixture, want 16:\n%s", len(found), strings.Join(found, "\n"))
	}
}
