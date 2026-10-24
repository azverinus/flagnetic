package flags

import (
	"slices"
	"strconv"
)

// Requirements are, by flag key, the keys of the flags each flag's
// configuration in one environment requires, through its prerequisites and
// its rules' prerequisite conditions, archived flags included: an archived
// flag can be restored, and its chains with it.
type Requirements map[string][]string

// requiredBy returns the keys of the flags c requires, in the order it
// names them, each once.
func (c Configuration) requiredBy() []string {
	var out []string
	add := func(key string) {
		if !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	for _, p := range c.Prerequisites {
		add(p.Flag)
	}
	for _, r := range c.Rules {
		for _, cond := range r.Conditions {
			if cond.Kind == ConditionPrerequisite {
				add(cond.Flag)
			}
		}
	}
	return out
}

// requirement is one flag a configuration requires, and the pointer to
// where the configuration names it.
type requirement struct {
	pointer, flag string
}

// requirements returns every place c names a flag it requires.
func (c Configuration) requirements() []requirement {
	var out []requirement
	for i, p := range c.Prerequisites {
		out = append(out, requirement{"/prerequisites/" + strconv.Itoa(i) + "/flag", p.Flag})
	}
	for i, r := range c.Rules {
		for j, cond := range r.Conditions {
			if cond.Kind == ConditionPrerequisite {
				out = append(out, requirement{"/rules/" + strconv.Itoa(i) + "/conditions/" + strconv.Itoa(j) + "/flag", cond.Flag})
			}
		}
	}
	return out
}

// chains refuses a configuration of the flag keyed self whose requirements,
// with every other flag's in the environment as reqs has them, would form a
// cycle, which evaluation could never finish, or a chain of more than 5
// steps, which no one could follow when explaining a result. Each refusal
// points at where the configuration names the flag that closes the cycle or
// starts the chain, with the chain's flag keys as its path.
func (ck *check) chains(self string, c Configuration, reqs Requirements) {
	graph := make(Requirements, len(reqs)+1)
	for k, v := range reqs {
		graph[k] = v
	}
	graph[self] = c.requiredBy()
	edges := c.requirements()
	cycle := false
	for _, e := range edges {
		if path := graph.path(e.flag, self, map[string]bool{}); path != nil {
			ck.add(e.pointer, "prerequisite_cycle", "the flags would require each other in a cycle",
				map[string]any{"path": append([]string{self}, path...)})
			cycle = true
		}
	}
	if cycle {
		return
	}
	up := graph.longestRequiring(self)
	for _, e := range edges {
		path := append(slices.Clone(up), graph.longestRequired(e.flag, map[string]bool{})...)
		if len(path)-1 > maxPrerequisiteDepth {
			ck.add(e.pointer, "prerequisite_too_deep", "a chain of prerequisites has at most 5 steps",
				map[string]any{"path": path, "max": maxPrerequisiteDepth})
		}
	}
}

// path returns the flag keys from from to to along requirements, both
// included, or nil when from does not lead to to.
func (g Requirements) path(from, to string, seen map[string]bool) []string {
	if from == to {
		return []string{to}
	}
	if seen[from] {
		return nil
	}
	seen[from] = true
	for _, next := range g[from] {
		if rest := g.path(next, to, seen); rest != nil {
			return append([]string{from}, rest...)
		}
	}
	return nil
}

// longestRequired returns the longest chain of flag keys from from down its
// requirements, from included; the first such chain in the order flags name
// their requirements.
func (g Requirements) longestRequired(from string, seen map[string]bool) []string {
	if seen[from] {
		return nil
	}
	seen[from] = true
	defer delete(seen, from)
	var longest []string
	for _, next := range g[from] {
		if chain := g.longestRequired(next, seen); len(chain) > len(longest) {
			longest = chain
		}
	}
	return append([]string{from}, longest...)
}

// longestRequiring returns the longest chain of flag keys requiring to, one
// after the other, ending with to; the first such chain by flag key.
func (g Requirements) longestRequiring(to string) []string {
	requiring := map[string][]string{}
	keys := make([]string, 0, len(g))
	for k := range g {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		for _, req := range g[k] {
			requiring[req] = append(requiring[req], k)
		}
	}
	var walk func(key string, seen map[string]bool) []string
	walk = func(key string, seen map[string]bool) []string {
		if seen[key] {
			return nil
		}
		seen[key] = true
		defer delete(seen, key)
		var longest []string
		for _, prev := range requiring[key] {
			if chain := walk(prev, seen); len(chain) > len(longest) {
				longest = chain
			}
		}
		return append(longest, key)
	}
	return walk(to, map[string]bool{})
}

// EnvironmentConfiguration is a flag's configuration with the key of its
// environment, by which a reference names it.
type EnvironmentConfiguration struct {
	Environment   string
	Configuration Configuration
}

// Dependent is a flag whose configuration in one environment requires a
// variation of another flag, as a prerequisite or in a rule's condition.
type Dependent struct {
	// Flag is the key of the requiring flag.
	Flag string
	// Environment is the key of the environment it requires it in.
	Environment string
	// Variation is the key of the variation it requires.
	Variation string
}

// servesVariation reports whether c names the variation key of its own
// flag: as the off or fallthrough variation, in a rule's serve or in an
// override.
func (c Configuration) servesVariation(key string) bool {
	return c.OffVariation == key || c.Fallthrough == key ||
		slices.ContainsFunc(c.Rules, func(r Rule) bool { return r.Serve.Variation == key }) ||
		slices.ContainsFunc(c.Overrides, func(o Override) bool { return o.Variation == key })
}

// VariationReferences returns what still names one of the variations of f
// in removed: f's configurations in configs naming it, one reference per
// environment in the order of configs, then the flags in dependents
// requiring it, one per flag and environment in the order of dependents.
func VariationReferences(f Flag, removed []string, configs []EnvironmentConfiguration, dependents []Dependent) []Reference {
	var refs []Reference
	for _, ec := range configs {
		if slices.ContainsFunc(removed, ec.Configuration.servesVariation) {
			refs = append(refs, Reference{Kind: "flag", Key: f.Key, Environment: ec.Environment})
		}
	}
	var requiring []Dependent
	for _, d := range dependents {
		if slices.Contains(removed, d.Variation) {
			requiring = append(requiring, d)
		}
	}
	return append(refs, DependentReferences(requiring)...)
}

// DependentReferences returns the flags in dependents, one reference per
// flag and environment in the order of dependents: what archiving the flag
// they require would leave requiring a flag no SDK receives.
func DependentReferences(dependents []Dependent) []Reference {
	var refs []Reference
	for _, d := range dependents {
		r := Reference{Kind: "flag", Key: d.Flag, Environment: d.Environment}
		if !slices.Contains(refs, r) {
			refs = append(refs, r)
		}
	}
	return refs
}

// ExperimentVariationReferences returns the experiments in experiments that
// list one of the variations in removed, whatever their status: an
// experiment's weights and the exposures counted for them mean nothing
// without the variation.
func ExperimentVariationReferences(experiments []ExperimentRef, removed []string) []Reference {
	var refs []Reference
	for _, e := range experiments {
		if slices.ContainsFunc(e.Weights, func(w ExperimentWeight) bool { return slices.Contains(removed, w.Variation) }) {
			refs = append(refs, Reference{Kind: "experiment", Key: e.Key})
		}
	}
	return refs
}

// ExperimentArchiveReferences returns the draft and running experiments in
// experiments: archiving their flag would leave them comparing variations
// of a flag no SDK receives.
func ExperimentArchiveReferences(experiments []ExperimentRef) []Reference {
	var refs []Reference
	for _, e := range experiments {
		if e.Status == ExperimentDraft || e.Status == ExperimentRunning {
			refs = append(refs, Reference{Kind: "experiment", Key: e.Key})
		}
	}
	return refs
}
