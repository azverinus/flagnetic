// Package shared is the root of the kernel every layer may import: error
// kinds, identifiers, the clock and secret values. It imports the standard
// library only, so the domain can depend on it.
package shared
