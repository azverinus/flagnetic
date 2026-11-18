// Command flagnetic-upgrade judges a rolling upgrade: it seeds a running
// instance, holds a Go SDK on the change stream and sends light load while a
// command the caller names replaces the replicas, and fails when a request or
// an evaluation failed meanwhile or the SDK did not receive a change made
// afterwards. .github/scripts/upgrade.sh runs it against kind.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/azverinus/flagnetic/e2e"
	"github.com/azverinus/flagnetic/e2e/upgrade"
)

const usage = `Usage:
  flagnetic-upgrade admin
      print the harness's instance admin as JSON (email, name, password), for
      the command that bootstraps it
  flagnetic-upgrade run [flags] -- <command> [args...]
      seed the instance at --url, start the SDK and the load, run the command
      under them, then change a flag and wait for the SDK to see it

Exit status: 0 the upgrade passed, 1 it failed, 2 a usage error.

Flags of run:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "admin":
		_ = json.NewEncoder(stdout).Encode(map[string]string{"email": e2e.Admin.Email, "name": e2e.Admin.Name, "password": e2e.Admin.Password})
		return 0
	case "run":
		return runUpgrade(args[1:], stdout, stderr)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}

func runUpgrade(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("flagnetic-upgrade run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "", "the base URL the replicas are reached at, such as a NodePort (required)")
	rate := fs.Float64("rate", 20, "requests per second of each of the two scenarios")
	settle := fs.Duration("settle", 20*time.Second, "how long the load goes on after the command returned")
	within := fs.Duration("change-within", 30*time.Second, "how long to wait for the SDK to see the change")
	summary := fs.String("summary", os.Getenv("GITHUB_STEP_SUMMARY"), "append the report as a Markdown table to this file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	command := fs.Args()
	if *url == "" || len(command) == 0 || *rate <= 0 {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	rep, err := upgrade.Run(ctx, upgrade.Options{
		URL: *url, Rate: *rate, Settle: *settle, ChangeWithin: *within,
		Upgrade: func(ctx context.Context) error {
			cmd := exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // the command is the caller's own
			cmd.Stdout, cmd.Stderr = stderr, stderr
			return cmd.Run()
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "flagnetic-upgrade: %v\n", err)
		return 1
	}
	table := rep.Markdown()
	fmt.Fprint(stderr, table)
	failures := rep.Verdict()
	_ = json.NewEncoder(stdout).Encode(map[string]any{"report": rep, "failures": failures})
	if *summary != "" {
		text := "### Rolling upgrade\n\n" + table
		for _, f := range failures {
			text += "\n- " + f
		}
		if f, err := os.OpenFile(*summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil { //nolint:gosec // the job names its summary file
			_, _ = f.WriteString(text + "\n")
			_ = f.Close()
		}
	}
	for _, f := range failures {
		fmt.Fprintf(stderr, "flagnetic-upgrade: %s\n", f)
	}
	if len(failures) > 0 {
		return 1
	}
	return 0
}
