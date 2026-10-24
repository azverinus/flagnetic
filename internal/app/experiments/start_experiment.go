package experiments

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// Holders reads where an experiment is served.
type Holders interface {
	// HoldingEnvironments returns the environments of project whose
	// configuration of some flag has a rule serving the experiment keyed
	// key.
	HoldingEnvironments(ctx context.Context, project workspace.ProjectID, key string) ([]workspace.EnvironmentID, error)
}

// Rulesets marks rulesets changed.
type Rulesets interface {
	// Environment marks the ruleset of environment changed, in the unit
	// of work ctx carries.
	Environment(ctx context.Context, environment workspace.EnvironmentID) error
}

// bumpHolders marks changed the ruleset of every environment holding a rule
// serving e, which embeds what a running experiment's rules serve: the
// environments holding none carry nothing of it.
func bumpHolders(ctx context.Context, holders Holders, rulesets Rulesets, e experiments.Experiment) error {
	envs, err := holders.HoldingEnvironments(ctx, e.Project, e.Key)
	if err != nil {
		return err
	}
	for _, env := range envs {
		if err := rulesets.Environment(ctx, env); err != nil {
			return err
		}
	}
	return nil
}

// StartExperimentDeps are what StartExperiment runs on.
type StartExperimentDeps struct {
	Tx        Transactor
	Projects  ProjectLocker
	Store     ExperimentUpdater
	Flags     Flags
	UnitTypes UnitTypes
	Holders   Holders
	Rulesets  Rulesets
	Recorder  Recorder
}

// StartExperiment starts a draft experiment.
type StartExperiment struct{ d StartExperimentDeps }

// NewStartExperiment returns the handler.
func NewStartExperiment(d StartExperimentDeps) *StartExperiment { return &StartExperiment{d: d} }

// Handle starts the experiment and returns it running. In the same unit of
// work it moves on the ruleset of every environment holding a rule serving
// it, so every SDK of those environments is given the experiment from the
// same commit, and none of the others is moved. An experiment no
// environment holds starts all the same.
//
// The handler runs in the unit of work ctx carries when there is one, so a
// caller applying a start together with its own write commits or rolls
// back both at once.
func (h *StartExperiment) Handle(ctx context.Context, cmd ExperimentCommand) (experiments.Experiment, error) {
	now := h.d.Recorder.now()
	var out experiments.Experiment
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		// The project's row lock is the one every configuration write
		// takes too, so no rule serving the experiment is added or
		// removed between reading the holders and committing.
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
		facts, err := h.facts(ctx, before)
		if err != nil {
			return err
		}
		after, err := before.Start(facts, now)
		if err != nil {
			return err
		}
		if err = h.d.Store.UpdateExperiment(ctx, after); err != nil {
			return err
		}
		if err = bumpHolders(ctx, h.d.Holders, h.d.Rulesets, after); err != nil {
			return err
		}
		out = after
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "experiment.started", Entity: experimentEntity(after),
			Before: experimentValues(before), After: experimentValues(after),
		})
	})
	return out, err
}

// facts returns the experiment's flag and the project's unit types as they
// are now. The flag is the experiment's own, which is deleted with it, so
// it is always found.
func (h *StartExperiment) facts(ctx context.Context, e experiments.Experiment) (experiments.StartFacts, error) {
	f, err := h.d.Flags.ExperimentFlag(ctx, e.Project, e.FlagKey)
	if err != nil {
		return experiments.StartFacts{}, err
	}
	units, err := h.d.UnitTypes.UnitTypes(ctx, e.Project)
	if err != nil {
		return experiments.StartFacts{}, err
	}
	facts := experiments.StartFacts{Flag: f}
	for _, u := range units {
		facts.DeclaredUnitTypes = append(facts.DeclaredUnitTypes, u.Key)
	}
	return facts, nil
}
