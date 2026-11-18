package load

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// defaultProfile is the profile checked in beside the code: the shape of
// every scenario and the ceiling of its p99. The ceilings are generous
// absolute numbers, since a hosted runner varies by more than a percentage
// can tell apart from a regression: they catch a regression of a multiple.
//
//go:embed profile.json
var defaultProfile []byte

// Scenario names, in the order a run measures them.
const (
	Evaluate = "evaluate"
	Ruleset  = "ruleset"
	Events   = "events"
	Stream   = "stream"
)

// Names lists the scenarios in the order a run measures them.
var Names = []string{Evaluate, Ruleset, Events, Stream}

// Profile is the shape and the ceilings of a run.
type Profile struct {
	// Warmup is sent before each rate scenario and left out of its numbers,
	// so connections and caches are warm.
	Warmup Duration `json:"warmup"`
	// MaxErrorRate is the share of failed samples a scenario may have.
	MaxErrorRate float64 `json:"max_error_rate"`
	// Scenarios are the scenario shapes by name.
	Scenarios map[string]Shape `json:"scenarios"`
}

// Shape is one scenario's load and ceiling.
type Shape struct {
	// Rate and Duration are the arrival rate in requests per second and the
	// length of the run of evaluate, ruleset and events.
	Rate     float64  `json:"rate,omitempty"`
	Duration Duration `json:"duration,omitempty"`
	// Subscribers and Changes are the streams held open and the
	// configuration changes made while they are, for stream.
	Subscribers int `json:"subscribers,omitempty"`
	Changes     int `json:"changes,omitempty"`
	// P99CeilingMs fails the run when the scenario's p99 is above it.
	P99CeilingMs float64 `json:"p99_ceiling_ms"`
}

// Duration is a time.Duration written as a string in JSON, such as "30s".
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("a duration is a string such as \"30s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// LoadProfile reads the profile in the file at path, or the one checked in
// when path is empty.
func LoadProfile(path string) (Profile, error) {
	b := defaultProfile
	if path != "" {
		var err error
		if b, err = os.ReadFile(path); err != nil { //nolint:gosec // the operator names the profile
			return Profile{}, err
		}
	}
	return ParseProfile(b)
}

// ParseProfile reads a profile and refuses one that names an unknown
// scenario or leaves a scenario without a shape or a ceiling, so a typo
// never turns a gate off.
func ParseProfile(b []byte) (Profile, error) {
	var p Profile
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("read the profile: %w", err)
	}
	for _, name := range Names {
		if _, ok := p.Scenarios[name]; !ok {
			return Profile{}, fmt.Errorf("the profile has no scenario %s", name)
		}
	}
	for name, s := range p.Scenarios {
		if !slices.Contains(Names, name) {
			return Profile{}, fmt.Errorf("the profile names scenario %q; the scenarios are %s", name, strings.Join(Names, ", "))
		}
		if s.P99CeilingMs <= 0 {
			return Profile{}, fmt.Errorf("scenario %s has no p99_ceiling_ms", name)
		}
		if name == Stream {
			if s.Subscribers <= 0 || s.Changes <= 0 {
				return Profile{}, fmt.Errorf("scenario stream needs subscribers and changes")
			}
		} else if s.Rate <= 0 || s.Duration <= 0 {
			return Profile{}, fmt.Errorf("scenario %s needs a rate and a duration", name)
		}
	}
	if p.MaxErrorRate < 0 || p.MaxErrorRate >= 1 {
		return Profile{}, fmt.Errorf("max_error_rate is a share of the samples, at least 0 and below 1")
	}
	return p, nil
}

// Check returns what each result breaks: a p99 above its ceiling, more
// failed samples than the profile allows, and a scenario that took no
// samples at all.
func (p Profile) Check(results []Result) []string {
	var broken []string
	for _, r := range results {
		s := p.Scenarios[r.Scenario]
		switch {
		case r.Sent == 0:
			broken = append(broken, fmt.Sprintf("%s: took no samples", r.Scenario))
			continue
		case r.P99 > s.P99CeilingMs:
			broken = append(broken, fmt.Sprintf("%s: p99 %.1f ms is above the ceiling of %.0f ms", r.Scenario, r.P99, s.P99CeilingMs))
		}
		if rate := float64(r.Errors) / float64(r.Sent); rate > p.MaxErrorRate {
			broken = append(broken, fmt.Sprintf("%s: %d of %d samples failed (first: %s)", r.Scenario, r.Errors, r.Sent, r.FirstError))
		}
	}
	return broken
}

// Markdown writes the results as a table for a job summary.
func (p Profile) Markdown(results []Result) string {
	var b strings.Builder
	b.WriteString("| Scenario | Target rate | Achieved | Samples | Errors | p50 ms | p99 ms | p99.9 ms | Max ms | p99 ceiling ms |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		target := "-"
		if r.Target > 0 {
			target = fmt.Sprintf("%.0f/s", r.Target)
		}
		fmt.Fprintf(&b, "| %s | %s | %.1f/s | %d | %d | %.1f | %.1f | %.1f | %.1f | %.0f |\n",
			r.Scenario, target, r.Achieved, r.Sent, r.Errors, r.P50, r.P99, r.P999, r.Max, p.Scenarios[r.Scenario].P99CeilingMs)
	}
	return b.String()
}
