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

// ImportStore is what the import writes experiments with.
type ImportStore interface {
	ExperimentCreator
	ExperimentUpdater
}

// ImporterDeps are what the Importer runs on.
type ImporterDeps struct {
	Tx        Transactor
	Projects  ProjectLocker
	Store     ImportStore
	Flags     Flags
	UnitTypes UnitTypes
	Rollouts  RolloutSalts
	Owners    Owners
	Holders   Holders
	Recorder  Recorder
}

// Importer writes the experiments of a project document. It is the import's
// port into the context: each method is the ordinary write of the experiment,
// run in the unit of work the caller's context carries and recorded as the
// ordinary write is, except that it moves no ruleset on but says which
// environments the ordinary write would have, since the import does that
// once per environment when it is done, and that it takes what no client
// write may set: the status, both salts, the timestamps and the decision a
// document names, which is what keeps a unit in its assignment in a copy. It
// never moves an experiment through its status: an experiment that exists
// changes only as an edit of its status allows.
type Importer struct{ d ImporterDeps }

// NewImporter returns the importer.
func NewImporter(d ImporterDeps) *Importer { return &Importer{d: d} }

// ImportCommand is an experiment as a document gives it. Owner is the member
// the document's owner resolved to, nil when it resolved to none: the
// importing member, Caller, owns a new experiment then, and an existing one
// keeps its owner.
type ImportCommand struct {
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
	TrafficAllocation int
	// InclusionSalt and VariationSalt are the document's; a new draft is given
	// the one it lacks, and an experiment that exists keeps it.
	InclusionSalt string
	VariationSalt string
	Status        experiments.Status
	ExpectedEndAt *time.Time
	StartedAt     *time.Time
	StoppedAt     *time.Time
	ArchivedAt    *time.Time
	Decision      *experiments.Decision
	// Avoid are salts the import is about to give rollouts, which no salt it
	// draws for the experiment may be.
	Avoid   []string
	Request governance.Request
}

// ImportEffect is what a write of the importer did.
type ImportEffect struct {
	// Changed is false for a write that changed nothing, which stores and
	// records nothing.
	Changed bool
	// Environments are the environments whose ruleset the ordinary write
	// would have moved on: the ones holding a rule that serves a running
	// experiment whose traffic allocation grew.
	Environments []workspace.EnvironmentID
}

// CreateExperiment creates the experiment exactly as the command says,
// recorded as created.
func (i *Importer) CreateExperiment(ctx context.Context, cmd ImportCommand) (ImportEffect, error) {
	now := i.d.Recorder.now()
	err := i.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, i.d.Projects, cmd.Project); err != nil {
			return err
		}
		in, err := i.facts(ctx, cmd)
		if err != nil {
			return err
		}
		owner := cmd.Owner
		if owner == nil {
			owner = cmd.Caller
		}
		in.ID, in.Project, in.Key, in.Name, in.Hypothesis = id.New[experiments.Experiment](i.d.Recorder.ids), cmd.Project, cmd.Key, cmd.Name, cmd.Hypothesis
		in.Tags, in.FlagKey, in.UnitType, in.Variations, in.TrafficAllocation = cmd.Tags, cmd.Flag, cmd.UnitType, cmd.Variations, cmd.TrafficAllocation
		in.Status, in.ExpectedEndAt = cmd.Status, cmd.ExpectedEndAt
		in.StartedAt, in.StoppedAt, in.ArchivedAt, in.Decision = cmd.StartedAt, cmd.StoppedAt, cmd.ArchivedAt, cmd.Decision
		in.CreatedBy, in.Now = cmd.Caller, now
		if owner != nil {
			var eligible bool
			if eligible, err = i.d.Owners.MayOwn(ctx, cmd.Project, *owner); err != nil {
				return err
			}
			in.Owner = experiments.Owner{Member: *owner, Eligible: eligible}
		}
		if in.Salts, err = i.salts(ctx, cmd); err != nil {
			return err
		}
		e, err := experiments.Import(in)
		if err != nil {
			return err
		}
		if err = i.d.Store.CreateExperiment(ctx, e); err != nil {
			return err
		}
		return i.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "experiment.created", Entity: experimentEntity(e), After: experimentValues(e),
		})
	})
	return ImportEffect{Changed: err == nil}, err
}

// facts loads what the experiment the command describes is judged against:
// its flag, if the project has it, and the project's unit types.
func (i *Importer) facts(ctx context.Context, cmd ImportCommand) (experiments.Imported, error) {
	var in experiments.Imported
	f, err := i.d.Flags.ExperimentFlag(ctx, cmd.Project, cmd.Flag)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return in, err
	default:
		in.Flag = &f
	}
	units, err := i.d.UnitTypes.UnitTypes(ctx, cmd.Project)
	if err != nil {
		return in, err
	}
	for _, u := range units {
		in.DeclaredUnitTypes = append(in.DeclaredUnitTypes, u.Key)
	}
	return in, nil
}

