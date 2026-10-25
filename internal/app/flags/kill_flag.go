package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/protection"
)

// KillFlag switches a flag off in one environment at once: the kill switch.
type KillFlag struct{ d ConfigurationDeps }

// NewKillFlag returns the handler.
func NewKillFlag(d ConfigurationDeps) *KillFlag { return &KillFlag{d: d} }

// KillFlagCommand is a kill switch of the configuration Configuration
// names, with the reason given, if any. The version it names is never
// judged: whoever stops an incident must not be refused for an old page.
type KillFlagCommand struct {
	Configuration ConfigurationCommand
	Reason        *string
}

// Handle switches the flag off and returns the configuration as it is
// after, and whether it was on. A flag already off changes nothing and
// records nothing, so a repeated click is harmless; otherwise the audit
// entry carries the reason and is marked as a kill switch.
func (h *KillFlag) Handle(ctx context.Context, cmd KillFlagCommand) (flags.EnvironmentConfiguration, bool, error) {
	reason, err := flags.KillReason(cmd.Reason)
	if err != nil {
		return flags.EnvironmentConfiguration{}, false, err
	}
	var killed bool
	// The kill switch stops an incident without waiting for an approval,
	// in a protected environment too: the one write that changes a
	// protected ruleset directly.
	ctx = protection.BypassEverywhere(ctx)
	ec, err := writeConfiguration(ctx, h.d, cmd.Configuration, func(_ context.Context, _ flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
		after, changed := before.Kill(flags.Write{By: cmd.Configuration.Caller, Now: now})
		killed = changed.Any
		var r any
		if reason != "" {
			r = reason
		}
		return configurationChange{
			after: after, changed: changed, action: "flag.killed", extra: map[string]any{"reason": r}, killSwitch: true,
		}, nil
	})
	return ec, killed, err
}
