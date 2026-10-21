package flags

import (
	"context"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// CopyConfiguration makes a flag's configuration in one environment serve
// what it serves in another.
type CopyConfiguration struct{ d ReplaceConfigurationDeps }

// NewCopyConfiguration returns the handler.
func NewCopyConfiguration(d ReplaceConfigurationDeps) *CopyConfiguration {
	return &CopyConfiguration{d: d}
}

// CopyConfigurationCommand copies to the configuration Configuration names
// the flag's configuration in the environment keyed From, its overrides
// too when Overrides is set.
type CopyConfigurationCommand struct {
	Configuration ConfigurationCommand
	From          string
	Overrides     bool
}

// Handle copies and returns the target configuration as it is after. The
// source is read under the project's lock, so the copy is of what it is
// when the copy commits; the result is validated against the target's
// environment, then the target's version the write was based on is judged.
func (h *CopyConfiguration) Handle(ctx context.Context, cmd CopyConfigurationCommand) (flags.EnvironmentConfiguration, error) {
	d := ConfigurationDeps{Tx: h.d.Tx, Projects: h.d.Projects, Store: h.d.Store, Rulesets: h.d.Rulesets, Recorder: h.d.Recorder}
	c := cmd.Configuration
	return writeConfiguration(ctx, d, c, func(ctx context.Context, f flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
		source, err := h.d.Store.Configuration(ctx, f.ID, cmd.From)
		if errors.Is(err, ErrNotFound) {
			return configurationChange{}, errEnvironmentNotFound
		}
		if err != nil {
			return configurationChange{}, err
		}
		r, err := referentsOf(ctx, h.d.Store, h.d.Declarations, c.Project, before.Environment)
		if err != nil {
			return configurationChange{}, err
		}
		after, changed, err := before.Copy(f, source.Configuration, cmd.Overrides, r, flags.Write{By: c.Caller, Now: now})
		if err != nil {
			return configurationChange{}, err
		}
		if err = precondition.Check(c.Expected, before.Version); err != nil {
			return configurationChange{}, err
		}
		return configurationChange{after: after, changed: changed, action: "flag.configuration_copied",
			extra: map[string]any{"source_environment": source.Environment, "source_version": source.Configuration.Version}}, nil
	})
}
