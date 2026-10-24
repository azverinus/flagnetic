package experiments

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ExperimentCloner reads the experiment to clone and stores the clone.
type ExperimentCloner interface {
	Reader
	ExperimentCreator
}

// CloneExperimentDeps are what CloneExperiment runs on.
type CloneExperimentDeps struct {
	Tx        Transactor
	Projects  ProjectLocker
	Store     ExperimentCloner
	Flags     Flags
	UnitTypes UnitTypes
	Rollouts  RolloutSalts
	Owners    Owners
	Recorder  Recorder
}

// CloneExperiment copies an experiment of any status into a new draft.
type CloneExperiment struct {
	d      CloneExperimentDeps
	create *CreateExperiment
}

// NewCloneExperiment returns the handler.
func NewCloneExperiment(d CloneExperimentDeps) *CloneExperiment {
	return &CloneExperiment{d: d, create: NewCreateExperiment(CreateExperimentDeps{
		Tx: d.Tx, Projects: d.Projects, Store: d.Store, Flags: d.Flags, UnitTypes: d.UnitTypes, Rollouts: d.Rollouts,
		Owners: d.Owners, Recorder: d.Recorder,
	})}
}

// CloneExperimentCommand clones the experiment Source of Project as Key. Name
// is the clone's, the source's when empty; Caller is the member making the
// request, nil for a project token.
type CloneExperimentCommand struct {
	Project workspace.ProjectID
	Source  string
	Key     string
	Name    *string
	Caller  *access.MemberID
	Request governance.Request
}

// Handle creates the clone and returns it. It is a draft with new salts and no
// decision, whatever the source's status, over what the source compared: a
// rerun never reuses an assignment. It is judged as a create is, so a source
// whose flag has since been archived cannot be cloned. The owner is the
// calling member, the source's for a project token, which acts for nobody.
func (h *CloneExperiment) Handle(ctx context.Context, cmd CloneExperimentCommand) (experiments.Experiment, error) {
	d := h.d
	now := d.Recorder.now()
	var out experiments.Experiment
	err := d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, d.Projects, cmd.Project); err != nil {
			return err
		}
		src, err := d.Store.ExperimentByKey(ctx, cmd.Project, cmd.Source)
		if err != nil {
			return experimentNotFound(err)
		}
		create := CreateExperimentCommand{
			Project: cmd.Project, Key: cmd.Key, Name: src.Name, Hypothesis: src.Hypothesis, Owner: &src.Owner,
			Caller: cmd.Caller, Tags: src.Tags, Flag: src.FlagKey, UnitType: src.UnitType, Variations: src.Variations,
			TrafficAllocation: &src.TrafficAllocation, ExpectedEndAt: src.ExpectedEndAt, Request: cmd.Request,
		}
		if cmd.Name != nil {
			create.Name = *cmd.Name
		}
		if cmd.Caller != nil {
			create.Owner = cmd.Caller
		}
		e, err := h.create.create(ctx, create, now)
		if err != nil {
			return err
		}
		out = e
		after := experimentValues(e)
		after["cloned_from"] = src.Key
		return d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "experiment.cloned", Entity: experimentEntity(e), After: after,
		})
	})
	return out, err
}
