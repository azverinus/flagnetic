package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/azverinus/flagnetic/e2e"
)

// TestTheCommandServesAnSDKInAnyLanguage drives flagnetic-e2e as a smoke in
// another language does: `up` in the background, the seed file it writes,
// and a control command against the running instance. It runs on a
// database of its own, since `up` seeds new keys and revokes the others.
func TestTheCommandServesAnSDKInAnyLanguage(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "flagnetic-e2e")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", command, "./cmd/flagnetic-e2e")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build flagnetic-e2e: %v\n%s", err, out)
	}
	seedPath, statePath := filepath.Join(dir, "seed.json"), filepath.Join(dir, "state.json")
	env := append(os.Environ(),
		"FLAGNETIC_E2E_SEED="+seedPath,
		"FLAGNETIC_E2E_STATE="+statePath,
		"FLAGNETIC_E2E_BINARY="+binary,
		"FLAGNETIC_E2E_LOGS="+filepath.Join(dir, "replicas.log"),
		"DATABASE_URL="+separateDatabase(t),
	)

	up := exec.CommandContext(context.WithoutCancel(t.Context()), command, "up") //nolint:gosec // the command this test built
	up.Env = env
	var upErr syncBuffer
	up.Stderr = &upErr
	// The replicas up started outlive a killed up and hold its output open.
	up.WaitDelay = 10 * time.Second
	if err := up.Start(); err != nil {
		t.Fatal(err)
	}
	// exited is closed once up has ended, with its error in upExit.
	exited := make(chan struct{})
	var upExit error
	go func() { upExit = up.Wait(); close(exited) }()
	t.Cleanup(func() {
		select {
		case <-exited:
			return
		default:
		}
		_ = up.Process.Signal(syscall.SIGINT)
		select {
		case <-exited:
		case <-time.After(60 * time.Second):
			_ = up.Process.Kill()
			<-exited
		}
	})

	var s e2e.Seed
	deadline := time.Now().Add(120 * time.Second)
	for {
		if b, err := os.ReadFile(seedPath); err == nil && json.Unmarshal(b, &s) == nil {
			break
		}
		select {
		case <-exited:
			t.Fatalf("up exited before writing the seed: %v\n%s", upExit, upErr.String())
		case <-time.After(200 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("up wrote no seed within 120 s\n%s", upErr.String())
		}
	}
	if s.Replicas.A == "" || s.Replicas.A == s.Replicas.B || s.ServerKey == "" || s.ClientKey == "" ||
		s.Project != e2e.ProjectKey || s.Environment != e2e.EnvironmentKey || len(s.Cases) == 0 {
		t.Fatalf("seed = %+v", s)
	}
	for _, c := range s.Cases {
		if c.Reason == "" || c.ClientReason == "" || len(c.Context.Units) == 0 || len(c.Value) == 0 || c.Variation == "" {
			t.Errorf("case %q is incomplete: %+v", c.Name, c)
		}
	}
	for _, replica := range []string{s.Replicas.A, s.Replicas.B} {
		if res := sdkGet(t, replica+"/sdk/v1/ruleset", s.ServerKey); res.StatusCode != http.StatusOK {
			t.Errorf("ruleset on %s = %d", replica, res.StatusCode)
		}
	}

	control := func(args ...string) string {
		cmd := exec.CommandContext(t.Context(), command, args...) //nolint:gosec // the command this test built
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("flagnetic-e2e %s: %v", strings.Join(args, " "), err)
		}
		return string(out)
	}
	var first, second e2e.Killed
	if err := json.Unmarshal([]byte(control("kill", e2e.FlagOn)), &first); err != nil || !first.Changed || first.CommittedAt.IsZero() {
		t.Fatalf("kill = %+v, %v; want changed with its commit time", first, err)
	}
	if err := json.Unmarshal([]byte(control("kill", e2e.FlagOn)), &second); err != nil || second.Changed {
		t.Errorf("kill again = %+v, %v; want unchanged", second, err)
	}
	control("restore")

	if err := up.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
		if upExit != nil {
			t.Errorf("up ended with %v\n%s", upExit, upErr.String())
		}
	case <-time.After(60 * time.Second):
		t.Error("up did not stop within 60 s of an interrupt")
	}
}

// separateDatabase returns an empty database of its own on the server of
// DATABASE_URL, dropped when the test ends, or "" so that `up` starts a
// PostgreSQL of its own.
func separateDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		return ""
	}
	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	const name = "flagnetic_e2e_command"
	for _, stmt := range []string{"DROP DATABASE IF EXISTS " + name + " WITH (FORCE)", "CREATE DATABASE " + name} {
		if _, execErr := conn.Exec(t.Context(), stmt); execErr != nil {
			t.Fatal(execErr)
		}
	}
	t.Cleanup(func() {
		c, connErr := pgx.Connect(context.Background(), base)
		if connErr != nil {
			return
		}
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = c.Close(context.Background())
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
