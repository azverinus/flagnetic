package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/cli"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// fixturesV1 is the first migration of fixtures alone: the migration set of
// an older binary.
var fixturesV1 = cli.WithMigrations(os.DirFS("testdata/migrations-v1"))

func migrated(t *testing.T, opts ...cli.Option) string {
	t.Helper()
	url := testkit.EmptyDatabase(t)
	if res := migrate(url, opts...); res.code != 0 {
		t.Fatalf("migrate: exit code = %d:\n%s", res.code, res.stderr)
	}
	return url
}

func TestWithoutAutoMigrateAServerBehindTheBinaryIsRefused(t *testing.T) {
	url := migrated(t, fixturesV1)
	res := run(map[string]string{"DATABASE_URL": url, "FLAGNETIC_AUTO_MIGRATE": "false", "FLAGNETIC_LISTEN_ADDR": "127.0.0.1:0"}, []cli.Option{fixtures}, "serve")
	if res.code != 1 {
		t.Fatalf("exit code = %d, want 1:\n%s", res.code, res.stderr)
	}
	for _, want := range []string{"version 1", "version 2", "flagnetic migrate"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, res.stderr)
		}
	}
	if strings.Contains(res.stderr, `"msg":"listening"`) || strings.Contains(res.stderr, `"msg":"migrations applied"`) {
		t.Errorf("serve listened or migrated before refusing:\n%s", res.stderr)
	}
}

func TestWithoutAutoMigrateAServerAheadOfTheBinaryStartsWithAWarning(t *testing.T) {
	url := migrated(t, fixtures)
	r := serve(t, map[string]string{"DATABASE_URL": url, "FLAGNETIC_AUTO_MIGRATE": "false"}, fixturesV1)
	rec := r.findLog("database schema is ahead of this binary")
	if rec == nil || rec["level"] != "WARN" || rec["database_version"] != 2.0 || rec["binary_version"] != 1.0 {
		t.Errorf("want one warning naming both versions, got %v:\n%s", rec, r.stderr.String())
	}
	if r.findLog("migrations applied") != nil {
		t.Error("serve migrated with FLAGNETIC_AUTO_MIGRATE=false")
	}
}

func TestWithoutAutoMigrateAMatchingSchemaStartsSilently(t *testing.T) {
	for name, opts := range map[string][]cli.Option{
		"fixture set":       {fixtures},
		"empty set on both": nil,
	} {
		t.Run(name, func(t *testing.T) {
			url := migrated(t, opts...)
			r := serve(t, map[string]string{"DATABASE_URL": url, "FLAGNETIC_AUTO_MIGRATE": "false"}, opts...)
			if r.findLog("database schema is ahead of this binary") != nil {
				t.Errorf("a matching schema was reported:\n%s", r.stderr.String())
			}
			if r.findLog("migrations applied") != nil {
				t.Error("serve migrated with FLAGNETIC_AUTO_MIGRATE=false")
			}
		})
	}
}

func TestWithoutAutoMigrateAnUnmigratedDatabaseIsRefused(t *testing.T) {
	res := run(map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_AUTO_MIGRATE": "false"}, []cli.Option{fixtures}, "serve")
	if res.code != 1 || !strings.Contains(res.stderr, "flagnetic migrate") {
		t.Errorf("exit code = %d, want 1 pointing at flagnetic migrate:\n%s", res.code, res.stderr)
	}
}
