package load

import (
	"strings"
	"testing"
)

func TestTheCheckedInProfileIsValid(t *testing.T) {
	p, err := ReadProfile("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Names {
		if p.Scenarios[name].P99CeilingMs <= 0 {
			t.Errorf("scenario %s has no ceiling", name)
		}
	}
}

func TestParseProfileRefusesWhatWouldTurnTheGateOff(t *testing.T) {
	const ok = `{"warmup":"1s","max_error_rate":0.01,"scenarios":{
	  "evaluate":{"rate":1,"duration":"1s","p99_ceiling_ms":1},
	  "ruleset":{"rate":1,"duration":"1s","p99_ceiling_ms":1},
	  "events":{"rate":1,"duration":"1s","p99_ceiling_ms":1},
	  "stream":{"subscribers":1,"changes":1,"p99_ceiling_ms":1}}}`
	if _, err := ParseProfile([]byte(ok)); err != nil {
		t.Fatalf("a valid profile: %v", err)
	}
	for name, c := range map[string]struct{ from, to, want string }{
		"a scenario that is not one":   {`"evaluate"`, `"evalute"`, "has no scenario evaluate"},
		"a missing ceiling":            {`"events":{"rate":1,"duration":"1s","p99_ceiling_ms":1}`, `"events":{"rate":1,"duration":"1s"}`, "no p99_ceiling_ms"},
		"a rate scenario without rate": {`"ruleset":{"rate":1,`, `"ruleset":{"rate":0,`, "needs a rate"},
		"a stream without subscribers": {`"subscribers":1`, `"subscribers":0`, "needs subscribers"},
		"a duration that is not one": {`"duration":"1s","p99_ceiling_ms":1},
	  "ruleset"`, `"duration":"soon","p99_ceiling_ms":1},
	  "ruleset"`, "invalid duration"},
		"an error rate of everything":  {`"max_error_rate":0.01`, `"max_error_rate":1`, "max_error_rate"},
		"a profile missing a scenario": {`"stream":{"subscribers":1,"changes":1,"p99_ceiling_ms":1}`, `"unused":{"p99_ceiling_ms":1}`, "has no scenario stream"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(ok, c.from) {
				t.Fatalf("test bug: %q not in the profile", c.from)
			}
			_, err := ParseProfile([]byte(strings.Replace(ok, c.from, c.to, 1)))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	p, err := ReadProfile("")
	if err != nil {
		t.Fatal(err)
	}
	ceiling := p.Scenarios[Evaluate].P99CeilingMs
	good := Result{Scenario: Evaluate, Sent: 1000, P99: ceiling / 2}
	for name, c := range map[string]struct {
		r    Result
		want string
	}{
		"within the ceiling":      {good, ""},
		"p99 above the ceiling":   {Result{Scenario: Evaluate, Sent: 1000, P99: ceiling + 1}, "above the ceiling"},
		"errors above the share":  {Result{Scenario: Evaluate, Sent: 1000, Errors: 10, P99: 1, FirstError: "status 500"}, "10 of 1000 samples failed (first: status 500)"},
		"errors within the share": {Result{Scenario: Evaluate, Sent: 10000, Errors: 5, P99: 1}, ""},
		"no samples":              {Result{Scenario: Evaluate}, "took no samples"},
	} {
		t.Run(name, func(t *testing.T) {
			got := strings.Join(p.Check([]Result{c.r}), "\n")
			if c.want == "" && got != "" || !strings.Contains(got, c.want) {
				t.Errorf("Check = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMarkdownHasARowPerResult(t *testing.T) {
	p, _ := ReadProfile("")
	md := p.Markdown([]Result{{Scenario: Evaluate, Target: 100, Sent: 3000, P99: 12.34}, {Scenario: Stream, Sent: 500}})
	for _, want := range []string{"| evaluate | 100/s |", "| stream | - |", "12.3"} {
		if !strings.Contains(md, want) {
			t.Errorf("the summary lacks %q:\n%s", want, md)
		}
	}
}
