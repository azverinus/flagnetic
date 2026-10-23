package experiments

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ExperimentCreator stores a new experiment and reads the salts of the
// project's experiments.
type ExperimentCreator interface {
	// CreateExperiment refuses a key the project uses as already_exists.
	CreateExperiment(ctx context.Context, e experiments.Experiment) error
	// ExperimentSalts returns both salts of every experiment of project.
	ExperimentSalts(ctx context.Context, project workspace.ProjectID) ([]string, error)
}

// CreateExperimentDeps are what CreateExperiment runs on.
type CreateExperimentDeps struct {
	Tx        Transactor
	Projects  ProjectLocker
	Store     ExperimentCreator
	Flags     Flags
	UnitTypes UnitTypes
	Rollouts  RolloutSalts
	Owners    Owners
	Recorder  Recorder
}

// CreateExperiment creates an experiment as a draft, with two salts no other
// experiment or rollout of the project uses. A draft changes nothing SDKs
// receive, so no ruleset moves.
type CreateExperiment struct{ d CreateExperimentDeps }

// NewCreateExperiment returns the handler.
func NewCreateExperiment(d CreateExperimentDeps) *CreateExperiment { return &CreateExperiment{d: d} }

// CreateExperimentCommand is the experiment to create. Owner is the member
// the request names, nil when it names none; Caller is the member making the
// request, nil for a project token.
type CreateExperimentCommand struct {
	Project           workspace.ProjectID
	Key               string
	Name              string
	Hypothesis        string
	Owner             *access.MemberID
	Caller            *access.MemberID
	Tags              []string
	Flag              string
	UnitType          string
	Variations        []experiments.Weight
	TrafficAllocation *int
	ExpectedEndAt     *time.Time
	Request           governance.Request
}

// Handle creates the experiment and returns it.
func (h *CreateExperiment) Handle(ctx context.Context, cmd CreateExperimentCommand) (experiments.Experiment, error) {
	now := h.d.Recorder.now()
	var out experiments.Experiment
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, h.d.Projects, cmd.Project); err != nil {
			return err
		}
		owner, err := h.owner(ctx, cmd)
		if err != nil {
			return err
		}
		flag, err := h.flag(ctx, cmd)
		if err != nil {
			return err
		}
		units, err := h.d.UnitTypes.UnitTypes(ctx, cmd.Project)
		if err != nil {
			return err
		}
		salts, err := h.salts(ctx, cmd.Project)
		if err != nil {
			return err
		}
		declared := make([]string, len(units))
		for i, u := range units {
			declared[i] = u.Key
		}
		e, err := experiments.New(experiments.Input{
			ID: id.New[experiments.Experiment](h.d.Recorder.ids), Project: cmd.Project, Key: cmd.Key, Name: cmd.Name,
			Hypothesis: cmd.Hypothesis, Owner: owner, Tags: cmd.Tags, Flag: flag, FlagKey: cmd.Flag, UnitType: cmd.UnitType,
			DeclaredUnitTypes: declared, Variations: cmd.Variations, TrafficAllocation: cmd.TrafficAllocation,
			ExpectedEndAt: cmd.ExpectedEndAt, Salts: salts, CreatedBy: cmd.Caller, Now: now,
		})
		if err != nil {
			return err
		}
		if err := h.d.Store.CreateExperiment(ctx, e); err != nil {
			return err
		}
		out = e
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "experiment.created", Entity: experimentEntity(e), After: experimentValues(e),
		})
	})
	return out, err
}

// owner returns the owner cmd names, else its caller, and whether they may
// own the experiment. A project token acts for no member, so an experiment
// it creates without naming an owner has none, which the experiment
// refuses: a pipeline is nobody to ask about a hypothesis.
func (h *CreateExperiment) owner(ctx context.Context, cmd CreateExperimentCommand) (experiments.Owner, error) {
	member := cmd.Owner
	if member == nil {
		member = cmd.Caller
	}
	if member == nil {
		return experiments.Owner{}, nil
	}
	eligible, err := h.d.Owners.MayOwn(ctx, cmd.Project, *member)
	return experiments.Owner{Member: *member, Eligible: eligible}, err
}

// flag returns the facts of the flag cmd names, nil when the project has no
// such flag.
func (h *CreateExperiment) flag(ctx context.Context, cmd CreateExperimentCommand) (*experiments.FlagFacts, error) {
	f, err := h.d.Flags.ExperimentFlag(ctx, cmd.Project, cmd.Flag)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// salts draws the experiment's salts, apart from those of every experiment
// and rollout of the project. The project's lock is held, so no other write
// of the project takes a salt meanwhile.
func (h *CreateExperiment) salts(ctx context.Context, project workspace.ProjectID) (experiments.Salts, error) {
	taken, err := h.d.Store.ExperimentSalts(ctx, project)
	if err != nil {
		return experiments.Salts{}, err
	}
	rollouts, err := h.d.Rollouts.RolloutSalts(ctx, project)
	if err != nil {
		return experiments.Salts{}, err
	}
	return experiments.NewSalts(append(taken, rollouts...), randomSalt)
}
