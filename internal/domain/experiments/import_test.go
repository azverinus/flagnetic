package experiments_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	started  = now.Add(-72 * time.Hour)
	stoppedA = now.Add(-24 * time.Hour)
	end      = time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
)

// imported is an experiment as a document gives it, a draft, valid in every
// field.
func imported() experiments.Imported {
	return experiments.Imported{
		ID: newID(), Project: project, Key: "checkout_v2", Name: "Checkout v2", Hypothesis: "Short titles convert.",
		Owner: experiments.Owner{Member: anna, Eligible: true}, Tags: []string{"web"},
		Flag: &flag, FlagKey: "checkout_title", UnitType: "user", DeclaredUnitTypes: []string{"user", "anonymous"},
		Variations:        []experiments.Weight{{Variation: "long", Weight: 4000}, {Variation: "short", Weight: 6000}},
		TrafficAllocation: 2500, Salts: experiments.Salts{Inclusion: "a3f9c2e17b4d0e58", Variation: "7e1d4c90b2a6f3d1"},
		Status: experiments.StatusDraft, CreatedBy: &anna, Now: now,
	}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func newID() experiments.ExperimentID { return id.New[experiments.Experiment](ids) }

func newMember() access.MemberID { return id.New[access.Member](ids) }

func runningImport() experiments.Imported {
	in := imported()
	in.Status, in.StartedAt, in.ExpectedEndAt = experiments.StatusRunning, &started, &end
	return in
}

func stoppedImport() experiments.Imported {
	in := runningImport()
	in.Status, in.StoppedAt = experiments.StatusStopped, &stoppedA
	in.Decision = &experiments.Decision{Outcome: experiments.OutcomeWinner, WinnerVariation: "short", Note: "Short wins."}
	return in
}

func archivedAfterStop() experiments.Imported {
	in := stoppedImport()
	at := now.Add(-time.Hour)
	in.Status, in.ArchivedAt = experiments.StatusArchived, &at
	return in
}

func TestAnExperimentIsImportedExactlyAsTheDocumentHasIt(t *testing.T) {
	for name, in := range map[string]experiments.Imported{
		"draft": imported(), "running": runningImport(), "stopped": stoppedImport(), "archived after a stop": archivedAfterStop(),
	} {
		e, err := experiments.Import(in)
		if err != nil {
			t.Fatalf("%s: Import = %v", name, err)
		}
		if e.Status != in.Status || e.InclusionSalt != in.Salts.Inclusion || e.VariationSalt != in.Salts.Variation ||
			e.TrafficAllocation != 2500 || !sameTime(e.StartedAt, in.StartedAt) || !sameTime(e.StoppedAt, in.StoppedAt) ||
			!sameTime(e.ArchivedAt, in.ArchivedAt) || (e.Decision == nil) != (in.Decision == nil) ||
			(e.Decision != nil && *e.Decision != *in.Decision) || e.Version != 1 || !e.CreatedAt.Equal(now) ||
			!e.UpdatedAt.Equal(now) || e.Flag != flag.ID || e.Owner != anna {
			t.Errorf("%s: imported = %+v, want the document's status, salts, timestamps and decision", name, e)
		}
	}
}

func TestAnArchivedDraftIsImportedWithoutAnyHistoryOfARun(t *testing.T) {
	in := imported()
	at := now.Add(-time.Hour)
	in.Status, in.ArchivedAt, in.Hypothesis = experiments.StatusArchived, &at, ""
	if _, err := experiments.Import(in); err != nil {
		t.Fatalf("Import = %v, want an archived draft accepted, hypothesis blank included", err)
	}
}

func TestAnImportedDayIsKeptAsTheDayAndTimestampsToTheMicrosecond(t *testing.T) {
	in := stoppedImport()
	odd := started.Add(123 * time.Nanosecond)
	in.StartedAt = &odd
	e, err := experiments.Import(in)
	if err != nil {
		t.Fatal(err)
	}
	if !e.StartedAt.Equal(started) || e.StartedAt.Location() != time.UTC {
		t.Errorf("started_at = %v, want %v to the microsecond in UTC", e.StartedAt, started)
	}
}

func TestAnImportedExperimentFailsEveryRuleOfAnOrdinaryCreate(t *testing.T) {
	in := imported()
	in.Key, in.Name, in.Hypothesis, in.Tags = "9bad", " ", string(make([]byte, 2001)), []string{"", "a", "a"}
	in.Owner.Eligible = false
	in.TrafficAllocation = 10001
	in.UnitType = "company"
	in.Variations = []experiments.Weight{{Variation: "long", Weight: 4000}, {Variation: "huge2", Weight: 5000}}
	_, err := experiments.Import(in)
	want := []string{
		"/key invalid_key", "/name invalid_name", "/hypothesis too_long", "/owner invalid_owner", "/tags/0 invalid_value",
		"/tags/2 duplicate", "/traffic_allocation out_of_range", "/unit_type undeclared_unit_type",
		"/variations/1/variation unknown_variation", "/variations invalid_weights_sum",
	}
	if got := violations(t, err); !slices.Equal(got, want) {
		t.Errorf("violations = %v, want %v", got, want)
	}
}

func TestADraftOrRunningExperimentNeedsAFlagThatIsNotArchivedAStoppedOneOnlyAFlag(t *testing.T) {
	gone := imported()
	gone.Flag = nil
	for _, in := range []experiments.Imported{gone, runningImport(), stoppedImport(), archivedAfterStop()} {
		in.Flag = nil
		if got := violations(t, mustFail(t, in)); !slices.Equal(got, []string{"/flag unknown_flag"}) {
			t.Errorf("%s without a flag: violations = %v", in.Status, got)
		}
	}
	archivedFlag := flag
	archivedFlag.Archived = true
	for status, in := range map[experiments.Status]experiments.Imported{
		experiments.StatusDraft: imported(), experiments.StatusRunning: runningImport(),
		experiments.StatusStopped: stoppedImport(), experiments.StatusArchived: archivedAfterStop(),
	} {
		in.Flag = &archivedFlag
		_, err := experiments.Import(in)
		switch status {
		case experiments.StatusDraft, experiments.StatusRunning:
			if got := violations(t, err); !slices.Equal(got, []string{"/flag flag_archived"}) {
				t.Errorf("%s on an archived flag: violations = %v", status, err)
			}
		default:
			if err != nil {
				t.Errorf("%s on an archived flag: %v, want it outliving the flag's archiving", status, err)
			}
		}
	}
}

func mustFail(t *testing.T, in experiments.Imported) error {
	t.Helper()
	_, err := experiments.Import(in)
	if err == nil {
		t.Fatalf("Import(%s) succeeded, want a refusal", in.Status)
	}
	return err
}

func TestADraftOrRunningOrStoppedExperimentNeedsItsUnitTypeAnArchivedOneDoesNot(t *testing.T) {
	for _, in := range []experiments.Imported{imported(), runningImport(), stoppedImport()} {
		in.UnitType = "company"
		if got := violations(t, mustFail(t, in)); !slices.Equal(got, []string{"/unit_type undeclared_unit_type"}) {
			t.Errorf("%s with an undeclared unit type: violations = %v", in.Status, got)
		}
	}
	in := archivedAfterStop()
	in.UnitType = "company"
	if _, err := experiments.Import(in); err != nil {
		t.Errorf("archived with an undeclared unit type: %v, want it kept", err)
	}
}

func TestTheSaltsOfAnImportedExperimentAreWellFormedAndApart(t *testing.T) {
	in := imported()
	in.Salts = experiments.Salts{Inclusion: "NOT-HEX", Variation: ""}
	if got := violations(t, mustFail(t, in)); !slices.Equal(got, []string{"/inclusion_salt invalid_value", "/variation_salt invalid_value"}) {
		t.Errorf("violations = %v", got)
	}
	in.Salts = experiments.Salts{Inclusion: "a3f9c2e17b4d0e58", Variation: "a3f9c2e17b4d0e58"}
	if got := violations(t, mustFail(t, in)); !slices.Equal(got, []string{"/variation_salt duplicate"}) {
		t.Errorf("violations = %v", got)
	}
}

func TestAnImportedStatusIsOneOfTheFour(t *testing.T) {
	in := imported()
	in.Status = "paused"
	if got := violations(t, mustFail(t, in)); !slices.Equal(got, []string{"/status invalid_value"}) {
		t.Errorf("violations = %v", got)
	}
}

func TestTheHistoryOfAnImportedExperimentIsWhatItsStatusLeavesBehind(t *testing.T) {
	at := now.Add(-time.Hour)
	for name, c := range map[string]struct {
		in   experiments.Imported
		edit func(in *experiments.Imported)
		want []string
	}{
		"draft that started": {imported(), func(in *experiments.Imported) { in.StartedAt = &started },
			[]string{"/started_at invalid_value"}},
		"draft with a decision": {imported(), func(in *experiments.Imported) {
			in.StoppedAt, in.Decision = &stoppedA, &experiments.Decision{Outcome: experiments.OutcomeRemoved}
		}, []string{"/stopped_at invalid_value", "/decision invalid_value"}},
		"draft archived without archived_at": {imported(), func(in *experiments.Imported) { in.Status = experiments.StatusArchived },
			[]string{"/archived_at required"}},
		"draft with archived_at": {imported(), func(in *experiments.Imported) { in.ArchivedAt = &at },
			[]string{"/archived_at invalid_value"}},
		"running without started_at": {runningImport(), func(in *experiments.Imported) { in.StartedAt = nil },
			[]string{"/started_at required"}},
		"running without expected_end_at": {runningImport(), func(in *experiments.Imported) { in.ExpectedEndAt = nil },
			[]string{"/expected_end_at required"}},
		"running with a decision": {runningImport(), func(in *experiments.Imported) {
			in.Decision = &experiments.Decision{Outcome: experiments.OutcomeRemoved}
		}, []string{"/decision invalid_value"}},
		"running without a hypothesis": {runningImport(), func(in *experiments.Imported) { in.Hypothesis = "" },
			[]string{"/hypothesis required"}},
		"stopped without stopped_at": {stoppedImport(), func(in *experiments.Imported) { in.StoppedAt = nil },
			[]string{"/stopped_at required"}},
		"stopped without a decision": {stoppedImport(), func(in *experiments.Imported) { in.Decision = nil },
			[]string{"/decision required"}},
		"stopped before it started": {stoppedImport(), func(in *experiments.Imported) { early := started.Add(-time.Hour); in.StoppedAt = &early },
			[]string{"/stopped_at invalid_value"}},
		"stopped and archived": {stoppedImport(), func(in *experiments.Imported) { in.ArchivedAt = &at },
			[]string{"/archived_at invalid_value"}},
		"archived before it stopped": {archivedAfterStop(), func(in *experiments.Imported) { early := stoppedA.Add(-time.Hour); in.ArchivedAt = &early },
			[]string{"/archived_at invalid_value"}},
		"archived from a run without a stop": {archivedAfterStop(), func(in *experiments.Imported) { in.StoppedAt, in.Decision = nil, nil },
			[]string{"/stopped_at required", "/decision required"}},
		"archived draft with a decision": {archivedAfterStop(), func(in *experiments.Imported) { in.StartedAt, in.StoppedAt = nil, nil },
			[]string{"/decision invalid_value"}},
	} {
		in := c.in
		c.edit(&in)
		if got := violations(t, mustFail(t, in)); !slices.Equal(got, c.want) {
			t.Errorf("%s: violations = %v, want %v", name, got, c.want)
		}
	}
}

func TestAnImportedDecisionIsJudgedAsAStopJudgesIt(t *testing.T) {
	for name, c := range map[string]struct {
		decision experiments.Decision
		want     string
	}{
		"winner not compared":    {experiments.Decision{Outcome: experiments.OutcomeWinner, WinnerVariation: "huge"}, "/decision/winner_variation unknown_variation"},
		"winner without variant": {experiments.Decision{Outcome: experiments.OutcomeWinner}, "/decision/winner_variation required"},
		"removal naming a winner": {experiments.Decision{Outcome: experiments.OutcomeRemoved, WinnerVariation: "short"},
			"/decision/winner_variation invalid_value"},
		"unknown outcome": {experiments.Decision{Outcome: "tie"}, "/decision/outcome invalid_value"},
	} {
		in := stoppedImport()
		in.Decision = &c.decision
		if got := violations(t, mustFail(t, in)); !slices.Equal(got, []string{c.want}) {
			t.Errorf("%s: violations = %v, want %s", name, got, c.want)
		}
	}
}

// existing returns the experiment in describes, as the project has it.
func existing(t *testing.T, in experiments.Imported) experiments.Experiment {
	t.Helper()
	e, err := experiments.Import(in)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// desired is what a document gives for e, field for field.
func desired(e experiments.Experiment) experiments.Desired {
	return experiments.Desired{
		Name: e.Name, Hypothesis: e.Hypothesis, Tags: e.Tags, Flag: &flag, FlagKey: e.FlagKey, UnitType: e.UnitType,
		DeclaredUnitTypes: []string{"user", "anonymous"}, Variations: e.Variations, TrafficAllocation: e.TrafficAllocation,
		Status: e.Status, ExpectedEndAt: e.ExpectedEndAt, StartedAt: e.StartedAt, StoppedAt: e.StoppedAt,
		ArchivedAt: e.ArchivedAt, Decision: e.Decision,
	}
}

func TestAnExperimentTheDocumentRepeatsIsNotChanged(t *testing.T) {
	for _, in := range []experiments.Imported{imported(), runningImport(), stoppedImport(), archivedAfterStop()} {
		e := existing(t, in)
		got, changed, err := e.Reconcile(desired(e), later)
		if err != nil || changed || got.Version != 1 || !got.UpdatedAt.Equal(now) {
			t.Errorf("%s: Reconcile = version %d, changed %v, err %v, want nothing", e.Status, got.Version, changed, err)
		}
	}
}

func TestADraftTakesEveryFieldOfTheDocumentItsSaltsAndAClearedEndDateIncluded(t *testing.T) {
	in := imported()
	in.ExpectedEndAt = &end
	e := existing(t, in)
	bella := newMember()
	d := desired(e)
	d.Name, d.Hypothesis, d.Tags = "Renamed", "", []string{}
	d.Owner = &experiments.Owner{Member: bella, Eligible: true}
	d.UnitType = "anonymous"
	d.Variations = []experiments.Weight{{Variation: "huge", Weight: 3000}, {Variation: "short", Weight: 7000}}
	d.TrafficAllocation = 9000
	d.ExpectedEndAt = nil
	d.Salts = experiments.Salts{Inclusion: "0123456789abcdef", Variation: "fedcba9876543210"}
	got, changed, err := e.Reconcile(d, later)
	if err != nil || !changed {
		t.Fatalf("Reconcile = %v, changed %v", err, changed)
	}
	if got.Name != "Renamed" || got.Hypothesis != "" || len(got.Tags) != 0 || got.Owner != bella || got.UnitType != "anonymous" ||
		got.Variations[0].Variation != "huge" || got.TrafficAllocation != 9000 || got.ExpectedEndAt != nil ||
		got.InclusionSalt != "0123456789abcdef" || got.VariationSalt != "fedcba9876543210" || got.Version != 2 || !got.UpdatedAt.Equal(later) {
		t.Errorf("reconciled draft = %+v", got)
	}
}

func TestADraftKeepsTheSaltsADocumentLeavesOut(t *testing.T) {
	e := existing(t, imported())
	d := desired(e)
	d.Name = "Renamed"
	got, changed, err := e.Reconcile(d, later)
	if err != nil || !changed || got.InclusionSalt != e.InclusionSalt || got.VariationSalt != e.VariationSalt {
		t.Errorf("Reconcile = salts %q %q, changed %v, err %v, want the salts kept", got.InclusionSalt, got.VariationSalt, changed, err)
	}
}

func TestOnlyTheSaltsOfADraftChange(t *testing.T) {
	e := existing(t, runningImport())
	d := desired(e)
	d.Salts = experiments.Salts{Inclusion: "0123456789abcdef", Variation: e.VariationSalt}
	_, _, err := e.Reconcile(d, later)
	if kind, code, fields := problem(t, err); code != "frozen" || kind != apperrors.KindConflict || !slices.Equal(fields, []string{"/inclusion_salt frozen"}) {
		t.Errorf("Reconcile = %v %s %v, want frozen at /inclusion_salt", kind, code, fields)
	}
}

func TestARunningExperimentChangesItsDescriptionAndGrowsItsAllocation(t *testing.T) {
	e := existing(t, runningImport())
	d := desired(e)
	d.Name, d.Tags, d.TrafficAllocation = "Renamed", []string{"done"}, 9000
	sooner := end.Add(-24 * time.Hour)
	d.ExpectedEndAt = &sooner
	got, changed, err := e.Reconcile(d, later)
	if err != nil || !changed || got.Name != "Renamed" || got.TrafficAllocation != 9000 || got.ExpectedEndAt == nil ||
		got.ExpectedEndAt.Day() != 31 || got.Status != experiments.StatusRunning {
		t.Errorf("Reconcile = %+v, changed %v, err %v", got, changed, err)
	}
	d.TrafficAllocation = 1000
	_, _, err = e.Reconcile(d, later)
	if _, code, _ := problem(t, err); code != "traffic_allocation_decrease" {
		t.Errorf("shrinking: code = %q, want traffic_allocation_decrease", code)
	}
	d = desired(e)
	d.ExpectedEndAt = nil
	_, _, err = e.Reconcile(d, later)
	if got := violations(t, err); !slices.Equal(got, []string{"/expected_end_at required"}) {
		t.Errorf("clearing the end date of a running experiment: violations = %v", got)
	}
}

func TestAFrozenFieldIsNamedWithEveryOtherThatCannotChange(t *testing.T) {
	bella := newMember()
	otherDecision := &experiments.Decision{Outcome: experiments.OutcomeRemoved}
	at := now.Add(time.Hour)
	for name, c := range map[string]struct {
		e    experiments.Experiment
		edit func(d *experiments.Desired)
		want []string
	}{
		"running split": {existing(t, runningImport()), func(d *experiments.Desired) {
			d.UnitType = "anonymous"
			d.Variations = []experiments.Weight{{Variation: "short", Weight: 6000}, {Variation: "long", Weight: 4000}}
		}, []string{"/unit_type frozen", "/variations frozen"}},
		"running to stopped": {existing(t, runningImport()), func(d *experiments.Desired) {
			d.Status, d.StoppedAt, d.Decision = experiments.StatusStopped, &stoppedA, otherDecision
		}, []string{"/status frozen", "/stopped_at frozen", "/decision frozen"}},
		"draft to running": {existing(t, imported()), func(d *experiments.Desired) {
			d.Status, d.StartedAt, d.ExpectedEndAt = experiments.StatusRunning, &started, &end
		}, []string{"/status frozen", "/started_at frozen"}},
		"running started_at": {existing(t, runningImport()), func(d *experiments.Desired) { d.StartedAt = &at },
			[]string{"/started_at frozen"}},
		"stopped allocation and end": {existing(t, stoppedImport()), func(d *experiments.Desired) {
			d.TrafficAllocation = 9000
			e := end.Add(24 * time.Hour)
			d.ExpectedEndAt = &e
		}, []string{"/traffic_allocation frozen", "/expected_end_at frozen"}},
		"stopped decision": {existing(t, stoppedImport()), func(d *experiments.Desired) {
			d.Decision = &experiments.Decision{
				Outcome: experiments.OutcomeWinner, WinnerVariation: "short", Note: "Changed."}
		}, []string{"/decision frozen"}},
		"stopped owner is not frozen but a salt is": {existing(t, stoppedImport()), func(d *experiments.Desired) {
			d.Owner = &experiments.Owner{Member: bella, Eligible: true}
			d.Salts = experiments.Salts{Inclusion: "0123456789abcdef", Variation: "fedcba9876543210"}
		}, []string{"/inclusion_salt frozen", "/variation_salt frozen"}},
		"archived name": {existing(t, archivedAfterStop()), func(d *experiments.Desired) { d.Name = "Renamed" }, []string{"/name frozen"}},
		"archived everything": {existing(t, archivedAfterStop()), func(d *experiments.Desired) {
			d.Name, d.Hypothesis, d.Tags, d.TrafficAllocation = "n", "h", []string{"x"}, 1
			d.Owner = &experiments.Owner{Member: bella, Eligible: true}
		}, []string{"/name frozen", "/hypothesis frozen", "/owner frozen", "/tags frozen", "/traffic_allocation frozen"}},
	} {
		d := desired(c.e)
		c.edit(&d)
		_, _, err := c.e.Reconcile(d, later)
		kind, code, fields := problem(t, err)
		if code != "frozen" || kind != apperrors.KindConflict || !slices.Equal(fields, c.want) {
			t.Errorf("%s: %v %s %v, want frozen %v", name, kind, code, fields, c.want)
		}
	}
}

func TestAStoppedExperimentChangesItsDescriptionOwnerAndTags(t *testing.T) {
	e := existing(t, stoppedImport())
	bella := newMember()
	d := desired(e)
	d.Name, d.Hypothesis, d.Tags = "Renamed", "Restated.", []string{"done"}
	d.Owner = &experiments.Owner{Member: bella, Eligible: true}
	got, changed, err := e.Reconcile(d, later)
	if err != nil || !changed || got.Name != "Renamed" || got.Owner != bella || got.Hypothesis != "Restated." || got.Status != experiments.StatusStopped {
		t.Errorf("Reconcile = %+v, changed %v, err %v", got, changed, err)
	}
	d.Owner = &experiments.Owner{Member: bella}
	d.Name = e.Name
	_, _, err = e.Reconcile(d, later)
	if got := violations(t, err); !slices.Equal(got, []string{"/owner invalid_owner"}) {
		t.Errorf("an owner who cannot own: violations = %v", got)
	}
}

func TestADocumentTimestampToTheNanosecondIsTheOneStoredToTheMicrosecond(t *testing.T) {
	e := existing(t, stoppedImport())
	d := desired(e)
	odd := started.Add(123 * time.Nanosecond)
	d.StartedAt = &odd
	if _, changed, err := e.Reconcile(d, later); err != nil || changed {
		t.Errorf("Reconcile = changed %v, err %v, want the same experiment", changed, err)
	}
}
