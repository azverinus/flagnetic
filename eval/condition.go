package eval

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

// compiledCondition is a condition prepared once at decode time. err is set
// when the condition cannot be evaluated against this document; evaluating
// it then yields inconsistent_ruleset.
type compiledCondition struct {
	kind string
	err  error

	// attribute
	attribute string
	unit      bool // the attribute names a unit type: its value is the unit ID
	match     func(v any) bool

	// in_segment, not_in_segment
	segment string

	// prerequisite
	flag, variation string
}

type compiler struct {
	types map[string]AttributeType
	units map[string]bool
}

func (c compiler) compile(cond Condition) compiledCondition {
	cc := compiledCondition{kind: cond.Kind}
	switch cond.Kind {
	case ConditionAttribute:
		typ, ok := c.types[cond.Attribute]
		if !ok {
			cc.err = fmt.Errorf("%w: attribute %q is not declared", errInconsistent, cond.Attribute)
			return cc
		}
		match, err := compileOperator(typ, cond.Op, cond.Value)
		if err != nil {
			cc.err = fmt.Errorf("%w: attribute %q: %w", errInconsistent, cond.Attribute, err)
			return cc
		}
		cc.attribute, cc.unit, cc.match = cond.Attribute, c.units[cond.Attribute], match
	case ConditionInSegment, ConditionNotInSegment:
		cc.segment = cond.Segment
	case ConditionPrerequisite:
		cc.flag, cc.variation = cond.Flag, cond.Variation
	default:
		cc.err = fmt.Errorf("%w: unknown condition kind %q", errInconsistent, cond.Kind)
	}
	return cc
}

// CheckOperator returns why this evaluator cannot apply op with value to an
// attribute of typ, or nil when it can: op must be defined for typ and
// value must have the JSON type op needs (a string, number, boolean or list
// as decoded by encoding/json). It is the operator table the server
// validates conditions against on write. It does not reject what the
// evaluator treats as a false condition, such as an unparseable semantic
// version or date in the value.
func CheckOperator(typ AttributeType, op string, value any) error {
	_, err := compileOperator(typ, op, value)
	return err
}

// compileOperator returns a matcher of a context value for one
// operator of one attribute type. The matcher receives nil for an absent
// attribute. An absent, null or wrong-typed value makes every operator
// false except not_exists, which is then true; there is no coercion.
func compileOperator(typ AttributeType, op string, value any) (func(any) bool, error) {
	var extract func(any) (any, bool)
	switch typ {
	case TypeString:
		extract = func(v any) (any, bool) { s, ok := v.(string); return s, ok }
	case TypeNumber:
		extract = func(v any) (any, bool) { return toNumber(v) }
	case TypeBoolean:
		extract = func(v any) (any, bool) { b, ok := v.(bool); return b, ok }
	case TypeSemver:
		extract = func(v any) (any, bool) {
			s, ok := v.(string)
			if !ok {
				return nil, false
			}
			return ParseSemver(s)
		}
	case TypeDate:
		extract = func(v any) (any, bool) { return toInstant(v) }
	case TypeList:
		extract = func(v any) (any, bool) { return toStrings(v) }
	default:
		return nil, fmt.Errorf("unknown attribute type %q", typ)
	}

	switch op {
	case "exists":
		return func(v any) bool { _, ok := extract(v); return ok }, nil
	case "not_exists":
		return func(v any) bool { _, ok := extract(v); return !ok }, nil
	}

	test, err := operatorTest(typ, op, value)
	if err != nil {
		return nil, err
	}
	return func(v any) bool {
		x, ok := extract(v)
		return ok && test(x)
	}, nil
}

