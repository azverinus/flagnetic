package eval

import (
	"fmt"
	"strings"
	"testing"
)

// TestEvaluateAllMemoizesPrerequisites: within one call every flag is
// computed once, however many flags require it. A lattice of five levels
// of five flags, each requiring every flag of the level below, would need
// thousands of computations without memoization.
func TestEvaluateAllMemoizesPrerequisites(t *testing.T) {
	const levels, width = 5, 5
	var flags []string
	for l := range levels {
		for w := range width {
			var prereqs []string
			if l > 0 {
				for p := range width {
					prereqs = append(prereqs, fmt.Sprintf(`{"flag":"f%d_%d","variation":"true"}`, l-1, p))
				}
			}
			flags = append(flags, fmt.Sprintf(`{"key":"f%d_%d","type":"boolean","on":true,
				"variations":[{"key":"true","value":true},{"key":"false","value":false}],
				"off_variation":"false","fallthrough":"true","prerequisites":[%s]}`, l, w, strings.Join(prereqs, ",")))
		}
	}
	rs, err := Decode([]byte(`{"schema_version":1,"version":1,"scope":"server","unit_types":[],
		"flags":[` + strings.Join(flags, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}

	e := rs.newEvaluation(Context{})
	for _, f := range rs.Flags {
		if r := e.flag(f.Key); r.Reason != ReasonFallthrough {
			t.Fatalf("%s: %+v", f.Key, r)
		}
	}
	if e.computed != levels*width {
		t.Errorf("computed %d flags, want each of the %d once", e.computed, levels*width)
	}

	one := rs.newEvaluation(Context{})
	one.flag(fmt.Sprintf("f%d_0", levels-1))
	if want := (levels-1)*width + 1; one.computed != want {
		t.Errorf("a single evaluation computed %d flags, want %d", one.computed, want)
	}
}
