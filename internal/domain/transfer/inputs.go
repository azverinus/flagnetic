package transfer

import (
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The functions below read a document's entities as the inputs the contexts
// write them with, so that every context judges the document by the rules
// it judges its own writes by. They assume the faults CheckCreate finds are
// absent: a rule identifier that is not a UUID is read as absent.

// FlagConditions returns the conditions as the flags context holds them.
func FlagConditions(in []Condition) []flags.Condition {
	if in == nil {
		return nil
	}
	out := make([]flags.Condition, len(in))
	for i, c := range in {
		out[i] = flags.Condition{
			Kind: c.Kind, Attribute: c.Attribute, Op: c.Op, Value: c.Value, Segment: c.Segment, Flag: c.Flag, Variation: c.Variation,
		}
	}
	return out
}

// VariationInputs returns the flag's variations as a write gives them, nil
// when the document lists none, which gives a boolean flag its two.
func (f Flag) VariationInputs() *[]flags.VariationInput {
	if f.Variations == nil {
		return nil
	}
	out := make([]flags.VariationInput, len(f.Variations))
	for i, v := range f.Variations {
		name, description := v.Name, v.Description
		out[i] = flags.VariationInput{Key: v.Key, Value: v.Value, Name: &name, Description: &description}
	}
	return &out
}

// Input returns the configuration as a write gives it whole. A rule keeps
// the identifier and a rollout the salt the document names; the flags
// context gives the rest.
func (c Configuration) Input() flags.ConfigurationInput {
	in := flags.ConfigurationInput{
		On: c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough,
		Prerequisites: make([]flags.Prerequisite, len(c.Prerequisites)),
		Overrides:     make([]flags.OverrideInput, len(c.Overrides)),
		Rules:         make([]flags.RuleInput, len(c.Rules)),
	}
	for i, p := range c.Prerequisites {
		in.Prerequisites[i] = flags.Prerequisite{Flag: p.Flag, Variation: p.Variation}
	}
	for i, o := range c.Overrides {
		note := o.Note
		in.Overrides[i] = flags.OverrideInput{UnitType: o.UnitType, UnitID: o.UnitID, Variation: o.Variation, Note: &note}
	}
	for i, r := range c.Rules {
		rule := flags.RuleInput{
			Description: r.Description, Conditions: FlagConditions(r.Conditions),
			Serve: flags.ServeInput{
				Kind: r.Serve.Kind, Variation: r.Serve.Variation, UnitType: r.Serve.UnitType, Experiment: r.Serve.Experiment,
				Percentage: r.Serve.Percentage, Salt: r.Serve.Salt,
			},
		}
		if ruleID, err := id.Parse[flags.Rule](r.ID); err == nil {
			rule.ID = &ruleID
		}
		in.Rules[i] = rule
	}
	return in
}
