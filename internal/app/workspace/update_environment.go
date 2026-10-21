package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// EnvironmentUpdater stores an environment's changed name.
type EnvironmentUpdater interface {
	// EnvironmentByKey returns the environment, locking its row when lock is
	// set, or ErrNotFound.
	EnvironmentByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Environment, error)
	UpdateEnvironment(ctx context.Context, e workspace.Environment) error
}

// UpdateEnvironment renames an environment.
type UpdateEnvironment struct {
	tx    Transactor
	store EnvironmentUpdater
	rec   Recorder
}

// NewUpdateEnvironment returns the handler.
func NewUpdateEnvironment(tx Transactor, store EnvironmentUpdater, rec Recorder) *UpdateEnvironment {
	return &UpdateEnvironment{tx: tx, store: store, rec: rec}
}

// UpdateEnvironmentCommand is a change to the environment Key of Project.
type UpdateEnvironmentCommand struct {
	Project workspace.ProjectID
	Key     string
	Change  workspace.EnvironmentChange
	Request governance.Request
}

// Handle applies the change and returns the environment as it is after it;
// a change to the values it already has stores and records nothing.
func (h *UpdateEnvironment) Handle(ctx context.Context, cmd UpdateEnvironmentCommand) (workspace.Environment, error) {
	now := h.rec.clock.Now()
	var out workspace.Environment
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.store.EnvironmentByKey(ctx, cmd.Project, cmd.Key, true)
		if err != nil {
			return notFound(err, "environment")
		}
		after, changed, err := before.Change(cmd.Change, now)
		if err != nil || !changed {
			out = before
			return err
		}
		if err := h.store.UpdateEnvironment(ctx, after); err != nil {
			return err
		}
		out = after
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: after.Project, Environment: after.ID, Action: "environment.updated", Entity: environmentEntity(after),
			Before: environmentValues(before), After: environmentValues(after),
		})
	})
	return out, err
}
