package governance

import (
	"encoding/json"
	"strings"

	"github.com/azverinus/flagnetic/eval"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ErrChangeRequestRequired refuses a write that would change the ruleset of
// the protected environments keyed environments: such a change is proposed
// as a change request, and the write's transaction rolls back whole, the
// environments it changed that are not protected included.
func ErrChangeRequestRequired(environments []string) error {
	noun := "environment"
	if len(environments) > 1 {
		noun = "environments"
	}
	return apperrors.Forbidden("change_request_required", "this write would change the ruleset of the protected "+noun+" "+
		strings.Join(environments, ", ")+"; propose it as a change request")
}

// RulesetChanged reports whether after, a protected environment's ruleset as
// a write leaves it, serves anything differently from before, the ruleset
// of its committed version. What no evaluation depends on does not count,
// which keeps metadata edits, creating a flag, archiving or restoring an
// off flag, unused segments and declarations direct:
//
//   - the version and the environment's protection marker;
//   - the declarations, unit types and attributes;
//   - a segment unless a flag references it on both sides: one referenced
//     on one side only is so through a rule that changed, appeared or went,
//     which its flag's comparison judges;
//   - a flag present on one side only when it is off there, so that
//     creating a flag, and archiving or restoring one that is off, are not
//     changes to approve: no application relies on a flag nobody switched
//     on.
//
// Everything else of a flag present on both sides counts, an off flag's
// rules included: switching it on would serve them.
func RulesetChanged(before, after *eval.Ruleset) bool {
	beforeFlags, afterFlags := byKey(before.Flags, flagKey), byKey(after.Flags, flagKey)
	for key, b := range beforeFlags {
		a, ok := afterFlags[key]
		if !ok {
			if b.On {
				return true
			}
			continue
		}
		if !sameJSON(b, a) {
			return true
		}
	}
	for key, a := range afterFlags {
		if _, ok := beforeFlags[key]; !ok && a.On {
			return true
		}
	}
	referencedAfter := referencedSegments(after)
	beforeSegments, afterSegments := byKey(before.Segments, segmentKey), byKey(after.Segments, segmentKey)
	for key := range referencedSegments(before) {
		if referencedAfter[key] && !sameJSON(beforeSegments[key], afterSegments[key]) {
			return true
		}
	}
	return false
}

func flagKey(f eval.Flag) string       { return f.Key }
func segmentKey(s eval.Segment) string { return s.Key }

func byKey[T any](items []T, key func(T) string) map[string]T {
	out := make(map[string]T, len(items))
	for _, item := range items {
		out[key(item)] = item
	}
	return out
}

// referencedSegments are the segments a flag's rule of rs names, and those
// a segment so named names in turn, in case segments ever nest.
func referencedSegments(rs *eval.Ruleset) map[string]bool {
	segments := byKey(rs.Segments, segmentKey)
	out := map[string]bool{}
	var visit func(conditions []eval.Condition)
	visit = func(conditions []eval.Condition) {
		for _, c := range conditions {
			if (c.Kind == eval.ConditionInSegment || c.Kind == eval.ConditionNotInSegment) && !out[c.Segment] {
				out[c.Segment] = true
				visit(segments[c.Segment].Conditions)
			}
		}
	}
	for _, f := range rs.Flags {
		for _, r := range f.Rules {
			visit(r.Conditions)
		}
	}
	return out
}

// sameJSON compares two parts of a ruleset as SDKs receive them. Both come
// from decoded documents, so encoding cannot fail; were it to, the parts
// are judged different, which refuses rather than lets a change through.
func sameJSON(a, b any) bool {
	aj, aErr := json.Marshal(a)
	bj, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && string(aj) == string(bj)
}
