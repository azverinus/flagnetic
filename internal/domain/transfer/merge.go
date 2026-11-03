package transfer

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Planned is an entity a merge creates or updates.
type Planned struct {
	// Index is the entity's place in its section of the document.
	Index int
	Key   string
	// Patch turns the entity as the project has it into the entity as the
	// merge leaves it, over the entity's entry of the document; nil for a
	// created entity.
	Patch []PatchOperation
}

// SectionPlan is what a merge does to the entities of one section, in the
// order of the document.
type SectionPlan struct {
	Created []Planned
	Updated []Planned
}

// Plan is what merging a document into a project would create and update. A
// merge deletes nothing, and the experiments are not in it.
type Plan struct {
	Environments SectionPlan
	UnitTypes    SectionPlan
	Attributes   SectionPlan
	Segments     SectionPlan
	Flags        SectionPlan
}

// Compare returns what applying doc, as Effective leaves it, to the project
// whose document is current creates and updates. An entity the two agree on
// is in neither list: whether two entities agree is whether the document
// writes them alike, which is the patch between them being empty. A section
// doc lacks plans nothing.
func Compare(current, doc Document) Plan {
	return Plan{
		Environments: compareSection(current.Environments, doc.Environments, func(e Environment) string { return e.Key }, entryValue, nil),
		UnitTypes:    compareSection(current.UnitTypes, doc.UnitTypes, func(u UnitType) string { return u.Key }, entryValue, nil),
		Attributes: compareSection(current.Attributes, doc.Attributes, func(a Attribute) string { return a.Key }, entryValue,
			// A standard attribute every project has is never created or
			// changed by a document.
			func(a Attribute) bool { _, standard := workspace.StandardAttributeType(a.Key); return standard }),
		Segments: compareSection(current.Segments, doc.Segments, func(s Segment) string { return s.Key }, segmentEntry, nil),
		Flags:    compareSection(current.Flags, doc.Flags, func(f Flag) string { return f.Key }, flagEntry, nil),
	}
}

func compareSection[T any](current, doc []T, key func(T) string, entry func(any) any, skip func(T) bool) SectionPlan {
	have := make(map[string]T, len(current))
	for _, e := range current {
		have[key(e)] = e
	}
	var out SectionPlan
	for i, e := range doc {
		if skip != nil && skip(e) {
			continue
		}
		k := key(e)
		before, ok := have[k]
		if !ok {
			out.Created = append(out.Created, Planned{Index: i, Key: k})
			continue
		}
		if patch := patchOf(entry(before), entry(e)); len(patch) > 0 {
			out.Updated = append(out.Updated, Planned{Index: i, Key: k, Patch: patch})
		}
	}
	return out
}

// segmentEntry and flagEntry return an entity as the document writes it,
// every list there and in the order a document keeps.
func segmentEntry(v any) any {
	s := v.(Segment)
	s.Conditions = nonNil(s.Conditions)
	return entryValue(s)
}

func flagEntry(v any) any {
	f, err := v.(Flag).canonical()
	if err != nil {
		return entryValue(v)
	}
	return entryValue(f)
}

// Effective returns doc as it applies to the project whose document is
// current: what a document that names a flag leaves of the flag is its
// definition and the configurations it holds, so the configuration in an
// environment the document does not hold stays as the project has it; and
// what the document leaves open it takes from the project. A rule without an
// identifier that equals a rule the project has in that configuration is that
// rule, and a rollout without a salt keeps the one it has, so that a document
// written by hand, with neither, is the same project when imported again; a
// rule that equals none is new, and is given both when it is written. The
// order of overrides and prerequisites is the project's where the same
// ones are named, since it carries no meaning a document can write.
func Effective(current, doc Document) Document {
	if doc.Flags == nil {
		return doc
	}
	have := make(map[string]Flag, len(current.Flags))
	for _, f := range current.Flags {
		have[f.Key] = f
	}
	out := doc
	out.Flags = make([]Flag, len(doc.Flags))
	for i, f := range doc.Flags {
		out.Flags[i] = f
		before, ok := have[f.Key]
		if !ok {
			continue
		}
		merged := make(map[string]Configuration, len(before.Configurations))
		maps.Copy(merged, before.Configurations)
		for env, c := range f.Configurations {
			if was, ok := before.Configurations[env]; ok {
				c = c.adopting(was)
			}
			merged[env] = c
		}
		out.Flags[i].Configurations = merged
	}
	return out
}

// adopting returns c, a configuration a document gives, as it applies to
// was, the one the project has.
func (c Configuration) adopting(was Configuration) Configuration {
	out := c
	out.Prerequisites = inOrderOf(c.Prerequisites, was.Prerequisites, func(a, b Prerequisite) bool { return a.Flag == b.Flag })
	out.Overrides = inOrderOf(c.Overrides, was.Overrides, func(a, b Override) bool { return a.UnitType == b.UnitType && a.UnitID == b.UnitID })
	out.Rules = adoptRules(c.Rules, was.Rules)
	return out
}

// inOrderOf returns doc's elements ordered as the ones of cur they are the
// same as, the others after them in the order they have.
func inOrderOf[T any](doc, cur []T, same func(a, b T) bool) []T {
	if len(doc) == 0 || len(cur) == 0 {
		return doc
	}
	out := make([]T, 0, len(doc))
	used := make([]bool, len(doc))
	for _, c := range cur {
		for i, d := range doc {
			if !used[i] && same(c, d) {
				out, used[i] = append(out, d), true
				break
			}
		}
	}
	for i, d := range doc {
		if !used[i] {
			out = append(out, d)
		}
	}
	return out
}

