// Package delivery holds the use cases of the delivery context: for now,
// moving an environment's ruleset version on when a write changes what its
// SDKs receive.
package delivery

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// RulesetVersions increments the ruleset versions of environments.
type RulesetVersions interface {
	// BumpEnvironment increments the ruleset version of one environment.
	BumpEnvironment(ctx context.Context, environment workspace.EnvironmentID) error
	// BumpProject increments the ruleset version of every environment of
	// project.
	BumpProject(ctx context.Context, project workspace.ProjectID) error
}

// BumpRulesets is the one way a write marks the rulesets it changed: every
// handler whose write changes what an environment's SDKs receive calls it
// after the write, in the same unit of work, so the version moves exactly
// when the change commits.
type BumpRulesets struct {
	store RulesetVersions
}

// NewBumpRulesets returns the handler.
func NewBumpRulesets(store RulesetVersions) *BumpRulesets { return &BumpRulesets{store: store} }

// Environment marks the ruleset of one environment changed, for a write to
// one flag's configuration there.
func (h *BumpRulesets) Environment(ctx context.Context, environment workspace.EnvironmentID) error {
	return h.store.BumpEnvironment(ctx, environment)
}

// Project marks the ruleset of every environment of project changed, for a
// write to what every environment carries alike, such as a flag's
// variations or a segment's conditions.
func (h *BumpRulesets) Project(ctx context.Context, project workspace.ProjectID) error {
	return h.store.BumpProject(ctx, project)
}
