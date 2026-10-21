package flags

import (
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// Configuration is what one flag serves in one environment. Every flag has
// one in every environment of its project, each changing on its own.
type Configuration struct {
	Flag        FlagID
	Environment workspace.EnvironmentID
	// On is false while the flag serves its off variation to everyone.
	On bool
	// OffVariation is the key of the variation served while the flag is
	// off.
	OffVariation string
	// Fallthrough is the key of the variation served when no rule matches.
	Fallthrough string
	UpdatedAt   time.Time
	// Version counts the configuration's accepted changes, from 1.
	Version int64
}

// InitialConfiguration returns the configuration f arrives with in
// environment: switched off, so creating a flag or an environment never
// changes what any application is served. A boolean flag serves false when
// off and true when on with no rule matching; another type its first
// variation either way.
func InitialConfiguration(f Flag, environment workspace.EnvironmentID, now time.Time) Configuration {
	off, fallthroughVariation := f.Variations[0].Key, f.Variations[0].Key
	if f.Type == TypeBoolean {
		off, fallthroughVariation = "false", "true"
	}
	return Configuration{
		Flag: f.ID, Environment: environment, OffVariation: off, Fallthrough: fallthroughVariation,
		UpdatedAt: now, Version: 1,
	}
}

// servesVariation reports whether c names the variation key.
func (c Configuration) servesVariation(key string) bool {
	return c.OffVariation == key || c.Fallthrough == key
}

// EnvironmentConfiguration is a flag's configuration with the key of its
// environment, by which a reference names it.
type EnvironmentConfiguration struct {
	Environment   string
	Configuration Configuration
}

// VariationReferences returns what still names one of the variations of f
// in removed: f's configurations in configs that serve it, one reference
// per environment, in the order of configs.
func VariationReferences(f Flag, removed []string, configs []EnvironmentConfiguration) []Reference {
	var refs []Reference
	for _, ec := range configs {
		for _, key := range removed {
			if ec.Configuration.servesVariation(key) {
				refs = append(refs, Reference{Kind: "flag", Key: f.Key, Environment: ec.Environment})
				break
			}
		}
	}
	return refs
}
