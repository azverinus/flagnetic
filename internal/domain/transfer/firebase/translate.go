package firebase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// The warning codes. Each says what the translation could not reproduce
// exactly, with a pointer into the template at what it came from.
const (
	// WarnInAppDefault: a value left to the app is served as the type's
	// empty value.
	WarnInAppDefault = "in_app_default"
	// WarnConditionSkipped: a rule was left out; the params name the
	// parameter, the condition and the reason.
	WarnConditionSkipped = "condition_skipped"
	// WarnRolloutReshuffled: a percent term became a rollout whose units are
	// not the units Firebase chose.
	WarnRolloutReshuffled = "rollout_reshuffled"
	// WarnAttributeDeclared: a user property or custom signal became an
	// attribute under another key.
	WarnAttributeDeclared = "attribute_declared"
	// WarnRuleLimit: a rule was left out because the configuration holds
	// the most rules it can.
	WarnRuleLimit = "rule_limit"
	// WarnArchivedFlag: the flag the parameter matches is archived and stays
	// so.
	WarnArchivedFlag = "archived_flag"
)

// Warning is something the translation could not reproduce exactly.
type Warning struct {
	Code string
	// Pointer is a JSON Pointer (RFC 6901) into the template.
	Pointer string
	// Params are the values the warning is about, so a client explains it in
	// its own words from the code and these.
	Params map[string]any
	// Detail explains the warning to a person.
	Detail string
}

// Target says where the template's rollouts are bucketed.
type Target struct {
	// UnitType is the key of the unit type percent rollouts bucket on.
	UnitType string
}

// Current is what the project holds that a translation reads: its flags with
// the rules each has in the environment the template is merged into, and its
// declarations. The standard attributes are among Attributes.
type Current struct {
	Flags      []ExistingFlag
	UnitTypes  []workspace.UnitType
	Attributes []workspace.Attribute
}

// ExistingFlag is a flag the project has, and the rules its configuration in
// the target environment holds, which a re-import keeps the identities of.
type ExistingFlag struct {
	Flag  flags.Flag
	Rules []flags.Rule
}

// Result is what to merge into the target environment: a flag for every
// parameter, the attributes the rules need declared, and a warning for
// everything the template holds that the flags cannot.
type Result struct {
	// Flags are in key order.
	Flags []Flag
	// Attributes are the attributes to declare, in key order. A rule that
	// needs one is skipped when it cannot be declared, so none is declared
	// for nothing.
	Attributes []Attribute
	Warnings   []Warning
}

// Flag is one translated parameter.
type Flag struct {
	Key  string
	Type flags.Type
	// Name, Description and Tags are those of the flag the parameter
	// matches, completed by the template (a non-empty description replaces
	// the old one; the group is added as a tag), or the new flag's.
	Name        string
	Description string
	Tags        []string
	// Existing says the project already has a flag with the key; the merge
	// leaves everything the result does not name as it is.
	Existing bool
	// Variations are the flag's variations: those it has, then one per new
	// value.
	Variations []flags.Variation
	// On, OffVariation, Fallthrough and Rules are the flag's configuration
	// in the target environment. A rule equal to one the flag has there keeps
	// its ID and salt; a rollout without a salt is new, and its salt is
	// minted when it is written.
	On           bool
	OffVariation string
	Fallthrough  string
	Rules        []flags.Rule
}

// Attribute is an attribute the translation declares.
type Attribute struct {
	Key         string
	Type        workspace.AttributeType
	Name        string
	Description string
}

// Translate returns what merging t into the environment current describes
// leaves behind: a flag for every parameter serving the template's default
// value, with the conditional values as rules in the template's condition
// order, and warnings about everything it cannot reproduce. A template that
// cannot be translated at all, such as a value that is not of its
// parameter's type, is refused with the invalid error invalid_document
// naming every fault.
func Translate(t Template, current Current, target Target) (Result, error) {
	params, err := t.validate()
	if err != nil {
		return Result{}, err
	}
	tr := &translation{target: target, current: current, declared: map[string]workspace.AttributeType{},
		unitTypes: map[string]bool{}, expressions: map[string]expression{}, conditionPointers: map[string]string{},
		result: Result{Flags: []Flag{}, Attributes: []Attribute{}, Warnings: []Warning{}}}
	for _, a := range current.Attributes {
		tr.declared[a.Key] = a.Type
	}
	for _, u := range current.UnitTypes {
		tr.unitTypes[u.Key] = true
	}
	for i, c := range t.Conditions {
		a, err := parseExpression(c.Expression)
		tr.expressions[c.Name] = expression{alternatives: a, err: err}
		tr.conditionPointers[c.Name] = "/conditions/" + strconv.Itoa(i) + "/expression"
	}
	var faults []apperrors.Violation
	for _, p := range params {
		f, fs := tr.flag(p, t.Conditions)
		faults = append(faults, fs...)
		if len(fs) == 0 {
			tr.result.Flags = append(tr.result.Flags, f)
		}
	}
	if len(faults) > 0 {
		return Result{}, invalid(faults...)
	}
	slices.SortFunc(tr.result.Attributes, func(a, b Attribute) int { return strings.Compare(a.Key, b.Key) })
	return tr.result, nil
}

