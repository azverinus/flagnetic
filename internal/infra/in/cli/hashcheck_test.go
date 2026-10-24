package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/infra/in/cli"
)

// hashCheckOutput is the JSON the command prints: the management API's
// answer to the same check.
type hashCheckOutput struct {
	Salt         string  `json:"salt"`
	SecondSalt   *string `json:"second_salt"`
	Bins         int     `json:"bins"`
	Counts       []int   `json:"counts"`
	Min          int     `json:"min"`
	Max          int     `json:"max"`
	ChiSquare    float64 `json:"chi_square"`
	DF           int     `json:"df"`
	PValue       float64 `json:"p_value"`
	Verdict      string  `json:"verdict"`
	Independence *struct {
		ChiSquare float64 `json:"chi_square"`
		DF        int     `json:"df"`
		PValue    float64 `json:"p_value"`
		Verdict   string  `json:"verdict"`
	} `json:"independence"`
}

func lines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("device-%d", i)
	}
	return out
}

// hashCheckWith runs the command with stdin as its standard input. It needs
// no database and no configuration: nothing in env is set.
func hashCheckWith(stdin string, args ...string) result {
	return run(map[string]string{}, []cli.Option{cli.WithStdin(strings.NewReader(stdin))}, append([]string{"hash-check"}, args...)...)
}

func decodeHashCheck(t *testing.T, r result) hashCheckOutput {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("hash-check exited %d: %s", r.code, r.stderr)
	}
	var out hashCheckOutput
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, r.stdout)
	}
	return out
}

func TestHashCheckReadsStandardInputOrAFile(t *testing.T) {
	ids := lines(5000)
	second := "0f1e2d3c4b5a6978"
	want, err := experiments.CheckUniformity(experiments.UniformityRequest{UnitIDs: ids, Salt: "a1b2c3d4e5f60718", SecondSalt: &second, Bins: 20})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "ids.txt")
	// CRLF line breaks and no break after the last line read as LF ones.
	if err := os.WriteFile(file, []byte(strings.Join(ids, "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]result{
		"standard input": hashCheckWith(strings.Join(ids, "\n")+"\n", "--salt", "a1b2c3d4e5f60718", "--second-salt", second, "--bins", "20"),
		"a dash":         hashCheckWith(strings.Join(ids, "\n"), "--salt", "a1b2c3d4e5f60718", "--second-salt", second, "--bins", "20", "-"),
		"a file":         hashCheckWith("", "--salt", "a1b2c3d4e5f60718", "--second-salt", second, "--bins", "20", file),
	} {
		got := decodeHashCheck(t, r)
		if got.Salt != want.Salt || got.SecondSalt == nil || *got.SecondSalt != second || got.Bins != 20 || !slices.Equal(got.Counts, want.Counts) ||
			got.Min != want.Min || got.Max != want.Max || got.ChiSquare != want.ChiSquare || got.DF != want.DF || got.PValue != want.PValue ||
			got.Verdict != string(want.Verdict) || got.Independence == nil || got.Independence.ChiSquare != want.Independence.ChiSquare ||
			got.Independence.Verdict != string(want.Independence.Verdict) {
			t.Errorf("%s: printed %s", name, r.stdout)
		}
	}
}

func TestHashCheckDrawsASaltAndDefaultsTo100Bins(t *testing.T) {
	got := decodeHashCheck(t, hashCheckWith(strings.Join(lines(1000), "\n")))
	if !experiments.ValidSalt(got.Salt) || got.Bins != 100 || len(got.Counts) != 100 || got.Independence != nil || got.SecondSalt != nil {
		t.Errorf("printed %+v", got)
	}
}

func TestHashCheckRefusesWhatItCannotCheck(t *testing.T) {
	bad := lines(1000)
	bad[4] = ""
	for _, c := range []struct {
		name, stdin string
		args        []string
		code        int
		want        string
	}{
		{"too few", strings.Join(lines(999), "\n"), nil, 1, "1,000 to 200,000 unit IDs"},
		{"an empty line", strings.Join(bad, "\n"), nil, 1, "line 5: a unit ID has 1 to 256 bytes"},
		{"one bin", strings.Join(lines(1000), "\n"), []string{"--bins", "1"}, 1, "bins"},
		{"an empty salt", strings.Join(lines(1000), "\n"), []string{"--salt", ""}, 1, "--salt: a salt has 1 to 256 bytes"},
		{"two files", "", []string{"a", "b"}, 2, "usage: flagnetic hash-check"},
		{"an unknown flag", "", []string{"--colour"}, 2, "-colour"},
		{"a missing file", "", []string{filepath.Join(t.TempDir(), "absent")}, 1, "absent"},
	} {
		r := hashCheckWith(c.stdin, c.args...)
		if r.code != c.code || !strings.Contains(r.stderr, c.want) || r.stdout != "" {
			t.Errorf("%s: exited %d, stderr %q, stdout %q; want %d and %q", c.name, r.code, r.stderr, r.stdout, c.code, c.want)
		}
	}
}

func TestHashCheckIsListedInTheUsage(t *testing.T) {
	if r := run(nil, nil, "help"); !strings.Contains(r.stdout, "hash-check") {
		t.Errorf("help = %s", r.stdout)
	}
}
