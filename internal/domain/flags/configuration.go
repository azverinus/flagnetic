package flags

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Configuration is what one flag serves in one environment. Every flag has
// one in every environment of its project, each changing on its own.
type Configuration struct {
	Flag        FlagID
	Environment workspace.EnvironmentID
	// On is false while the flag serves its off variation to everyone.
	On bool
	// OffVariation is the key of the variation served while the flag is
	// off, and when a prerequisite fails.
	OffVariation string
	// Fallthrough is the key of the variation served when no rule matches.
	Fallthrough string
	// Prerequisites are the variations other flags must serve for this one
	// to be evaluated at all.
	Prerequisites []Prerequisite
	Overrides     []Override
	// Rules are checked in order; the first whose conditions all hold
	// serves.
	Rules     []Rule
	UpdatedAt time.Time
	// UpdatedBy is the member whose write made the configuration what it
	// is; nil for a project token.
	UpdatedBy *access.MemberID
	// Version counts the configuration's accepted changes, from 1.
	Version int64
}

// Prerequisite requires the flag keyed Flag to serve its variation keyed
// Variation to the same context.
type Prerequisite struct {
	Flag, Variation string
}

// Override pins a variation for one unit.
type Override struct {
	UnitType, UnitID, Variation string
	// Note says why, "" for nothing. It and AddedBy stay on the server: no
	// ruleset carries either.
	Note string
	// AddedBy is the member who last wrote the override; nil for a project
	// token.
	AddedBy *access.MemberID
}

// RuleID identifies a rule across the versions of its configuration.
type RuleID = id.Of[Rule]

// Rule is one ordered set of conditions, all of which a context meets for
// the rule to serve it.
type Rule struct {
	ID RuleID
	// Description stays on the server: no ruleset carries it.
	Description string
	Conditions  []Condition
	Serve       Serve
}

// The serve kinds.
const (
	ServeVariation  = eval.ServeVariation
	ServeRollout    = eval.ServeRollout
	ServeExperiment = eval.ServeExperiment
)

// Serve is what a rule serves: a variation, a rollout of one variation to a
// share of one unit type's units, the others continuing to the next rule, or
// an experiment of the flag.
type Serve struct {
	Kind      string
	Variation string
	// Experiment is the key of the experiment an experiment serve names.
	Experiment string
	// UnitType, Percentage and Salt are a rollout's: the unit type whose
	// units are bucketed, the share served in basis points, and the salt
	// mixed into each unit's bucket.
	UnitType   string
	Percentage int
	Salt       string
}

// InitialConfiguration returns the configuration f arrives with in
// environment, written by by at now: switched off, so creating a flag or an
// environment never changes what any application is served. A boolean flag
// serves false when off and true when on with no rule matching; another type
// its first variation either way.
func InitialConfiguration(f Flag, environment workspace.EnvironmentID, by *access.MemberID, now time.Time) Configuration {
	off, fallthroughVariation := f.Variations[0].Key, f.Variations[0].Key
	if f.Type == TypeBoolean {
		off, fallthroughVariation = "false", "true"
	}
	return Configuration{
		Flag: f.ID, Environment: environment, OffVariation: off, Fallthrough: fallthroughVariation,
		Prerequisites: []Prerequisite{}, Overrides: []Override{}, Rules: []Rule{},
		UpdatedAt: now, UpdatedBy: by, Version: 1,
	}
}

// Bounds of a configuration, which every SDK evaluates on every call.
const (
	maxRules             = 50
	maxOverrides         = 200
	maxNoteLength        = 200
	maxPercentage        = 10_000
	maxPrerequisiteDepth = 5
)

// MaxUnitIDBytes bounds a unit ID, in an override as in a context
// evaluated: SDKs hash it on every evaluation of a rollout.
const MaxUnitIDBytes = 256

// The refusal of an invalid write to a configuration.
const (
	invalidConfiguration  = "invalid_configuration"
	configurationNotValid = "the configuration is not valid"
)

// ConfigurationInput is a configuration as a write gives it whole.
type ConfigurationInput struct {
	On                        bool
	OffVariation, Fallthrough string
	Prerequisites             []Prerequisite
	Overrides                 []OverrideInput
	Rules                     []RuleInput
}

