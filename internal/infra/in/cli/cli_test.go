package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/infra/in/cli"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// fixtures is a migration set of two migrations, standing in for the
// embedded one, which may be empty.
var fixtures = cli.WithMigrations(os.DirFS("testdata/migrations"))

type result struct {
	code           int
	stdout, stderr string
}

// logRecords parses the JSON log lines written to stderr.
func (r result) logRecords(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(r.stderr))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("stderr line %q is not JSON: %v", sc.Text(), err)
		}
		out = append(out, rec)
	}
	return out
}

func (r result) logRecord(t *testing.T, msg string) map[string]any {
	t.Helper()
	for _, rec := range r.logRecords(t) {
		if rec["msg"] == msg {
			return rec
		}
	}
	t.Fatalf("no %q log record in stderr:\n%s", msg, r.stderr)
	return nil
}

// secretKey is the secret key every command runs with unless a test sets
// FLAGNETIC_SECRET_KEY itself, to the empty string for none.
const secretKey = "0123456789abcdef0123456789abcdef"

func run(env map[string]string, opts []cli.Option, args ...string) result {
	var stdout, stderr bytes.Buffer
	lookup := func(k string) (string, bool) {
		v, ok := env[k]
		if !ok && k == "FLAGNETIC_SECRET_KEY" {
			return secretKey, true
		}
		return v, ok
	}
	code := cli.Main(args, lookup, &stdout, &stderr, make(chan os.Signal), opts...)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func migrate(url string, opts ...cli.Option) result {
	return run(map[string]string{"DATABASE_URL": url}, opts, "migrate")
}

func TestMigrateAppliesTheMigrationSet(t *testing.T) {
	r := migrate(testkit.EmptyDatabase(t), fixtures)
	if r.code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr:\n%s", r.code, r.stderr)
	}
	rec := r.logRecord(t, "migrations applied")
	if rec["applied"] != 2.0 || rec["version"] != 2.0 {
		t.Errorf("applied %v up to version %v, want 2 up to 2", rec["applied"], rec["version"])
	}
}

func TestMigrateTwiceSucceeds(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	if r := migrate(url, fixtures); r.code != 0 {
		t.Fatalf("first migrate: exit code = %d; stderr:\n%s", r.code, r.stderr)
	}
	r := migrate(url, fixtures)
	if r.code != 0 {
		t.Fatalf("second migrate: exit code = %d, want 0; stderr:\n%s", r.code, r.stderr)
	}
	rec := r.logRecord(t, "migrations applied")
	if rec["applied"] != 0.0 || rec["version"] != 2.0 {
		t.Errorf("second migrate applied %v up to version %v, want 0 up to 2", rec["applied"], rec["version"])
	}
}

func TestConcurrentMigrationsAllSucceed(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	const replicas = 5
	results := make([]result, replicas)
	var wg sync.WaitGroup
	for i := range replicas {
		wg.Go(func() { results[i] = migrate(url, fixtures) })
	}
	wg.Wait()

	applied := 0.0
	for i, r := range results {
		if r.code != 0 {
			t.Fatalf("replica %d: exit code = %d; stderr:\n%s", i, r.code, r.stderr)
		}
		applied += r.logRecord(t, "migrations applied")["applied"].(float64)
	}
	if applied != 2 {
		t.Errorf("replicas applied %v migrations between them, want each of the 2 exactly once", applied)
	}
}

func TestMigrateWithTheEmbeddedSet(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	for range 2 {
		if r := migrate(url); r.code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr:\n%s", r.code, r.stderr)
		}
	}
}

func TestMigrateRefusesAnUnsupportedPostgreSQL(t *testing.T) {
	pg15 := cli.WithServerVersionNum(func(context.Context, *pgxpool.Pool) (int, error) { return 150004, nil })
	r := migrate(testkit.EmptyDatabase(t), fixtures, pg15)
	if r.code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr:\n%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "PostgreSQL 15.4") || !strings.Contains(r.stderr, "PostgreSQL 16") {
		t.Errorf("stderr does not name the server's version and the minimum:\n%s", r.stderr)
	}
}

func TestMigrateWithoutADatabaseIsAConfigurationError(t *testing.T) {
	r := run(map[string]string{}, nil, "migrate")
	if r.code != 2 {
		t.Fatalf("exit code = %d, want 2", r.code)
	}
	if !strings.Contains(r.stderr, "DATABASE_URL") {
		t.Errorf("stderr does not name DATABASE_URL:\n%s", r.stderr)
	}
}

func TestMigrateWithAMalformedDatabaseURLIsAConfigurationError(t *testing.T) {
	if r := migrate("postgres://flagnetic@localhost:5432/flagnetic?connect_timeout=never"); r.code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr:\n%s", r.code, r.stderr)
	}
}

func TestMigrateAgainstAnUnreachableDatabaseFails(t *testing.T) {
	r := migrate("postgres://flagnetic@127.0.0.1:1/flagnetic?connect_timeout=2")
	if r.code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr:\n%s", r.code, r.stderr)
	}
}

func TestVersionPrintsTheVersion(t *testing.T) {
	r := run(nil, nil, "version")
	if r.code != 0 || strings.TrimSpace(r.stdout) == "" {
		t.Errorf("exit code = %d, stdout = %q; want 0 and a version", r.code, r.stdout)
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	r := run(nil, nil, "help")
	if r.code != 0 || !strings.Contains(r.stdout, "Usage: flagnetic") {
		t.Errorf("exit code = %d, stdout = %q; want 0 and the usage", r.code, r.stdout)
	}
}

func TestUnknownCommandPrintsUsage(t *testing.T) {
	for _, args := range [][]string{{"nope"}, {}} {
		r := run(nil, nil, args...)
		if r.code != 2 {
			t.Errorf("%v: exit code = %d, want 2", args, r.code)
		}
		if !strings.Contains(r.stderr, "Usage: flagnetic") {
			t.Errorf("%v: stderr = %q, want the usage", args, r.stderr)
		}
	}
}
