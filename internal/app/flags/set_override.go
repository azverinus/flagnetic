package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
)

// SetOverrideDeps are what SetOverride runs on.
type SetOverrideDeps struct {
	Tx           Transactor
	Projects     ProjectLocker
	Declarations Declarations
	Store        ConfigurationWriter
	Rulesets     Rulesets
	Recorder     Recorder
}

// SetOverride pins a variation for one unit without replacing the rest of
// a configuration.
type SetOverride struct{ d SetOverrideDeps }

// NewSetOverride returns the handler.
func NewSetOverride(d SetOverrideDeps) *SetOverride { return &SetOverride{d: d} }

// SetOverrideCommand is the override to set in the configuration
// Configuration names. The version it names is never judged: one unit's
// override never conflicts with an edit of the rest.
type SetOverrideCommand struct {
	Configuration ConfigurationCommand
	Override      flags.OverrideInput
}

// Handle sets the override and returns the configuration as it is after.
func (h *SetOverride) Handle(ctx context.Context, cmd SetOverrideCommand) (flags.EnvironmentConfiguration, error) {
	d := ConfigurationDeps{Tx: h.d.Tx, Projects: h.d.Projects, Store: h.d.Store, Rulesets: h.d.Rulesets, Recorder: h.d.Recorder}
	c := cmd.Configuration
	return writeConfiguration(ctx, d, c, func(ctx context.Context, f flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
		decls, err := declarationsOf(ctx, h.d.Declarations, c.Project)
		if err != nil {
			return configurationChange{}, err
		}
		after, changed, err := before.SetOverride(f, cmd.Override, decls, flags.Write{By: c.Caller, Now: now})
		return configurationChange{after: after, changed: changed, action: "flag.override_set"}, err
	})
}
