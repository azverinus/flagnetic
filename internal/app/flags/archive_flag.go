package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ArchivalDeps are what restoring a flag runs on, and archiving one besides
// finding the flags that require it.
type ArchivalDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    FlagUpdater
	Rulesets Rulesets
	Recorder Recorder
}

// FlagArchiver stores a flag archived once no other flag requires it.
type FlagArchiver interface {
	FlagUpdater
	DependentFinder
}

// ArchiveFlagDeps are what ArchiveFlag runs on.
type ArchiveFlagDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    FlagArchiver
	Rulesets Rulesets
	Recorder Recorder
}

// ArchiveFlag archives a flag: SDKs stop receiving it in every environment,
// and its definition and configurations are kept until it is restored or
// deleted.
type ArchiveFlag struct{ d ArchiveFlagDeps }

// NewArchiveFlag returns the handler.
func NewArchiveFlag(d ArchiveFlagDeps) *ArchiveFlag { return &ArchiveFlag{d: d} }

// FlagCommand names one flag of a project and the version of it a write is
// based on.
type FlagCommand struct {
	Project  workspace.ProjectID
	Key      string
	Expected precondition.Version
	Request  governance.Request
}

// Handle archives the flag and returns it; an archived flag stays as it is
// and nothing is recorded. A flag another flag requires, in any environment
// and archived or not, is not archived: the requiring flag would then
// require a flag no SDK receives.
func (h *ArchiveFlag) Handle(ctx context.Context, cmd FlagCommand) (FlagView, error) {
	d := ArchivalDeps{Tx: h.d.Tx, Projects: h.d.Projects, Store: h.d.Store, Rulesets: h.d.Rulesets, Recorder: h.d.Recorder}
	return archival(ctx, d, cmd, "flag.archived", flags.Flag.Archive, func(ctx context.Context, f flags.Flag) error {
		dependents, err := h.d.Store.Dependents(ctx, cmd.Project, f.Key)
		if err != nil {
			return err
		}
		if refs := flags.DependentReferences(dependents); len(refs) > 0 {
			return flags.ErrInUse(refs)
		}
		return nil
	})
}

// archival applies change, archiving or restoring, to the flag cmd names,
// recorded as action, once allowed, if given, lets it. Either changes what
// every environment's SDKs receive, so it moves every ruleset on.
func archival(ctx context.Context, d ArchivalDeps, cmd FlagCommand, action string,
	change func(flags.Flag, time.Time) (flags.Flag, bool), allowed func(context.Context, flags.Flag) error,
) (FlagView, error) {
	now := d.Recorder.now()
	var out FlagView
	err := d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, d.Projects, cmd.Project); err != nil {
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
		if allowed != nil {
			if err = allowed(ctx, before); err != nil {
				return err
			}
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
