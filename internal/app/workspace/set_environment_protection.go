package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// SetEnvironmentProtection protects an environment or stops protecting it.
// It is an operation apart from renaming, under a permission of its own, so
// that whoever may rename an environment cannot also lift the review its
// changes need.
type SetEnvironmentProtection struct {
	tx       Transactor
	store    EnvironmentUpdater
	rulesets Rulesets
	rec      Recorder
}

// NewSetEnvironmentProtection returns the handler.
func NewSetEnvironmentProtection(tx Transactor, store EnvironmentUpdater, rulesets Rulesets, rec Recorder) *SetEnvironmentProtection {
	return &SetEnvironmentProtection{tx: tx, store: store, rulesets: rulesets, rec: rec}
}

// SetEnvironmentProtectionCommand protects the environment Key of Project
// or not as Protected says, based on the version Expected names.
type SetEnvironmentProtectionCommand struct {
	Project   workspace.ProjectID
	Key       string
	Protected bool
	Expected  precondition.Version
	Request   governance.Request
}

// Handle sets the protection, judged against the version it was based on,
// and returns the environment as it is after it; asking for the protection
// it already has stores and records nothing. The environment's ruleset
// moves with it, since SDKs read the protection from the ruleset to decide
// whether local overrides apply.
func (h *SetEnvironmentProtection) Handle(ctx context.Context, cmd SetEnvironmentProtectionCommand) (workspace.Environment, error) {
	now := h.rec.clock.Now()
	var out workspace.Environment
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.store.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := h.store.EnvironmentByKey(ctx, cmd.Project, cmd.Key, false)
		if err != nil {
			return notFound(err, "environment")
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		after, changed := before.Protect(cmd.Protected, now)
		out = after
		if !changed {
			return nil
		}
		if err := h.store.UpdateEnvironment(ctx, after); err != nil {
			return err
		}
		if err := h.rulesets.Environment(ctx, after.ID); err != nil {
			return err
		}
		action := "environment.unprotected"
		if after.Protected {
			action = "environment.protected"
		}
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: after.Project, Environment: after.ID, Action: action, Entity: environmentEntity(after),
			Before: environmentValues(before), After: environmentValues(after),
		})
	})
	return out, err
}
