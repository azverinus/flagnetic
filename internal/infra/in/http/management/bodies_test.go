package management

import (
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
