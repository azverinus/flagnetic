package experiments

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ExperimentRules rewrites the rules serving an experiment.
type ExperimentRules interface {
	// ResolveExperiment makes every rule of project's flag keyed flag
	// serving the experiment keyed experiment serve the variation winner,
	// or deletes it when winner is "", in every environment, each
	// configuration it changes written and recorded on its own and its
	// ruleset moved on, as the member by (nil for a project token) at now in
	// req, in the unit of work ctx carries.
	ResolveExperiment(ctx context.Context, project workspace.ProjectID, flag, experiment, winner string,
		by *access.MemberID, now time.Time, req governance.Request) error
}

// StopExperimentDeps are what StopExperiment runs on.
type StopExperimentDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    ExperimentUpdater
	Rules    ExperimentRules
	Recorder Recorder
}

// StopExperiment stops a running experiment with a decision.
type StopExperiment struct{ d StopExperimentDeps }

// NewStopExperiment returns the handler.
func NewStopExperiment(d StopExperimentDeps) *StopExperiment { return &StopExperiment{d: d} }

// StopExperimentCommand stops the experiment Experiment names with
// Decision. Caller is the member stopping it, nil for a project token, who
// is recorded as the writer of every configuration the stop rewrites.
type StopExperimentCommand struct {
	Experiment ExperimentCommand
	Decision   experiments.Decision
	Caller     *access.MemberID
}

// Handle stops the experiment and returns it stopped. In the same unit of
// work every rule serving it, in every environment, serves the winner in
// its place or is deleted, and each environment whose configuration changed
// has its ruleset moved on, so every SDK stops assigning the experiment from
// the same commit that records the decision.
func (h *StopExperiment) Handle(ctx context.Context, cmd StopExperimentCommand) (experiments.Experiment, error) {
	now := h.d.Recorder.now()
	c := cmd.Experiment
	var out experiments.Experiment
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		// The project's row lock is the one every configuration write
		// takes too, so no rule serving the experiment is added or
		// changed between rewriting the rules and committing.
		if err := lockProject(ctx, h.d.Projects, c.Project); err != nil {
			return err
		}
		before, err := h.d.Store.ExperimentByKey(ctx, c.Project, c.Key)
		if err != nil {
			return experimentNotFound(err)
		}
		if err = precondition.Check(c.Expected, before.Version); err != nil {
			return err
		}
		after, err := before.Stop(cmd.Decision, now)
		if err != nil {
			return err
		}
		if err = h.d.Store.UpdateExperiment(ctx, after); err != nil {
			return err
		}
		if err = h.d.Rules.ResolveExperiment(ctx, c.Project, after.FlagKey, after.Key, after.Decision.WinnerVariation,
			cmd.Caller, now, c.Request); err != nil {
			return err
		}
		out = after
		return h.d.Recorder.record(ctx, now, c.Request, governance.Record{
			Project: c.Project, Action: "experiment.stopped", Entity: experimentEntity(after),
			Before: experimentValues(before), After: experimentValues(after),
		})
	})
	return out, err
}

// ArchiveExperimentDeps are what ArchiveExperiment runs on.
type ArchiveExperimentDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    ExperimentUpdater
	Recorder Recorder
}

// ArchiveExperiment archives a draft or stopped experiment.
type ArchiveExperiment struct{ d ArchiveExperimentDeps }

// NewArchiveExperiment returns the handler.
func NewArchiveExperiment(d ArchiveExperimentDeps) *ArchiveExperiment {
	return &ArchiveExperiment{d: d}
}

// Handle archives the experiment and returns it archived. Neither a draft
// nor a stopped experiment is in any ruleset, so no ruleset moves.
func (h *ArchiveExperiment) Handle(ctx context.Context, cmd ExperimentCommand) (experiments.Experiment, error) {
	return transition(ctx, h.d.Tx, h.d.Projects, h.d.Store, h.d.Recorder, cmd, "experiment.archived",
		func(e experiments.Experiment, now time.Time) (experiments.Experiment, bool, error) {
			out, err := e.Archive(now)
			return out, true, err
		})
}

// SetDecisionNoteDeps are what SetDecisionNote runs on.
type SetDecisionNoteDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    ExperimentUpdater
	Recorder Recorder
}

// SetDecisionNote changes the note of a stopped experiment's decision.
type SetDecisionNote struct{ d SetDecisionNoteDeps }

// NewSetDecisionNote returns the handler.
func NewSetDecisionNote(d SetDecisionNoteDeps) *SetDecisionNote { return &SetDecisionNote{d: d} }

// SetDecisionNoteCommand sets the note of the decision of the experiment
// Experiment names to Note, "" clearing it.
type SetDecisionNoteCommand struct {
	Experiment ExperimentCommand
	Note       string
}

// Handle sets the note and returns the experiment as it is after; the same
// note stores and records nothing. The note is in no ruleset.
func (h *SetDecisionNote) Handle(ctx context.Context, cmd SetDecisionNoteCommand) (experiments.Experiment, error) {
	return transition(ctx, h.d.Tx, h.d.Projects, h.d.Store, h.d.Recorder, cmd.Experiment, "experiment.updated",
		func(e experiments.Experiment, now time.Time) (experiments.Experiment, bool, error) {
			return e.SetDecisionNote(cmd.Note, now)
		})
}

// transition makes the change change decides to the experiment cmd names,
// at now under the project's lock, after the version it was based on is
// judged, and records it as action; a change that changes nothing stores
// and records nothing.
func transition(ctx context.Context, tx Transactor, projects ProjectLocker, store ExperimentUpdater, rec Recorder,
	cmd ExperimentCommand, action string,
	change func(e experiments.Experiment, now time.Time) (experiments.Experiment, bool, error),
) (experiments.Experiment, error) {
	now := rec.now()
	var out experiments.Experiment
	err := tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, projects, cmd.Project); err != nil {
			return err
		}
		before, err := store.ExperimentByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return experimentNotFound(err)
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		after, changed, err := change(before, now)
		if err != nil {
			return err
		}
		out = after
		if !changed {
			return nil
		}
		if err = store.UpdateExperiment(ctx, after); err != nil {
			return err
		}
		return rec.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: action, Entity: experimentEntity(after),
			Before: experimentValues(before), After: experimentValues(after),
		})
	})
	return out, err
}
