package cli_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/testkit"
)

// The test clock belongs to the end-to-end harness alone: serve refuses
// FLAGNETIC_TEST_CLOCK_FILE unless the harness's marker is set and it
// listens on loopback only, so no deployment moves its clock by accident.
func TestServeRefusesTheTestClockOutsideTheHarness(t *testing.T) {
	file := filepath.Join(t.TempDir(), "clock-offset")
	for name, overrides := range map[string]map[string]string{
		"without the marker":        {},
		"with another marker":       {"FLAGNETIC_TEST_HARNESS": "true"},
		"listening beyond loopback": {"FLAGNETIC_TEST_HARNESS": "playwright", "FLAGNETIC_LISTEN_ADDR": ":8080"},
		"listening on a public IP":  {"FLAGNETIC_TEST_HARNESS": "playwright", "FLAGNETIC_LISTEN_ADDR": "192.0.2.1:8080"},
	} {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{
				"DATABASE_URL":              "postgres://unused@localhost/unused",
				"FLAGNETIC_TEST_CLOCK_FILE": file,
				"FLAGNETIC_LISTEN_ADDR":     "127.0.0.1:0",
			}
			for k, v := range overrides {
				env[k] = v
			}
			r := run(env, nil, "serve")
			if r.code != 2 || !strings.Contains(r.stderr, "FLAGNETIC_TEST_CLOCK_FILE") {
				t.Fatalf("exit %d, stderr %q; want exit 2 naming FLAGNETIC_TEST_CLOCK_FILE", r.code, r.stderr)
			}
		})
	}
}

// Under the harness's marker and on loopback, serve starts with the test
// clock, saying how far it is moved.
func TestServeStartsWithTheTestClockUnderTheHarness(t *testing.T) {
	file := filepath.Join(t.TempDir(), "clock-offset")
	if err := os.WriteFile(file, []byte("25h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := serve(t, map[string]string{
		"DATABASE_URL":              testkit.EmptyDatabase(t),
		"FLAGNETIC_AUTO_MIGRATE":    "true",
		"FLAGNETIC_TEST_CLOCK_FILE": file,
		"FLAGNETIC_TEST_HARNESS":    "playwright",
	})
	if status, body := getStatus(t, r.baseURL+"/readyz"); status != http.StatusOK {
		t.Fatalf("readyz = %d %s, want 200", status, body)
	}
	rec := r.findLog("the clock is moved by a test-only offset file")
	if rec == nil || rec["offset"] != "25h0m0s" {
		t.Errorf("log record = %v, want the warning naming the 25h offset", rec)
	}
}