// OverrideInput is an override as a write gives it: a note that may be
// absent, and no author, who is the writer.
type OverrideInput struct {
	UnitType, UnitID, Variation string
	Note                        *string
}

// RuleInput is a rule as a write gives it: with the identifier of the rule
// of the current configuration it is, nil for a new rule.
type RuleInput struct {
	ID          *RuleID
	Description string
	Conditions  []Condition
	Serve       ServeInput
}

// ServeInput is a serve as a write gives it. Percentage is nil when absent;
// Salt is "" when absent, since only the server mints one.
type ServeInput struct {
	Kind, Variation, UnitType, Experiment string
	Percentage                            *int
	Salt                                  string
}

// Referents are what a configuration may name, as the handler loaded them
// under its project's row lock: every flag of the project, archived ones
// and the configured one included, the keys of its segments, its
// declarations, and the requirements between flags in the configuration's
// environment.
type Referents struct {
	Flags        []Flag
	Segments     []string
	Declarations Declarations
	Requirements Requirements
	// Experiments are the project's experiments, whatever their status.
	Experiments []ExperimentRef
}

// ExperimentRef is an experiment as a rule may name it, and as a flag's
// archival and variations are judged against it.
type ExperimentRef struct {
	Key string
	// Flag is the flag the experiment compares variations of.
	Flag FlagID
	// Status is draft, running, stopped or archived.
	Status string
	// UnitType is the unit type whose units the experiment splits.
	UnitType string
	// InclusionSalt and VariationSalt are the experiment's public salts.
	InclusionSalt, VariationSalt string
	// TrafficAllocation is the share of units included, in basis points.
	TrafficAllocation int
	// Weights are the variations the experiment lists with their shares in
	// basis points, in the order that fixes the cumulative bucket ranges.
	Weights []ExperimentWeight
}

// ExperimentWeight is one variation's share of an experiment's allocated
// traffic, in basis points.
type ExperimentWeight struct {
	Variation string
	Weight    int
}

// The statuses of an experiment a rule or a flag is judged by.
const (
	ExperimentDraft   = "draft"
	ExperimentRunning = "running"
)

func (r Referents) experiment(key string) (ExperimentRef, bool) {
	i := slices.IndexFunc(r.Experiments, func(e ExperimentRef) bool { return e.Key == key })
	if i < 0 {
		return ExperimentRef{}, false
	}
	return r.Experiments[i], true
}

func (r Referents) flag(key string) (Flag, bool) {
	i := slices.IndexFunc(r.Flags, func(f Flag) bool { return f.Key == key })
	if i < 0 {
		return Flag{}, false
	}
	return r.Flags[i], true
}

// Mint draws what a write gives what it creates: a new rule's identifier,
// and a new rollout's salt. The handler supplies both, since drawing either
// is not a rule of the domain.
type Mint struct {
	RuleID func() RuleID
	Salt   func() string
}

// Write is who makes a write to a configuration, and when.
type Write struct {
	// By is the member writing; nil for a project token.
	By  *access.MemberID
	Now time.Time
}

// Replace returns the configuration of f replaced by in, checked against r,
// its version incremented when anything changed, and what changed; or
// invalid_configuration with every field to fix. A rule keeps its
// identifier and, while it stays a rollout, its salt; an override left as it
// was keeps its author.
func (c Configuration) Replace(f Flag, in ConfigurationInput, r Referents, m Mint, w Write) (Configuration, Changed, error) {
	return c.write(c, f, in, r, m, w)
}

// Revert returns the configuration of f made recorded again by w, the
// document an earlier version of it was, whole: rule identifiers and salts,
// so the same units fall in every rollout again, and override notes and
// authors as they were. It is checked against r as a replace is, since a
// variation, segment, declaration or flag it names may be gone since.
func (c Configuration) Revert(f Flag, recorded Configuration, r Referents, w Write) (Configuration, Changed, error) {
	return c.write(recorded, f, recorded.input(), r, Mint{}, w)
}

