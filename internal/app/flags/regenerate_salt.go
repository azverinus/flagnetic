package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// RegenerateSalt gives a rollout a new salt, on purpose: its units are
// bucketed afresh.
type RegenerateSalt struct{ d ConfigurationDeps }

// NewRegenerateSalt returns the handler.
func NewRegenerateSalt(d ConfigurationDeps) *RegenerateSalt { return &RegenerateSalt{d: d} }

// RegenerateSaltCommand names the rule whose rollout gets a new salt, in
// the configuration Configuration names.
type RegenerateSaltCommand struct {
	Configuration ConfigurationCommand
	Rule          flags.RuleID
}

// Handle regenerates the salt and returns the configuration as it is
// after. The rule is judged first, then the version the write was based
// on, as for any write.
func (h *RegenerateSalt) Handle(ctx context.Context, cmd RegenerateSaltCommand) (flags.EnvironmentConfiguration, error) {
	c := cmd.Configuration
	return writeConfiguration(ctx, h.d, c, func(_ context.Context, _ flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
		after, changed, err := before.RegenerateSalt(cmd.Rule, h.d.Recorder.mint(), flags.Write{By: c.Caller, Now: now})
		if err != nil {
			return configurationChange{}, err
		}
		if err = precondition.Check(c.Expected, before.Version); err != nil {
			return configurationChange{}, err
		}
		return configurationChange{after: after, changed: changed, action: "flag.salt_regenerated"}, nil
	})
}