// adoptRules returns doc, the rules a document gives, with the identifier and
// salt each of them lacks that the project's rule it is has.
func adoptRules(doc, cur []Rule) []Rule {
	if len(doc) == 0 || len(cur) == 0 {
		return doc
	}
	out := slices.Clone(doc)
	taken := map[string]bool{}
	for i, r := range out {
		if ruleID, ok := normalizedRuleID(r.ID); ok {
			if j := slices.IndexFunc(cur, func(c Rule) bool { return c.ID == ruleID }); j >= 0 {
				taken[ruleID] = true
				out[i].Serve.Salt = adoptedSalt(r, cur[j])
			}
		}
	}
	for i, r := range out {
		if r.ID != "" {
			continue
		}
		for _, c := range cur {
			if !taken[c.ID] && sameRule(r, c) {
				taken[c.ID] = true
				out[i].ID, out[i].Serve.Salt = c.ID, adoptedSalt(r, c)
				break
			}
		}
	}
	return out
}

// adoptedSalt is the salt a rollout of the document has: its own, or the
// one the rollout it is has.
func adoptedSalt(r, was Rule) string {
	if r.Serve.Kind == flags.ServeRollout && r.Serve.Salt == "" && was.Serve.Kind == flags.ServeRollout {
		return was.Serve.Salt
	}
	return r.Serve.Salt
}

// sameRule reports whether the document's rule r is the project's rule c:
// they read alike, the salt compared when r has one.
func sameRule(r, c Rule) bool {
	withSalt := r.Serve.Salt != ""
	return ruleContent(r, withSalt) == ruleContent(c, withSalt)
}

func ruleContent(r Rule, withSalt bool) string {
	s := r.Serve
	if !withSalt {
		s.Salt = ""
	}
	raw, _ := json.Marshal(struct {
		Description string
		Conditions  []Condition
		Serve       Serve
	}{r.Description, nonNil(r.Conditions), s})
	return string(raw)
}

// normalizedRuleID returns the identifier a rule is known by, whatever the
// case it was written in, and whether id is one.
func normalizedRuleID(s string) (string, bool) {
	ruleID, err := id.Parse[flags.Rule](s)
	if err != nil {
		return "", false
	}
	return ruleID.String(), true
}

// CheckMerge returns the refusal of d as a document to merge into the
// project whose document is current, nil when it holds what a document
// alone, read with the project, can tell. Every fault is reported at once,
// with a pointer into the document, in the order of the document. No section
// is required.
//
// It judges what no context judges alone, as CheckCreate does, and besides
// a type that changes, which an existing flag or attribute never does, and
// an experiment, which a merge cannot import yet.
func CheckMerge(d, current Document) error {
	var f faults
	for i := range d.Experiments {
		f.add(Pointer("experiments", i), "unsupported", "a merge cannot import experiments yet", nil)
	}
	f.uniqueKeys(d)
	f.declarations(d)
	f.mergedConfigurations(d, current)
	f.types(d, current)
	f.salts(d, keptSalts(d, current))
	if len(f) == 0 {
		return nil
	}
	return apperrors.Invalid(invalidDocument, invalidDocumentDetail, f...)
}

// mergedConfigurations refuses a configuration for an environment that
// neither the document nor the project has.
func (f *faults) mergedConfigurations(d, current Document) {
	environments := map[string]bool{}
	for _, e := range current.Environments {
		environments[e.Key] = true
	}
	for _, e := range d.Environments {
		environments[e.Key] = true
	}
	for i, fl := range d.Flags {
		for _, env := range sortedConfigurationKeys(fl.Configurations) {
			if !environments[env] {
				f.add(Pointer("flags", i, "configurations", env), "unknown_environment",
					"neither the document nor the project has an environment "+strconv.Quote(env), map[string]any{"environment": env})
			}
		}
	}
}

// types refuses a flag or an attribute the project has as another type: a
// type never changes, since everything written against it relies on it.
func (f *faults) types(d, current Document) {
	attributeTypes := map[string]string{}
	for _, a := range current.Attributes {
		attributeTypes[a.Key] = a.Type
	}
	for i, a := range d.Attributes {
		if typ, ok := attributeTypes[a.Key]; ok && typ != a.Type {
			f.add(Pointer("attributes", i, "type"), "type_immutable", "an attribute's type never changes", map[string]any{"type": typ})
		}
	}
	flagTypes := map[string]string{}
	for _, fl := range current.Flags {
		flagTypes[fl.Key] = fl.Type
	}
	for i, fl := range d.Flags {
		if typ, ok := flagTypes[fl.Key]; ok && typ != fl.Type {
			f.add(Pointer("flags", i, "type"), "type_immutable", "a flag's type never changes; create a new flag instead", map[string]any{"type": typ})
		}
	}
}

// keptSalts returns the rollout salts the project keeps once d is merged: the
// ones of every configuration the document does not give, which no rollout
// of the document may reuse unless it is the same rule of the same flag.
func keptSalts(d, current Document) map[string]saltOwner {
	named := map[string]map[string]Configuration{}
	for _, f := range d.Flags {
		named[f.Key] = f.Configurations
	}
	kept := map[string]saltOwner{}
	for _, fl := range current.Flags {
		for _, env := range sortedConfigurationKeys(fl.Configurations) {
			if _, replaced := named[fl.Key][env]; replaced {
				continue
			}
			for _, r := range fl.Configurations[env].Rules {
				if r.Serve.Kind != flags.ServeRollout || r.Serve.Salt == "" {
					continue
				}
				if _, taken := kept[r.Serve.Salt]; !taken {
					kept[r.Serve.Salt] = saltOwner{flag: fl.Key, rule: r.ID}
				}
			}
		}
	}
	return kept
}
