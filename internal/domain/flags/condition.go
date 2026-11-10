package flags

import (
	"regexp/syntax"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// The condition kinds.
const (
	ConditionAttribute    = eval.ConditionAttribute
	ConditionInSegment    = eval.ConditionInSegment
	ConditionNotInSegment = eval.ConditionNotInSegment
	ConditionPrerequisite = eval.ConditionPrerequisite
)

// Condition is one comparison inside a rule or a segment: an attribute, an
// operator and a value; membership in a segment; or a prerequisite. It is
// what a ruleset carries, so SDKs evaluate exactly what was validated.
type Condition struct {
	Kind      string
	Attribute string
	Op        string
	// Value is the condition's value as encoding/json decodes it: a string,
	// a float64, a bool or a []any; nil when absent.
	Value     any
	Segment   string
	Flag      string
	Variation string
}

// maxListEntries bounds a list value, so a condition stays quick for an SDK
// to evaluate and a ruleset quick to download.
const maxListEntries = 10_000

// A matches pattern is bounded twice, since its length alone does not bound
// what it costs: a{1000} is seven characters and a thousand steps. The
// evaluator compiles every pattern of a ruleset when the ruleset is loaded
// and keeps the programs, and matching takes time in proportion to the
// program's size times the value's length, which a pattern left unbounded
// turned into gigabytes of memory on every replica and SDK holding the
// ruleset and hours of processor time for one evaluation.
const (
	// maxPatternLength is how many characters a matches pattern has at most.
	maxPatternLength = 500
	// maxPatternSize is how many instructions the program a matches pattern
	// compiles to has at most. A literal of the longest pattern fits, and so
	// does a{1,400}; no pattern written by hand comes near.
	maxPatternSize = 1000
)

// patternSize returns how many instructions the program of pattern, a
// regular expression the evaluator accepts, has.
func patternSize(pattern string) int {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return 0
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return 0
	}
	return len(prog.Inst)
}

// valuelessOperators test whether the attribute is there or empty, and take
// no value.
var valuelessOperators = []string{"exists", "not_exists", "is_empty"}

// Declarations are what a project's conditions may name: its attributes by
// type, and its unit types, each working as a string attribute holding that
// unit's ID.
type Declarations struct {
	types map[string]eval.AttributeType
	// units are the unit types, which rollouts and overrides name.
	units map[string]bool
}

// NewDeclarations returns the declarations of a project with units and
// attributes.
func NewDeclarations(units []workspace.UnitType, attributes []workspace.Attribute) Declarations {
	d := Declarations{types: make(map[string]eval.AttributeType, len(units)+len(attributes)), units: make(map[string]bool, len(units))}
	for _, u := range units {
		d.types[u.Key], d.units[u.Key] = eval.TypeString, true
	}
	for _, a := range attributes {
		d.types[a.Key] = eval.AttributeType(a.Type)
	}
	return d
}

// attributeCondition checks the attribute condition c at pointer: it names
// a declaration, with an operator the evaluator defines for its type, and a
// value that operator accepts, which on write is held to more than the
// evaluator holds it to: every semantic version and date in it parses, so a
// typo is refused instead of silently making the condition false.
func (c *check) attributeCondition(pointer string, cond Condition, d Declarations) {
	if cond.Segment != "" || cond.Flag != "" || cond.Variation != "" {
		c.add(pointer, "invalid_value", "an attribute condition names an attribute, an operator and a value, and nothing else", nil)
		return
	}
	if cond.Attribute == "" {
		c.add(pointer+"/attribute", "invalid_value", "an attribute condition names an attribute", nil)
		return
	}
	typ, ok := d.types[cond.Attribute]
	if !ok {
		c.add(pointer+"/attribute", "unknown_attribute", "the project declares no attribute or unit type "+strconv.Quote(cond.Attribute),
			map[string]any{"attribute": cond.Attribute})
		return
	}
	if !operatorDefined(typ, cond.Op) {
		c.add(pointer+"/op", "invalid_operator", "the operator is not defined for the attribute's type",
			map[string]any{"type": string(typ)})
		return
	}
	valueless := slices.Contains(valuelessOperators, cond.Op)
	switch {
	case valueless && cond.Value != nil:
		c.add(pointer+"/value", "invalid_value", "the operator takes no value", nil)
	case !valueless && cond.Value == nil:
		c.add(pointer+"/value", "invalid_value", "the operator needs a value", nil)
	case valueless:
		// Nothing to parse: whether the attribute is there holds for every
		// type alike.
	case cond.Op == "matches" && overlong(cond.Value):
		// Judged before the pattern is compiled, which is what a long one
		// makes costly.
		c.add(pointer+"/value", "too_long", "a pattern has at most 500 characters", map[string]any{"max": maxPatternLength})
	case eval.CheckOperator(typ, cond.Op, cond.Value) != nil:
		c.add(pointer+"/value", "invalid_value", "the value does not fit the operator and the attribute's type", nil)
	case cond.Op == "matches" && patternSize(cond.Value.(string)) > maxPatternSize:
		c.add(pointer+"/value", "too_complex", "the pattern repeats too much: shorten its repetitions or split it into conditions",
			map[string]any{"max": maxPatternSize})
	default:
		c.listAndEntries(pointer+"/value", typ, cond.Value)
	}
}

// overlong reports whether value is a pattern of more than maxPatternLength
// characters.
func overlong(value any) bool {
	pattern, ok := value.(string)
	return ok && utf8.RuneCountInString(pattern) > maxPatternLength
}

// operatorDefined reports whether the evaluator defines op for typ: whether
// it accepts op with a value of some JSON type.
func operatorDefined(typ eval.AttributeType, op string) bool {
	for _, probe := range []any{nil, "", float64(0), true, []any{}} {
		if eval.CheckOperator(typ, op, probe) == nil {
			return true
		}
	}
	return false
}

// listAndEntries refuses a list of more than 10,000 entries, and a semantic
// version or date that does not parse, alone or in a list.
func (c *check) listAndEntries(pointer string, typ eval.AttributeType, value any) {
	entries, isList := value.([]any)
	if isList && len(entries) > maxListEntries {
		c.add(pointer, "too_many", "a list holds at most 10,000 entries", map[string]any{"max": maxListEntries})
		return
	}
	if !isList {
		entries = []any{value}
	}
	for _, e := range entries {
		s, _ := e.(string)
		if _, ok := eval.ParseSemver(s); typ == eval.TypeSemver && !ok {
			c.add(pointer, "invalid_value", "every version is a semantic version, such as 1.41.0", nil)
			return
		}
		if _, err := time.Parse(time.RFC3339Nano, s); typ == eval.TypeDate && err != nil {
			c.add(pointer, "invalid_value", "a date is an RFC 3339 instant, such as 2026-01-01T00:00:00Z", nil)
			return
		}
	}
}

// segmentCondition checks a condition of a segment at pointer: attribute
// conditions only, since segments do not nest and a segment is the same
// set in every environment, which a prerequisite is not.
func (c *check) segmentCondition(pointer string, cond Condition, d Declarations) {
	switch cond.Kind {
	case ConditionAttribute:
		c.attributeCondition(pointer, cond, d)
	case ConditionInSegment, ConditionNotInSegment, ConditionPrerequisite:
		c.add(pointer+"/kind", "invalid_value", "a segment holds attribute conditions only: segments do not nest",
			map[string]any{"kinds": []string{ConditionAttribute}})
	default:
		c.add(pointer+"/kind", "invalid_value", "the condition kind is not one a segment holds",
			map[string]any{"kinds": []string{ConditionAttribute}})
	}
}
