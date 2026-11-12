package transfer_test

import (
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/transfer"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// replacing is the project's document with a second flag, which requires the
// first, and an experiment of each status.
func replacing(t *testing.T) transfer.Document {
	t.Helper()
	d := clone(t, existing())
	d.Flags = append(d.Flags,
		transfer.Flag{Key: "extra", Type: "boolean", Name: "Extra", Configurations: map[string]transfer.Configuration{
			"production": {Prerequisites: []transfer.Prerequisite{{Flag: "checkout", Variation: "true"}}},
		}},
		transfer.Flag{Key: "extra_too", Type: "boolean", Name: "Extra too", Configurations: map[string]transfer.Configuration{
			"production": {Prerequisites: []transfer.Prerequisite{{Flag: "extra", Variation: "true"}}},
		}},
	)
	d.Experiments = []transfer.Experiment{
		{Key: "a_draft", Flag: "checkout", Status: "draft"}, {Key: "b_running", Flag: "checkout", Status: "running", InclusionSalt: "0123456789abcdef", VariationSalt: "fedcba9876543210"},
	}
	return d
}

func TestReplacingAProjectWithItsOwnDocumentDeletesNothing(t *testing.T) {
	cur := replacing(t)
	del := transfer.PlanDeletions(cur, clone(t, cur))
	if !del.Empty() || del.Refusal() != nil || len(del.Held) != 0 {
		t.Errorf("deletions = %+v, want none", del)
	}
}

func TestReplacingDeletesWhatTheDocumentLacksButTheStandardAttributes(t *testing.T) {
	cur := replacing(t)
	cur.Attributes = append(cur.Attributes, transfer.Attribute{Key: "app_version", Type: "semver", Name: "App version"})
	doc := clone(t, cur)
	doc.Environments, doc.UnitTypes, doc.Attributes = doc.Environments[:1], nil, nil
	doc.Segments, doc.Flags, doc.Experiments = nil, nil, nil
	cur.Experiments = cur.Experiments[:1]

	del := transfer.PlanDeletions(cur, doc)
	if !slices.Equal(del.Environments, []string{"production"}) || !slices.Equal(del.UnitTypes, []string{"user"}) ||
		!slices.Equal(del.Attributes, []string{"plan"}) || !slices.Equal(del.Segments, []string{"beta"}) ||
		!slices.Equal(del.Experiments, []string{"a_draft"}) {
		t.Errorf("deletions = %+v", del)
	}
	if del.Empty() {
		t.Error("Empty = true")
	}
}

func TestAFlagIsDeletedAfterTheAbsentFlagsThatRequireIt(t *testing.T) {
	cur := replacing(t)
	doc := clone(t, cur)
	doc.Flags, doc.Experiments = nil, nil
	cur.Experiments = nil

	del := transfer.PlanDeletions(cur, doc)
	if want := []string{"extra_too", "extra", "checkout"}; !slices.Equal(del.Flags, want) {
		t.Errorf("flags = %v, want %v", del.Flags, want)
	}

	// A flag that is kept is not ordered at all; the one requiring it goes.
	doc = clone(t, cur)
	doc.Flags = doc.Flags[:2]
	if got := transfer.PlanDeletions(cur, doc).Flags; !slices.Equal(got, []string{"extra_too"}) {
		t.Errorf("flags = %v, want extra_too", got)
	}
}

func TestAnAbsentExperimentThatStartedIsNamedWithItsStatusAndNotDeleted(t *testing.T) {
	cur := replacing(t)
	doc := clone(t, cur)
	doc.Experiments = nil

	del := transfer.PlanDeletions(cur, doc)
	if !slices.Equal(del.Experiments, []string{"a_draft"}) {
		t.Errorf("experiments = %v, want the draft", del.Experiments)
	}
	err := del.Refusal()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindConflict || e.Code != "not_draft" {
		t.Fatalf("refusal = %v, want 409 not_draft", err)
	}
	if len(e.Violations) != 1 || e.Violations[0].Params["key"] != "b_running" || e.Violations[0].Params["status"] != "running" {
		t.Errorf("violations = %+v, want b_running as running", e.Violations)
	}
}

func TestAnExperimentOfTheDocumentKeepsItsAbsentFlagInUse(t *testing.T) {
	cur := replacing(t)
	doc := clone(t, cur)
	doc.Flags = doc.Flags[1:]

	del := transfer.PlanDeletions(cur, doc)
	if !slices.Equal(del.Flags, []string{"checkout"}) {
		t.Fatalf("flags = %v", del.Flags)
	}
	var held []string
	for _, r := range del.Held {
		held = append(held, r.Kind+" "+r.Key)
	}
	if want := []string{"experiment a_draft", "experiment b_running"}; !slices.Equal(held, want) {
		t.Errorf("held = %v, want %v", held, want)
	}
}

func TestAReplaceIsCheckedAsACreateIsAndAsAMergeIs(t *testing.T) {
	cur := replacing(t)
	if err := transfer.CheckReplace(clone(t, cur), cur); err != nil {
		t.Fatalf("the project's own document = %v, want it accepted", err)
	}

	got := func(d transfer.Document) []string {
		e, ok := apperrors.From(transfer.CheckReplace(d, cur))
		if !ok || e.Code != "invalid_document" {
			t.Fatalf("check = %v, want invalid_document", e)
		}
		var out []string
		for _, v := range e.Violations {
			out = append(out, v.Pointer+" "+v.Code)
		}
		return out
	}

	if faults := got(transfer.Document{SchemaVersion: transfer.Version}); !slices.Contains(faults, "/flags required") {
		t.Errorf("faults = %v, want every section required", faults)
	}

	d := clone(t, cur)
	d.Flags[0].Type = "string"
	if faults := got(d); !slices.Equal(faults, []string{"/flags/0/type type_immutable"}) {
		t.Errorf("faults = %v, want the type kept", faults)
	}

	d = clone(t, cur)
	d.Environments = d.Environments[:1]
	if faults := got(d); !slices.Contains(faults, "/flags/0/configurations/production unknown_environment") {
		t.Errorf("faults = %v, want a configuration of a deleted environment refused", faults)
	}
}

func TestAReplaceMayReuseTheSaltOfWhatItDeletes(t *testing.T) {
	cur := replacing(t)
	cur.Experiments = []transfer.Experiment{}
	// checkout is deleted with its rollout, whose salt the new flag takes.
	doc := clone(t, cur)
	reused := doc.Flags[0].Configurations["production"].Rules[0]
	reused.ID = ruleB
	doc.Flags = []transfer.Flag{{Key: "fresh", Type: "boolean", Name: "Fresh", Configurations: map[string]transfer.Configuration{
		"production": {Rules: []transfer.Rule{reused}},
	}}}
	if err := transfer.CheckReplace(doc, cur); err != nil {
		e, _ := apperrors.From(err)
		t.Errorf("replace = %+v, want the salt of a deleted flag free", e.Violations)
	}
	if err := transfer.CheckMerge(doc, cur); err == nil {
		t.Error("merge accepted a salt the project's flag keeps")
	}
}
