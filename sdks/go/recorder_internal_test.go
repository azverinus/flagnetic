package flagnetic

import (
	"strconv"
	"testing"
)

// The Recorder's bound: a long-running offline client, a service in a
// development environment say, records for as long as it lives, so every
// list keeps only the most recent entries. Filling a list past its cap
// through the public calls would take a ruleset change or a flush per
// summary, so the lists are filled here directly.

const overCap = 5

func TestRecorderKeepsTheMostRecentExposures(t *testing.T) {
	r := newRecorder()
	for i := range recorderCap + overCap {
		r.recordEvent(&exposureEvent{UnitID: strconv.Itoa(i)})
	}
	got := r.Exposures()
	if len(got) != recorderCap {
		t.Fatalf("%d exposures held, want the cap of %d", len(got), recorderCap)
	}
	if got[0].UnitID != strconv.Itoa(overCap) || got[len(got)-1].UnitID != strconv.Itoa(recorderCap+overCap-1) {
		t.Errorf("held exposures %s..%s, want the oldest %d dropped", got[0].UnitID, got[len(got)-1].UnitID, overCap)
	}
}

func TestRecorderKeepsTheMostRecentAliases(t *testing.T) {
	r := newRecorder()
	for i := range recorderCap + overCap {
		r.recordEvent(&aliasEvent{ToUnitID: strconv.Itoa(i)})
	}
	got := r.Aliases()
	if len(got) != recorderCap {
		t.Fatalf("%d aliases held, want the cap of %d", len(got), recorderCap)
	}
	if got[0].ToUnitID != strconv.Itoa(overCap) || got[len(got)-1].ToUnitID != strconv.Itoa(recorderCap+overCap-1) {
		t.Errorf("held aliases %s..%s, want the oldest %d dropped", got[0].ToUnitID, got[len(got)-1].ToUnitID, overCap)
	}
}

func TestRecorderKeepsTheMostRecentEvaluationSummaries(t *testing.T) {
	r := newRecorder()
	for i := range recorderCap + overCap {
		r.record(EvaluationSummary{RulesetVersion: int64(i)})
	}
	got := r.EvaluationSummaries()
	if len(got) != recorderCap {
		t.Fatalf("%d summaries held, want the cap of %d", len(got), recorderCap)
	}
	if got[0].RulesetVersion != overCap || got[len(got)-1].RulesetVersion != recorderCap+overCap-1 {
		t.Errorf("held summaries %d..%d, want the oldest %d dropped", got[0].RulesetVersion, got[len(got)-1].RulesetVersion, overCap)
	}
}
