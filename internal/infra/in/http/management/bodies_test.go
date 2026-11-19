package management

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management/operations"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func pointersOf(t *testing.T, err error) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok {
		t.Fatalf("err = %v, want an application error", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer)
	}
	return out
}

func TestAMemberRequiredBelowTheTopOfABodyIsNamedByItsPointer(t *testing.T) {
	op := operations.Table["PUT /projects/{project}/flags/{flag}/environments/{environment}"]
	body := `{"on": true, "off_variation": "off", "fallthrough": "on", "prerequisites": [{"flag": "a", "variation": "x"}, {}, {"flag": null}]}`
	got := pointersOf(t, refuseMissingMembers([]byte(body), op))
	want := []string{"/prerequisites/1/flag", "/prerequisites/1/variation", "/prerequisites/2/flag", "/prerequisites/2/variation"}
	if !slices.Equal(got, want) {
		t.Errorf("pointers = %v, want %v", got, want)
	}
}

func TestABodyHoldingEverythingRequiredIsLeftAlone(t *testing.T) {
	op := operations.Table["PUT /projects/{project}/flags/{flag}/environments/{environment}"]
	for name, body := range map[string]string{
		"complete":             `{"on": false, "off_variation": "off", "fallthrough": "on", "unknown": [1, {"x": null}]}`,
		"empty":                ``,
		"not JSON at all":      `{"on": `,
		"a misplaced optional": `{"on": true, "off_variation": "a", "fallthrough": "b", "rules": null}`,
	} {
		if err := refuseMissingMembers([]byte(body), op); err != nil {
			t.Errorf("%s: %v, want it left to the decoder and the use case", name, err)
		}
	}
	if err := refuseMissingMembers([]byte(`{}`), operations.Entry{}); err != nil {
		t.Errorf("an operation with no body to check refused {}: %v", err)
	}
}

// A value of another type than its shape says, or no JSON at all, is the
// decoder's to refuse; what is the shape's is judged at every depth.
func TestAValueOfAnotherTypeThanItsShapeIsLeftToTheDecoder(t *testing.T) {
	got := func(shape operations.Shape, body string) []string {
		var out []string
		checkShape(json.RawMessage(body), shape, "", func(pointer, _ string) { out = append(out, pointer) })
		return out
	}
	table := operations.Table["PUT /projects/{project}/flags/{flag}/environments/{environment}"]
	root := operations.Shapes[table.Body]
	if len(got(root, `[1, 2]`)) != 0 || len(got(root, `"text"`)) != 0 || len(got(root, `{`)) != 0 {
		t.Error("a value of another type than the shape's, or no JSON, was judged here")
	}
	if got := got(root, `{"on": true, "off_variation": "a", "fallthrough": "b", "overrides": [{}]}`); len(got) == 0 {
		t.Errorf("an override lacking its members passed: %v", got)
	}
}
