package experiments

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ExperimentCommand names the experiment Key of Project, based on the version
// Expected names.
type ExperimentCommand struct {
	Project  workspace.ProjectID
	Key      string
	Expected precondition.Version
	Request  governance.Request
}

// ExperimentDeleter deletes an experiment.
type ExperimentDeleter interface {
	Reader
	DeleteExperiment(ctx context.Context, e experiments.ExperimentID) error
}

// DeleteExperimentDeps are what DeleteExperiment runs on.
type DeleteExperimentDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    ExperimentDeleter
	Recorder Recorder
}

// DeleteExperiment deletes a draft experiment.
type DeleteExperiment struct{ d DeleteExperimentDeps }

// NewDeleteExperiment returns the handler.
func NewDeleteExperiment(d DeleteExperimentDeps) *DeleteExperiment { return &DeleteExperiment{d: d} }

// Handle deletes the experiment, recording it as it was. A draft is in no
// ruleset, so no ruleset moves; the change requests proposing to start it
// are superseded by the deletion.
func (h *DeleteExperiment) Handle(ctx context.Context, cmd ExperimentCommand) error {
	now := h.d.Recorder.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, h.d.Projects, cmd.Project); err != nil {
			return err
		}
		e, err := h.d.Store.ExperimentByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return experimentNotFound(err)
		}
		if err = precondition.Check(cmd.Expected, e.Version); err != nil {
			return err
		}
		if err = e.CanDelete(); err != nil {
			return err
		}
		// The entry is recorded before the row goes, so the change requests
		// proposing a transition of the experiment are found and superseded
		// by it before the deletion cascades to them.
		if err = h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "experiment.deleted", Entity: experimentEntity(e), Before: experimentValues(e),
		}); err != nil {
			return err
		}
		return h.d.Store.DeleteExperiment(ctx, e.ID)
	})
}
