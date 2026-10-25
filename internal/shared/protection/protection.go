// Package protection carries, in a context, which protected environments a
// write may change without a change request, as the context carries the
// write's transaction. The guard on the ruleset bump reads it; the kill
// switch and the application of an approved change request set it. Every
// other write carries none, so a new write path is guarded by construction.
package protection

import (
	"context"

	"github.com/azverinus/flagnetic/internal/shared/id"
)

type bypassKey struct{}

// bypass is what a context lets through: every environment, or those in
// environments.
type bypass struct {
	everywhere   bool
	environments map[[16]byte]bool
}

// BypassEverywhere returns a context under which a write changes the
// ruleset of every protected environment directly: the kill switch, the
// deliberate emergency exception.
func BypassEverywhere(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassKey{}, bypass{everywhere: true})
}

// Bypass returns a context under which a write changes the rulesets of
// environments directly, and is still refused for every other protected
// environment it would change: the application of a change request, whose
// approval covered those environments and no other.
func Bypass[E any](ctx context.Context, environments ...id.Of[E]) context.Context {
	b := bypass{environments: make(map[[16]byte]bool, len(environments))}
	for _, e := range environments {
		b.environments[e.Bytes()] = true
	}
	return context.WithValue(ctx, bypassKey{}, b)
}

// Bypassed reports whether a write under ctx changes environment's ruleset
// directly.
func Bypassed[E any](ctx context.Context, environment id.Of[E]) bool {
	b, _ := ctx.Value(bypassKey{}).(bypass)
	return b.everywhere || b.environments[environment.Bytes()]
}
