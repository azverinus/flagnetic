package governance_test

import (
	"slices"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var (
	checkoutV2 = id.MustParse[experiments.Experiment]("0190d2a0-0000-7000-8000-0000000000d1")
	bannerTest = id.MustParse[experiments.Experiment]("0190d2a0-0000-7000-8000-0000000000d2")
	qa         = id.MustParse[workspace.Environment]("0190d2a0-0000-7000-8000-000000000e03")
)

// heldState is checkout_v2 as an experiment change request bases itself on
// it: a draft or running experiment whose rule staging and production hold.
func heldState(t *testing.T, status string) governance.Document {
	t.Helper()
	return doc(t, `{"status": "`+status+`", "traffic_allocation": 7000, "rules": {
		"production": [{"id": "r1", "description": "", "conditions": [], "serve": {"kind": "experiment", "experiment": "checkout_v2"}}],
		"staging": [{"id": "r2", "description": "", "conditions": [], "serve": {"kind": "experiment", "experiment": "checkout_v2"}}]
	}}`)
}

// experimentProposal is a change request proposing transition on
// checkout_v2 based on base, held by holders.
func experimentProposal(t *testing.T, base governance.Document, transition string, holders ...workspace.EnvironmentID,
) governance.NewChangeRequestInput {
	t.Helper()
	in := proposal(t)
	in.Subject = governance.Subject{
		Kind: governance.KindExperiment, Flag: &governance.FlagRef{ID: checkout, Key: "checkout_title"},
		Experiment: &governance.ExperimentRef{ID: checkoutV2, Key: "checkout_v2"}, Base: base, BaseVersion: 3,
		BaseHolders: holders,
	}
	in.Proposed = doc(t, transition)
	return in
}

func pathsOf(ops []governance.PatchOperation) []string {
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = op.Op + " " + op.Path
	}
	return out
}

// An experiment change request proposes a transition, and its diff runs
// from the experiment and its holding rules to what the transition leaves
// of them, so a reviewer of a stop reads each rule it rewrites.
func TestAnExperimentChangeRequestDiffsWhatItsTransitionLeaves(t *testing.T) {
	for _, tc := range []struct {
		name, status, transition string
		want                     []string
	}{
		{"a start", "draft", `{"action": "start"}`, []string{"replace /status"}},
		{"a growth", "running", `{"action": "set_traffic_allocation", "traffic_allocation": 9000}`,
			[]string{"replace /traffic_allocation"}},
		{"a stop with a winner", "running",
			`{"action": "stop", "decision": {"outcome": "winner", "winner_variation": "long", "note": "Long won."}}`,
			[]string{
				"add /decision",
				"remove /rules/production/0/serve/experiment", "replace /rules/production/0/serve/kind",
				"add /rules/production/0/serve/variation",
				"remove /rules/staging/0/serve/experiment", "replace /rules/staging/0/serve/kind",
				"add /rules/staging/0/serve/variation",
				"replace /status",
			}},
		{"a stop removing the experiment", "running",
			`{"action": "stop", "decision": {"outcome": "removed", "winner_variation": null, "note": ""}}`,
			[]string{"add /decision", "remove /rules/production/0", "remove /rules/staging/0", "replace /status"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cr := newChangeRequest(t, experimentProposal(t, heldState(t, tc.status), tc.transition, production, staging))
			if got := pathsOf(cr.Diff); !slices.Equal(got, tc.want) {
				t.Errorf("diff = %v, want %v", got, tc.want)
			}
		})
	}

	// The winner is served where the experiment was, each rule keeping
	// what else it had.
	cr := newChangeRequest(t, experimentProposal(t, heldState(t, "running"),
		`{"action": "stop", "decision": {"outcome": "winner", "winner_variation": "long", "note": ""}}`, production, staging))
	for _, op := range cr.Diff {
		if op.Path == "/rules/production/0/serve/variation" && op.Value != "long" ||
			op.Path == "/rules/production/0/serve/kind" && op.Value != "variation" {
			t.Errorf("%s %s = %v", op.Op, op.Path, op.Value)
		}
	}
	// An edit and a rebase recompute the diff the same way.
	edited, changed, err := cr.Edit(byAda, governance.Edit{Proposed: doc(t, `{"action": "set_traffic_allocation", "traffic_allocation": 8000}`)}, now)
	if err != nil || !changed || !slices.Equal(pathsOf(edited.Diff), []string{"replace /traffic_allocation"}) {
		t.Errorf("edited = %v, %v, %v", pathsOf(edited.Diff), changed, err)
	}
	rebased, changed, err := edited.Rebase(byAda, governance.Subject{Base: doc(t, `{"status": "running", "traffic_allocation": 8000,
		"rules": {}}`), BaseVersion: 4}, now)
	if err != nil || !changed || len(rebased.Diff) != 0 {
		t.Errorf("rebased = %v, %v, %v; want no diff on an allocation already there", pathsOf(rebased.Diff), changed, err)
	}
}

