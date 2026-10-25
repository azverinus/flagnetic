package governance_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
)

func doc(t *testing.T, s string) governance.Document {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestADiffNamesEveryChangedMemberByItsPointer(t *testing.T) {
	base := doc(t, `{"on":false,"fallthrough":"a","overrides":[],"rules":[{"id":"r1","serve":{"kind":"variation","variation":"a"}}]}`)
	proposed := doc(t, `{"on":true,"fallthrough":"b","overrides":[{"unit_id":"u1"}],"rules":[{"id":"r1","serve":{"kind":"variation","variation":"b"}}]}`)

	got := governance.Diff(base, proposed)
	want := []governance.PatchOperation{
		{Op: "replace", Path: "/fallthrough", Value: "b"},
		{Op: "replace", Path: "/on", Value: true},
		{Op: "add", Path: "/overrides/0", Value: map[string]any{"unit_id": "u1"}},
		{Op: "replace", Path: "/rules/0/serve/variation", Value: "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %+v, want %+v", got, want)
	}
}

func TestEqualDocumentsDiffToAnEmptyPatch(t *testing.T) {
	d := `{"on":true,"rules":[{"id":"r1"}],"prerequisites":[]}`
	if got := governance.Diff(doc(t, d), doc(t, d)); got == nil || len(got) != 0 {
		t.Errorf("diff = %#v, want an empty patch, not nil", got)
	}
}

func TestADiffAddsAndRemovesMembers(t *testing.T) {
	got := governance.Diff(doc(t, `{"a":1,"b":2}`), doc(t, `{"b":2,"c":{"d":null}}`))
	want := []governance.PatchOperation{
		{Op: "remove", Path: "/a"},
		{Op: "add", Path: "/c", Value: map[string]any{"d": nil}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %+v, want %+v", got, want)
	}
}

func TestARuleInsertedInFrontIsOneAddNotARewriteOfEveryRule(t *testing.T) {
	base := doc(t, `{"rules":[{"id":"r1"},{"id":"r2"}]}`)
	proposed := doc(t, `{"rules":[{"id":"r0"},{"id":"r1"},{"id":"r2"}]}`)
	want := []governance.PatchOperation{{Op: "add", Path: "/rules/0", Value: map[string]any{"id": "r0"}}}
	if got := governance.Diff(base, proposed); !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %+v, want %+v", got, want)
	}
}

func TestPointersEscapeTildeAndSlash(t *testing.T) {
	got := governance.Diff(doc(t, `{"a/b~c":1}`), doc(t, `{"a/b~c":2}`))
	if len(got) != 1 || got[0].Path != "/a~1b~0c" {
		t.Errorf("diff = %+v, want the path /a~1b~0c", got)
	}
}

// TestApplyingTheDiffGivesTheProposedDocument checks the patch against an
// RFC 6902 interpreter, over documents shaped like configurations whose
// arrays grow, shrink, reorder and change in place.
func TestApplyingTheDiffGivesTheProposedDocument(t *testing.T) {
	docs := []string{
		`{"on":false,"rules":[]}`,
		`{"on":true,"rules":[{"id":"r1","c":[1,2,3]}]}`,
		`{"on":true,"rules":[{"id":"r2"},{"id":"r1","c":[3,2]}],"x":"y"}`,
		`{"rules":[{"id":"r3"},{"id":"r1","c":[]},{"id":"r2"},{"id":"r4","c":[1]}]}`,
		`{"on":"no","rules":{"id":"r1"}}`,
		`{"rules":[1,2,3,4,5,6],"o":{"p":[{"q":1},{"q":2}]}}`,
		`{"rules":[6,5,4,3,2,1],"o":{"p":[{"q":2}]}}`,
		`[]`,
		`[{"a":1},2,"3"]`,
		`null`,
	}
	for i, b := range docs {
		for j, p := range docs {
			base, proposed := doc(t, b), doc(t, p)
			patch := governance.Diff(base, proposed)
			got, err := applyPatch(doc(t, b), patch)
			if err != nil {
				t.Errorf("%d→%d: applying %+v: %v", i, j, patch, err)
				continue
			}
			if !reflect.DeepEqual(got, proposed) {
				t.Errorf("%d→%d: applying %+v gives %v, want %v", i, j, patch, got, proposed)
			}
			if i == j && len(patch) != 0 {
				t.Errorf("%d: a document diffs against itself as %+v", i, patch)
			}
		}
	}
}

// applyPatch applies patch to d as RFC 6902 does, for the operations Diff
// emits.
func applyPatch(d any, patch []governance.PatchOperation) (any, error) {
	for _, op := range patch {
		var err error
		if d, err = applyOp(d, op.Op, pointerTokens(op.Path), op.Value); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func pointerTokens(p string) []string {
	if p == "" {
		return nil
	}
	parts := strings.Split(p[1:], "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts
}

func applyOp(d any, op string, path []string, value any) (any, error) {
	if len(path) == 0 {
		if op == "remove" {
			return nil, nil
		}
		return value, nil
	}
	head, rest := path[0], path[1:]
	switch v := d.(type) {
	case map[string]any:
		if len(rest) > 0 {
			child, err := applyOp(v[head], op, rest, value)
			v[head] = child
			return v, err
		}
		if op == "remove" {
			delete(v, head)
		} else {
			v[head] = value
		}
		return v, nil
	case []any:
		i, err := strconv.Atoi(head)
		if err != nil || i < 0 || i > len(v) {
			return nil, &json.UnsupportedValueError{Str: "index " + head}
		}
		if len(rest) > 0 {
			child, err := applyOp(v[i], op, rest, value)
			v[i] = child
			return v, err
		}
		switch op {
		case "add":
			return append(v[:i], append([]any{value}, v[i:]...)...), nil
		case "remove":
			return append(v[:i], v[i+1:]...), nil
		default:
			v[i] = value
			return v, nil
		}
	}
	return nil, &json.UnsupportedValueError{Str: "path into a scalar"}
}

func TestAnArrayTooLongToDiffElementByElementIsReplacedWhole(t *testing.T) {
	long := make([]any, 1001)
	for i := range long {
		long[i] = float64(i)
	}
	changed := slices.Clone(long)
	changed[500] = "moved"

	got := governance.Diff(map[string]any{"overrides": long}, map[string]any{"overrides": changed})

	want := []governance.PatchOperation{{Op: "replace", Path: "/overrides", Value: changed}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Diff = %d operations, want the array replaced whole", len(got))
	}
}
