package flags_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// staged is a configuration with something in every part a ruleset
// carries, as two environments could both hold it.
func staged() flags.Configuration {
	author := id.FromBytes[access.Member]([16]byte{1})
	return flags.Configuration{
		On: true, OffVariation: "a", Fallthrough: "b",
		Prerequisites: []flags.Prerequisite{{Flag: "banner", Variation: "true"}},
		Overrides: []flags.Override{
			{UnitType: "user", UnitID: "u-1", Variation: "c", Note: "support case", AddedBy: &author},
			{UnitType: "organization", UnitID: "o-1", Variation: "a"},
		},
		Rules: []flags.Rule{
			{ID: id.FromBytes[flags.Rule]([16]byte{1}), Description: "Beta testers",
				Conditions: []flags.Condition{{Kind: flags.ConditionInSegment, Segment: "beta_testers"}},
				Serve:      flags.Serve{Kind: flags.ServeVariation, Variation: "c"}},
			{ID: id.FromBytes[flags.Rule]([16]byte{2}),
				Conditions: []flags.Condition{{Kind: flags.ConditionAttribute, Attribute: "plan", Op: "in", Value: []any{"pro", "team"}}},
				Serve:      flags.Serve{Kind: flags.ServeRollout, UnitType: "user", Variation: "c", Percentage: 2500, Salt: "k3j4h5g6f7d8s9a0"}},
		},
		Version: 4,
	}
}

func TestTwoConfigurationsAreComparedOnWhatTheirRulesetsCarry(t *testing.T) {
	differ := map[string]func(c *flags.Configuration){
		"on or off":               func(c *flags.Configuration) { c.On = false },
		"the off variation":       func(c *flags.Configuration) { c.OffVariation = "b" },
		"the fallthrough":         func(c *flags.Configuration) { c.Fallthrough = "a" },
		"a prerequisite":          func(c *flags.Configuration) { c.Prerequisites[0].Variation = "false" },
		"no prerequisites":        func(c *flags.Configuration) { c.Prerequisites = nil },
		"an override's unit ID":   func(c *flags.Configuration) { c.Overrides[0].UnitID = "u-2" },
		"an override's unit type": func(c *flags.Configuration) { c.Overrides[1].UnitType = "user" },
		"an override's variation": func(c *flags.Configuration) { c.Overrides[0].Variation = "b" },
		"the overrides' order": func(c *flags.Configuration) {
			c.Overrides[0], c.Overrides[1] = c.Overrides[1], c.Overrides[0]
		},
		"a rule's identifier":    func(c *flags.Configuration) { c.Rules[0].ID = id.FromBytes[flags.Rule]([16]byte{3}) },
		"a condition":            func(c *flags.Configuration) { c.Rules[1].Conditions[0].Value = []any{"pro"} },
		"a serve":                func(c *flags.Configuration) { c.Rules[0].Serve.Variation = "a" },
		"a rollout's percentage": func(c *flags.Configuration) { c.Rules[1].Serve.Percentage = 5000 },
		"a rollout's salt":       func(c *flags.Configuration) { c.Rules[1].Serve.Salt = "0a9s8d7f6g5h4j3k" },
		"the rules' order": func(c *flags.Configuration) {
			c.Rules[0], c.Rules[1] = c.Rules[1], c.Rules[0]
		},
		"a rule fewer": func(c *flags.Configuration) { c.Rules = c.Rules[:1] },
	}
	for name, change := range differ {
		other := staged()
		change(&other)
		if staged().SameRuleset(other) || other.SameRuleset(staged()) {
			t.Errorf("configurations differing in %s compare equal", name)
		}
	}

	member := id.FromBytes[access.Member]([16]byte{2})
	same := map[string]func(c *flags.Configuration){
		"a rule's description": func(c *flags.Configuration) { c.Rules[0].Description = "Staff" },
		"an override's note":   func(c *flags.Configuration) { c.Overrides[0].Note = "" },
		"an override's author": func(c *flags.Configuration) { c.Overrides[0].AddedBy = &member },
		"the flag and environment": func(c *flags.Configuration) {
			c.Flag, c.Environment = id.FromBytes[flags.Flag]([16]byte{9}), id.FromBytes[workspace.Environment]([16]byte{9})
		},
		"the version and its author": func(c *flags.Configuration) {
			c.Version, c.UpdatedAt, c.UpdatedBy = 9, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), &member
		},
	}
	for name, change := range same {
		other := staged()
		change(&other)
		if !staged().SameRuleset(other) || !other.SameRuleset(staged()) {
			t.Errorf("configurations differing only in %s compare different", name)
		}
	}
}
