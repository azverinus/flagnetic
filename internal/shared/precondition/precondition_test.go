package precondition_test

import (
	"errors"
	"testing"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

func TestAWriteMustNameTheVersionItRead(t *testing.T) {
	if err := precondition.Check(precondition.None(), 3); !errors.Is(err, precondition.ErrRequired) {
		t.Errorf("no version = %v, want ErrRequired", err)
	}
	if err := precondition.Check(precondition.On(3), 3); err != nil {
		t.Errorf("the current version = %v, want nil", err)
	}
	err := precondition.Check(precondition.On(2), 3)
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindPreconditionFailed || e.Code != "version_mismatch" || e.Extensions["current_version"] != int64(3) {
		t.Errorf("a stale version = %v, want version_mismatch naming 3", err)
	}
}

func TestAVersionIsParsedQuotedOrBare(t *testing.T) {
	for in, want := range map[string]int64{`"7"`: 7, `7`: 7, ` "12" `: 12} {
		got, err := precondition.Parse(in)
		if err != nil || got != precondition.On(want) {
			t.Errorf("Parse(%q) = %v, %v, want %d", in, got, err, want)
		}
	}
	for _, in := range []string{``, `W/"7"`, `*`, `"x"`, `"7`, `-1`, `"0"`, `"7", "8"`} {
		if _, err := precondition.Parse(in); err == nil {
			t.Errorf("Parse(%q) accepted, want refused", in)
		}
	}
}
