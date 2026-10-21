package postgres_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
)

// createExtension matches each CREATE EXTENSION statement up to its end.
var createExtension = regexp.MustCompile(`(?is)\bCREATE\s+EXTENSION\b[^;]*;`)

var withSchema = regexp.MustCompile(`(?i)\bSCHEMA\s+\S`)

// An extension is installed once per database, in one schema, and goes when
// that schema is dropped, taking every index on its operators with it. A
// migration that creates one without naming the schema puts it in the schema
// being migrated, so dropping one Flagnetic schema would break the others
// sharing the database.
func TestEveryExtensionIsCreatedInANamedSchema(t *testing.T) {
	migrations := postgres.Migrations()
	files, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	var unplaced []string
	for _, f := range files {
		src, err := fs.ReadFile(migrations, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range createExtension.FindAllString(sqlComment.ReplaceAllString(string(src), ""), -1) {
			if !withSchema.MatchString(stmt) {
				unplaced = append(unplaced, f+": "+strings.Join(strings.Fields(stmt), " "))
			}
		}
	}
	if len(unplaced) > 0 {
		t.Errorf("extensions created without WITH SCHEMA, so they would live in the migrated schema:\n%s", strings.Join(unplaced, "\n"))
	}
}
