package flags_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
)

func TestImportingTheConfigurationAFlagHasChangesNothing(t *testing.T) {
	in := base()
	in.Overrides = []flags.OverrideInput{{UnitType: "user", UnitID: "u-1", Variation: "b", Note: ptr("Demo")}}
	in.Rules = []flags.RuleInput{{Serve: rollout("user", "b", 5000)}}
	c := mustReplace(t, initial(), in)

	same, changed, err := c.Import(copyFlag, c.Input(), referents(nil), mint, flags.Write{By: &anna, Now: now.Add(time.Hour)})
	if err != nil || changed.Any || same.Version != c.Version || *same.Overrides[0].AddedBy != bob {
		t.Errorf("importing what the flag has = %+v, %+v, %v, want nothing changed and the override still bob's", same.Overrides, changed, err)
	}
}

func TestImportingAChangedOverrideMakesTheImporterItsAuthor(t *testing.T) {
	in := base()
	in.Overrides = []flags.OverrideInput{
		{UnitType: "user", UnitID: "u-1", Variation: "b"},
		{UnitType: "user", UnitID: "u-2", Variation: "b"},
	}
	c := mustReplace(t, initial(), in)

	doc := c.Input()
	doc.Overrides[1].Variation = "c"
	after, changed, err := c.Import(copyFlag, doc, referents(nil), mint, flags.Write{By: &anna, Now: now.Add(time.Hour)})
	if err != nil || !changed.Any {
		t.Fatalf("import = %v, %+v, want a change", err, changed)
	}
	if *after.Overrides[0].AddedBy != bob || *after.Overrides[1].AddedBy != anna {
		t.Errorf("authors = %v, %v, want bob's kept and anna on the changed one", after.Overrides[0].AddedBy, after.Overrides[1].AddedBy)
	}
}
