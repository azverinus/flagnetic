// Package delivery is the domain of what an environment's SDKs receive: the
// ruleset materialized from its flags, segments and declarations, and the
// evaluation of it that explains what a context is served.
package delivery

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// Materials are what the ruleset of one environment is materialized from,
// read in one snapshot so the ruleset never mixes the states before and
// after a write.
type Materials struct {
	// Environment is the environment's key.
	Environment string
	// Protected marks a protected environment, in which SDKs ignore local
	// overrides.
	Protected bool
	// Version is the environment's ruleset version.
	Version    int64
	UnitTypes  []workspace.UnitType
	Attributes []workspace.Attribute
	Segments   []flags.Segment
	// Flags are the project's flags, archived ones included.
	Flags []flags.Flag
	// Configurations are the flags' configurations in the environment.
	Configurations []flags.Configuration
	// Experiments are the project's experiments, whatever their status.
	Experiments []flags.ExperimentRef
}

// With returns the materials with c in place of the configuration of c's
// flag in the environment, as an unsaved draft is evaluated.
func (m Materials) With(c flags.Configuration) Materials {
	m.Configurations = slices.Clone(m.Configurations)
	for i := range m.Configurations {
		if m.Configurations[i].Flag == c.Flag {
			m.Configurations[i] = c
		}
	}
	return m
}

// Materialize returns the server-scope ruleset document of the environment
// (schema 1), which the reference evaluator and every SDK read. Archived
// flags are left out, so SDKs answer flag_not_found for them as for a
// deleted flag. What stays on the server never reaches a ruleset: rule
// descriptions, override notes and their authors, names, descriptions and
// versions of everything. Every list is ordered by key in byte order, so
// the same materials always give the same document.
func Materialize(m Materials) ([]byte, error) {
	rs := eval.Ruleset{
		SchemaVersion: eval.SchemaVersion, Version: m.Version, Scope: eval.ScopeServer,
		Environment: &eval.Environment{Key: m.Environment, Protected: m.Protected},
		UnitTypes:   make([]string, 0, len(m.UnitTypes)), Attributes: make([]eval.Attribute, 0, len(m.Attributes)),
		Segments: make([]eval.Segment, 0, len(m.Segments)), Flags: []eval.Flag{},
	}
	for _, u := range m.UnitTypes {
		rs.UnitTypes = append(rs.UnitTypes, u.Key)
	}
	slices.Sort(rs.UnitTypes)
	for _, a := range m.Attributes {
		rs.Attributes = append(rs.Attributes, eval.Attribute{Key: a.Key, Type: eval.AttributeType(a.Type)})
	}
	slices.SortFunc(rs.Attributes, func(a, b eval.Attribute) int { return cmp.Compare(a.Key, b.Key) })
	for _, s := range m.Segments {
		rs.Segments = append(rs.Segments, eval.Segment{Key: s.Key, Private: s.Private, Conditions: conditions(s.Conditions)})
	}
	slices.SortFunc(rs.Segments, func(a, b eval.Segment) int { return cmp.Compare(a.Key, b.Key) })
	experiments := make(map[string]flags.ExperimentRef, len(m.Experiments))
	for _, e := range m.Experiments {
		experiments[e.Key] = e
	}
	configured := make(map[flags.FlagID]flags.Configuration, len(m.Configurations))
	for _, c := range m.Configurations {
		configured[c.Flag] = c
	}
	for _, f := range m.Flags {
		c, ok := configured[f.ID]
		if f.ArchivedAt != nil || !ok {
			continue
		}
		rs.Flags = append(rs.Flags, flagOf(f, c, experiments))
	}
	slices.SortFunc(rs.Flags, func(a, b eval.Flag) int { return cmp.Compare(a.Key, b.Key) })
	doc, err := json.Marshal(rs)
	if err != nil {
		return nil, fmt.Errorf("materializing the ruleset of %s: %w", m.Environment, err)
	}
	return doc, nil
}

// flagOf returns f configured by c as a ruleset carries it. A rule serving
// an experiment that is not running is left out: preparing an experiment
// must change nothing any application is served, and one that stopped has
// its rules rewritten away.
func flagOf(f flags.Flag, c flags.Configuration, experiments map[string]flags.ExperimentRef) eval.Flag {
	out := eval.Flag{
		Key: f.Key, Type: string(f.Type), AutomaticExposure: f.AutomaticExposure,
		Variations: make([]eval.Variation, len(f.Variations)),
		On:         c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough,
		Prerequisites: make([]eval.Prerequisite, len(c.Prerequisites)), Overrides: make([]eval.Override, len(c.Overrides)),
		Rules: make([]eval.Rule, 0, len(c.Rules)),
	}
	for i, v := range f.Variations {
		out.Variations[i] = eval.Variation{Key: v.Key, Value: v.Value}
	}
	for i, p := range c.Prerequisites {
		out.Prerequisites[i] = eval.Prerequisite{Flag: p.Flag, Variation: p.Variation}
	}
	for i, o := range c.Overrides {
		out.Overrides[i] = eval.Override{UnitType: o.UnitType, UnitID: o.UnitID, Variation: o.Variation}
	}
	for _, r := range c.Rules {
		serve := eval.Serve{Kind: r.Serve.Kind, Variation: r.Serve.Variation}
		switch r.Serve.Kind {
		case flags.ServeRollout:
			serve.UnitType, serve.Percentage, serve.Salt = r.Serve.UnitType, r.Serve.Percentage, r.Serve.Salt
		case flags.ServeExperiment:
			e, ok := experiments[r.Serve.Experiment]
			if !ok || e.Status != flags.ExperimentRunning {
				continue
			}
			serve = experimentServe(e)
		}
		out.Rules = append(out.Rules, eval.Rule{ID: r.ID.String(), Conditions: conditions(r.Conditions), Serve: serve})
	}
	return out
}

// experimentServe returns what a rule serving the running experiment e
// carries in a ruleset: the experiment's frozen fields, which SDKs assign
// units by.
func experimentServe(e flags.ExperimentRef) eval.Serve {
	weights := make([]eval.Weight, len(e.Weights))
	for i, w := range e.Weights {
		weights[i] = eval.Weight{Variation: w.Variation, Weight: w.Weight}
	}
	return eval.Serve{
		Kind: flags.ServeExperiment, Experiment: e.Key, UnitType: e.UnitType, InclusionSalt: e.InclusionSalt,
		VariationSalt: e.VariationSalt, TrafficAllocation: e.TrafficAllocation, Weights: weights,
	}
}

func conditions(in []flags.Condition) []eval.Condition {
	out := make([]eval.Condition, len(in))
	for i, c := range in {
		out[i] = eval.Condition(c)
	}
	return out
}