type translation struct {
	target            Target
	current           Current
	declared          map[string]workspace.AttributeType // the project's attributes and those the result declares
	unitTypes         map[string]bool
	expressions       map[string]expression // by condition name
	conditionPointers map[string]string     // the pointer of each condition's expression, by name
	result            Result
}

type expression struct {
	alternatives alternatives
	err          error
}

func (tr *translation) warn(code, pointer, detail string, params map[string]any) {
	tr.result.Warnings = append(tr.result.Warnings, Warning{Code: code, Pointer: pointer, Params: params, Detail: detail})
}

// valueTypes maps a parameter's valueType to a flag type; unset means string.
var valueTypes = map[string]flags.Type{
	"": flags.TypeString, "PARAMETER_VALUE_TYPE_UNSPECIFIED": flags.TypeString, "STRING": flags.TypeString,
	"BOOLEAN": flags.TypeBoolean, "NUMBER": flags.TypeNumber, "JSON": flags.TypeJSON,
}

// emptyValues are what a flag serves where the template leaves the value to
// the app.
var emptyValues = map[flags.Type]json.RawMessage{
	flags.TypeBoolean: json.RawMessage(`false`), flags.TypeString: json.RawMessage(`""`),
	flags.TypeNumber: json.RawMessage(`0`), flags.TypeJSON: json.RawMessage(`{}`),
}

