package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// EnvironmentUpdater stores an environment's changed name or protection.
type EnvironmentUpdater interface {
	ProjectLocker
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

// UpdateEnvironmentCommand is a change to the environment Key of Project,
// based on the version Expected names.
type UpdateEnvironmentCommand struct {
	Project  workspace.ProjectID
	Key      string
	Change   workspace.EnvironmentChange
	Expected precondition.Version
	Request  governance.Request
}

// Handle applies the change, validated first and then judged against the
// version it was based on, and returns the environment as it is after it;
// a change to the values it already has stores and records nothing.
func (h *UpdateEnvironment) Handle(ctx context.Context, cmd UpdateEnvironmentCommand) (workspace.Environment, error) {
	now := h.rec.clock.Now()
	var out workspace.Environment
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		// The project's row, not the environment's: whether an environment
		// is protected decides which writes in the project may change its
		// ruleset, and those writes hold the project's row alone.
		if _, err := h.store.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := h.store.EnvironmentByKey(ctx, cmd.Project, cmd.Key, false)
		if err != nil {
			return notFound(err, "environment")
		}
		after, changed, err := before.Change(cmd.Change, now)
		if err != nil {
			return err
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		if !changed {
			out = before
			return nil
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