// Copy returns the configuration of f made by w what source, f's
// configuration in another environment, serves: its rules with their
// identifiers and salts, so the same units fall in every rollout in both
// environments, its prerequisites and its off and fallthrough variations.
// It stays on or off as it is, so a copy never switches a flag on by
// accident, and keeps its overrides unless overrides asks for source's,
// notes and authors with them. It is checked against r, the requirements
// of c's environment, as a replace is. A configuration is not copied from
// its own environment, which would change nothing on purpose.
func (c Configuration) Copy(f Flag, source Configuration, overrides bool, r Referents, w Write) (Configuration, Changed, error) {
	if source.Environment == c.Environment {
		ck := newCheck(invalidConfiguration, configurationNotValid)
		ck.add("/from", "invalid_value", "a configuration is copied from another environment", nil)
		return c, Changed{}, ck.err()
	}
	doc := source
	doc.On = c.On
	if !overrides {
		doc.Overrides = c.Overrides
	}
	return c.write(doc, f, doc.input(), r, Mint{}, w)
}

// write returns c replaced by in, checked against r: a rule keeps the
// identifier and, while it stays a rollout, the salt of the rule of basis
// it names, and an override left as basis has it keeps its author. A
// replace's basis is c; a revert's or a copy's is the document it writes,
// which names every identifier and salt, so m draws nothing for them.
func (c Configuration) write(basis Configuration, f Flag, in ConfigurationInput, r Referents, m Mint, w Write,
) (Configuration, Changed, error) {
	ck := newCheck(invalidConfiguration, configurationNotValid)
	out := c
	out.On = in.On
	out.OffVariation = ck.variationOf("/off_variation", f, in.OffVariation)
	out.Fallthrough = ck.variationOf("/fallthrough", f, in.Fallthrough)
	out.Prerequisites = ck.prerequisites(in.Prerequisites, r)
	out.Overrides = ck.overrides(basis.Overrides, in.Overrides, f, r.Declarations, w.By)
	out.Rules = ck.rules(basis.Rules, in.Rules, f, r, m)
	ck.chains(f.Key, out, r.Requirements)
	if err := ck.err(); err != nil {
		return c, Changed{}, err
	}
	after, changed := c.changedTo(out, w)
	return after, changed, nil
}

// input returns c as a write gives it whole, every rule with its
// identifier, every rollout with its salt and every experiment serve with
// its experiment.
func (c Configuration) input() ConfigurationInput {
	in := ConfigurationInput{
		On: c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough, Prerequisites: c.Prerequisites,
		Overrides: make([]OverrideInput, len(c.Overrides)), Rules: make([]RuleInput, len(c.Rules)),
	}
	for i, o := range c.Overrides {
		in.Overrides[i] = OverrideInput{UnitType: o.UnitType, UnitID: o.UnitID, Variation: o.Variation, Note: &o.Note}
	}
	for i, rule := range c.Rules {
		serve := ServeInput{Kind: rule.Serve.Kind, Variation: rule.Serve.Variation, Experiment: rule.Serve.Experiment}
		if rule.Serve.Kind == ServeRollout {
			serve.UnitType, serve.Percentage, serve.Salt = rule.Serve.UnitType, &rule.Serve.Percentage, rule.Serve.Salt
		}
		in.Rules[i] = RuleInput{ID: &rule.ID, Description: rule.Description, Conditions: rule.Conditions, Serve: serve}
	}
	return in
}

// changedTo returns out stamped by w as the next version of c, and what
// changed; c itself when nothing did.
func (c Configuration) changedTo(out Configuration, w Write) (Configuration, Changed) {
	if out.sameDocument(c) {
		return c, Changed{}
	}
	out.UpdatedAt, out.UpdatedBy, out.Version = w.Now, w.By, c.Version+1
	switched := c
	switched.On = out.On
	return out, Changed{Any: true, Ruleset: !out.SameRuleset(c), Switched: out.On != c.On && out.SameRuleset(switched)}
}

