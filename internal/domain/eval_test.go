package domain_test

import (
	"testing"

	"github.com/azverinus/flagnetic/eval"
)

// TestTheEvaluatorIsAvailableToTheDomain: the server evaluates with the same
// module the SDKs import, resolved from this repository rather than a
// published version, so a change to the evaluator reaches the server in the
// same pull request.
func TestTheEvaluatorIsAvailableToTheDomain(t *testing.T) {
	rs, err := eval.Decode([]byte(`{"schema_version":1,"version":1,"scope":"server"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r := rs.Evaluate(eval.Context{}, "absent"); r.ErrorCode != eval.ErrorFlagNotFound {
		t.Errorf("Evaluate(absent) = %+v, want error %s", r, eval.ErrorFlagNotFound)
	}
}
