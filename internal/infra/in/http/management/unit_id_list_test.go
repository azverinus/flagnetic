package management_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// A list far over the most a check takes is cut one past the bound, so the
// request is refused without holding millions of strings.
func TestAUnitIDListStopsOneEntryPastTheBound(t *testing.T) {
	body := `[` + strings.Repeat(`"",`, 1_000_000) + `""]`
	var ids management.UnitIDList
	if err := json.Unmarshal([]byte(body), &ids); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := experiments.MaxUniformityUnitIDs + 1; len(ids) != want {
		t.Errorf("decoded %d entries, want %d", len(ids), want)
	}
}

func TestAUnitIDListKeepsAListWithinTheBound(t *testing.T) {
	var ids management.UnitIDList
	if err := json.Unmarshal([]byte(`["a","bé"]`), &ids); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "bé" {
		t.Errorf("decoded %q", []string(ids))
	}
	var null management.UnitIDList
	if err := json.Unmarshal([]byte(`null`), &null); err != nil || null != nil {
		t.Errorf("null = %v, %v", null, err)
	}
}

func TestAUnitIDListRefusesWhatIsNotAnArrayOfStrings(t *testing.T) {
	for _, body := range []string{`{}`, `"a"`, `[1]`, `[null]`, `["a",["b"]]`} {
		var ids management.UnitIDList
		if err := json.Unmarshal([]byte(body), &ids); err == nil {
			t.Errorf("%s decoded to %q", body, []string(ids))
		}
	}
}
