package openfeature

import (
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// defaultUnitType is the unit type the targeting key maps to when
// WithUnitType is not given.
const defaultUnitType = "user"

// defaultReadyTimeout bounds Init when the caller's context carries no
// deadline of its own (InitWithContext always defers to the caller's
// context instead).
const defaultReadyTimeout = 10 * time.Second

// Option configures New.
type Option func(*config)

type config struct {
	unitType      string
	readyTimeout  time.Duration
	clientOptions []flagnetic.Option
}

// WithUnitType names the unit type the targeting key maps to; default
// "user".
func WithUnitType(unitType string) Option {
	return func(c *config) {
		if unitType != "" {
			c.unitType = unitType
		}
	}
}

// WithReadyTimeout bounds Init, the path with no context.Context of its
// own; default 10 seconds. InitWithContext, what OpenFeature's
// SetProviderWithContext family actually calls, waits on the caller's
// context instead and ignores this.
func WithReadyTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.readyTimeout = d
		}
	}
}

// WithClientOptions passes options through to the wrapped flagnetic.Client,
// for example flagnetic.WithOffline for tests or flagnetic.WithStore for a
// persisted ruleset. A flagnetic.WithLogger given here replaces the
// provider's own logger, which is how PROVIDER_ERROR on a revoked SDK key
// is observed; wrap flagnetic.EventKeyRevoked in the replacement to keep
// that behaviour.
func WithClientOptions(opts ...flagnetic.Option) Option {
	return func(c *config) {
		c.clientOptions = append(c.clientOptions, opts...)
	}
}
