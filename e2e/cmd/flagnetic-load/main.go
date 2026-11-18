// Command flagnetic-load measures the SDK API's latency under a fixed
// arrival rate against the instance a running `flagnetic-e2e up` brought
// up, and fails when a scenario's p99 is above its ceiling. e2e/README.md
// describes the scenarios and how CI runs them.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/azverinus/flagnetic/e2e"
	"github.com/azverinus/flagnetic/e2e/load"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("flagnetic-load", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage: flagnetic-load [flags]

Seeds the project of the running flagnetic-e2e up with load flags, runs the
scenarios (%s) and prints the results as JSON. Exit status: 0 every scenario
is within its ceiling, 1 a ceiling is broken or the run failed, 2 a usage
error.

Environment:
  FLAGNETIC_E2E_STATE    the state flagnetic-e2e up wrote (default: the
                         system temporary directory)

Flags:
`, strings.Join(load.Names, ", "))
		fs.PrintDefaults()
	}
	profilePath := fs.String("profile", "", "a profile file instead of the checked-in e2e/load/profile.json")
	only := fs.String("scenario", "", "the scenarios to run, comma separated (default: all)")
	flags := fs.Int("flags", e2e.LoadFlags, "the number of flags to seed and evaluate")
	summary := fs.String("summary", os.Getenv("GITHUB_STEP_SUMMARY"), "append the results as a Markdown table to this file")
	report := fs.Bool("report-only", false, "print the results and exit 0 whatever they are, to measure a ceiling")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	var names []string
	for _, n := range strings.Split(*only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	for _, n := range names {
		if !contains(load.Names, n) {
			fmt.Fprintf(stderr, "flagnetic-load: unknown scenario %q; the scenarios are %s\n", n, strings.Join(load.Names, ", "))
			return 2
		}
	}
	profile, err := load.LoadProfile(*profilePath)
	if err != nil {
		fmt.Fprintf(stderr, "flagnetic-load: %v\n", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	statePath := os.Getenv("FLAGNETIC_E2E_STATE")
	if statePath == "" {
		statePath = e2e.DefaultStatePath()
	}
	state, err := e2e.ReadState(statePath)
	if err != nil {
		fmt.Fprintf(stderr, "flagnetic-load: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "flagnetic-load: seeding %d flags\n", *flags)
	if err := e2e.SeedLoad(ctx, state.Seed.Replicas, *flags); err != nil {
		fmt.Fprintf(stderr, "flagnetic-load: seed: %v\n", err)
		return 1
	}
	results := load.Measure(ctx, load.NewTarget(state.Seed, *flags), profile, names)
	if err := json.NewEncoder(stdout).Encode(results); err != nil {
		fmt.Fprintf(stderr, "flagnetic-load: %v\n", err)
		return 1
	}
	table := profile.Markdown(results)
	fmt.Fprint(stderr, table)
	if *summary != "" {
		if err := appendTo(*summary, "### SDK API load\n\n"+table+"\n"); err != nil {
			fmt.Fprintf(stderr, "flagnetic-load: summary: %v\n", err)
		}
	}
	broken := profile.Check(results)
	for _, b := range broken {
		fmt.Fprintf(stderr, "flagnetic-load: %s\n", b)
	}
	if len(broken) > 0 && !*report {
		return 1
	}
	return 0
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func appendTo(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the job names its summary file
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
