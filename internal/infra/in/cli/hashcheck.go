package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

const hashCheckUsage = "usage: flagnetic hash-check [--salt S] [--second-salt S] [--bins N] [FILE]"

// maxUnitIDLine bounds a line read as a unit ID. A unit ID has at most 256
// bytes, so a longer line is refused by the check anyway; a line past this
// bound is not worth holding in memory to say so.
const maxUnitIDLine = 1 << 20

// hashCheck runs the uniformity check offline on the unit IDs of FILE, or
// of standard input without one or with "-", one per line, and prints what
// the management API answers for them. It needs neither a database nor a
// configuration, so unit IDs never have to leave the machine.
func hashCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hash-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var salt, secondSalt optionalString
	fs.Var(&salt, "salt", "the salt to hash with, 1 to 256 bytes; a random one when omitted")
	fs.Var(&secondSalt, "second-salt", "a second salt, to test both salts' buckets for independence")
	bins := fs.Int("bins", experiments.DefaultUniformityBins, "how many bins the 10000 buckets are grouped into, 2 to 10000")
	fs.Usage = func() {
		fmt.Fprintln(stderr, hashCheckUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, hashCheckUsage)
		return exitUsage
	}
	source, name := stdin, "standard input"
	if path := fs.Arg(0); path != "" && path != "-" {
		f, err := os.Open(path) //nolint:gosec // the file the operator names
		if err != nil {
			fmt.Fprintf(stderr, "flagnetic hash-check: %v\n", err)
			return exitFailure
		}
		defer func() { _ = f.Close() }()
		source, name = f, path
	}
	ids, err := readUnitIDs(source)
	if err != nil {
		fmt.Fprintf(stderr, "flagnetic hash-check: read %s: %v\n", name, err)
		return exitFailure
	}

	u, err := appexperiments.NewCheckUniformity().Handle(appexperiments.UniformityCommand{
		UnitIDs: ids, Salt: salt.value, SecondSalt: secondSalt.value, Bins: bins,
	})
	if invalid := (*apperrors.Error)(nil); errors.As(err, &invalid) && invalid.Kind == apperrors.KindInvalid {
		fmt.Fprintf(stderr, "flagnetic hash-check: %s cannot be checked:\n", name)
		for _, v := range invalid.Violations {
			fmt.Fprintf(stderr, "  %s: %s\n", where(v.Pointer), v.Detail)
		}
		return exitFailure
	}
	if err != nil {
		fmt.Fprintf(stderr, "flagnetic hash-check: %v\n", err)
		return exitFailure
	}
	b, err := json.MarshalIndent(uniformityOut(u), "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "flagnetic hash-check: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "%s\n", b)
	return exitOK
}

// readUnitIDs reads one unit ID per line, LF or CRLF, the last line with or
// without its line break. Every line is a unit ID, so an empty one is
// refused as one. Reading stops one past the most a check takes, which is
// enough to refuse the list.
func readUnitIDs(r io.Reader) ([]string, error) {
	var ids []string
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64<<10), maxUnitIDLine)
	for len(ids) <= experiments.MaxUniformityUnitIDs && s.Scan() {
		ids = append(ids, strings.TrimSuffix(s.Text(), "\r"))
	}
	return ids, s.Err()
}

// where names the input a violation's pointer points at in the command's
// terms: a line of the list, or a flag.
func where(pointer string) string {
	if i, ok := strings.CutPrefix(pointer, "/unit_ids/"); ok {
		if n, err := strconv.Atoi(i); err == nil {
			return "line " + strconv.Itoa(n+1)
		}
	}
	switch pointer {
	case "/unit_ids":
		return "unit IDs"
	case "/salt", "/second_salt", "/bins":
		return "--" + strings.ReplaceAll(strings.TrimPrefix(pointer, "/"), "_", "-")
	}
	return pointer
}

// uniformityJSON is the management API's answer to a uniformity check, field
// for field, so a script reads the command's output and the API's alike.
type uniformityJSON struct {
	Salt         string            `json:"salt"`
	SecondSalt   *string           `json:"second_salt,omitempty"`
	Bins         int               `json:"bins"`
	Counts       []int             `json:"counts"`
	Min          int               `json:"min"`
	Max          int               `json:"max"`
	ChiSquare    float64           `json:"chi_square"`
	DF           int               `json:"df"`
	PValue       float64           `json:"p_value"`
	Verdict      string            `json:"verdict"`
	Independence *independenceJSON `json:"independence,omitempty"`
}

type independenceJSON struct {
	ChiSquare float64 `json:"chi_square"`
	DF        int     `json:"df"`
	PValue    float64 `json:"p_value"`
	Verdict   string  `json:"verdict"`
}

func uniformityOut(u experiments.Uniformity) uniformityJSON {
	out := uniformityJSON{
		Salt: u.Salt, SecondSalt: u.SecondSalt, Bins: u.Bins, Counts: u.Counts, Min: u.Min, Max: u.Max,
		ChiSquare: u.ChiSquare, DF: u.DF, PValue: u.PValue, Verdict: string(u.Verdict),
	}
	if i := u.Independence; i != nil {
		out.Independence = &independenceJSON{ChiSquare: i.ChiSquare, DF: i.DF, PValue: i.PValue, Verdict: string(i.Verdict)}
	}
	return out
}

// optionalString is a flag that tells an absent value from an empty one.
type optionalString struct{ value *string }

func (o *optionalString) String() string {
	if o == nil || o.value == nil {
		return ""
	}
	return *o.value
}

func (o *optionalString) Set(s string) error {
	o.value = &s
	return nil
}
