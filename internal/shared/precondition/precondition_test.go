package precondition_test

import (
	"errors"
	"reflect"
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
		if err != nil || !reflect.DeepEqual(got, precondition.On(want)) {
			t.Errorf("Parse(%q) = %v, %v, want %d", in, got, err, want)
		}
	}
	for _, in := range []string{``, `"7`, `7"`, `"7" "8"`, `W/`, `"7",`, `*,`, `a b`} {
		if _, err := precondition.Parse(in); err == nil {
			t.Errorf("Parse(%q) accepted, want refused", in)
		}
	}
}

// RFC 9110: If-Match compares strongly, so a weak tag matches nothing; `*`
// matches any current version; a list matches when it holds the current one.
func TestIfMatchIsReadAsTheRFCReadsIt(t *testing.T) {
	for in, want := range map[string]bool{
		`"6"`: false, `"7"`: true, `W/"7"`: false, `*`: true, `"6", "7"`: true, `"6","8"`: false, `W/"7", "6"`: false,
		`7`: true, `"x"`: false, `"0"`: false,
	} {
		v, err := precondition.Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) = %v, want it read", in, err)
			continue
		}
		err = precondition.Check(v, 7)
		if (err == nil) != want {
			t.Errorf("If-Match: %s against version 7 = %v, want match %t", in, err, want)
		}
		if err != nil {
			if e, ok := apperrors.From(err); !ok || e.Kind != apperrors.KindPreconditionFailed {
				t.Errorf("If-Match: %s answered %v, want a failed precondition", in, err)
			}
		}
	}
}

func TestATagListMatchesTheCurrentTagOfAnOpaqueEntity(t *testing.T) {
	tags, err := precondition.ParseTags(`"0197a1", W/"0197b2", 0197c3`)
	if err != nil {
		t.Fatal(err)
	}
	for tag, want := range map[string]bool{"0197a1": true, "0197b2": false, "0197c3": true, "": false} {
		if got := tags.Matches(tag); got != want {
			t.Errorf("Matches(%q) = %t, want %t", tag, got, want)
		}
	}
}
