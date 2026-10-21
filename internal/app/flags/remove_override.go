package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
)

// RemoveOverride removes the override of one unit without replacing the
// rest of a configuration.
type RemoveOverride struct{ d ConfigurationDeps }

// NewRemoveOverride returns the handler.
func NewRemoveOverride(d ConfigurationDeps) *RemoveOverride { return &RemoveOverride{d: d} }

// RemoveOverrideCommand names the unit whose override to remove from the
// configuration Configuration names, whose version is never judged.
type RemoveOverrideCommand struct {
	Configuration    ConfigurationCommand
	UnitType, UnitID string
}

// Handle removes the override and returns the configuration as it is
// after; not_found when the unit has none.
func (h *RemoveOverride) Handle(ctx context.Context, cmd RemoveOverrideCommand) (flags.EnvironmentConfiguration, error) {
	c := cmd.Configuration
	return writeConfiguration(ctx, h.d, c, func(_ context.Context, _ flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
		after, changed, err := before.RemoveOverride(cmd.UnitType, cmd.UnitID, flags.Write{By: c.Caller, Now: now})
		return configurationChange{after: after, changed: changed, action: "flag.override_removed"}, err
	})
}
