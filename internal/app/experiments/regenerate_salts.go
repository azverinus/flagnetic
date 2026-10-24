package experiments

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// SaltRegenerator stores an experiment with new salts and reads the salts
// the project's experiments hold.
type SaltRegenerator interface {
	ExperimentUpdater
	// ExperimentSalts returns both salts of every experiment of project.
	ExperimentSalts(ctx context.Context, project workspace.ProjectID) ([]string, error)
}

// RegenerateSaltsDeps are what RegenerateSalts runs on.
type RegenerateSaltsDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    SaltRegenerator
	Rollouts RolloutSalts
	Recorder Recorder
}

// RegenerateSalts draws new salts for a draft experiment.
type RegenerateSalts struct{ d RegenerateSaltsDeps }

// NewRegenerateSalts returns the handler.
func NewRegenerateSalts(d RegenerateSaltsDeps) *RegenerateSalts { return &RegenerateSalts{d: d} }

// Handle gives the draft two new salts, apart from every salt of the project's
// experiments (its own old ones included) and rollouts, and returns it. The
// project's lock is held, so no other write of the project takes a salt
// meanwhile.
func (h *RegenerateSalts) Handle(ctx context.Context, cmd ExperimentCommand) (experiments.Experiment, error) {
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
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		experimentSalts, err := h.d.Store.ExperimentSalts(ctx, cmd.Project)
		if err != nil {
			return err
		}
		rollouts, err := h.d.Rollouts.RolloutSalts(ctx, cmd.Project)
		if err != nil {
			return err
		}
		salts, err := experiments.NewSalts(append(experimentSalts, rollouts...), randomSalt)
		if err != nil {
			return err
		}
		after, err := before.RegenerateSalts(salts, now)
		if err != nil {
			return err
		}
		if err = h.d.Store.UpdateExperiment(ctx, after); err != nil {
			return err
		}
		out = after
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "experiment.salts_regenerated", Entity: experimentEntity(after),
			Before: experimentValues(before), After: experimentValues(after),
		})
	})
	return out, err
}
