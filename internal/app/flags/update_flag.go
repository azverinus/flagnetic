package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// FlagReader reads a flag with its configurations.
type FlagReader interface {
	// FlagByKey returns project's flag with key, or ErrNotFound.
	FlagByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Flag, error)
	// Configurations returns the flag's configuration in every environment,
	// by the environments' order.
	Configurations(ctx context.Context, flag flags.FlagID) ([]flags.EnvironmentConfiguration, error)
}

// FlagUpdater stores a flag's changed definition.
type FlagUpdater interface {
	FlagReader
	UpdateFlag(ctx context.Context, f flags.Flag) error
}

// UpdateFlagDeps are what UpdateFlag runs on.
type UpdateFlagDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    FlagUpdater
	Owners   Owners
	Rulesets Rulesets
	Recorder Recorder
}

// UpdateFlag changes a flag's definition.
type UpdateFlag struct{ d UpdateFlagDeps }

// NewUpdateFlag returns the handler.
func NewUpdateFlag(d UpdateFlagDeps) *UpdateFlag { return &UpdateFlag{d: d} }

// UpdateFlagCommand is a change to the flag Key of Project, based on the
// version Expected names. Owner is the new owner the change names, if any.
type UpdateFlagCommand struct {
	Project  workspace.ProjectID
	Key      string
	Change   flags.FlagChange
	Owner    *access.MemberID
	Expected precondition.Version
	Request  governance.Request
}

// Handle applies the change and returns the flag as it is after it. The
// change is validated first, then judged against the version it was based
// on, then against what still serves a variation it removes; a change to
// the values the flag already has stores and records nothing. A change to
// what a ruleset carries moves every environment's ruleset on.
func (h *UpdateFlag) Handle(ctx context.Context, cmd UpdateFlagCommand) (FlagView, error) {
	now := h.d.Recorder.now()
	var out FlagView
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.d.Projects.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := h.d.Store.FlagByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return flagNotFound(err)
		}
		change := cmd.Change
		if change.Owner, err = h.owner(ctx, cmd); err != nil {
			return err
		}
		after, changed, err := before.Change(change, now)
		if err != nil {
			return err
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		configs, err := h.d.Store.Configurations(ctx, before.ID)
		if err != nil {
			return err
		}
		out = FlagView{Flag: after, Configurations: configs}
		if !changed.Changed {
			return nil
		}
		if refs := flags.VariationReferences(before, changed.RemovedVariations, configs); len(refs) > 0 {
			return flags.ErrInUse(refs)
		}
		if err := h.d.Store.UpdateFlag(ctx, after); err != nil {
			return err
		}
		if changed.Ruleset {
			if err := h.d.Rulesets.Project(ctx, cmd.Project); err != nil {
				return err
			}
		}
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "flag.updated", Entity: flagEntity(after),
			Before: definitionValues(before), After: definitionValues(after),
		})
	})
	return out, err
}

// owner returns the new owner cmd names, if any, and whether they may own
// the flag.
func (h *UpdateFlag) owner(ctx context.Context, cmd UpdateFlagCommand) (*flags.Owner, error) {
	if cmd.Owner == nil {
		return nil, nil
	}
	eligible, err := h.d.Owners.MayOwn(ctx, cmd.Project, *cmd.Owner)
	return &flags.Owner{Member: *cmd.Owner, Eligible: eligible}, err
}