// SameRuleset reports whether c and d carry the same ruleset: on or off,
// the off and fallthrough variations, the prerequisites, the overrides by
// unit type, unit ID and variation, and the rules with their identifiers,
// conditions and serves, salts included, each in order. Rule descriptions
// and override notes and authors stay on the server, and the version and
// who made it describe the write rather than what SDKs receive, so none of
// them counts. Whether a write changed what an environment's SDKs receive,
// and whether two environments differ for a flag, are judged by it alike.
func (c Configuration) SameRuleset(d Configuration) bool {
	return c.On == d.On && c.OffVariation == d.OffVariation && c.Fallthrough == d.Fallthrough &&
		slices.Equal(c.Prerequisites, d.Prerequisites) &&
		slices.EqualFunc(c.Overrides, d.Overrides, func(a, b Override) bool {
			return a.UnitType == b.UnitType && a.UnitID == b.UnitID && a.Variation == b.Variation
		}) &&
		slices.EqualFunc(c.Rules, d.Rules, func(a, b Rule) bool {
			return a.ID == b.ID && a.Serve == b.Serve && reflect.DeepEqual(a.Conditions, b.Conditions)
		})
}

// sameDocument reports whether c and d are the same document, what stays
// on the server included.
func (c Configuration) sameDocument(d Configuration) bool {
	return c.SameRuleset(d) &&
		slices.EqualFunc(c.Overrides, d.Overrides, func(a, b Override) bool {
			return a.Note == b.Note && sameMember(a.AddedBy, b.AddedBy)
		}) &&
		slices.EqualFunc(c.Rules, d.Rules, func(a, b Rule) bool { return a.Description == b.Description })
}

