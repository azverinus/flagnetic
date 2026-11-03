package transfer_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/transfer"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

const (
	ruleA = "0192b6a0-0000-7000-8000-00000000000a"
	ruleB = "0192b6a0-0000-7000-8000-00000000000b"
)

// existing is the document of a project a merge applies to.
func existing() transfer.Document {
	one := 5000
	return transfer.Document{
		SchemaVersion: transfer.Version,
		Project:       &transfer.Project{Key: "shop", Name: "Shop"},
		Environments:  []transfer.Environment{{Key: "development", Name: "Development"}, {Key: "production", Name: "Production", Protected: true}},
		UnitTypes:     []transfer.UnitType{{Key: "user", Name: "User"}},
		Attributes:    []transfer.Attribute{{Key: "plan", Type: "string", Name: "Plan"}},
		Segments:      []transfer.Segment{{Key: "beta", Name: "Beta", Conditions: []transfer.Condition{}}},
		Flags: []transfer.Flag{{
			Key: "checkout", Type: "boolean", Name: "Checkout", Owner: "ada@example.com",
			Variations: []transfer.Variation{{Key: "true", Value: json.RawMessage(`true`)}, {Key: "false", Value: json.RawMessage(`false`)}},
			Configurations: map[string]transfer.Configuration{
				"development": {OffVariation: "false", Fallthrough: "false"},
				"production": {
					On: true, OffVariation: "false", Fallthrough: "false",
					Overrides: []transfer.Override{{UnitType: "user", UnitID: "u-2", Variation: "true"}, {UnitType: "user", UnitID: "u-1", Variation: "true"}},
					Rules: []transfer.Rule{{
						ID: ruleA, Description: "Half",
						Serve: transfer.Serve{Kind: "rollout", Variation: "true", UnitType: "user", Percentage: &one, Salt: "abcdefghij012345"},
					}},
				},
			},
		}},
		Experiments: []transfer.Experiment{},
	}
}

func clone(t *testing.T, d transfer.Document) transfer.Document {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var out transfer.Document
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func keys(ps []transfer.Planned) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Key)
	}
	return out
}

func TestMergingWhatTheProjectHasPlansNothing(t *testing.T) {
	cur := existing()
	plan := transfer.Compare(cur, transfer.Effective(cur, clone(t, cur)))
	for name, s := range map[string]transfer.SectionPlan{
		"environments": plan.Environments, "unit types": plan.UnitTypes, "attributes": plan.Attributes, "segments": plan.Segments, "flags": plan.Flags,
	} {
		if len(s.Created) != 0 || len(s.Updated) != 0 {
			t.Errorf("%s: %+v, want nothing", name, s)
		}
	}
}

func TestAnEntityTheProjectLacksIsCreatedAndOneThatDiffersIsUpdatedWithItsPatch(t *testing.T) {
	cur := existing()
	doc := clone(t, cur)
	doc.Environments[0].Name = "Dev"
	doc.Environments = append(doc.Environments, transfer.Environment{Key: "staging", Name: "Staging"})
	doc.Segments = append(doc.Segments, transfer.Segment{Key: "internal", Name: "Internal"})
	doc.Flags[0].Configurations["production"] = func() transfer.Configuration {
		c := doc.Flags[0].Configurations["production"]
		c.On = false
		c.Rules = append(c.Rules, transfer.Rule{Serve: transfer.Serve{Kind: "variation", Variation: "true"}})
		return c
	}()
	doc.Flags[0].Tags = []string{"payments"}

	plan := transfer.Compare(cur, transfer.Effective(cur, doc))
	if got := keys(plan.Environments.Created); !slices.Equal(got, []string{"staging"}) || plan.Environments.Created[0].Index != 2 {
		t.Errorf("created environments = %+v, want staging at index 2", plan.Environments.Created)
	}
	if got := plan.Environments.Updated; len(got) != 1 || got[0].Key != "development" ||
		len(got[0].Patch) != 1 || got[0].Patch[0] != (transfer.PatchOperation{Op: "replace", Path: "/name", Value: "Dev"}) {
		t.Errorf("updated environments = %+v, want development renamed by a replace of /name", got)
	}
	if got := keys(plan.Segments.Created); !slices.Equal(got, []string{"internal"}) {
		t.Errorf("created segments = %v", got)
	}
	if len(plan.Flags.Updated) != 1 {
		t.Fatalf("updated flags = %+v, want checkout", plan.Flags.Updated)
	}
	var paths []string
	for _, op := range plan.Flags.Updated[0].Patch {
		paths = append(paths, op.Op+" "+op.Path)
	}
	want := []string{"add /configurations/production/rules/1", "add /tags/0", "replace /configurations/production/on"}
	slices.Sort(paths)
	if !slices.Equal(paths, want) {
		t.Errorf("patch = %v, want %v", paths, want)
	}
}

