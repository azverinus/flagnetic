package eval

import (
	"encoding/json"
	"errors"
	"fmt"
)

// SchemaVersion is the ruleset schema version this evaluator reads.
const SchemaVersion = 1

// Scopes of a ruleset document. A client document omits private segments
// and overrides, and marks every flag depending on a private segment
// server_only with no configuration.
const (
	ScopeServer = "server"
	ScopeClient = "client"
)

// Ruleset is the complete evaluable configuration of one environment at one
// moment. Decode it with Decode; the zero value is not usable.
type Ruleset struct {
	SchemaVersion int          `json:"schema_version"`
	Version       int64        `json:"version"`
	Scope         string       `json:"scope"`
	Environment   *Environment `json:"environment,omitempty"`
	UnitTypes     []string     `json:"unit_types"`
	Attributes    []Attribute  `json:"attributes"`
	Segments      []Segment    `json:"segments"`
	Flags         []Flag       `json:"flags"`

	index *index
}

// Environment identifies the environment a ruleset belongs to. A document
// without it (a hand-written offline file) is treated as unprotected.
type Environment struct {
	Key       string `json:"key"`
	Protected bool   `json:"protected"`
}

// Protected reports whether the ruleset belongs to a protected environment.
// SDKs refuse local overrides there.
func (rs *Ruleset) Protected() bool {
	return rs.Environment != nil && rs.Environment.Protected
}

// Flag returns the flag with key, as the document has it, and whether the
// document has it. SDKs check a flag's type against the one requested.
func (rs *Ruleset) Flag(key string) (*Flag, bool) {
	f, ok := rs.index.flags[key]
	if !ok {
		return nil, false
	}
	return f.Flag, true
}

// Attribute is a declared attribute with its type.
type Attribute struct {
	Key  string        `json:"key"`
	Type AttributeType `json:"type"`
}

// AttributeType is the type of a declared attribute. A unit type used as
// an attribute in a condition behaves as a string attribute holding the
// unit ID of that type.
type AttributeType string

// Attribute types.
const (
	TypeString  AttributeType = "string"
	TypeNumber  AttributeType = "number"
	TypeBoolean AttributeType = "boolean"
	TypeSemver  AttributeType = "semver"
	TypeDate    AttributeType = "date"
	TypeList    AttributeType = "list"
)

// Segment is a reusable, named AND-set of attribute conditions.
type Segment struct {
	Key        string      `json:"key"`
	Private    bool        `json:"private"`
	Conditions []Condition `json:"conditions"`
}

// Condition kinds.
const (
	ConditionAttribute    = "attribute"
	ConditionInSegment    = "in_segment"
	ConditionNotInSegment = "not_in_segment"
	ConditionPrerequisite = "prerequisite"
)

// Condition is one comparison inside a rule or segment: an attribute, an
// operator and a value; membership in a segment; or a prerequisite.
type Condition struct {
	Kind      string `json:"kind"`
	Attribute string `json:"attribute,omitempty"`
	Op        string `json:"op,omitempty"`
	Value     any    `json:"value,omitempty"`
	Segment   string `json:"segment,omitempty"`
	Flag      string `json:"flag,omitempty"`
	Variation string `json:"variation,omitempty"`
}

// Flag is one flag with its configuration in the ruleset's environment.
// A server_only flag in a client document carries no configuration.
type Flag struct {
	Key               string         `json:"key"`
	Type              string         `json:"type"`
	AutomaticExposure bool           `json:"automatic_exposure"`
	ServerOnly        bool           `json:"server_only,omitempty"`
	Variations        []Variation    `json:"variations,omitempty"`
	On                bool           `json:"on"`
	OffVariation      string         `json:"off_variation,omitempty"`
	Fallthrough       string         `json:"fallthrough,omitempty"`
	Prerequisites     []Prerequisite `json:"prerequisites,omitempty"`
	Overrides         []Override     `json:"overrides,omitempty"`
	Rules             []Rule         `json:"rules,omitempty"`
}

// MarshalJSON encodes a server_only flag with its key, type and
// automatic_exposure only: a client document carries no configuration for
// it. Any other flag encodes field by field.
func (f Flag) MarshalJSON() ([]byte, error) {
	if f.ServerOnly {
		return json.Marshal(struct {
			Key               string `json:"key"`
			Type              string `json:"type"`
			AutomaticExposure bool   `json:"automatic_exposure"`
			ServerOnly        bool   `json:"server_only"`
		}{f.Key, f.Type, f.AutomaticExposure, true})
	}
	type fields Flag
	return json.Marshal(fields(f))
}

// Variation is one named value a flag can serve.
type Variation struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// Prerequisite requires another flag of the project to evaluate to a
// variation for the same context.
type Prerequisite struct {
	Flag      string `json:"flag"`
	Variation string `json:"variation"`
}