// flag translates one parameter, or reports why it cannot be.
func (tr *translation) flag(p grouped, conditions []Condition) (Flag, []apperrors.Violation) {
	typ, ok := valueTypes[p.ValueType]
	if !ok {
		return Flag{}, []apperrors.Violation{{Pointer: p.pointer + "/valueType", Code: "invalid_value_type",
			Detail: "valueType is one of STRING, BOOLEAN, NUMBER and JSON", Params: map[string]any{"parameter": p.key, "value_type": p.ValueType}}}
	}
	var existing *ExistingFlag
	if i := slices.IndexFunc(tr.current.Flags, func(f ExistingFlag) bool { return f.Flag.Key == p.key }); i >= 0 {
		existing = &tr.current.Flags[i]
	}
	if existing != nil && existing.Flag.Type != typ {
		return Flag{}, []apperrors.Violation{{Pointer: p.pointer + "/valueType", Code: "type_immutable",
			Detail: "the flag " + p.key + " is a " + string(existing.Flag.Type) + "; a flag's type never changes",
			Params: map[string]any{"parameter": p.key, "type": string(existing.Flag.Type)}}}
	}

	var faults []apperrors.Violation
	// translated warnings are kept apart until the parameter is known to be
	// valid, so that a refused template reports faults and nothing else.
	mark := len(tr.result.Warnings)
	vs := newVariations(typ, existing)
	value, fault := tr.value(p.key, p.pointer+"/defaultValue", "", typ, p.DefaultValue)
	if fault != nil {
		faults = append(faults, *fault)
	}
	fallthroughKey := vs.key(value, "defaultValue")

	rules := []flags.Rule{}
	for _, c := range conditions {
		v, ok := p.ConditionalValues[c.Name]
		if !ok {
			continue
		}
		pointer := p.pointer + "/conditionalValues/" + escape(c.Name)
		skip := func(reason string) {
			tr.warn(WarnConditionSkipped, pointer, fmt.Sprintf("%s, condition %s: the rules are skipped: %s", p.key, c.Name, reason),
				map[string]any{"parameter": p.key, "condition": c.Name, "reason": reason})
		}
		if present(v.PersonalizationValue) || present(v.RolloutValue) {
			skip("personalization and rollout values are not supported")
			continue
		}
		value, fault := tr.value(p.key, pointer, c.Name, typ, &v)
		if fault != nil {
			faults = append(faults, *fault)
			continue
		}
		e := tr.expressions[c.Name]
		if e.err != nil {
			skip("the expression cannot be read: " + e.err.Error())
			continue
		}
		for _, cl := range e.alternatives {
			// The limit is checked before the rule is translated, so that a
			// rule left out declares no attribute and warns of no rollout.
			if len(rules) == flags.MaxRules {
				reason := fmt.Sprintf("a configuration holds at most %d rules", flags.MaxRules)
				tr.warn(WarnRuleLimit, pointer, fmt.Sprintf("%s, condition %s: the rule for \"%s\" is skipped: %s",
					p.key, c.Name, strings.Join(cl.terms, " && "), reason),
					map[string]any{"parameter": p.key, "condition": c.Name, "max": flags.MaxRules})
				continue
			}
			r, ok := tr.rule(p, c, pointer, cl)
			if !ok {
				continue
			}
			r.Description = fmt.Sprintf("Firebase condition %q", c.Name)
			r.Serve.Variation = vs.key(value, c.Name)
			rules = append(rules, r)
		}
	}
	if len(faults) > 0 {
		tr.result.Warnings = tr.result.Warnings[:mark]
		return Flag{}, faults
	}

	f := Flag{Key: p.key, Type: typ, Variations: vs.list, On: true, OffVariation: fallthroughKey, Fallthrough: fallthroughKey, Rules: rules}
	if existing == nil {
		f.Name, f.Description, f.Tags = p.key, p.Description, []string{}
		if p.group != "" {
			f.Tags = []string{p.group}
		}
		return f, nil
	}
	f.Existing = true
	f.Name, f.Description, f.Tags = existing.Flag.Name, existing.Flag.Description, slices.Clone(existing.Flag.Tags)
	if f.Tags == nil {
		f.Tags = []string{}
	}
	if p.Description != "" {
		f.Description = p.Description
	}
	if p.group != "" && !slices.Contains(f.Tags, p.group) {
		f.Tags = append(f.Tags, p.group)
	}
	f.Rules = keepIdentities(existing.Rules, rules)
	if existing.Flag.ArchivedAt != nil {
		tr.warn(WarnArchivedFlag, p.pointer, p.key+": the flag is archived; the import configures it, and it stays archived",
			map[string]any{"parameter": p.key})
	}
	return f, nil
}

// value is v as a value of a flag of type typ. The in-app default, or no
// value at all, is the type's empty value, with a warning; a value that is
// not of the type is a fault.
func (tr *translation) value(parameter, pointer, condition string, typ flags.Type, v *Value) (json.RawMessage, *apperrors.Violation) {
	if v == nil || v.UseInAppDefault || v.Value == nil {
		empty := emptyValues[typ]
		where := parameter
		params := map[string]any{"parameter": parameter, "serves": empty}
		if condition != "" {
			where += ", condition " + condition
			params["condition"] = condition
		}
		tr.warn(WarnInAppDefault, pointer, fmt.Sprintf("%s: the value is the in-app default; the flag serves %s instead", where, empty), params)
		return empty, nil
	}
	fault := func(code, detail string) *apperrors.Violation {
		return &apperrors.Violation{Pointer: pointer + "/value", Code: code, Detail: detail,
			Params: map[string]any{"parameter": parameter, "type": string(typ)}}
	}
	s := *v.Value
	switch typ {
	case flags.TypeBoolean:
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true":
			return json.RawMessage(`true`), nil
		case "false":
			return json.RawMessage(`false`), nil
		}
		return nil, fault("invalid_value", fmt.Sprintf("%q is not a boolean", s))
	case flags.TypeNumber:
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, fault("invalid_value", fmt.Sprintf("%q is not a number", s))
		}
		raw, _ := json.Marshal(n)
		return raw, nil
	case flags.TypeJSON:
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(s)); err != nil {
			return nil, fault("invalid_value", "the value is not JSON: "+err.Error())
		}
		return b.Bytes(), nil
	default:
		raw, _ := json.Marshal(s)
		return raw, nil
	}
}

