// Package e2e runs the real flagnetic binary for end-to-end tests: it builds
// it, applies the migrations once, starts two replicas on one PostgreSQL,
// bootstraps an instance admin and seeds a project through the management
// API. The Go SDK's run is this package's own test; an SDK in any other
// language drives the same harness through the flagnetic-e2e command, as
// README.md describes.
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Admin is the instance admin the harness bootstraps and signs in as. The
// password is a test credential of this harness, never a real one.
var Admin = struct{ Email, Name, Password string }{
	Email:    "e2e-admin@flagnetic.test",
	Name:     "E2E Harness",
	Password: "harness-quartz-meadow-4471",
}

// secretKey is the FLAGNETIC_SECRET_KEY of the replicas the harness starts
// when the environment sets none: a test instance's key, shared by both
// replicas as a deployment's would be.
const secretKey = "flagnetic-e2e-insecure-secret-key-0123456789" //nolint:gosec // a test instance's key

// Build compiles the flagnetic binary of the repository at root into dir
// and returns its path. The console is embedded as far as it was built;
// the SDK API needs none of it.
func Build(ctx context.Context, root, dir string) (string, error) {
	binary := filepath.Join(dir, "flagnetic")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/flagnetic") //nolint:gosec // the output path is the harness's own work directory
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/flagnetic: %w\n%s", err, out)
	}
	return binary, nil
}

// RepositoryRoot returns the nearest directory at or above dir that holds
// the binary's sources.
func RepositoryRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "flagnetic", "main.go")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no flagnetic repository at or above the working directory")
		}
		dir = parent
	}
}

// environment is what every flagnetic command of the harness runs with.
func environment(databaseURL string, extra ...string) []string {
	env := append(os.Environ(), "DATABASE_URL="+databaseURL)
	if os.Getenv("FLAGNETIC_SECRET_KEY") == "" {
		env = append(env, "FLAGNETIC_SECRET_KEY="+secretKey)
	}
	return append(env, extra...)
}

// Migrate applies the migrations once, before any replica starts, as a
// deployment running `flagnetic migrate` as a job does.
func Migrate(ctx context.Context, binary, databaseURL string, logs io.Writer) error {
	cmd := exec.CommandContext(ctx, binary, "migrate") //nolint:gosec // the binary the harness built
	cmd.Env = environment(databaseURL)
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("flagnetic migrate: %w", err)
	}
	return nil
}

// BootstrapAdmin makes Admin the first instance admin of the database, or
// does nothing when the database has one, so a rerun on the same database
// succeeds. The token the bootstrap prints is not kept: the harness signs
// in with the password, which works on every run.
func BootstrapAdmin(ctx context.Context, binary, databaseURL string, logs io.Writer) error {
	cmd := exec.CommandContext(ctx, binary, "admin", "bootstrap", //nolint:gosec // the binary the harness built
		"--email", Admin.Email, "--name", Admin.Name, "--if-absent")
	cmd.Env = environment(databaseURL, "FLAGNETIC_ADMIN_PASSWORD="+Admin.Password)
	cmd.Stdout, cmd.Stderr = io.Discard, logs
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("flagnetic admin bootstrap: %w", err)
	}
	return nil
}

// Replica is one `flagnetic serve` the harness started.
type Replica struct {
	// URL is the replica's base URL, without a trailing slash.
	URL string

	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// StartReplica runs `flagnetic serve` on a free loopback port, without
// applying migrations and without a metrics listener, whose fixed port
// would clash with the other replica, and waits until it answers /readyz.
func StartReplica(ctx context.Context, binary, databaseURL string, logs io.Writer) (*Replica, error) {
	// ctx bounds the start only; Stop ends the replica.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), binary, "serve") //nolint:gosec // the binary the harness built
	cmd.Env = environment(databaseURL,
		"FLAGNETIC_LISTEN_ADDR=127.0.0.1:0",
		"FLAGNETIC_METRICS_LISTEN_ADDR=",
		"FLAGNETIC_AUTO_MIGRATE=false",
		// The address is read from the JSON "listening" line.
		"FLAGNETIC_LOG_FORMAT=json",
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = logs
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start flagnetic serve: %w", err)
	}
	r := &Replica{cmd: cmd, done: make(chan struct{})}
	addr := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			_, _ = fmt.Fprintln(logs, sc.Text())
			var line struct{ Msg, Addr string }
			if json.Unmarshal(sc.Bytes(), &line) == nil && line.Msg == "listening" && line.Addr != "" {
				select {
				case addr <- line.Addr:
				default:
				}
			}
		}
		_, _ = io.Copy(logs, stderr)
		r.err = cmd.Wait()
		close(r.done)
	}()

	select {
	case a := <-addr:
		r.URL = "http://" + a
	case <-r.done:
		return nil, fmt.Errorf("flagnetic serve exited before listening: %w", r.err)
	case <-ctx.Done():
		_ = r.Stop()
		return nil, ctx.Err()
	}
	if err := r.awaitReady(ctx); err != nil {
		_ = r.Stop()
		return nil, err
	}
	return r, nil
}

func (r *Replica) awaitReady(ctx context.Context) error {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL+"/readyz", nil)
		if err != nil {
			return err
		}
		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s/readyz: %w", r.URL, ctx.Err())
		case <-r.done:
			return fmt.Errorf("flagnetic serve exited before it was ready: %w", r.err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Stop ends the replica as SIGTERM does in a deployment and waits for it;
// after 30 seconds it is killed.
func (r *Replica) Stop() error {
	_ = r.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-r.done:
	case <-time.After(30 * time.Second):
		_ = r.cmd.Process.Kill()
		<-r.done
		return errors.New("flagnetic serve did not stop within 30 s of SIGTERM")
	}
	if r.err != nil {
		return fmt.Errorf("flagnetic serve: %w", r.err)
	}
	return nil
}

// Done is closed once the replica has exited.
func (r *Replica) Done() <-chan struct{} { return r.done }