func sameMember(a, b *access.MemberID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// variationOf returns key, refusing at pointer one f has no variation for.
func (ck *check) variationOf(pointer string, f Flag, key string) string {
	if !f.hasVariation(key) {
		ck.add(pointer, "unknown_variation", "the flag "+strconv.Quote(f.Key)+" has no variation "+strconv.Quote(key),
			map[string]any{"flag": f.Key, "variation": key})
	}
	return key
}

func (f Flag) hasVariation(key string) bool {
	return slices.ContainsFunc(f.Variations, func(v Variation) bool { return v.Key == key })
}

// prerequisites checks a configuration's prerequisites, each naming a
// different flag, and returns them, never nil.
func (ck *check) prerequisites(in []Prerequisite, r Referents) []Prerequisite {
	out := make([]Prerequisite, 0, len(in))
	for i, p := range in {
		pointer := "/prerequisites/" + strconv.Itoa(i)
		if slices.ContainsFunc(out, func(q Prerequisite) bool { return q.Flag == p.Flag }) {
			ck.add(pointer+"/flag", "duplicate", "the configuration requires this flag already", nil)
		}
		ck.prerequisite(pointer, p, r)
		out = append(out, p)
	}
	return out
}

// prerequisite checks at pointer that p names a flag of the project that is
// not archived, and one of its variations. A flag naming itself is left to
// the check of chains, as the cycle it is.
func (ck *check) prerequisite(pointer string, p Prerequisite, r Referents) {
	f, ok := r.flag(p.Flag)
	switch {
	case !ok:
		ck.add(pointer+"/flag", "unknown_flag", "the project has no flag "+strconv.Quote(p.Flag),
			map[string]any{"flag": p.Flag})
	case f.ArchivedAt != nil:
		ck.add(pointer+"/flag", "archived_flag", "the flag "+strconv.Quote(p.Flag)+" is archived; restore it first",
			map[string]any{"flag": p.Flag})
	default:
		ck.variationOf(pointer+"/variation", f, p.Variation)
	}
}

// overrides checks a configuration's overrides, at most one per unit, and
// returns them, never nil. An override left as it was in previous keeps its
// author; any other is by's.
func (ck *check) overrides(previous []Override, in []OverrideInput, f Flag, d Declarations, by *access.MemberID) []Override {
	if len(in) > maxOverrides {
		ck.add("/overrides", "too_many", "a configuration has at most 200 overrides", map[string]any{"max": maxOverrides})
		return previous
	}
	out := make([]Override, 0, len(in))
	for i, o := range in {
		pointer := "/overrides/" + strconv.Itoa(i)
		ov := ck.override(pointer, o, f, d, by)
		if slices.ContainsFunc(out, ov.sameUnit) {
			ck.add(pointer+"/unit_id", "duplicate", "the configuration overrides this unit already", nil)
		}
		if j := slices.IndexFunc(previous, ov.sameUnit); j >= 0 && previous[j].Variation == ov.Variation && previous[j].Note == ov.Note {
			ov.AddedBy = previous[j].AddedBy
		}
		out = append(out, ov)
	}
	return out
}

func (o Override) sameUnit(p Override) bool { return o.UnitType == p.UnitType && o.UnitID == p.UnitID }

// override checks one override at pointer, written by by: a declared unit
// type, a unit ID of 1 to 256 bytes, a variation of f and a note of at most
// 200 characters once trimmed.
func (ck *check) override(pointer string, o OverrideInput, f Flag, d Declarations, by *access.MemberID) Override {
	if !d.units[o.UnitType] {
		ck.add(pointer+"/unit_type", "unknown_unit_type", "the project declares no unit type "+strconv.Quote(o.UnitType),
			map[string]any{"unit_type": o.UnitType})
	}
	switch {
	case o.UnitID == "":
		ck.add(pointer+"/unit_id", "invalid_value", "a unit ID is not empty", map[string]any{"min": 1})
	case len(o.UnitID) > MaxUnitIDBytes:
		ck.add(pointer+"/unit_id", "too_long", "a unit ID has at most 256 bytes", map[string]any{"max": MaxUnitIDBytes})
	}
	note := ""
	if o.Note != nil {
		note = strings.TrimSpace(*o.Note)
	}
	if utf8.RuneCountInString(note) > maxNoteLength {
		ck.add(pointer+"/note", "too_long", "a note has at most 200 characters", map[string]any{"max": maxNoteLength})
	}
	return Override{
		UnitType: o.UnitType, UnitID: o.UnitID, Variation: ck.variationOf(pointer+"/variation", f, o.Variation),
		Note: note, AddedBy: by,
	}
}

// rules checks a configuration's rules and returns them, never nil: a rule
// sent without an identifier is new and given one; one sent with an
// identifier is the rule of previous it names, each at most once.
func (ck *check) rules(previous []Rule, in []RuleInput, f Flag, r Referents, m Mint) []Rule {
	if len(in) > maxRules {
		ck.add("/rules", "too_many", "a configuration has at most 50 rules", map[string]any{"max": maxRules})
		return previous
	}
	out := make([]Rule, 0, len(in))
	for i, ri := range in {
		pointer := "/rules/" + strconv.Itoa(i)
		var before *Rule
		ruleID, serve := RuleID{}, ri.Serve
		switch {
		case ri.ID == nil:
			ruleID = m.RuleID()
		case slices.ContainsFunc(out, func(rule Rule) bool { return rule.ID == *ri.ID }):
			ck.add(pointer+"/id", "duplicate", "the configuration has this rule already", nil)
		default:
			j := slices.IndexFunc(previous, func(rule Rule) bool { return rule.ID == *ri.ID })
			if j < 0 {
				ck.add(pointer+"/id", "unknown_rule", "the configuration has no rule with this identifier; omit it for a new rule", nil)
				// Which salt the rule may send depends on which rule it
				// is, so a refused identifier is the one thing to fix.
				serve.Salt = ""
				break
			}
			ruleID, before = *ri.ID, &previous[j]
		}
		out = append(out, Rule{
			ID: ruleID, Description: ck.description(pointer+"/description", ri.Description),
			Conditions: ck.ruleConditions(pointer+"/conditions", ri.Conditions, r),
			Serve:      ck.serve(pointer+"/serve", serve, before, f, r, m),
		})
	}
	return out
}

// ruleConditions checks a rule's conditions at pointer and returns them,
// never nil.
func (ck *check) ruleConditions(pointer string, conditions []Condition, r Referents) []Condition {
	if len(conditions) > maxConditions {
		ck.add(pointer, "too_many", "a rule has at most 20 conditions", map[string]any{"max": maxConditions})
		return conditions
	}
	for i, cond := range conditions {
		ck.ruleCondition(pointer+"/"+strconv.Itoa(i), cond, r)
	}
	if conditions == nil {
		return []Condition{}
	}
	return conditions
}

// ruleCondition checks one condition of a rule at pointer: an attribute
// condition as a segment's, a segment condition naming a segment of the
// project, or a prerequisite as a configuration's.
func (ck *check) ruleCondition(pointer string, cond Condition, r Referents) {
	switch cond.Kind {
	case ConditionAttribute:
		ck.attributeCondition(pointer, cond, r.Declarations)
	case ConditionInSegment, ConditionNotInSegment:
		switch {
		case cond.Attribute != "" || cond.Op != "" || cond.Value != nil || cond.Flag != "" || cond.Variation != "":
			ck.add(pointer, "invalid_value", "a segment condition names a segment and nothing else", nil)
		case !slices.Contains(r.Segments, cond.Segment):
			ck.add(pointer+"/segment", "unknown_segment", "the project has no segment "+strconv.Quote(cond.Segment),
				map[string]any{"segment": cond.Segment})
		}
	case ConditionPrerequisite:
		if cond.Attribute != "" || cond.Op != "" || cond.Value != nil || cond.Segment != "" {
			ck.add(pointer, "invalid_value", "a prerequisite condition names a flag and a variation and nothing else", nil)
			return
		}
		ck.prerequisite(pointer, Prerequisite{Flag: cond.Flag, Variation: cond.Variation}, r)
	default:
		ck.add(pointer+"/kind", "invalid_value", "the condition kind is not one a rule holds",
			map[string]any{"kinds": []string{ConditionAttribute, ConditionInSegment, ConditionNotInSegment, ConditionPrerequisite}})
	}
}

// serve checks what a rule serves at pointer. before is the rule of the
// current configuration the rule is, nil for a new one: a rollout keeps the
// salt it was given for as long as it stays a rollout, so growing its
// percentage never moves a unit out; any other rollout is given a new one.
func (ck *check) serve(pointer string, in ServeInput, before *Rule, f Flag, r Referents, m Mint) Serve {
	d := r.Declarations
	out := Serve{Kind: in.Kind, Variation: in.Variation}
	switch in.Kind {
	case ServeVariation:
		if in.UnitType != "" || in.Percentage != nil || in.Salt != "" || in.Experiment != "" {
			ck.add(pointer, "invalid_value", "a variation serve names a variation and nothing else", nil)
		}
		ck.variationOf(pointer+"/variation", f, in.Variation)
	case ServeRollout:
		if in.Experiment != "" {
			ck.add(pointer+"/experiment", "invalid_value", "a rollout serves no experiment", nil)
		}
		if !d.units[in.UnitType] {
			ck.add(pointer+"/unit_type", "unknown_unit_type", "the project declares no unit type "+strconv.Quote(in.UnitType),
				map[string]any{"unit_type": in.UnitType})
		}
		ck.variationOf(pointer+"/variation", f, in.Variation)
		if in.Percentage == nil || *in.Percentage < 0 || *in.Percentage > maxPercentage {
			ck.add(pointer+"/percentage", "invalid_value", "a rollout serves 0 to 10,000 basis points of the units",
				map[string]any{"min": 0, "max": maxPercentage})
		} else {
			out.Percentage = *in.Percentage
		}
		out.UnitType, out.Salt = in.UnitType, ck.salt(pointer+"/salt", in.Salt, before, m)
	case ServeExperiment:
		out = Serve{Kind: in.Kind, Experiment: in.Experiment}
		if in.Variation != "" || in.UnitType != "" || in.Percentage != nil || in.Salt != "" {
			ck.add(pointer, "invalid_value", "an experiment serve names an experiment and nothing else", nil)
		}
		ck.experimentOf(pointer+"/experiment", f, in.Experiment, r)
	default:
		ck.add(pointer+"/kind", "invalid_value", "the serve kind is not one a rule serves",
			map[string]any{"kinds": []string{ServeVariation, ServeRollout, ServeExperiment}})
	}
	return out
}

// experimentOf checks at pointer that key names an experiment of f that a
// rule may serve: a draft or a running one. Several rules may serve the
// same experiment.
func (ck *check) experimentOf(pointer string, f Flag, key string, r Referents) {
	e, ok := r.experiment(key)
	switch {
	case !ok:
		ck.add(pointer, "unknown_experiment", "the project has no experiment "+strconv.Quote(key),
			map[string]any{"experiment": key})
	case e.Flag != f.ID:
		ck.add(pointer, "experiment_of_another_flag", "the experiment "+strconv.Quote(key)+" compares variations of another flag",
			map[string]any{"experiment": key})
	case e.Status != ExperimentDraft && e.Status != ExperimentRunning:
		ck.add(pointer, "experiment_not_servable", "the experiment "+strconv.Quote(key)+" is "+e.Status+"; only a draft or running experiment is served",
			map[string]any{"experiment": key, "status": e.Status})
	}
}

// salt returns the salt of a rollout whose rule was before: its salt while
// it was a rollout already, a new one otherwise. A write sends none or the
// one the rollout has: only regenerating it changes it.
func (ck *check) salt(pointer, sent string, before *Rule, m Mint) string {
	current := ""
	if before != nil && before.Serve.Kind == ServeRollout {
		current = before.Serve.Salt
	}
	if sent != "" && sent != current {
		ck.add(pointer, "immutable", "a rollout's salt is given by the server and changes only when regenerated; omit it", nil)
	}
	if current != "" {
		return current
	}
	return m.Salt()
}

// errArchived refuses to write the configuration of an archived flag: SDKs
// receive none of it, and a restored flag comes back as it was archived.
var errArchived = apperrors.Conflict("archived", "the flag is archived; restore it before changing its configuration")

// CanConfigure refuses to write a configuration of f while it is archived.
func (f Flag) CanConfigure() error {
	if f.ArchivedAt != nil {
		return errArchived
	}
	return nil
}

// Propose returns what replacing c with in would leave, checked as Replace
// checks it, once what neither c nor held may hold is left out of in, as
// Proposable leaves it out. held are the rules of the proposal in revises,
// nil for a new one: the server named its new rules and gave its new
// rollouts salts when it was made, and they are the proposal's until it is
// applied, so a rule keeps them while the proposal is edited. Otherwise
// sending back a proposal as it was answered would make every new rule new
// again, and a rollout grown in an edit would move units out.
func (c Configuration) Propose(f Flag, in ConfigurationInput, held []RuleInput, r Referents, m Mint) (Configuration, error) {
	basis := c
	basis.Rules = slices.Clone(c.Rules)
	for _, ri := range held {
		if ri.ID == nil || slices.ContainsFunc(basis.Rules, func(rule Rule) bool { return rule.ID == *ri.ID }) {
			continue
		}
		basis.Rules = append(basis.Rules, Rule{ID: *ri.ID, Serve: Serve{Kind: ri.Serve.Kind, Salt: ri.Serve.Salt}})
	}
	out, _, err := c.write(basis, f, basis.Proposable(in), r, m, Write{})
	return out, err
}

// Proposable returns in with what only c may hold left out: a rule
// identifier c has no rule with, and a salt that is not the salt of c's
// rule the rule names. A proposal is written against the configuration of
// its time and kept while others are written, and it may come from
// elsewhere (another environment, an earlier proposal of whose new rules
// the server named provisionally): what it names of a configuration that is
// not c's is not c's to keep. The rule becomes a new one and the rollout
// is given a salt of its own, instead of the proposal being refused for
// naming what c never had.
func (c Configuration) Proposable(in ConfigurationInput) ConfigurationInput {
	out := in
	out.Rules = slices.Clone(in.Rules)
	for i, ri := range out.Rules {
		live := -1
		if ri.ID != nil {
			live = slices.IndexFunc(c.Rules, func(rule Rule) bool { return rule.ID == *ri.ID })
		}
		if live < 0 {
			out.Rules[i].ID = nil
		}
		if live < 0 || c.Rules[live].Serve.Kind != ServeRollout || c.Rules[live].Serve.Salt != ri.Serve.Salt {
			out.Rules[i].Serve.Salt = ""
		}
	}
	return out
}