// rule translates one conjunction of a condition into a rule without a
// variation, or skips it with a warning.
func (tr *translation) rule(p grouped, c Condition, pointer string, cl conjunction) (flags.Rule, bool) {
	where := p.key + ", condition " + c.Name
	skip := func(reason string) (flags.Rule, bool) {
		tr.warn(WarnConditionSkipped, pointer, fmt.Sprintf("%s: the rule for \"%s\" is skipped: %s", where, strings.Join(cl.terms, " && "), reason),
			map[string]any{"parameter": p.key, "condition": c.Name, "reason": reason})
		return flags.Rule{}, false
	}
	if cl.unsupported != "" {
		return skip(cl.unsupported)
	}
	if len(cl.percents) > 1 {
		return skip("a rule holds one percent term at most")
	}
	if len(cl.atoms) > flags.MaxConditions {
		return skip(fmt.Sprintf("a rule holds at most %d conditions", flags.MaxConditions))
	}
	conditions := make([]flags.Condition, 0, len(cl.atoms))
	var declare []Attribute
	var sources []*custom
	pending := map[string]workspace.AttributeType{}
	for _, a := range cl.atoms {
		attr := a.attribute
		if a.custom != nil {
			key, reason := tr.customAttribute(a.custom, pending)
			if reason != "" {
				return skip(reason)
			}
			_, declared := tr.declared[key]
			if _, ok := pending[key]; !ok && !declared {
				pending[key] = a.custom.typ
				declare = append(declare, Attribute{Key: key, Type: a.custom.typ, Name: a.custom.key,
					Description: fmt.Sprintf("The Firebase %s %q.", a.custom.source, a.custom.key)})
				sources = append(sources, a.custom)
			}
			attr = key
		}
		typ := tr.declared[attr]
		if typ == "" {
			typ = pending[attr]
		}
		if err := checkValue(typ, a.op, a.value); err != nil {
			return skip(err.Error())
		}
		conditions = append(conditions, flags.Condition{Kind: flags.ConditionAttribute, Attribute: attr, Op: a.op, Value: a.value})
	}
	// Declared only now that the whole rule is known to translate.
	for i, attr := range declare {
		tr.declared[attr.Key] = attr.Type
		tr.result.Attributes = append(tr.result.Attributes, attr)
		if attr.Key != sources[i].key {
			tr.warn(WarnAttributeDeclared, tr.conditionPointers[c.Name],
				fmt.Sprintf("%s: %s %q is the attribute %s; contexts must carry it under that key", where, sources[i].source, sources[i].key, attr.Key),
				map[string]any{"parameter": p.key, "condition": c.Name, "source": sources[i].source, "key": sources[i].key, "attribute": attr.Key})
		}
	}
	r := flags.Rule{Conditions: conditions, Serve: flags.Serve{Kind: flags.ServeVariation}}
	if len(cl.percents) == 1 {
		pc := cl.percents[0]
		share := strconv.FormatFloat(float64(pc.basisPoints)/100, 'f', -1, 64)
		if firebase := strconv.FormatFloat(pc.firebase, 'f', -1, 64); firebase != share {
			share = fmt.Sprintf("%s%% (%s%% in Firebase, rounded to basis points)", share, firebase)
		} else {
			share += "%"
		}
		tr.warn(WarnRolloutReshuffled, pointer, fmt.Sprintf("%s: percent becomes a rollout of %s on unit type %s with a fresh salt; "+
			"Firebase's hashing is unpublished, so which units are inside changes", where, share, tr.target.UnitType),
			map[string]any{"parameter": p.key, "condition": c.Name, "unit_type": tr.target.UnitType, "percentage": pc.basisPoints, "firebase_percent": pc.firebase})
		if pc.from > 0 {
			tr.warn(WarnRolloutReshuffled, pointer, fmt.Sprintf("%s: Firebase's percent range from %s keeps only its width: a rollout starts at bucket 0 "+
				"of its own salt, so ranges Firebase kept apart on one seed now overlap and an earlier rule takes part of this one's share",
				where, strconv.FormatFloat(pc.from, 'f', -1, 64)),
				map[string]any{"parameter": p.key, "condition": c.Name, "unit_type": tr.target.UnitType, "range_start": pc.from})
		}
		r.Serve = flags.Serve{Kind: flags.ServeRollout, UnitType: tr.target.UnitType, Percentage: pc.basisPoints}
	}
	return r, true
}

