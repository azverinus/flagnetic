package cli_test

import (
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestServeRequiresASecretKey(t *testing.T) {
	r := run(map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_SECRET_KEY": ""}, nil, "serve")
	if r.code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr:\n%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "FLAGNETIC_SECRET_KEY") {
		t.Errorf("stderr does not name FLAGNETIC_SECRET_KEY:\n%s", r.stderr)
	}
}

func TestServeRefusesASecretKeyShorterThan32Bytes(t *testing.T) {
	r := run(map[string]string{"DATABASE_URL": testkit.EmptyDatabase(t), "FLAGNETIC_SECRET_KEY": "0123456789abcdef0123456789abcde"}, nil, "serve")
	if r.code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr:\n%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "FLAGNETIC_SECRET_KEY") || !strings.Contains(r.stderr, "32 bytes") {
		t.Errorf("stderr does not name FLAGNETIC_SECRET_KEY and its minimum:\n%s", r.stderr)
	}
}

func TestServeWritesTheCanaryAndThenRefusesAChangedSecretKey(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	first := serve(t, map[string]string{"DATABASE_URL": url})
	if code, _ := first.stop(); code != 0 {
		t.Fatalf("first start exited %d:\n%s", code, first.stderr)
	}

	for _, autoMigrate := range []string{"true", "false"} {
		r := run(map[string]string{
			"DATABASE_URL": url, "FLAGNETIC_SECRET_KEY": "fedcba9876543210fedcba9876543210",
			"FLAGNETIC_AUTO_MIGRATE": autoMigrate, "FLAGNETIC_LISTEN_ADDR": "127.0.0.1:0",
		}, nil, "serve")
		if r.code != 1 {
			t.Errorf("auto-migrate %s: another key: exit code = %d, want 1; stderr:\n%s", autoMigrate, r.code, r.stderr)
		}
		if rec := r.logRecord(t, "start failed"); !strings.Contains(rec["error"].(string), "FLAGNETIC_SECRET_KEY") {
			t.Errorf("auto-migrate %s: the refusal does not name FLAGNETIC_SECRET_KEY: %v", autoMigrate, rec)
		}
	}

	again := serve(t, map[string]string{"DATABASE_URL": url})
	if code, _ := again.stop(); code != 0 {
		t.Errorf("the original key no longer starts the instance: exit %d:\n%s", code, again.stderr)
	}
}
