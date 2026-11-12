package transfer

import (
	"slices"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Deletions are what a replace deletes once the document is merged: every
// entity of the project the document does not name, by kind, in the order
// they are deleted. A kind is deleted before the kinds its entities can be
// used by, so that what an absent flag required is free when the flag has
// gone.
type Deletions struct {
	Experiments  []string
	Flags        []string
	Segments     []string
	Attributes   []string
	UnitTypes    []string
	Environments []string

	// NotDraft are the experiments the document does not name that cannot be
	// deleted, since only a draft is: deleting one would erase the exposures
	// and the history it has.
	NotDraft []ExperimentStatus
	// Held are the experiments the document names whose flag it does not: the
	// flag would take them with it.
	Held []flags.Reference
}

// ExperimentStatus is an experiment with its status.
type ExperimentStatus struct{ Key, Status string }

// Empty reports whether nothing is to be deleted.
func (d Deletions) Empty() bool {
	return len(d.Experiments)+len(d.Flags)+len(d.Segments)+len(d.Attributes)+len(d.UnitTypes)+len(d.Environments) == 0
}

// Refusal returns the refusal of a replace that cannot be applied whatever
// the project's state is, nil when there is none: 409 not_draft naming every
// experiment that is not a draft, with a violation each; a flag the
// document's experiments hold is reported by the deletion itself.
func (d Deletions) Refusal() error {
	if len(d.NotDraft) == 0 {
		return nil
	}
	vs := make([]apperrors.Violation, len(d.NotDraft))
	for i, e := range d.NotDraft {
		vs[i] = apperrors.Violation{
			Pointer: Pointer("experiments"), Code: "not_draft",
			Detail: "the experiment " + e.Key + " is " + e.Status + ", not a draft, and the document does not name it: only a draft is deleted",
			Params: map[string]any{"key": e.Key, "status": e.Status},
		}
	}
	err := apperrors.Conflict("not_draft", "a replace deletes only draft experiments; the document must name each experiment that has started")
	err.Violations = vs
	return err
}

// PlanDeletions returns what replacing the project whose document is current
// with doc deletes: whatever the project has that doc does not name, but the
// standard attributes every project has. Flags are ordered with the flags
// that require another first.
func PlanDeletions(current, doc Document) Deletions {
	var out Deletions
	named := func(keys []string) map[string]bool {
		m := make(map[string]bool, len(keys))
		for _, k := range keys {
			m[k] = true
		}
		return m
	}
	absent := func(have, doc []string) []string {
		in := named(doc)
		var out []string
		for _, k := range have {
			if !in[k] {
				out = append(out, k)
			}
		}
		return out
	}
	experimentKeys := func(es []Experiment) []string { return mapKeys(es, func(e Experiment) string { return e.Key }) }
	for _, key := range absent(experimentKeys(current.Experiments), experimentKeys(doc.Experiments)) {
		i := slices.IndexFunc(current.Experiments, func(e Experiment) bool { return e.Key == key })
		if status := current.Experiments[i].Status; status != "draft" {
			out.NotDraft = append(out.NotDraft, ExperimentStatus{Key: key, Status: status})
			continue
		}
		out.Experiments = append(out.Experiments, key)
	}
	out.Flags = orderedFlags(current, absent(mapKeys(current.Flags, func(f Flag) string { return f.Key }),
		mapKeys(doc.Flags, func(f Flag) string { return f.Key })))
	out.Segments = absent(mapKeys(current.Segments, func(s Segment) string { return s.Key }),
		mapKeys(doc.Segments, func(s Segment) string { return s.Key }))
	out.Attributes = absent(mapKeys(current.Attributes, func(a Attribute) string { return a.Key }),
		mapKeys(doc.Attributes, func(a Attribute) string { return a.Key }))
	out.Attributes = slices.DeleteFunc(out.Attributes, func(k string) bool { _, standard := workspace.StandardAttributeType(k); return standard })
	out.UnitTypes = absent(mapKeys(current.UnitTypes, func(u UnitType) string { return u.Key }),
		mapKeys(doc.UnitTypes, func(u UnitType) string { return u.Key }))
	out.Environments = absent(mapKeys(current.Environments, func(e Environment) string { return e.Key }),
		mapKeys(doc.Environments, func(e Environment) string { return e.Key }))

	deleting := named(out.Flags)
	for _, e := range doc.Experiments {
		if deleting[e.Flag] {
			out.Held = append(out.Held, flags.Reference{Kind: "experiment", Key: e.Key})
		}
	}
	return out
}

// orderedFlags returns the keys of the flags to delete in an order that
// deletes a flag only after every flag to delete that requires it: a flag
// another requires is never deleted, and the requiring one is going too.
func orderedFlags(current Document, keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	deleting := map[string]bool{}
	for _, k := range keys {
		deleting[k] = true
	}
	// requiredBy maps a flag to the flags to delete that require it, in any
	// environment, as a prerequisite or in a rule's prerequisite condition:
	// either keeps the required flag in use.
	requiredBy := map[string][]string{}
	require := func(required, by string) {
		if deleting[required] && !slices.Contains(requiredBy[required], by) {
			requiredBy[required] = append(requiredBy[required], by)
		}
	}
	for _, f := range current.Flags {
		if !deleting[f.Key] {
			continue
		}
		for _, env := range sortedConfigurationKeys(f.Configurations) {
			c := f.Configurations[env]
			for _, p := range c.Prerequisites {
				require(p.Flag, f.Key)
			}
			for _, rule := range c.Rules {
				for _, cond := range rule.Conditions {
					if cond.Kind == "prerequisite" {
						require(cond.Flag, f.Key)
					}
				}
			}
		}
	}
	var out []string
	done := map[string]bool{}
	var visit func(string)
	visit = func(k string) {
		if done[k] {
			return
		}
		done[k] = true
		for _, dependent := range requiredBy[k] {
			visit(dependent)
		}
		out = append(out, k)
	}
	for _, k := range keys {
		visit(k)
	}
	return out
}