// The environments holding an experiment's rule when its change request was
// based on it are what the transition was reviewed against: when they
// differ at opening, the author rebases; at approval or application, the
// change request is superseded.
func TestAnExperimentChangeRequestWhoseHoldersMovedIsRefused(t *testing.T) {
	later := now.Add(time.Minute)
	draft := newChangeRequest(t, experimentProposal(t, heldState(t, "draft"), `{"action": "start"}`, production, staging))

	if _, err := draft.Open(byAda, governance.Current{Version: 3, Holders: []workspace.EnvironmentID{staging}}, later); codeOf(err) != "base_moved" {
		t.Errorf("opening on fewer holders = %v, want base_moved", err)
	}
	// The order the holders are read in does not matter.
	opened, err := draft.Open(byAda, governance.Current{Version: 3, Holders: []workspace.EnvironmentID{staging, production}}, later)
	if err != nil {
		t.Fatalf("opening on the same holders = %v", err)
	}
	more := governance.Current{Version: 3, Holders: []workspace.EnvironmentID{production, staging, qa}}
	superseded, err := opened.Approve(byDecider, "", more, later)
	if codeOf(err) != "base_moved" || superseded.State != governance.StateSuperseded {
		t.Errorf("approving on more holders = %+v, %v; want superseded with base_moved", superseded.State, err)
	}
	approved, err := opened.Approve(byDecider, "", governance.Current{Version: 3, Holders: []workspace.EnvironmentID{production, staging}}, later)
	if err != nil {
		t.Fatalf("approving = %v", err)
	}
	if superseded, err = approved.Apply(byAda, more, later); codeOf(err) != "base_moved" || superseded.State != governance.StateSuperseded {
		t.Errorf("applying on more holders = %+v, %v; want superseded with base_moved", superseded.State, err)
	}

	// A rebase takes the holders of the new base.
	rebased, changed, err := draft.Rebase(byAda, governance.Subject{Base: draft.Base, BaseVersion: 3,
		BaseHolders: []workspace.EnvironmentID{staging}}, later)
	if err != nil || !changed || !slices.Equal(rebased.BaseHolders, []workspace.EnvironmentID{staging}) {
		t.Errorf("rebased = %+v, %v, %v; want staging as its holder", rebased.BaseHolders, changed, err)
	}
}

func experimentEntry(action string, experiment experiments.ExperimentID) governance.Entry {
	return governance.Entry{ID: moving, OccurredAt: now, Record: governance.Record{
		Action: action, Entity: governance.Entity{Kind: "experiment", ID: experiment.String(), Key: "experiment"},
	}}
}

// An experiment change request is moved by a write to its experiment, and by
// a write to its flag's configuration in an environment that held the
// experiment's rule when it was based on it; a write elsewhere leaves it, and
// an environment that starts holding the rule is found at the next step.
func TestAnEntryMovesTheExperimentChangeRequestsOfWhatItChanged(t *testing.T) {
	held := newChangeRequest(t, experimentProposal(t, heldState(t, "draft"), `{"action": "start"}`, production, staging))
	held.ID, held.State = id.FromBytes[governance.ChangeRequest]([16]byte{0x01, 0x90, 15: 1}), governance.StateApproved
	other := held
	other.ID, other.Experiment, other.Flag = id.FromBytes[governance.ChangeRequest]([16]byte{0x01, 0x90, 15: 2}),
		&governance.ExperimentRef{ID: bannerTest, Key: "banner_test"}, &governance.FlagRef{ID: banner, Key: "banner"}
	candidates := []governance.ChangeRequest{held, other}

	for _, tc := range []struct {
		name  string
		entry governance.Entry
		want  []governance.ChangeRequestID
	}{
		{"a configuration write where the rule is held", flagEntry("flag.configuration_updated", checkout, staging),
			[]governance.ChangeRequestID{held.ID}},
		{"a configuration write where it is not", flagEntry("flag.configuration_updated", checkout, qa), nil},
		{"the kill switch where it is held", flagEntry("flag.killed", checkout, production), []governance.ChangeRequestID{held.ID}},
		{"archiving the flag", flagEntry("flag.archived", checkout, workspace.EnvironmentID{}), []governance.ChangeRequestID{held.ID}},
		{"a definition edit of the flag", flagEntry("flag.updated", checkout, workspace.EnvironmentID{}), nil},
		{"an edit of the experiment", experimentEntry("experiment.updated", checkoutV2), []governance.ChangeRequestID{held.ID}},
		{"another experiment's start", experimentEntry("experiment.started", bannerTest), []governance.ChangeRequestID{other.ID}},
		{"a stop", experimentEntry("experiment.stopped", checkoutV2), []governance.ChangeRequestID{held.ID}},
		{"a growth", experimentEntry("experiment.traffic_allocation_changed", checkoutV2), []governance.ChangeRequestID{held.ID}},
		{"new salts", experimentEntry("experiment.salts_regenerated", checkoutV2), []governance.ChangeRequestID{held.ID}},
		{"archiving it", experimentEntry("experiment.archived", checkoutV2), []governance.ChangeRequestID{held.ID}},
		{"deleting it", experimentEntry("experiment.deleted", checkoutV2), []governance.ChangeRequestID{held.ID}},
		{"an alert", experimentEntry("experiment.alert_raised", checkoutV2), nil},
		{"a clone of it", experimentEntry("experiment.cloned", checkoutV2), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := idsOf(governance.Supersedes(tc.entry, candidates, governance.Referrers{}))
			if !slices.Equal(got, tc.want) && (len(got) != 0 || len(tc.want) != 0) {
				t.Errorf("Supersedes = %v, want %v", got, tc.want)
			}
		})
	}

	if moved, ok := governance.MovedBy(experimentEntry("experiment.started", checkoutV2)); !ok ||
		moved != (governance.Moved{Experiment: checkoutV2}) {
		t.Errorf("MovedBy(a start) = %+v, %v", moved, ok)
	}
	if _, ok := governance.MovedBy(experimentEntry("experiment.created", checkoutV2)); ok {
		t.Error("creating an experiment moves a change request")
	}
}
