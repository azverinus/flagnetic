// Package delivery holds the use cases of the delivery context: for now,
// moving an environment's ruleset version on when a write changes what its
// SDKs receive, and evaluating its ruleset for a context to explain what
// the context is served.
package delivery

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// RulesetVersions moves the ruleset versions of environments on, each to
// its next version at a time (delivery.NextRulesetVersion).
type RulesetVersions interface {
	// BumpProject moves the ruleset version of every environment of project
	// on to its next version at now.
	BumpProject(ctx context.Context, project workspace.ProjectID, now time.Time) error
	// BumpEnvironment moves the ruleset version of environment on to its
	// next version at now.
	BumpEnvironment(ctx context.Context, environment workspace.EnvironmentID, now time.Time) error
}

// BumpRulesets is the one way a write marks the rulesets it changed: every
// handler whose write changes what an environment's SDKs receive calls it
// after the write, in the same unit of work, so the version moves exactly
// when the change commits.
//
// A version is never reused, even after a failover or a restore that lost
// commits, because the next version is at least the application clock in
// milliseconds.
type BumpRulesets struct {
	store RulesetVersions
	clock clock.Clock
}

// NewBumpRulesets returns the handler, numbering versions from c.
func NewBumpRulesets(store RulesetVersions, c clock.Clock) *BumpRulesets {
	return &BumpRulesets{store: store, clock: c}
}

// Project marks the ruleset of every environment of project changed, for a
// write to what every environment carries alike, such as a flag's
// variations or a segment's conditions.
func (h *BumpRulesets) Project(ctx context.Context, project workspace.ProjectID) error {
	return h.store.BumpProject(ctx, project, h.clock.Now())
}

// Environment marks the ruleset of environment changed, for a write to what
// one environment carries, such as a flag's configuration there.
func (h *BumpRulesets) Environment(ctx context.Context, environment workspace.EnvironmentID) error {
	return h.store.BumpEnvironment(ctx, environment, h.clock.Now())
}