// salts returns the salts the experiment is created with: the document's,
// and for a draft that lacks one or both, as many drawn apart from every
// experiment and rollout of the project, from the ones the import is about to
// write and from each other. The project's lock is held, so no other write
// takes a salt meanwhile.
func (i *Importer) salts(ctx context.Context, cmd ImportCommand) (experiments.Salts, error) {
	given := experiments.Salts{Inclusion: cmd.InclusionSalt, Variation: cmd.VariationSalt}
	if cmd.Status != experiments.StatusDraft || (given.Inclusion != "" && given.Variation != "") {
		return given, nil
	}
	taken, err := i.d.Store.ExperimentSalts(ctx, cmd.Project)
	if err != nil {
		return given, err
	}
	rollouts, err := i.d.Rollouts.RolloutSalts(ctx, cmd.Project)
	if err != nil {
		return given, err
	}
	taken = append(append(taken, rollouts...), cmd.Avoid...)
	taken = append(taken, given.Inclusion, given.Variation)
	drawn, err := experiments.NewSalts(taken, randomSalt)
	if err != nil {
		return given, err
	}
	if given.Inclusion == "" {
		given.Inclusion = drawn.Inclusion
	}
	if given.Variation == "" {
		given.Variation = drawn.Variation
	}
	return given, nil
}

// UpdateExperiment changes the experiment keyed Key to what the command says
// as far as an edit of its status allows, and refuses with frozen what it
// does not. A growth of a running experiment's traffic allocation is
// recorded as the allocation change it is and names the environments that
// hold its rules, as an ordinary edit moves their rulesets on.
func (i *Importer) UpdateExperiment(ctx context.Context, cmd ImportCommand) (ImportEffect, error) {
	now := i.d.Recorder.now()
	var effect ImportEffect
	err := i.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, i.d.Projects, cmd.Project); err != nil {
			return err
		}
		before, err := i.d.Store.ExperimentByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return experimentNotFound(err)
		}
		facts, err := i.facts(ctx, cmd)
		if err != nil {
			return err
		}
		desired := experiments.Desired{
			Name: cmd.Name, Hypothesis: cmd.Hypothesis, Tags: cmd.Tags, Flag: facts.Flag, FlagKey: cmd.Flag, UnitType: cmd.UnitType,
			DeclaredUnitTypes: facts.DeclaredUnitTypes, Variations: cmd.Variations, TrafficAllocation: cmd.TrafficAllocation,
			Salts:  experiments.Salts{Inclusion: cmd.InclusionSalt, Variation: cmd.VariationSalt},
			Status: cmd.Status, ExpectedEndAt: cmd.ExpectedEndAt, StartedAt: cmd.StartedAt, StoppedAt: cmd.StoppedAt,
			ArchivedAt: cmd.ArchivedAt, Decision: cmd.Decision,
		}
		// The owner's eligibility is judged only when the owner changes, so
		// an owner who has lost their grants never blocks an edit.
		if cmd.Owner != nil && *cmd.Owner != before.Owner {
			var eligible bool
			if eligible, err = i.d.Owners.MayOwn(ctx, cmd.Project, *cmd.Owner); err != nil {
				return err
			}
			desired.Owner = &experiments.Owner{Member: *cmd.Owner, Eligible: eligible}
		}
		after, changed, err := before.Reconcile(desired, now)
		if err != nil || !changed {
			return err
		}
		if err = i.d.Store.UpdateExperiment(ctx, after); err != nil {
			return err
		}
		effect.Changed = true
		action := "experiment.updated"
		if after.Status == experiments.StatusRunning && after.TrafficAllocation != before.TrafficAllocation {
			action = "experiment.traffic_allocation_changed"
			var held collectedRulesets
			if err = bumpHolders(ctx, i.d.Holders, &held, after); err != nil {
				return err
			}
			effect.Environments = held.environments
		}
		return i.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: action, Entity: experimentEntity(after),
			Before: experimentValues(before), After: experimentValues(after),
		})
	})
	return effect, err
}

// collectedRulesets notes the environments whose ruleset a write would move
// on, and moves none.
type collectedRulesets struct{ environments []workspace.EnvironmentID }

func (c *collectedRulesets) Environment(_ context.Context, environment workspace.EnvironmentID) error {
	c.environments = append(c.environments, environment)
	return nil
}
