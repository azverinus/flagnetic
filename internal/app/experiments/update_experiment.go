package experiments

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ExperimentUpdater stores an experiment's changed definition.
type ExperimentUpdater interface {
	Reader
	UpdateExperiment(ctx context.Context, e experiments.Experiment) error
}

// UpdateExperimentDeps are what UpdateExperiment runs on.
type UpdateExperimentDeps struct {
	Tx        Transactor
	Projects  ProjectLocker
	Store     ExperimentUpdater
	Flags     Flags
	UnitTypes UnitTypes
	Owners    Owners
	Recorder  Recorder
}

// UpdateExperiment changes an experiment's definition.
type UpdateExperiment struct{ d UpdateExperimentDeps }

// NewUpdateExperiment returns the handler.
func NewUpdateExperiment(d UpdateExperimentDeps) *UpdateExperiment { return &UpdateExperiment{d: d} }

// UpdateExperimentCommand is a change to the experiment Key of Project, based
// on the version Expected names. Owner is the new owner the change names, if
// any; Change.Owner, Change.Flag and Change.DeclaredUnitTypes are the
// handler's to fill in.
type UpdateExperimentCommand struct {
	Project  workspace.ProjectID
	Key      string
	Change   experiments.Change
	Owner    *access.MemberID
	Expected precondition.Version
	Request  governance.Request
}

// Handle applies the change and returns the experiment as it is after it. The
// change is validated first, then judged against the version it was based on;
// a change to the values the experiment already has stores and records
// nothing. A draft is in no ruleset, so no ruleset moves.
func (h *UpdateExperiment) Handle(ctx context.Context, cmd UpdateExperimentCommand) (experiments.Experiment, error) {
	now := h.d.Recorder.now()
	var out experiments.Experiment
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, h.d.Projects, cmd.Project); err != nil {
			return err
		}
		before, err := h.d.Store.ExperimentByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return experimentNotFound(err)
		}
		change, err := h.facts(ctx, cmd, before)
		if err != nil {
			return err
		}
		after, changed, err := before.Change(change, now)
		if err != nil {
			return err
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		out = after
		if !changed {
			return nil
		}
		if err := h.d.Store.UpdateExperiment(ctx, after); err != nil {
			return err
		}
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "experiment.updated", Entity: experimentEntity(after),
			Before: experimentValues(before), After: experimentValues(after),
		})
	})
	return out, err
}

// facts completes the change with what it is judged against: the new owner's
// eligibility (judged only when the owner changes, so an owner who has lost
// their grants never blocks an edit of their experiment), the flag the
// experiment compares once changed and the project's declared unit types.
func (h *UpdateExperiment) facts(ctx context.Context, cmd UpdateExperimentCommand, before experiments.Experiment) (experiments.Change, error) {
	change := cmd.Change
	if cmd.Owner != nil && *cmd.Owner != before.Owner {
		eligible, err := h.d.Owners.MayOwn(ctx, cmd.Project, *cmd.Owner)
		if err != nil {
			return change, err
		}
		change.Owner = &experiments.Owner{Member: *cmd.Owner, Eligible: eligible}
	}
	if change.FlagKey != nil || change.Variations != nil {
		key := before.FlagKey
		if change.FlagKey != nil {
			key = *change.FlagKey
		}
		f, err := h.d.Flags.ExperimentFlag(ctx, cmd.Project, key)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return change, err
		default:
			change.Flag = &f
		}
	}
	if change.UnitType != nil {
		units, err := h.d.UnitTypes.UnitTypes(ctx, cmd.Project)
		if err != nil {
			return change, err
		}
		for _, u := range units {
			change.DeclaredUnitTypes = append(change.DeclaredUnitTypes, u.Key)
		}
	}
	return change, nil
}