// operatorTest compiles the condition value of one operator into a test
// of an already typed context value.
func operatorTest(typ AttributeType, op string, value any) (func(any) bool, error) {
	unknown := fmt.Errorf("operator %q is not defined for type %s", op, typ)
	switch typ {
	case TypeString:
		switch op {
		case "eq", "neq", "contains", "not_contains", "starts_with", "ends_with":
			want, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a string value", op)
			}
			var f func(string) bool
			switch op {
			case "eq", "neq":
				f = func(s string) bool { return s == want }
			case "contains", "not_contains":
				f = func(s string) bool { return strings.Contains(s, want) }
			case "starts_with":
				f = func(s string) bool { return strings.HasPrefix(s, want) }
			case "ends_with":
				f = func(s string) bool { return strings.HasSuffix(s, want) }
			}
			negate := op == "neq" || op == "not_contains"
			return func(x any) bool { return f(x.(string)) != negate }, nil
		case "in", "not_in":
			set, ok := toStrings(value)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a list of strings", op)
			}
			negate := op == "not_in"
			return func(x any) bool { return slices.Contains(set, x.(string)) != negate }, nil
		case "matches":
			pattern, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a string value", op)
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("operator %q: %w", op, err)
			}
			return func(x any) bool { return re.MatchString(x.(string)) }, nil
		}
	case TypeNumber:
		switch op {
		case "eq", "neq", "lt", "lte", "gt", "gte":
			want, ok := toNumber(value)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a number value", op)
			}
			return compare(op, func(x any) int { return cmp.Compare(x.(float64), want) }), nil
		case "in", "not_in":
			list, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a list of numbers", op)
			}
			set := make([]float64, len(list))
			for i, e := range list {
				n, ok := toNumber(e)
				if !ok {
					return nil, fmt.Errorf("operator %q needs a list of numbers", op)
				}
				set[i] = n
			}
			negate := op == "not_in"
			return func(x any) bool { return slices.Contains(set, x.(float64)) != negate }, nil
		}
	case TypeBoolean:
		if op == "eq" {
			want, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a boolean value", op)
			}
			return func(x any) bool { return x.(bool) == want }, nil
		}
	case TypeSemver:
		switch op {
		case "eq", "neq", "lt", "lte", "gt", "gte":
			s, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a string value", op)
			}
			want, ok := ParseSemver(s)
			if !ok {
				// An unparseable version makes the condition false.
				return func(any) bool { return false }, nil
			}
			return compare(op, func(x any) int { return x.(Semver).Compare(want) }), nil
		case "in", "not_in":
			list, ok := toStrings(value)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a list of strings", op)
			}
			var set []Semver
			for _, s := range list {
				if v, ok := ParseSemver(s); ok {
					set = append(set, v)
				}
			}
			negate := op == "not_in"
			return func(x any) bool {
				in := slices.ContainsFunc(set, func(v Semver) bool { return x.(Semver).Compare(v) == 0 })
				return in != negate
			}, nil
		}
	case TypeDate:
		switch op {
		case "before", "after":
			s, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("operator %q needs an RFC 3339 string", op)
			}
			want, ok := parseRFC3339(s)
			if !ok {
				// An unparseable date makes the condition false.
				return func(any) bool { return false }, nil
			}
			if op == "before" {
				return func(x any) bool { return x.(time.Time).Before(want) }, nil
			}
			return func(x any) bool { return x.(time.Time).After(want) }, nil
		}
	case TypeList:
		switch op {
		case "contains", "not_contains":
			want, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a string value", op)
			}
			negate := op == "not_contains"
			return func(x any) bool { return slices.Contains(x.([]string), want) != negate }, nil
		case "contains_any", "contains_all":
			wants, ok := toStrings(value)
			if !ok {
				return nil, fmt.Errorf("operator %q needs a list of strings", op)
			}
			if op == "contains_any" {
				return func(x any) bool {
					return slices.ContainsFunc(wants, func(w string) bool { return slices.Contains(x.([]string), w) })
				}, nil
			}
			return func(x any) bool {
				for _, w := range wants {
					if !slices.Contains(x.([]string), w) {
						return false
					}
				}
				return true
			}, nil
		case "is_empty":
			return func(x any) bool { return len(x.([]string)) == 0 }, nil
		}
	}
	return nil, unknown
}

// compare builds eq, neq, lt, lte, gt or gte over a three-way comparison of
// the context value against the condition value.
func compare(op string, cmp func(any) int) func(any) bool {
	switch op {
	case "eq":
		return func(x any) bool { return cmp(x) == 0 }
	case "neq":
		return func(x any) bool { return cmp(x) != 0 }
	case "lt":
		return func(x any) bool { return cmp(x) < 0 }
	case "lte":
		return func(x any) bool { return cmp(x) <= 0 }
	case "gt":
		return func(x any) bool { return cmp(x) > 0 }
	default: // gte
		return func(x any) bool { return cmp(x) >= 0 }
	}
}

// toNumber accepts a JSON number and Go's numeric types. Strings are not
// numbers: "5" is wrong-typed.
func toNumber(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int8:
		f = float64(n)
	case int16:
		f = float64(n)
	case int32:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint:
		f = float64(n)
	case uint8:
		f = float64(n)
	case uint16:
		f = float64(n)
	case uint32:
		f = float64(n)
	case uint64:
		f = float64(n)
	case json.Number:
		x, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = x
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// toStrings accepts a list whose every element is a string.
func toStrings(v any) ([]string, bool) {
	switch l := v.(type) {
	case []string:
		return l, true
	case []any:
		out := make([]string, len(l))
		for i, e := range l {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// toInstant accepts an RFC 3339 string with an offset, an integral number
// of Unix epoch milliseconds, or a time.Time.
func toInstant(v any) (time.Time, bool) {
	switch d := v.(type) {
	case string:
		return parseRFC3339(d)
	case time.Time:
		return d, true
	}
	ms, ok := toNumber(v)
	if !ok || ms != math.Trunc(ms) || math.Abs(ms) > 1<<53 {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(ms)), true
}

func parseRFC3339(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}
