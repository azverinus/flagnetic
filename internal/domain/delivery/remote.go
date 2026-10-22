package delivery

import (
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ReasonTargetingMatch is the reason a client key receives wherever
// targeting decided a value: it says that the value was targeted, never by
// which rule, rollout, segment or prerequisite.
const ReasonTargetingMatch eval.Reason = "targeting_match"

// MaskForClient returns r as a client key may learn it. A client key ships
// inside apps and browsers, where anyone can read what it receives, so it
// learns the value it is served and nothing about the targeting behind it:
// the reasons rule, rollout, fallthrough and prerequisite_failed become
// targeting_match, because telling them apart would reveal whether a rule
// (and with it a private segment) matched the context; the rule, the failed
// prerequisite and the rollout bucket are dropped. off, override, error and
// experiment pass, since the SDK acts on them, and so do the value, the
// variation, the experiment block and override_influenced, which an
// exposure carries.
func MaskForClient(r eval.Result) eval.Result {
	switch r.Reason {
	case eval.ReasonRule, eval.ReasonRollout, eval.ReasonFallthrough, eval.ReasonPrerequisiteFailed:
		r.Reason = ReasonTargetingMatch
	}
	r.RuleID = ""
	r.Prerequisite = ""
	r.Bucket = nil
	return r
}

// The limits of a context posted for remote evaluation: generous for any
// real context, and small enough that evaluating one stays cheap whoever
// posts it.
const (
	MaxContextAttributes = 64
	MaxContextString     = 256
	MaxContextListItems  = 64
)

// ContextUnit is one unit of a context posted for remote evaluation, in the
// order it was given, so a unit type given twice can be told.
type ContextUnit struct {
	Type string
	ID   string
}

// RemoteContext is a context as an SDK posts it for remote evaluation.
type RemoteContext struct {
	Units      []ContextUnit
	Attributes map[string]any
}

// CheckRemoteContext refuses a context over the limits with
// invalid_context, naming every violation at once by a JSON Pointer into
// the context, ordered by pointer: more than 64 attributes, a string over
// 256 characters (an attribute or an item of a list), a list over 64 items,
// a unit ID over 256 bytes, and a unit type given twice, which leaves the
// unit to evaluate for unclear.
func CheckRemoteContext(c RemoteContext) error {
	var violations []apperrors.Violation
	if len(c.Attributes) > MaxContextAttributes {
		violations = append(violations, tooMany("/attributes", "a context has at most "+strconv.Itoa(MaxContextAttributes)+" attributes", MaxContextAttributes))
	}
	for key, value := range c.Attributes {
		pointer := "/attributes/" + pointerToken.Replace(key)
		switch v := value.(type) {
		case string:
			if utf8.RuneCountInString(v) > MaxContextString {
				violations = append(violations, tooLongString(pointer))
			}
		case []any:
			if len(v) > MaxContextListItems {
				violations = append(violations, tooMany(pointer, "a list has at most "+strconv.Itoa(MaxContextListItems)+" items", MaxContextListItems))
				continue
			}
			for i, item := range v {
				if s, ok := item.(string); ok && utf8.RuneCountInString(s) > MaxContextString {
					violations = append(violations, tooLongString(pointer+"/"+strconv.Itoa(i)))
				}
			}
		}
	}
	seen := make(map[string]bool, len(c.Units))
	duplicated := map[string]bool{}
	for _, u := range c.Units {
		pointer := "/units/" + pointerToken.Replace(u.Type)
		if seen[u.Type] {
			if !duplicated[u.Type] {
				duplicated[u.Type] = true
				violations = append(violations, apperrors.Violation{Pointer: pointer, Code: "duplicate", Detail: "a unit type is given once"})
			}
			continue
		}
		seen[u.Type] = true
		if len(u.ID) > flags.MaxUnitIDBytes {
			violations = append(violations, apperrors.Violation{
				Pointer: pointer, Code: "too_long",
				Detail: "a unit ID has at most " + strconv.Itoa(flags.MaxUnitIDBytes) + " bytes",
				Params: map[string]any{"max": flags.MaxUnitIDBytes},
			})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	slices.SortStableFunc(violations, func(a, b apperrors.Violation) int {
		switch {
		case a.Pointer < b.Pointer:
			return -1
		case a.Pointer > b.Pointer:
			return 1
		}
		return 0
	})
	return apperrors.Invalid("invalid_context", "the context is not valid", violations...)
}

func tooMany(pointer, detail string, limit int) apperrors.Violation {
	return apperrors.Violation{Pointer: pointer, Code: "too_many", Detail: detail, Params: map[string]any{"max": limit}}
}

func tooLongString(pointer string) apperrors.Violation {
	return apperrors.Violation{
		Pointer: pointer, Code: "too_long",
		Detail: "a string has at most " + strconv.Itoa(MaxContextString) + " characters",
		Params: map[string]any{"max": MaxContextString},
	}
}

// IgnoredCode says why an attribute was left out of an evaluation.
type IgnoredCode string

// Why attributes are left out.
const (
	// IgnoredUndeclared is an attribute the project does not declare, or
	// one named like a unit type, which never stands in for the unit.
	IgnoredUndeclared IgnoredCode = "undeclared"
	// IgnoredTypeMismatch is a value not of its attribute's declared type.
	IgnoredTypeMismatch IgnoredCode = "type_mismatch"
)

// IgnoredAttribute is an attribute left out of an evaluation, and why.
type IgnoredAttribute struct {
	Name string
	Code IgnoredCode
}

// TypeAttributes returns the attributes of a context that rs declares with
// a value of the declared type, and the others, ordered by name, with why
// each was left out. A null value of a declared attribute is kept: it is
// the attribute absent, never a mismatch. Leaving the others out, rather
// than letting a condition see them, makes a context evaluate remotely as
// the SDK's own evaluator would with the same declarations, and tells the
// developer why an attribute had no effect.
func TypeAttributes(rs *eval.Ruleset, attributes map[string]any) (map[string]any, []IgnoredAttribute) {
	declared := make(map[string]eval.AttributeType, len(rs.Attributes))
	for _, a := range rs.Attributes {
		declared[a.Key] = a.Type
	}
	typed := make(map[string]any, len(attributes))
	var ignored []IgnoredAttribute
	for key, value := range attributes {
		typ, ok := declared[key]
		switch {
		case !ok || slices.Contains(rs.UnitTypes, key):
			ignored = append(ignored, IgnoredAttribute{Name: key, Code: IgnoredUndeclared})
		case value != nil && !ofType(typ, value):
			ignored = append(ignored, IgnoredAttribute{Name: key, Code: IgnoredTypeMismatch})
		default:
			typed[key] = value
		}
	}
	slices.SortFunc(ignored, func(a, b IgnoredAttribute) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return typed, ignored
}

// ofType reports whether value, as JSON decodes it, is of typ: a semver is
// a string; a date an RFC 3339 string or Unix milliseconds, which the
// evaluator reads further; a list a list of strings. A type this server
// does not know accepts any value, leaving it to the evaluator.
func ofType(typ eval.AttributeType, value any) bool {
	switch typ {
	case eval.TypeString, eval.TypeSemver:
		_, ok := value.(string)
		return ok
	case eval.TypeNumber:
		_, ok := value.(float64)
		return ok
	case eval.TypeBoolean:
		_, ok := value.(bool)
		return ok
	case eval.TypeDate:
		switch value.(type) {
		case string, float64:
			return true
		}
		return false
	case eval.TypeList:
		list, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	return true
}
