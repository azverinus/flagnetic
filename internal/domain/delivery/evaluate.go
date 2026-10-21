package delivery

import (
	"slices"
	"strconv"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// CheckContext refuses a context SDKs would not send: a unit ID over 256
// bytes, which no override can name and no rollout should hash, with
// invalid_context naming every such unit. An empty unit ID counts as
// absent, as every evaluator treats it.
func CheckContext(ctx eval.Context) error {
	var violations []apperrors.Violation
	types := make([]string, 0, len(ctx.Units))
	for unitType := range ctx.Units {
		types = append(types, unitType)
	}
	slices.Sort(types)
	for _, unitType := range types {
		if len(ctx.Units[unitType]) > flags.MaxUnitIDBytes {
			violations = append(violations, apperrors.Violation{
				Pointer: "/units/" + escapePointer(unitType), Code: "too_long",
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

// escapePointer escapes a key as a JSON Pointer reference token.
func escapePointer(key string) string {
	out := make([]byte, 0, len(key))
	for i := range len(key) {
		switch key[i] {
		case '~':
			out = append(out, "~0"...)
		case '/':
			out = append(out, "~1"...)
		default:
			out = append(out, key[i])
		}
	}
	return string(out)
}

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
// flag_not_found. Nothing is recorded: an exposure is the SDK's explicit
// call, never evaluation's.
func Evaluate(rs *eval.Ruleset, ctx eval.Context, keys []string) map[string]Result {
	var results map[string]eval.Result
	if keys == nil {
		results = rs.EvaluateAll(ctx)
	} else {
		results = make(map[string]eval.Result, len(keys))
		for _, key := range keys {
			results[key] = rs.Evaluate(ctx, key)
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
