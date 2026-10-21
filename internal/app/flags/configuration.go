package flags

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ConfigurationReader reads a flag's configuration in one environment.
type ConfigurationReader interface {
	FlagByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Flag, error)
	// Configuration returns the flag's configuration in the environment
	// keyed environment, or ErrNotFound when the project has no such
	// environment.
	Configuration(ctx context.Context, flag flags.FlagID, environment string) (flags.EnvironmentConfiguration, error)
}

// ConfigurationWriter stores a flag's changed configuration in one
// environment.
type ConfigurationWriter interface {
	ConfigurationReader
	UpdateConfiguration(ctx context.Context, c flags.Configuration) error
}

// ConfigurationDeps are what a write of one configuration runs on.
type ConfigurationDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    ConfigurationWriter
	Rulesets Rulesets
	Recorder Recorder
}

// ConfigurationCommand names a flag's configuration in one environment of a
// project, and the version of it a write is based on. Caller is the member
// writing, nil for a project token.
type ConfigurationCommand struct {
	Project     workspace.ProjectID
	Flag        string
	Environment string
	Expected    precondition.Version
	Caller      *access.MemberID
	Request     governance.Request
}

// configurationChange is what one write does to a configuration.
type configurationChange struct {
	after   flags.Configuration
	changed flags.Changed
	// action is the audit entry's, such as flag.configuration_updated.
	action string
	// extra are values the audit entry records after the configuration's,
	// such as the reason of a kill switch.
	extra map[string]any
}

// writeConfiguration makes one write to the configuration cmd names, as
// change decides it at now from the flag and the configuration before it.
// Under the project's lock it finds both, refusing an archived flag's
// configuration; a change that changes nothing stores and records nothing.
// A change to what a ruleset carries moves the environment's ruleset on.
// The audit entry records the whole configuration before and after, which
// is what a revert restores.
func writeConfiguration(ctx context.Context, d ConfigurationDeps, cmd ConfigurationCommand,
	change func(ctx context.Context, f flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error),
) (flags.EnvironmentConfiguration, error) {
	now := d.Recorder.now()
	var out flags.EnvironmentConfiguration
	err := d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, d.Projects, cmd.Project); err != nil {
			return err
		}
		f, before, err := configurationOf(ctx, d.Store, cmd.Project, cmd.Flag, cmd.Environment)
		if err != nil {
			return err
		}
		if err = f.CanConfigure(); err != nil {
			return err
		}
		ch, err := change(ctx, f, before.Configuration, now)
		if err != nil {
			return err
		}
		out = flags.EnvironmentConfiguration{Environment: before.Environment, Configuration: ch.after}
		if !ch.changed.Any {
			return nil
		}
		if err = d.Store.UpdateConfiguration(ctx, ch.after); err != nil {
			return err
		}
		if ch.changed.Ruleset {
			if err = d.Rulesets.Environment(ctx, ch.after.Environment); err != nil {
				return err
			}
		}
		after := configurationValues(ch.after)
		maps.Copy(after, ch.extra)
		return d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Environment: ch.after.Environment, Action: ch.action, Entity: flagEntity(f),
			Before: configurationValues(before.Configuration), After: after,
		})
	})
	return out, err
}

// configurationOf returns project's flag keyed flag and its configuration in
// the environment keyed environment, or not_found for either.
func configurationOf(ctx context.Context, store ConfigurationReader, project workspace.ProjectID, flag, environment string,
) (flags.Flag, flags.EnvironmentConfiguration, error) {
	f, err := store.FlagByKey(ctx, project, flag)
	if err != nil {
		return f, flags.EnvironmentConfiguration{}, flagNotFound(err)
	}
	ec, err := store.Configuration(ctx, f.ID, environment)
	if errors.Is(err, ErrNotFound) {
		return f, ec, errEnvironmentNotFound
	}
	return f, ec, err
}

// GetFlagConfiguration reads a flag's configuration in one environment.
type GetFlagConfiguration struct {
	snapshots Snapshotter
	store     ConfigurationReader
}

// NewGetFlagConfiguration returns the handler.
func NewGetFlagConfiguration(snapshots Snapshotter, store ConfigurationReader) *GetFlagConfiguration {
	return &GetFlagConfiguration{snapshots: snapshots, store: store}
}

// Handle returns the configuration of project's flag keyed flag in the
// environment keyed environment, or not_found for either. The flag and the
// configuration are read in one snapshot, so the configuration is the flag
// found's.
func (h *GetFlagConfiguration) Handle(ctx context.Context, project workspace.ProjectID, flag, environment string,
) (flags.EnvironmentConfiguration, error) {
	var ec flags.EnvironmentConfiguration
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		var err error
		_, ec, err = configurationOf(ctx, h.store, project, flag, environment)
		return err
	})
	return ec, err
}