// checkValue refuses a comparison the evaluator does not define for an
// attribute of type typ, or whose value does not fit it, as a write to a
// configuration would: a semantic version that does not parse is refused
// rather than silently making the condition false.
func checkValue(typ workspace.AttributeType, op string, value any) error {
	if err := eval.CheckOperator(eval.AttributeType(typ), op, value); err != nil {
		return err
	}
	if typ != workspace.TypeSemver {
		return nil
	}
	entries, ok := value.([]any)
	if !ok {
		entries = []any{value}
	}
	for _, e := range entries {
		s, _ := e.(string)
		if _, ok := eval.ParseSemver(s); !ok {
			return fmt.Errorf("%q is not a semantic version", s)
		}
	}
	return nil
}

// customAttribute returns the attribute a user property or custom signal
// maps to, or why it cannot: its key is a unit type's or cannot be an
// attribute's, or the attribute is declared (by the project, the
// translation or pending, the conjunction being translated) with another
// type.
func (tr *translation) customAttribute(c *custom, pending map[string]workspace.AttributeType) (key, reason string) {
	key = slug(c.key)
	switch {
	case key == "":
		return "", fmt.Sprintf("%s %q has no usable key", c.source, c.key)
	case !workspace.DiscoverableAttributeKey(key):
		return "", fmt.Sprintf("%s %q maps to %s, which no attribute can be named", c.source, c.key, key)
	case tr.unitTypes[key]:
		return "", fmt.Sprintf("%s %q maps to %s, which is a unit type", c.source, c.key, key)
	}
	typ, ok := tr.declared[key]
	if !ok {
		typ, ok = pending[key]
	}
	if ok && typ != c.typ {
		return "", fmt.Sprintf("the attribute %s is a %s, and the comparison needs a %s", key, typ, c.typ)
	}
	return key, ""
}

var notKey = regexp.MustCompile(`[^a-z0-9_-]+`)

// slug turns a user property or custom signal key into an attribute key:
// lower case, runs of other characters an underscore, starting with a
// letter, at most 64 characters.
func slug(s string) string {
	k := strings.Trim(notKey.ReplaceAllString(strings.ToLower(s), "_"), "_")
	if k != "" && (k[0] < 'a' || k[0] > 'z') {
		k = "a_" + k
	}
	if len(k) > 64 {
		k = strings.TrimRight(k[:64], "_-")
	}
	return k
}

// variations are a flag's variations as the translation leaves them: the
// existing flag's, or a new boolean flag's true and false, plus one per new
// distinct value.
type variations struct {
	list []flags.Variation
}

func newVariations(typ flags.Type, existing *ExistingFlag) *variations {
	if existing != nil {
		return &variations{list: slices.Clone(existing.Flag.Variations)}
	}
	if typ == flags.TypeBoolean {
		return &variations{list: []flags.Variation{
			{Key: "true", Value: json.RawMessage(`true`), Name: "true"},
			{Key: "false", Value: json.RawMessage(`false`), Name: "false"},
		}}
	}
	return &variations{list: []flags.Variation{}}
}

// key returns the key of the variation serving value, adding it as the first
// free v1, v2… named name when none does.
func (v *variations) key(value json.RawMessage, name string) string {
	for _, x := range v.list {
		if sameValue(x.Value, value) {
			return x.Key
		}
	}
	for n := 1; ; n++ {
		k := "v" + strconv.Itoa(n)
		if !slices.ContainsFunc(v.list, func(x flags.Variation) bool { return x.Key == k }) {
			v.list = append(v.list, flags.Variation{Key: k, Value: value, Name: name})
			return k
		}
	}
}

// present reports whether a template field holds something other than null.
func present(v json.RawMessage) bool {
	return len(v) > 0 && string(v) != "null"
}

// sameValue compares two JSON values by meaning: 3 and 3.0 are equal, as are
// objects with their keys in another order.
func sameValue(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// keepIdentities gives each rule of rules the ID and salt of the first
// unclaimed rule of before that equals it but for them, so that importing
// the same template twice changes nothing.
func keepIdentities(before, rules []flags.Rule) []flags.Rule {
	claimed := make([]bool, len(before))
	for i := range rules {
		for j, b := range before {
			if !claimed[j] && sameRule(b, rules[i]) {
				rules[i].ID, rules[i].Serve.Salt, claimed[j] = b.ID, b.Serve.Salt, true
				break
			}
		}
	}
	return rules
}

func sameRule(a, b flags.Rule) bool {
	a.Serve.Salt, b.Serve.Salt = "", ""
	if a.Description != b.Description || a.Serve != b.Serve || len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for i := range a.Conditions {
		if !reflect.DeepEqual(a.Conditions[i], b.Conditions[i]) {
			return false
		}
	}
	return true
}
