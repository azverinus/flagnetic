package management_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

func serveExperiment(key string) management.Serve {
	return management.Serve{Kind: "experiment", Experiment: ptr(key)}
}

// rulesOfFlag returns the rules the ruleset of project's environment
// carries for flag, once it is at least at version, the way an SDK receives
// it.
func rulesOfFlag(t *testing.T, c *management.ClientWithResponses, project, environment, flag string, version int64) []eval.Rule {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		res := do(c.GetEnvironmentRulesetWithResponse(t.Context(), project, environment))
		var rs eval.Ruleset
		if res.StatusCode() == http.StatusOK && json.Unmarshal(res.Body, &rs) == nil && rs.Version >= version {
			for _, f := range rs.Flags {
				if f.Key == flag {
					return f.Rules
				}
			}
			t.Fatalf("the ruleset has no flag %s: %s", flag, res.Body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET ruleset = %d %s, want version %d", res.StatusCode(), res.Body, version)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestARuleServesAnExperimentOfItsFlagAndADraftIsAbsentFromTheRuleset(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, validExperiment("title_test"))
	c := mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, management.FlagConfigurationPut{
		On: true, OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{
			{Description: ptr("Titles under test"), Serve: serveExperiment("title_test")},
			{Conditions: conditions(attr("country", "eq", `"NZ"`)), Serve: serves("b")},
		},
	})
	if got := c.Rules[0].Serve; got.Kind != "experiment" || got.Experiment == nil || *got.Experiment != "title_test" ||
		got.Variation != nil || got.UnitType != nil || got.Percentage != nil || got.Salt != nil {
		t.Errorf("serve = %+v, want the experiment alone", got)
	}
	again := getConfiguration(t, f.admin, f.project.Key, "checkout_title", "staging")
	if again.Rules[0].Serve.Experiment == nil || *again.Rules[0].Serve.Experiment != "title_test" {
		t.Errorf("stored serve = %+v, want it to survive a read", again.Rules[0].Serve)
	}

	// The draft's rule is stored but no SDK is shown it: the ruleset
	// carries only the rule that follows.
	version := rulesetVersions(t, f.project)["staging"]
	rules := rulesOfFlag(t, f.admin, f.project.Key, "staging", "checkout_title", version)
	if len(rules) != 1 || rules[0].Serve.Kind != "variation" {
		t.Errorf("rules in the ruleset = %+v, want only the variation rule", rules)
	}

	// Editing the rule is a write of its own, audited with the experiment
	// the rule serves before and after.
	edited := asPut(c)
	(*edited.Rules)[0].Description = ptr("Titles under test, again")
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 2, edited)
	entries := auditOf(t, f.srv, "flag.configuration_updated", getFlag(t, f.admin, f.project.Key, "checkout_title").Id.String())
	if len(entries) != 2 {
		t.Fatalf("configuration entries = %d, want 2", len(entries))
	}
	served := func(v any) any {
		rs := v.(map[string]any)["rules"].([]any)
		return rs[0].(map[string]any)["serve"].(map[string]any)["experiment"]
	}
	if served(entries[0].After) != "title_test" || served(entries[0].Before) != "title_test" {
		t.Errorf("edit entry = before %v, after %v, want the experiment named in both", entries[0].Before, entries[0].After)
	}
}

func TestARuleCannotServeAnExperimentThatIsUnknownOrOfAnotherFlag(t *testing.T) {
	f := startExperiments(t)
	stringFlag(t, f.admin, f.project.Key, "other_title")
	other := validExperiment("other_test")
	other.Flag = "other_title"
	createExperiment(t, f.admin, f.project.Key, other)

	res := putConfiguration(t, f.admin, f.project.Key, "checkout_title", "staging", 1, management.FlagConfigurationPut{
		OffVariation: "a", Fallthrough: "a",
		Rules: &[]management.RuleInput{
			{Serve: serveExperiment("missing")},
			{Serve: serveExperiment("other_test")},
			{Serve: management.Serve{Kind: "experiment", Experiment: ptr("other_test"), Variation: ptr("a")}},
		},
	})
	want := []string{
		"/rules/0/serve/experiment unknown_experiment",
		"/rules/1/serve/experiment experiment_of_another_flag",
		"/rules/2/serve invalid_value", "/rules/2/serve/experiment experiment_of_another_flag",
	}
	if got := violationsOf(t, "refused serves", res.StatusCode(), res.Body, "invalid_configuration"); !slices.Equal(got, want) {
		t.Errorf("violations = %v\nwant %v", got, want)
	}
}

func TestAFlagWithADraftExperimentCannotBeArchivedAndItsListedVariationsCannotBeRemoved(t *testing.T) {
	f := startExperiments(t)
	e := validExperiment("title_test")
	e.Variations = weights("b", 5000, "c", 5000)
	createExperiment(t, f.admin, f.project.Key, e)
	flag := getFlag(t, f.admin, f.project.Key, "checkout_title")

	archived := archive(t, f.admin, f.project.Key, "checkout_title", flag.Version)
	if got := referencesOf(t, "archiving", archived.StatusCode(), archived.Body); !slices.Equal(got, []string{"title_test "}) {
		t.Errorf("archive references = %v, want the experiment", got)
	}

	// The configurations serve a alone, so only the experiment names c.
	listed := updateFlag(t, f.admin, f.project.Key, "checkout_title", flag.Version, management.FlagPatch{Variations: variations("a", `"A"`, "b", `"B"`)})
	if got := referencesOf(t, "removing a listed variation", listed.StatusCode(), listed.Body); !slices.Equal(got, []string{"title_test "}) {
		t.Errorf("variation references = %v, want the experiment", got)
	}
	free := updateFlag(t, f.admin, f.project.Key, "checkout_title", flag.Version, management.FlagPatch{Variations: variations("b", `"B"`, "c", `"C"`, "d", `"D"`)})
	if got := referencesOf(t, "removing a variation a configuration serves", free.StatusCode(), free.Body); len(got) != 3 {
		t.Errorf("references = %v, want the configurations serving a", got)
	}
}

func TestListedFlagsShowAndFilterByAnExperimentServe(t *testing.T) {
	f := startExperiments(t)
	stringFlag(t, f.admin, f.project.Key, "plain")
	createExperiment(t, f.admin, f.project.Key, validExperiment("title_test"))
	mustPut(t, f.admin, f.project.Key, "checkout_title", "staging", 1, management.FlagConfigurationPut{
		OffVariation: "a", Fallthrough: "a", Rules: &[]management.RuleInput{{Serve: serveExperiment("title_test")}},
	})

	items := listedItems(t, f.admin, f.project.Key, management.ListFlagsParams{})
	has := map[string][]string{}
	for _, item := range items {
		for _, s := range item.States {
			if s.HasExperiment {
				has[item.Key] = append(has[item.Key], s.Environment)
			}
		}
	}
	if len(has) != 1 || !slices.Equal(has["checkout_title"], []string{"staging"}) {
		t.Errorf("has_experiment = %v, want only checkout_title in staging", has)
	}
	filter := management.ListFlagsParamsState("experiment")
	for env, want := range map[string][]string{"staging": {"checkout_title"}, "production": {}} {
		got := listing(t, f.admin, f.project.Key, management.ListFlagsParams{Environment: ptr(env), State: ptr(filter)})
		if !slices.Equal(got, want) {
			t.Errorf("experiment in %s lists %v, want %v", env, got, want)
		}
	}
}