// Override serves a fixed variation to one unit ID of one unit type.
type Override struct {
	UnitType  string `json:"unit_type"`
	UnitID    string `json:"unit_id"`
	Variation string `json:"variation"`
}

// Rule is one ordered AND-set of conditions and what it serves.
type Rule struct {
	ID         string      `json:"id"`
	Conditions []Condition `json:"conditions"`
	Serve      Serve       `json:"serve"`
}

// Serve kinds.
const (
	ServeVariation  = "variation"
	ServeRollout    = "rollout"
	ServeExperiment = "experiment"
)

// Serve is what a matching rule serves. Fields apply per kind: variation
// uses Variation; rollout uses UnitType, Variation, Percentage and Salt;
// experiment uses Experiment and the running experiment's frozen fields
// the ruleset materializes next to it.
type Serve struct {
	Kind              string   `json:"kind"`
	Variation         string   `json:"variation,omitempty"`
	UnitType          string   `json:"unit_type,omitempty"`
	Percentage        int      `json:"percentage,omitempty"`
	Salt              string   `json:"salt,omitempty"`
	Experiment        string   `json:"experiment,omitempty"`
	InclusionSalt     string   `json:"inclusion_salt,omitempty"`
	VariationSalt     string   `json:"variation_salt,omitempty"`
	TrafficAllocation int      `json:"traffic_allocation,omitempty"`
	Weights           []Weight `json:"weights,omitempty"`
}

// Weight is one variation's share of an experiment's allocated traffic, in
// basis points; weights sum to 10000 and their order fixes the ranges.
type Weight struct {
	Variation string `json:"variation"`
	Weight    int    `json:"weight"`
}

// Decode reads a ruleset document. It fails only when the document is not
// a ruleset this evaluator can read: malformed JSON, an unsupported
// schema_version or an unknown scope. Dangling references and invalid
// conditions do not fail decoding; evaluating a flag that reaches one
// returns reason error with inconsistent_ruleset, so one defective flag
// never takes the others down.
func Decode(data []byte) (*Ruleset, error) {
	var rs Ruleset
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("decode ruleset: %w", err)
	}
	if rs.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("decode ruleset: unsupported schema_version %d, this evaluator reads %d", rs.SchemaVersion, SchemaVersion)
	}
	switch rs.Scope {
	case ScopeServer, ScopeClient:
	default:
		return nil, fmt.Errorf("decode ruleset: unknown scope %q", rs.Scope)
	}
	rs.index = buildIndex(&rs)
	return &rs, nil
}

// errInconsistent marks a reference or condition the document cannot
// satisfy; evaluation reports it as inconsistent_ruleset.
var errInconsistent = errors.New("inconsistent ruleset")

// index holds the flag and segment maps and compiled conditions of a decoded ruleset.
type index struct {
	flags    map[string]*compiledFlag
	segments map[string]*compiledSegment
}

type compiledFlag struct {
	*Flag
	variations map[string]json.RawMessage
	rules      [][]compiledCondition
}

type compiledSegment struct {
	conditions []compiledCondition
}

func buildIndex(rs *Ruleset) *index {
	types := make(map[string]AttributeType, len(rs.Attributes)+len(rs.UnitTypes))
	for _, a := range rs.Attributes {
		types[a.Key] = a.Type
	}
	units := make(map[string]bool, len(rs.UnitTypes))
	for _, u := range rs.UnitTypes {
		units[u] = true
		types[u] = TypeString
	}
	c := compiler{types: types, units: units}

	idx := &index{
		flags:    make(map[string]*compiledFlag, len(rs.Flags)),
		segments: make(map[string]*compiledSegment, len(rs.Segments)),
	}
	for i := range rs.Segments {
		s := &rs.Segments[i]
		cs := &compiledSegment{conditions: make([]compiledCondition, len(s.Conditions))}
		for j, cond := range s.Conditions {
			if cond.Kind != ConditionAttribute {
				cs.conditions[j] = compiledCondition{err: fmt.Errorf("%w: segment %q has a %q condition", errInconsistent, s.Key, cond.Kind)}
				continue
			}
			cs.conditions[j] = c.compile(cond)
		}
		idx.segments[s.Key] = cs
	}
	for i := range rs.Flags {
		f := &rs.Flags[i]
		cf := &compiledFlag{
			Flag:       f,
			variations: make(map[string]json.RawMessage, len(f.Variations)),
			rules:      make([][]compiledCondition, len(f.Rules)),
		}
		for _, v := range f.Variations {
			cf.variations[v.Key] = v.Value
		}
		for j, r := range f.Rules {
			conds := make([]compiledCondition, len(r.Conditions))
			for k, cond := range r.Conditions {
				conds[k] = c.compile(cond)
			}
			cf.rules[j] = conds
		}
		idx.flags[f.Key] = cf
	}
	return idx
}
