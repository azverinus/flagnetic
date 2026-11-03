package transfer_test

import (
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/transfer"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// creating is a document a project can be created from.
func creating() transfer.Document {
	return transfer.Document{
		SchemaVersion: transfer.Version,
		Project:       &transfer.Project{Key: "shop", Name: "Shop"},
		Environments:  []transfer.Environment{{Key: "production", Name: "Production"}},
		UnitTypes:     []transfer.UnitType{{Key: "user", Name: "User"}},
		Attributes:    []transfer.Attribute{},
		Segments:      []transfer.Segment{},
		Flags:         []transfer.Flag{},
		Experiments:   []transfer.Experiment{},
	}
}

func rollout(id, salt string) transfer.Rule {
	percentage := 5000
	return transfer.Rule{ID: id, Serve: transfer.Serve{Kind: "rollout", Variation: "true", UnitType: "user", Percentage: &percentage, Salt: salt}}
}

func flagWith(key string, rules map[string][]transfer.Rule) transfer.Flag {
	f := transfer.Flag{Key: key, Type: "boolean", Name: key, Configurations: map[string]transfer.Configuration{}}
	for env, rs := range rules {
		f.Configurations[env] = transfer.Configuration{OffVariation: "false", Fallthrough: "true", Rules: rs}
	}
	return f
}

// faultsOf returns the pointer and code of each violation CheckCreate finds.
func faultsOf(t *testing.T, d transfer.Document) [][2]string {
	t.Helper()
	err := transfer.CheckCreate(d)
	if err == nil {
		return nil
	}
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "invalid_document" {
		t.Fatalf("CheckCreate = %v, want invalid_document", err)
	}
	var out [][2]string
	for _, v := range e.Violations {
		out = append(out, [2]string{v.Pointer, v.Code})
	}
	return out
}

func TestADocumentWithEverySectionAndAnEnvironmentCreatesAProject(t *testing.T) {
	if got := faultsOf(t, creating()); got != nil {
		t.Fatalf("faults = %v, want none", got)
	}
}

func TestACreateNeedsEverySectionAndAnEnvironmentAndReportsEachFault(t *testing.T) {
	got := faultsOf(t, transfer.Document{SchemaVersion: transfer.Version})
	want := [][2]string{
		{"/project", "required"}, {"/environments", "required"}, {"/unit_types", "required"}, {"/attributes", "required"},
		{"/segments", "required"}, {"/flags", "required"}, {"/experiments", "required"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}

	d := creating()
	d.Environments = []transfer.Environment{}
	if got := faultsOf(t, d); !slices.Equal(got, [][2]string{{"/environments", "invalid_value"}}) {
		t.Errorf("no environment: faults = %v", got)
	}
}

func TestAKeyRepeatedInOneKindIsRefusedAtTheSecond(t *testing.T) {
	d := creating()
	d.Environments = append(d.Environments, transfer.Environment{Key: "production", Name: "Again"})
	d.Flags = []transfer.Flag{flagWith("a", nil), flagWith("b", nil), flagWith("a", nil)}
	got := faultsOf(t, d)
	want := [][2]string{{"/environments/1/key", "duplicate"}, {"/flags/2/key", "duplicate"}}
	if !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestAnAttributeSharingAUnitTypesKeyOrRetypingAStandardOneIsRefused(t *testing.T) {
	d := creating()
	d.Attributes = []transfer.Attribute{
		{Key: "user", Type: "string", Name: "User"},
		{Key: "app_version", Type: "string", Name: "App version"},
		{Key: "platform", Type: "string", Name: "Platform"},
	}
	got := faultsOf(t, d)
	want := [][2]string{{"/attributes/0/key", "duplicate"}, {"/attributes/1/type", "type_immutable"}}
	if !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestAUnitTypeKeyedAsAStandardAttributeIsRefused(t *testing.T) {
	d := creating()
	d.UnitTypes = append(d.UnitTypes, transfer.UnitType{Key: "platform", Name: "Platform"})
	got := faultsOf(t, d)
	want := [][2]string{{"/unit_types/1/key", "duplicate"}}
	if !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestAConfigurationForAnEnvironmentTheDocumentLacksIsRefusedWithItsKey(t *testing.T) {
	d := creating()
	d.Flags = []transfer.Flag{flagWith("a", map[string][]transfer.Rule{"production": nil, "stagin/g": nil})}
	got := faultsOf(t, d)
	want := [][2]string{{"/flags/0/configurations/stagin~1g", "unknown_environment"}}
	if !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestARuleIdentifierMustBeAUUIDAndASaltSixteenLowerCaseLettersAndDigits(t *testing.T) {
	d := creating()
	d.Flags = []transfer.Flag{flagWith("a", map[string][]transfer.Rule{"production": {
		rollout("not-a-uuid", "abcdefghij012345"),
		rollout("0192f3a0-0000-7000-8000-0000000000b1", "ABCDEFGHIJ012345"),
		rollout("0192F3A0-0000-4000-8000-0000000000B2", "abcdefghij012346"),
	}})}
	got := faultsOf(t, d)
	want := [][2]string{
		{"/flags/0/configurations/production/rules/0/id", "invalid_value"},
		{"/flags/0/configurations/production/rules/1/serve/salt", "invalid_value"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestASaltIsSharedOnlyByTheSameRuleOfAFlagInAnotherEnvironment(t *testing.T) {
	const (
		salt  = "abcdefghij012345"
		ruleA = "0192f3a0-0000-7000-8000-0000000000b1"
		ruleB = "0192f3a0-0000-7000-8000-0000000000b2"
	)
	d := creating()
	d.Environments = append(d.Environments, transfer.Environment{Key: "staging", Name: "Staging"})
	d.Flags = []transfer.Flag{
		// A copy keeps the rule and its salt in every environment.
		flagWith("copied", map[string][]transfer.Rule{"production": {rollout(ruleA, salt)}, "staging": {rollout(ruleA, salt)}}),
	}
	if got := faultsOf(t, d); got != nil {
		t.Fatalf("a copied rule: faults = %v, want none", got)
	}

	d.Flags = []transfer.Flag{
		flagWith("one", map[string][]transfer.Rule{"production": {rollout(ruleA, salt)}}),
		flagWith("two", map[string][]transfer.Rule{"production": {rollout(ruleA, salt)}}),
		flagWith("three", map[string][]transfer.Rule{"production": {rollout(ruleB, salt)}, "staging": {rollout(ruleA, salt)}}),
	}
	got := faultsOf(t, d)
	want := [][2]string{
		{"/flags/1/configurations/production/rules/0/serve/salt", "duplicate"},
		{"/flags/2/configurations/production/rules/0/serve/salt", "duplicate"},
		{"/flags/2/configurations/staging/rules/0/serve/salt", "duplicate"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestASaltBelongsToTheFirstRolloutThatHasIt(t *testing.T) {
	const (
		salt  = "abcdefghij012345"
		ruleA = "0192f3a0-0000-7000-8000-0000000000b1"
		ruleB = "0192f3a0-0000-7000-8000-0000000000b2"
	)
	d := creating()
	d.Environments = append(d.Environments,
		transfer.Environment{Key: "qa", Name: "QA"}, transfer.Environment{Key: "staging", Name: "Staging"})
	d.Flags = []transfer.Flag{
		// The rule in staging is the rule in production, its identifier
		// written in upper case; only the other rule in qa takes a salt that
		// is not its own.
		flagWith("a", map[string][]transfer.Rule{
			"production": {rollout(ruleA, salt)},
			"qa":         {rollout(ruleB, salt)},
			"staging":    {rollout("0192F3A0-0000-7000-8000-0000000000B1", salt)},
		}),
	}
	got := faultsOf(t, d)
	want := [][2]string{{"/flags/0/configurations/qa/rules/0/serve/salt", "duplicate"}}
	if !slices.Equal(got, want) {
		t.Errorf("faults = %v, want %v", got, want)
	}
}

func TestACreateRefusesExperimentsUntilTheyAreImported(t *testing.T) {
	d := creating()
	d.Experiments = []transfer.Experiment{{Key: "e"}}
	if got := faultsOf(t, d); !slices.Equal(got, [][2]string{{"/experiments/0", "unsupported"}}) {
		t.Errorf("faults = %v", got)
	}
}

func TestAConfigurationIsReadAsAWriteWithItsRuleIdentifiersAndSalts(t *testing.T) {
	percentage := 2500
	c := transfer.Configuration{
		On: true, OffVariation: "false", Fallthrough: "true",
		Overrides: []transfer.Override{{UnitType: "user", UnitID: "u1", Variation: "true", Note: "demo"}},
		Rules: []transfer.Rule{
			{ID: "0192f3a0-0000-7000-8000-0000000000b1", Serve: transfer.Serve{Kind: "rollout", Variation: "true", UnitType: "user", Percentage: &percentage, Salt: "abcdefghij012345"}},
			{Description: "no identifier", Serve: transfer.Serve{Kind: "variation", Variation: "false"}},
		},
	}
	in := c.Input()
	if !in.On || len(in.Overrides) != 1 || in.Overrides[0].Note == nil || *in.Overrides[0].Note != "demo" {
		t.Errorf("input = %+v", in)
	}
	if in.Rules[0].ID == nil || in.Rules[0].ID.String() != "0192f3a0-0000-7000-8000-0000000000b1" || in.Rules[0].Serve.Salt != "abcdefghij012345" {
		t.Errorf("rule 0 = %+v, want its identifier and salt", in.Rules[0])
	}
	if in.Rules[1].ID != nil {
		t.Errorf("rule 1 identifier = %v, want none", in.Rules[1].ID)
	}
}
