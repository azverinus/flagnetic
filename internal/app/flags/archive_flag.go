package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ArchivalDeps are what archiving and restoring a flag run on.
type ArchivalDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    FlagUpdater
	Rulesets Rulesets
	Recorder Recorder
}

// ArchiveFlag archives a flag: SDKs stop receiving it in every environment,
// and its definition and configurations are kept until it is restored or
// deleted.
type ArchiveFlag struct{ d ArchivalDeps }

// NewArchiveFlag returns the handler.
func NewArchiveFlag(d ArchivalDeps) *ArchiveFlag { return &ArchiveFlag{d: d} }

// FlagCommand names one flag of a project and the version of it a write is
// based on.
type FlagCommand struct {
	Project  workspace.ProjectID
	Key      string
	Expected precondition.Version
	Request  governance.Request
}

// Handle archives the flag and returns it; an archived flag stays as it is
// and nothing is recorded.
func (h *ArchiveFlag) Handle(ctx context.Context, cmd FlagCommand) (FlagView, error) {
	return archival(ctx, h.d, cmd, "flag.archived", flags.Flag.Archive)
}

// archival applies change, archiving or restoring, to the flag cmd names,
// recorded as action. Either changes what every environment's SDKs
// receive, so it moves every ruleset on.
func archival(ctx context.Context, d ArchivalDeps, cmd FlagCommand, action string,
	change func(flags.Flag, time.Time) (flags.Flag, bool),
) (FlagView, error) {
	now := d.Recorder.now()
	var out FlagView
	err := d.Tx.Within(ctx, func(ctx context.Context) error {
		if _, err := d.Projects.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := d.Store.FlagByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return flagNotFound(err)
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		after, changed := change(before, now)
		configs, err := d.Store.Configurations(ctx, before.ID)
		if err != nil {
			return err
		}
		out = FlagView{Flag: after, Configurations: configs}
		if !changed {
			return nil
		}
		if err = d.Store.UpdateFlag(ctx, after); err != nil {
			return err
		}
		if err = d.Rulesets.Project(ctx, cmd.Project); err != nil {
			return err
		}
		return d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: action, Entity: flagEntity(after),
			Before: definitionValues(before), After: definitionValues(after),
		})
	})
	return out, err
}
