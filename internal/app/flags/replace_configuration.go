package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ReferentReader reads what a configuration may name.
type ReferentReader interface {
	// ProjectFlags returns every flag of project, archived ones included.
	ProjectFlags(ctx context.Context, project workspace.ProjectID) ([]flags.Flag, error)
	// Segments returns the project's segments by key.
	Segments(ctx context.Context, project workspace.ProjectID) ([]flags.Segment, error)
	// Requirements returns, by flag key, the keys of the flags each flag's
	// configuration in environment requires, archived flags included.
	Requirements(ctx context.Context, environment workspace.EnvironmentID) (flags.Requirements, error)
}

// ConfigurationReplacer stores a flag's configuration replaced whole, and
// reads what it may name.
type ConfigurationReplacer interface {
	ConfigurationWriter
	ReferentReader
}

// ReplaceConfigurationDeps are what ReplaceConfiguration runs on.
type ReplaceConfigurationDeps struct {
	Tx           Transactor
	Projects     ProjectLocker
	Declarations Declarations
	Store        ConfigurationReplacer
	Rulesets     Rulesets
	Recorder     Recorder
}

// ReplaceConfiguration replaces a flag's configuration in one environment
// as a whole.
type ReplaceConfiguration struct{ d ReplaceConfigurationDeps }

// NewReplaceConfiguration returns the handler.
func NewReplaceConfiguration(d ReplaceConfigurationDeps) *ReplaceConfiguration {
	return &ReplaceConfiguration{d: d}
}

// ReplaceConfigurationCommand is the configuration to write whole in place
// of the one Configuration names.
type ReplaceConfigurationCommand struct {
	Configuration ConfigurationCommand
	Input         flags.ConfigurationInput
}

// Handle replaces the configuration and returns it as it is after. The
// configuration is validated first, against the project's flags, segments
// and declarations and every flag's requirements in the environment, all
// read under the project's lock so they hold when the write commits; then
// the version it was based on is judged. A replace whose only change to
// what a ruleset carries is switching the flag on or off is recorded as
// that switch.
func (h *ReplaceConfiguration) Handle(ctx context.Context, cmd ReplaceConfigurationCommand) (flags.EnvironmentConfiguration, error) {
	d := ConfigurationDeps{Tx: h.d.Tx, Projects: h.d.Projects, Store: h.d.Store, Rulesets: h.d.Rulesets, Recorder: h.d.Recorder}
	c := cmd.Configuration
	return writeConfiguration(ctx, d, c, func(ctx context.Context, f flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
		r, err := referentsOf(ctx, h.d.Store, h.d.Declarations, c.Project, before.Environment)
		if err != nil {
			return configurationChange{}, err
		}
		after, changed, err := before.Replace(f, cmd.Input, r, h.d.Recorder.mint(), flags.Write{By: c.Caller, Now: now})
		if err != nil {
			return configurationChange{}, err
		}
		if err = precondition.Check(c.Expected, before.Version); err != nil {
			return configurationChange{}, err
		}
		action := "flag.configuration_updated"
		switch {
		case changed.Switched && after.On:
			action = "flag.switched_on"
		case changed.Switched:
			action = "flag.switched_off"
		}
		return configurationChange{after: after, changed: changed, action: action}, nil
	})
}

// referentsOf returns what a configuration of project in environment may
// name, read from store and declarations.
func referentsOf(ctx context.Context, store ReferentReader, declarations Declarations, project workspace.ProjectID,
	environment workspace.EnvironmentID,
) (flags.Referents, error) {
	var (
		r   flags.Referents
		err error
	)
	if r.Flags, err = store.ProjectFlags(ctx, project); err != nil {
		return r, err
	}
	segments, err := store.Segments(ctx, project)
	if err != nil {
		return r, err
	}
	for _, s := range segments {
		r.Segments = append(r.Segments, s.Key)
	}
	if r.Declarations, err = declarationsOf(ctx, declarations, project); err != nil {
		return r, err
	}
	r.Requirements, err = store.Requirements(ctx, environment)
	return r, err
}
