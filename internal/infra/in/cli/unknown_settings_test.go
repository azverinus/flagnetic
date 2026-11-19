package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/cli"
)

func TestAMistypedVariableIsWarnedAboutNotRefused(t *testing.T) {
	environ := func() []string {
		return []string{"PATH=/bin", "FLAGNETIC_LOG_LEVL=debug", "FLAGNETIC_LOG_LEVEL=info", "FLAGNETIC_ADMIN_PASSWORD=x"}
	}
	r := run(map[string]string{"DATABASE_URL": "postgres://localhost:1/flagnetic?connect_timeout=1"},
		[]cli.Option{cli.WithEnvironment(environ)}, "healthcheck")

	rec := r.logRecord(t, "ignored environment variable: not a setting")
	if rec["level"] != "WARN" || rec["variable"] != "FLAGNETIC_LOG_LEVL" {
		t.Errorf("record = %v, want a warning naming FLAGNETIC_LOG_LEVL", rec)
	}
	for _, rec := range r.logRecords(t) {
		if rec["variable"] == "FLAGNETIC_ADMIN_PASSWORD" || rec["variable"] == "FLAGNETIC_LOG_LEVEL" {
			t.Errorf("warned about %v, which the command line or the replica reads", rec["variable"])
		}
	}
	if r.code == 2 {
		t.Errorf("exit code 2: the warning refused the configuration:\n%s", r.stderr)
	}
}

func TestAFileKeyThisVersionDoesNotKnowIsWarnedAbout(t *testing.T) {
	file := filepath.Join(t.TempDir(), "flagnetic.yaml")
	if err := os.WriteFile(file, []byte("log_level: info\nbase_ur: https://flags.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run(map[string]string{
		"DATABASE_URL":          "postgres://localhost:1/flagnetic?connect_timeout=1",
		"FLAGNETIC_CONFIG_FILE": file,
	}, nil, "healthcheck")

	rec := r.logRecord(t, "ignored configuration file key: not a setting of this version")
	if rec["level"] != "WARN" || rec["key"] != "base_ur" || rec["file"] != file {
		t.Errorf("record = %v, want a warning naming base_ur and the file", rec)
	}
	if r.code == 2 {
		t.Errorf("exit code 2: the unknown key refused the configuration:\n%s", r.stderr)
	}
}
