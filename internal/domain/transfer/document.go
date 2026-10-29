// Package transfer is the domain of moving a project's configuration in and
// out of the platform as one project document: the document's types, its
// byte-stable encodings in JSON and YAML, and its strict decoding. The
// document is a contract read by people and tools the server never sees, so
// it is versioned apart from the management API and its names are spelled
// out here and not borrowed from any other type.
package transfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Version is the schema_version this server writes.
const Version = 1

// SupportedVersions are the schema_versions this server reads: every one it
// ever wrote, since a document kept in git outlives the server that wrote
// it.
var SupportedVersions = []int{1}

// Document is a project's whole configuration: what an export writes and an
// import reads. Identity is the key; no entity identifier travels, except
// the rule identifiers and rollout salts that keep a unit in its bucket.
//
// A section that is nil was absent from the document read, which an import
// into a project treats as untouched; an export always writes every section.
type Document struct {
	SchemaVersion int           `json:"schema_version"`
	Project       *Project      `json:"project"`
	Environments  []Environment `json:"environments"`
	UnitTypes     []UnitType    `json:"unit_types"`
	Attributes    []Attribute   `json:"attributes"`
	Segments      []Segment     `json:"segments"`
	Flags         []Flag        `json:"flags"`
	Experiments   []Experiment  `json:"experiments"`
}

// Project is the project's own fields.
type Project struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Environment is an environment; its place in the document's list is its
// sort order.
type Environment struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
}

// UnitType is a declared kind of unit.
type UnitType struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Attribute is a declared attribute. The standard attributes every project
// has are never listed.
type Attribute struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Segment is a named set of attribute conditions.
type Segment struct {
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Private     bool        `json:"private"`
	Conditions  []Condition `json:"conditions"`
}

// Condition is one comparison inside a rule or a segment, spelled as the
// management API spells it so that a person moving between the two sees one
// shape.
type Condition struct {
	Kind      string `json:"kind"`
	Attribute string `json:"attribute,omitempty"`
	Op        string `json:"op,omitempty"`
	// Value is a string, a number, a boolean or a list; absent for an
	// operator that takes none.
	Value     any    `json:"value,omitempty"`
	Segment   string `json:"segment,omitempty"`
	Flag      string `json:"flag,omitempty"`
	Variation string `json:"variation,omitempty"`
}

// Flag is a flag's definition with its configuration in every environment.
type Flag struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Owner       string   `json:"owner"`
	Tags        []string `json:"tags"`
	Lifetime    string   `json:"lifetime"`
	// ExpectedRemovalAt is null for a permanent flag.
	ExpectedRemovalAt *time.Time  `json:"expected_removal_at"`
	AutomaticExposure bool        `json:"automatic_exposure"`
	Archived          bool        `json:"archived"`
	Variations        []Variation `json:"variations"`
	// Configurations are keyed by environment key.
	Configurations map[string]Configuration `json:"configurations"`
}

// Variation is one of the values a flag serves.
type Variation struct {
	Key string `json:"key"`
	// Value is JSON of the flag's type.
	Value       json.RawMessage `json:"value"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
}

// Configuration is what a flag serves in one environment.
type Configuration struct {
	On            bool           `json:"on"`
	OffVariation  string         `json:"off_variation"`
	Fallthrough   string         `json:"fallthrough"`
	Prerequisites []Prerequisite `json:"prerequisites"`
	Overrides     []Override     `json:"overrides"`
	Rules         []Rule         `json:"rules"`
}

// Prerequisite requires another flag to serve one of its variations.
type Prerequisite struct {
	Flag      string `json:"flag"`
	Variation string `json:"variation"`
}

// Override pins a variation for one unit. Who wrote it does not travel: the
// importer is its author.
type Override struct {
	UnitType  string `json:"unit_type"`
	UnitID    string `json:"unit_id"`
	Variation string `json:"variation"`
	Note      string `json:"note"`
}

// Rule is an ordered set of conditions, all of which must hold for it to
// serve.
type Rule struct {
	// ID keeps the rule's identity across instances, so an SDK's rule_id
	// stays stable; a hand-written document may leave it out.
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Conditions  []Condition `json:"conditions"`
	Serve       Serve       `json:"serve"`
}

// Serve is what a matching rule serves, spelled as the management API
// spells it.
type Serve struct {
	Kind       string `json:"kind"`
	Variation  string `json:"variation,omitempty"`
	Experiment string `json:"experiment,omitempty"`
	UnitType   string `json:"unit_type,omitempty"`
	// Percentage is a rollout's share in basis points; nil for another
	// kind, since 0 is a share too.
	Percentage *int `json:"percentage,omitempty"`
	// Salt is a rollout's; it decides which units fall in the share, so it
	// travels. A hand-written document may leave it out.
	Salt string `json:"salt,omitempty"`
}

// Experiment is an experiment as it stands, whatever its status.
type Experiment struct {
	Key               string                `json:"key"`
	Name              string                `json:"name"`
	Hypothesis        string                `json:"hypothesis"`
	Owner             string                `json:"owner"`
	Tags              []string              `json:"tags"`
	Flag              string                `json:"flag"`
	UnitType          string                `json:"unit_type"`
	Variations        []ExperimentVariation `json:"variations"`
	TrafficAllocation int                   `json:"traffic_allocation"`
	InclusionSalt     string                `json:"inclusion_salt"`
	VariationSalt     string                `json:"variation_salt"`
	Status            string                `json:"status"`
	ExpectedEndAt     *Date                 `json:"expected_end_at"`
	StartedAt         *time.Time            `json:"started_at"`
	StoppedAt         *time.Time            `json:"stopped_at"`
	ArchivedAt        *time.Time            `json:"archived_at"`
	Decision          *Decision             `json:"decision"`
}

// ExperimentVariation is one variation's share of an experiment's allocated
// traffic, in basis points. The list's order fixes the cumulative bucket
// ranges, so it is kept.
type ExperimentVariation struct {
	Variation string `json:"variation"`
	Weight    int    `json:"weight"`
}

// Decision is what stopping an experiment decided.
type Decision struct {
	Outcome         string `json:"outcome"`
	WinnerVariation string `json:"winner_variation"`
	Note            string `json:"note"`
}

// Date is a calendar day, written as 2006-01-02.
type Date struct{ time.Time }

// MarshalJSON writes the day.
func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.UTC().Format(time.DateOnly))
}

// UnmarshalJSON reads a day, or an instant of which the UTC day counts: a
// YAML reader hands a bare date over as an instant.
func (d *Date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		d.Time = t
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("not a date: %q", s)
	}
	y, m, day := t.UTC().Date()
	d.Time = time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	return nil
}

// canonicalJSON returns raw as compact JSON with object keys in byte order,
// so that a value stored with its keys in any order is written in one.
func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}