func TestAListShorterThanBeforeLosesItsTailFromTheEnd(t *testing.T) {
	cur := existing()
	cur.Flags[0].Tags = []string{"a", "b", "c"}
	doc := clone(t, cur)
	doc.Flags[0].Tags = []string{"a"}
	patch := transfer.Compare(cur, transfer.Effective(cur, doc)).Flags.Updated[0].Patch
	if len(patch) != 2 || patch[0].Path != "/tags/2" || patch[1].Path != "/tags/1" || patch[0].Op != "remove" {
		t.Errorf("patch = %+v, want /tags/2 then /tags/1 removed", patch)
	}
}

func TestEnvironmentsADocumentDoesNotConfigureKeepTheirConfiguration(t *testing.T) {
	cur := existing()
	doc := clone(t, cur)
	delete(doc.Flags[0].Configurations, "production")
	eff := transfer.Effective(cur, doc)
	if _, ok := eff.Flags[0].Configurations["production"]; !ok {
		t.Error("the production configuration is gone, want it kept")
	}
	if plan := transfer.Compare(cur, eff); len(plan.Flags.Updated) != 0 {
		t.Errorf("updated = %+v, want nothing", plan.Flags.Updated)
	}
}

func TestSectionsADocumentLacksArePlannedNothing(t *testing.T) {
	cur := existing()
	doc := transfer.Document{SchemaVersion: transfer.Version, Segments: []transfer.Segment{{Key: "beta", Name: "Beta", Conditions: []transfer.Condition{}}}}
	plan := transfer.Compare(cur, transfer.Effective(cur, doc))
	if len(plan.Flags.Created)+len(plan.Flags.Updated)+len(plan.Environments.Created)+len(plan.Segments.Updated) != 0 {
		t.Errorf("plan = %+v, want nothing", plan)
	}
}

func TestARuleOrRolloutWithoutAnIdentifierOrSaltAdoptsTheOneItEquals(t *testing.T) {
	cur := existing()
	doc := clone(t, cur)
	prod := doc.Flags[0].Configurations["production"]
	prod.Rules[0].ID, prod.Rules[0].Serve.Salt = "", ""
	doc.Flags[0].Configurations["production"] = prod
	eff := transfer.Effective(cur, doc)
	got := eff.Flags[0].Configurations["production"].Rules[0]
	if got.ID != ruleA || got.Serve.Salt != "abcdefghij012345" {
		t.Errorf("rule = %+v, want identifier and salt adopted", got)
	}
	if plan := transfer.Compare(cur, eff); len(plan.Flags.Updated) != 0 {
		t.Errorf("updated = %+v, want nothing", plan.Flags.Updated)
	}

	prod.Rules[0].Description = "Another"
	doc.Flags[0].Configurations["production"] = prod
	got = transfer.Effective(cur, doc).Flags[0].Configurations["production"].Rules[0]
	if got.ID != "" || got.Serve.Salt != "" {
		t.Errorf("a different rule = %+v, want none adopted, so that both are minted", got)
	}

	prod.Rules[0].ID = ruleA
	doc.Flags[0].Configurations["production"] = prod
	got = transfer.Effective(cur, doc).Flags[0].Configurations["production"].Rules[0]
	if got.ID != ruleA || got.Serve.Salt != "abcdefghij012345" {
		t.Errorf("a named rule without a salt = %+v, want its salt kept so that no unit moves", got)
	}
}

