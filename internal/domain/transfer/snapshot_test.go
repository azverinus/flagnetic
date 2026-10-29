package transfer_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestADocumentHoldsTheConfigurationAndNoIdentifierButTheRulesAndTheirSalts(t *testing.T) {
	member := id.MustParse[access.Member]("0192f3a0-0000-7000-8000-0000000000a1")
	author := id.MustParse[access.Member]("0192f3a0-0000-7000-8000-0000000000a2")
	flagID := id.MustParse[flags.Flag]("0192f3a0-0000-7000-8000-0000000000f1")
	ruleID := id.MustParse[flags.Rule]("0192f3a0-0000-7000-8000-0000000000b1")
	removal := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	archived := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

	s := transfer.Snapshot{
		Project: workspace.Project{Key: "shop", Name: "Shop", Description: "d"},
		Environments: []workspace.Environment{
			{Key: "production", Name: "Production", Protected: true}, {Key: "qa", Name: "QA"},
		},
		UnitTypes: []workspace.UnitType{{Key: "user", Name: "User", Description: "People"}},
		Attributes: []workspace.Attribute{
			{Key: "app_version", Type: "semver", Name: "App version", Standard: true},
			{Key: "plan", Type: "string", Name: "Plan", Description: "Billing"},
		},
		Segments: []flags.Segment{{Key: "pro", Name: "Pro", Private: true,
			Conditions: []flags.Condition{{Kind: "attribute", Attribute: "plan", Op: "eq", Value: "pro"}}}},
		Flags: []flags.Flag{{
			ID: flagID, Key: "matching", Type: flags.TypeBoolean, Name: "Matching", Owner: member, Tags: []string{"a"},
			Lifetime: flags.Temporary, ExpectedRemovalAt: &removal, ArchivedAt: &archived,
			Variations: []flags.Variation{{Key: "true", Value: []byte(`true`), Name: "true"}, {Key: "false", Value: []byte(`false`), Name: "false"}},
		}},
		Configurations: map[flags.FlagID][]flags.EnvironmentConfiguration{flagID: {
			{Environment: "production", Configuration: flags.Configuration{
				On: true, OffVariation: "false", Fallthrough: "true",
				Prerequisites: []flags.Prerequisite{{Flag: "other", Variation: "true"}},
				Overrides:     []flags.Override{{UnitType: "user", UnitID: "u1", Variation: "true", Note: "demo", AddedBy: &author}},
				Rules: []flags.Rule{
					{ID: ruleID, Description: "Pro", Conditions: []flags.Condition{{Kind: "in_segment", Segment: "pro"}},
						Serve: flags.Serve{Kind: flags.ServeRollout, Variation: "true", UnitType: "user", Percentage: 0, Salt: "abcdefghij012345"}},
					{ID: ruleID, Serve: flags.Serve{Kind: flags.ServeExperiment, Experiment: "exp"}},
					{ID: ruleID, Serve: flags.Serve{Kind: flags.ServeVariation, Variation: "false"}},
				},
				Version: 7, UpdatedBy: &author,
			}},
		}},
		Experiments: []experiments.Experiment{{
			Key: "exp", Name: "Exp", Hypothesis: "h", Owner: author, Tags: []string{"t"}, FlagKey: "matching", UnitType: "user",
			Variations:        []experiments.Weight{{Variation: "true", Weight: 5000}, {Variation: "false", Weight: 5000}},
			TrafficAllocation: 8000, InclusionSalt: "i", VariationSalt: "v", Status: experiments.StatusStopped,
			ExpectedEndAt: &removal, StartedAt: &removal, StoppedAt: &removal,
			Decision: &experiments.Decision{Outcome: experiments.OutcomeWinner, WinnerVariation: "true", Note: "n"},
		}},
		Emails: map[access.MemberID]string{member: "owner@example.com", author: "author@example.com"},
	}

	d := transfer.FromSnapshot(s)
	b, err := transfer.Encode(transfer.JSON, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := transfer.Decode(transfer.JSON, b)
	if err != nil {
		t.Fatal(err)
	}

	if d.SchemaVersion != 1 || *d.Project != (transfer.Project{Key: "shop", Name: "Shop", Description: "d"}) {
		t.Errorf("header = %d %+v", d.SchemaVersion, d.Project)
	}
	if want := []transfer.Environment{{Key: "production", Name: "Production", Protected: true}, {Key: "qa", Name: "QA"}}; !slices.Equal(d.Environments, want) {
		t.Errorf("environments = %+v", d.Environments)
	}
	if len(d.Attributes) != 1 || d.Attributes[0].Key != "plan" {
		t.Errorf("attributes = %+v, want the declared one and not the standard one", d.Attributes)
	}
	f := got.Flags[0]
	if f.Owner != "owner@example.com" || !f.Archived || f.ExpectedRemovalAt == nil || f.Key != "matching" {
		t.Errorf("flag = %+v", f)
	}
	c := f.Configurations["production"]
	if c.Overrides[0] != (transfer.Override{UnitType: "user", UnitID: "u1", Variation: "true", Note: "demo"}) {
		t.Errorf("overrides = %+v, want the note and no author", c.Overrides)
	}
	r := c.Rules
	if r[0].ID != ruleID.String() || r[0].Serve.Salt != "abcdefghij012345" || r[0].Serve.Percentage == nil || *r[0].Serve.Percentage != 0 ||
		r[0].Serve.UnitType != "user" {
		t.Errorf("rollout rule = %+v", r[0])
	}
	if r[1].Serve.Experiment != "exp" || r[1].Serve.Percentage != nil || r[1].Serve.Salt != "" || r[1].Serve.Variation != "" {
		t.Errorf("experiment rule = %+v", r[1])
	}
	if r[2].Serve.Variation != "false" || r[2].Serve.Percentage != nil {
		t.Errorf("variation rule = %+v", r[2])
	}
	e := got.Experiments[0]
	if e.Owner != "author@example.com" || e.Flag != "matching" || e.Status != "stopped" || e.Decision == nil ||
		e.Decision.WinnerVariation != "true" || e.ExpectedEndAt == nil || e.InclusionSalt != "i" {
		t.Errorf("experiment = %+v", e)
	}
}
