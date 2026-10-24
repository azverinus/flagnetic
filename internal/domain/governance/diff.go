package governance

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// PatchOperation is one operation of an RFC 6902 JSON Patch: add, remove or
// replace the value at Path, a JSON Pointer. Value is unused by remove.
type PatchOperation struct {
	Op    string
	Path  string
	Value any
}

// Diff returns the JSON Patch turning base into proposed, both JSON values as
// encoding/json decodes them into an any; empty, never nil, when they are
// equal. Object members are compared in the order of their names, so the
// same two documents always give the same patch. Arrays are matched by their
// longest common subsequence of equal elements, so a rule inserted or removed
// is one operation and not a rewrite of every rule after it; an element
// changed in place is diffed member by member, which shows a reviewer the
// one field that moved.
func Diff(base, proposed any) []PatchOperation {
	return diffValue(make([]PatchOperation, 0), "", base, proposed)
}

func diffValue(ops []PatchOperation, path string, a, b any) []PatchOperation {
	if reflect.DeepEqual(a, b) {
		return ops
	}
	switch av := a.(type) {
	case map[string]any:
		if bv, ok := b.(map[string]any); ok {
			return diffObject(ops, path, av, bv)
		}
	case []any:
		if bv, ok := b.([]any); ok {
			return diffArray(ops, path, av, bv)
		}
	}
	return append(ops, PatchOperation{Op: "replace", Path: path, Value: b})
}

func diffObject(ops []PatchOperation, path string, a, b map[string]any) []PatchOperation {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		av, inA := a[k]
		bv, inB := b[k]
		p := path + "/" + escapePointer(k)
		switch {
		case !inB:
			ops = append(ops, PatchOperation{Op: "remove", Path: p})
		case !inA:
			ops = append(ops, PatchOperation{Op: "add", Path: p, Value: bv})
		default:
			ops = diffValue(ops, p, av, bv)
		}
	}
	return ops
}

// diffArray walks the edit script from a's longest common subsequence with
// b. Each run of elements between two kept ones is removals from a and
// insertions from b: as many as both have are taken as elements changed in
// place, the rest removed or added. Indices are those of the array as the
// operations before have left it, as RFC 6902 applies them in order.
func diffArray(ops []PatchOperation, path string, a, b []any) []PatchOperation {
	// common[i][j] is the length of the longest common subsequence of a[i:]
	// and b[j:].
	common := make([][]int, len(a)+1)
	for i := range common {
		common[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if reflect.DeepEqual(a[i], b[j]) {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	at := 0
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if i < len(a) && j < len(b) && reflect.DeepEqual(a[i], b[j]) {
			i, j, at = i+1, j+1, at+1
			continue
		}
		// Collect the run of removals and insertions up to the next kept
		// element.
		removed, added := []any{}, []any{}
		for i < len(a) || j < len(b) {
			if i < len(a) && j < len(b) && reflect.DeepEqual(a[i], b[j]) {
				break
			}
			if j == len(b) || (i < len(a) && common[i+1][j] >= common[i][j+1]) {
				removed, i = append(removed, a[i]), i+1
			} else {
				added, j = append(added, b[j]), j+1
			}
		}
		changed := min(len(removed), len(added))
		for k := range changed {
			ops = diffValue(ops, path+"/"+strconv.Itoa(at+k), removed[k], added[k])
		}
		for range len(removed) - changed {
			ops = append(ops, PatchOperation{Op: "remove", Path: path + "/" + strconv.Itoa(at+changed)})
		}
		for k, v := range added[changed:] {
			ops = append(ops, PatchOperation{Op: "add", Path: path + "/" + strconv.Itoa(at+changed+k), Value: v})
		}
		at += len(added)
	}
	return ops
}

// escapePointer escapes a member name as a JSON Pointer reference token.
func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