func TestOverridesAndPrerequisitesKeepTheOrderTheProjectHas(t *testing.T) {
	cur := existing()
	doc := clone(t, cur)
	prod := doc.Flags[0].Configurations["production"]
	slices.SortFunc(prod.Overrides, func(a, b transfer.Override) int { return -1 * compareStrings(a.UnitID, b.UnitID) })
	doc.Flags[0].Configurations["production"] = prod
	if plan := transfer.Compare(cur, transfer.Effective(cur, doc)); len(plan.Flags.Updated) != 0 {
		t.Errorf("updated = %+v, want nothing", plan.Flags.Updated)
	}
	got := transfer.Effective(cur, doc).Flags[0].Configurations["production"].Overrides
	if got[0].UnitID != "u-2" || got[1].UnitID != "u-1" {
		t.Errorf("overrides = %+v, want the order the project has", got)
	}
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// mergeFaultsOf returns the pointer and code of each violation CheckMerge finds.
func mergeFaultsOf(t *testing.T, d transfer.Document) [][2]string {
	t.Helper()
	err := transfer.CheckMerge(d, existing())
	if err == nil {
		return nil
	}
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "invalid_document" {
		t.Fatalf("CheckMerge = %v, want invalid_document", err)
	}
	var out [][2]string
	for _, v := range e.Violations {
		out = append(out, [2]string{v.Pointer, v.Code})
	}
	return out
}

func TestAMergeNeedsNoSection(t *testing.T) {
	if got := mergeFaultsOf(t, transfer.Document{SchemaVersion: transfer.Version}); got != nil {
		t.Errorf("faults = %v, want none", got)
	}
}

func TestAMergeRefusesExperimentsUntilTheyAreImported(t *testing.T) {
	d := transfer.Document{SchemaVersion: transfer.Version, Experiments: []transfer.Experiment{{Key: "a"}, {Key: "b"}}}
	want := [][2]string{{"/experiments/0", "unsupported"}, {"/experiments/1", "unsupported"}}
	if got := mergeFaultsOf(t, d); !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestAConfigurationMayNameAnEnvironmentTheProjectHasWithoutTheDocument(t *testing.T) {
	d := transfer.Document{SchemaVersion: transfer.Version, Flags: []transfer.Flag{
		flagWith("other", map[string][]transfer.Rule{"production": nil, "nowhere": nil}),
	}}
	want := [][2]string{{"/flags/0/configurations/nowhere", "unknown_environment"}}
	if got := mergeFaultsOf(t, d); !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
	d.Environments = []transfer.Environment{{Key: "nowhere", Name: "Nowhere"}}
	if got := mergeFaultsOf(t, d); got != nil {
		t.Errorf("with the environment in the document: faults = %v, want none", got)
	}
}

func TestATypeOfAnExistingFlagOrAttributeNeverChanges(t *testing.T) {
	d := transfer.Document{
		SchemaVersion: transfer.Version,
		Attributes:    []transfer.Attribute{{Key: "plan", Type: "number"}},
		Flags:         []transfer.Flag{{Key: "checkout", Type: "string"}},
	}
	want := [][2]string{{"/attributes/0/type", "type_immutable"}, {"/flags/0/type", "type_immutable"}}
	if got := mergeFaultsOf(t, d); !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestARolloutSaltOfAnUntouchedConfigurationIsNotReusedByAnotherFlag(t *testing.T) {
	d := transfer.Document{SchemaVersion: transfer.Version, Flags: []transfer.Flag{
		flagWith("other", map[string][]transfer.Rule{"development": {rollout(ruleB, "abcdefghij012345")}}),
	}}
	want := [][2]string{{"/flags/0/configurations/development/rules/0/serve/salt", "duplicate"}}
	if got := mergeFaultsOf(t, d); !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}

	// The same rule of the same flag in another environment shares it, as a copy does.
	d.Flags[0] = flagWith("checkout", map[string][]transfer.Rule{"development": {rollout(ruleA, "abcdefghij012345")}})
	if got := mergeFaultsOf(t, d); got != nil {
		t.Errorf("the same rule: faults = %v, want none", got)
	}

	// A configuration the document replaces no longer holds its salt.
	d.Flags[0] = flagWith("other", map[string][]transfer.Rule{"production": {rollout(ruleB, "abcdefghij012345")}})
	d.Flags = append(d.Flags, flagWith("checkout", map[string][]transfer.Rule{"production": nil}))
	if got := mergeFaultsOf(t, d); got != nil {
		t.Errorf("a salt released by the document: faults = %v, want none", got)
	}
}

func TestARolloutSaltARuleKeepsIsNotReusedByAnotherFlag(t *testing.T) {
	// The rule of checkout written without its identifier and salt is the
	// project's rule, and keeps its salt, so another flag may not take it.
	checkout := clone(t, existing()).Flags[0]
	prod := checkout.Configurations["production"]
	prod.Rules[0].ID, prod.Rules[0].Serve.Salt = "", ""
	checkout.Configurations = map[string]transfer.Configuration{"production": prod}
	d := transfer.Document{SchemaVersion: transfer.Version, Flags: []transfer.Flag{
		checkout,
		flagWith("other", map[string][]transfer.Rule{"production": {rollout(ruleB, "abcdefghij012345")}}),
	}}
	want := [][2]string{{"/flags/1/configurations/production/rules/0/serve/salt", "duplicate"}}
	if got := mergeFaultsOf(t, d); !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}
