package delivery

import (
	"slices"
	"strconv"
	"strings"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// MaxAttributeStringBytes is how long a string of a context evaluated for
// the management API may be, as an attribute's value or in its list.
// Matching a pattern takes time in proportion to the value's length, and
// matching cannot be interrupted, so a value of any length would let one
// evaluation hold a processor for as long as its sender liked.
const MaxAttributeStringBytes = 1024

// CheckContext refuses a context SDKs would not send: a unit ID over 256
// bytes, which no override can name and no rollout should hash, and an
// attribute holding a string over MaxAttributeStringBytes, with
// invalid_context naming every such unit and attribute. An empty unit ID
// counts as absent, as every evaluator treats it.
func CheckContext(ctx eval.Context) error {
	var violations []apperrors.Violation
	attributes := make([]string, 0, len(ctx.Attributes))
	for key := range ctx.Attributes {
		attributes = append(attributes, key)
	}
	slices.Sort(attributes)
	for _, key := range attributes {
		if overlongString(ctx.Attributes[key]) {
			violations = append(violations, apperrors.Violation{
				Pointer: "/attributes/" + pointerToken.Replace(key), Code: "too_long",
				Detail: "a string of an attribute has at most " + strconv.Itoa(MaxAttributeStringBytes) + " bytes",
				Params: map[string]any{"max": MaxAttributeStringBytes},
			})
		}
	}
	types := make([]string, 0, len(ctx.Units))
	for unitType := range ctx.Units {
		types = append(types, unitType)
	}
	slices.Sort(types)
	for _, unitType := range types {
		if len(ctx.Units[unitType]) > flags.MaxUnitIDBytes {
			violations = append(violations, apperrors.Violation{
				Pointer: "/units/" + pointerToken.Replace(unitType), Code: "too_long",
				Detail: "a unit ID has at most " + strconv.Itoa(flags.MaxUnitIDBytes) + " bytes",
				Params: map[string]any{"max": flags.MaxUnitIDBytes},
			})
		}
	}
	if len(violations) > 0 {
		return apperrors.Invalid("invalid_context", "the context is not valid", violations...)
	}
	return nil
}

// overlongString reports whether value, an attribute's, is a string over
// MaxAttributeStringBytes or a list holding one.
func overlongString(value any) bool {
	switch v := value.(type) {
	case string:
		return len(v) > MaxAttributeStringBytes
	case []any:
		return slices.ContainsFunc(v, overlongString)
	}
	return false
}

// pointerToken escapes a key as a reference token of a JSON Pointer.
var pointerToken = strings.NewReplacer("~", "~0", "/", "~1")

// Result is what evaluating one flag served a context, with the full
// reason the reference evaluator gives, and for a rollout the salt its
// bucket was computed with, so anyone can recompute it.
type Result struct {
	eval.Result
	// Salt is the salt of the rollout that served, for reason rollout.
	Salt string
}

// Evaluate evaluates rs for ctx: every flag of it, or only those keyed by
// keys when keys is not nil, a key it does not hold answering
// flag_not_found and a key named twice evaluated once. Nothing is recorded:
// an exposure is the SDK's explicit call, never evaluation's.
func Evaluate(rs *eval.Ruleset, ctx eval.Context, keys []string) map[string]Result {
	var results map[string]eval.Result
	if keys == nil {
		results = rs.EvaluateAll(ctx)
	} else {
		results = make(map[string]eval.Result, len(keys))
		for _, key := range keys {
			if _, done := results[key]; !done {
				results[key] = rs.Evaluate(ctx, key)
			}
		}
	}
	out := make(map[string]Result, len(results))
	for key, r := range results {
		out[key] = Result{Result: r, Salt: saltOf(rs, key, r)}
	}
	return out
}

// saltOf returns the salt of the rollout that served r for the flag keyed
// key, "" for a result no rollout served.
func saltOf(rs *eval.Ruleset, key string, r eval.Result) string {
	if r.Reason != eval.ReasonRollout {
		return ""
	}
	f, _ := rs.Flag(key)
	for _, rule := range f.Rules {
		if rule.ID == r.RuleID {
			return rule.Serve.Salt
		}
	}
	return ""
}
