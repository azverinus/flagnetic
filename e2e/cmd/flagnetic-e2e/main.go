// Command flagnetic-e2e brings up two seeded replicas of the real flagnetic
// binary for an SDK's end-to-end smoke, in any language, and performs the
// management actions the smoke needs against them. e2e/README.md is the
// contract an SDK follows.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/azverinus/flagnetic/e2e"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

const usage = `Usage: flagnetic-e2e <command>

Commands:
  up                     build flagnetic, migrate, start two replicas on one
                         PostgreSQL, bootstrap an admin, seed the project,
                         write the seed and keep serving until interrupted
  seed                   seed the running instance again and print the seed
  kill <flag>            switch a flag off through replica A; prints the
                         commit time
  restore                write the seed's flags and segment back
  revoke <server|client> revoke the seed's key of that kind
  inventory              print the environment's SDK inventory

Environment:
  FLAGNETIC_E2E_SEED     up writes the seed JSON here; printed when unset
  FLAGNETIC_E2E_STATE    the state up writes and the other commands read
                         (default: the system temporary directory)
  FLAGNETIC_E2E_BINARY   a flagnetic binary built already; up builds one
                         from the repository when unset
  FLAGNETIC_E2E_LOGS     where up writes the replicas' logs (default: next
                         to the state)
  DATABASE_URL           the PostgreSQL to use; up starts a throwaway one in
                         Docker when unset

Exit status: 0 success, 1 a failure, 2 a usage error.
`

// upTimeout bounds up from its start until the seed is written: an SDK's CI
// job fails early rather than waiting on an instance that will not come.
const upTimeout = 120 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	statePath := os.Getenv("FLAGNETIC_E2E_STATE")
	if statePath == "" {
		statePath = e2e.DefaultStatePath()
	}
	command, rest := args[0], args[1:]
	want := map[string]int{"up": 0, "seed": 0, "kill": 1, "restore": 0, "revoke": 1, "inventory": 0}
	n, ok := want[command]
	switch {
	case command == "help" || command == "-h" || command == "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case !ok:
		fmt.Fprintf(stderr, "flagnetic-e2e: unknown command %q\n\n%s", command, usage)
		return 2
	case len(rest) != n:
		fmt.Fprintf(stderr, "flagnetic-e2e %s: takes %d argument(s)\n\n%s", command, n, usage)
		return 2
	}
	if command == "up" {
		return up(ctx, statePath, stdout, stderr)
	}
	state, err := e2e.ReadState(statePath)
	if err != nil {
		return fail(stderr, command, err)
	}
	switch command {
	case "seed":
		state, err = e2e.SeedInstance(ctx, state.Seed.Replicas, state.Keys)
		if err == nil {
			err = e2e.WriteFile(statePath, state)
		}
		return answer(stdout, stderr, command, state.Seed, err)
	case "kill":
		killed, err := e2e.Kill(ctx, state, rest[0])
		return answer(stdout, stderr, command, killed, err)
	case "restore":
		return answer(stdout, stderr, command, nil, e2e.RestoreSeed(ctx, state))
	case "revoke":
		kind := management.SdkKeyKind(rest[0])
		if !kind.Valid() {
			fmt.Fprintf(stderr, "flagnetic-e2e revoke: the kind is server or client, not %q\n", rest[0])
			return 2
		}
		return answer(stdout, stderr, command, nil, e2e.Revoke(ctx, state, kind))
	default:
		items, err := e2e.Inventory(ctx, state)
		return answer(stdout, stderr, command, items, err)
	}
}

func up(ctx context.Context, statePath string, stdout, stderr io.Writer) int {
	logPath := os.Getenv("FLAGNETIC_E2E_LOGS")
	if logPath == "" {
		logPath = filepath.Join(filepath.Dir(statePath), "replicas.log")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil { //nolint:gosec // the operator names the log file
		return fail(stderr, "up", err)
	}
	logs, err := os.Create(logPath) //nolint:gosec // the operator names the log file
	if err != nil {
		return fail(stderr, "up", err)
	}
	defer func() { _ = logs.Close() }()
	fmt.Fprintf(stderr, "flagnetic-e2e up: the replicas log to %s\n", logPath)

	opts := e2e.UpOptions{Binary: os.Getenv("FLAGNETIC_E2E_BINARY"), DatabaseURL: os.Getenv("DATABASE_URL"), Logs: logs}
	if opts.Binary == "" {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return fail(stderr, "up", wdErr)
		}
		if opts.Root, err = e2e.RepositoryRoot(wd); err != nil {
			return fail(stderr, "up", err)
		}
		if opts.WorkDir, err = os.MkdirTemp("", "flagnetic-e2e-*"); err != nil {
			return fail(stderr, "up", err)
		}
		defer func() { _ = os.RemoveAll(opts.WorkDir) }()
	}
	upCtx, cancel := context.WithTimeout(ctx, upTimeout)
	inst, err := e2e.Up(upCtx, opts)
	cancel()
	if err != nil {
		return fail(stderr, "up", err)
	}
	defer func() {
		if err := inst.Stop(); err != nil {
			fmt.Fprintf(stderr, "flagnetic-e2e up: stopping: %v\n", err)
		}
	}()
	if err := e2e.WriteFile(statePath, inst.State); err != nil {
		return fail(stderr, "up", err)
	}
	if seedPath := os.Getenv("FLAGNETIC_E2E_SEED"); seedPath != "" {
		if err := e2e.WriteFile(seedPath, inst.State.Seed); err != nil {
			return fail(stderr, "up", err)
		}
		fmt.Fprintf(stderr, "flagnetic-e2e up: the seed is in %s\n", seedPath)
	} else if err := json.NewEncoder(stdout).Encode(inst.State.Seed); err != nil { //nolint:gosec // the seed's SDK keys are a test instance's, printed for the smoke that asked
		return fail(stderr, "up", err)
	}
	fmt.Fprintf(stderr, "flagnetic-e2e up: replica A at %s, replica B at %s; serving until interrupted\n", inst.A.URL, inst.B.URL)

	select {
	case <-ctx.Done():
		return 0
	case <-inst.A.Done():
	case <-inst.B.Done():
	}
	return fail(stderr, "up", errors.New("a replica exited; its log is in "+logPath))
}

// answer prints v as one JSON line, or the error.
func answer(stdout, stderr io.Writer, command string, v any, err error) int {
	if err != nil {
		return fail(stderr, command, err)
	}
	if v == nil {
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(v); err != nil {
		return fail(stderr, command, err)
	}
	return 0
}

func fail(stderr io.Writer, command string, err error) int {
	fmt.Fprintf(stderr, "flagnetic-e2e %s: %v\n", command, err)
	return 1
}
