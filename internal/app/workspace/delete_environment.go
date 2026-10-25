package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// EnvironmentDeleter deletes an environment unless it is its project's last.
type EnvironmentDeleter interface {
	ProjectLocker
	EnvironmentByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Environment, error)
	CountEnvironments(ctx context.Context, project workspace.ProjectID) (int, error)
	DeleteEnvironment(ctx context.Context, environment workspace.EnvironmentID) error
}

// DeleteEnvironment deletes an environment.
type DeleteEnvironment struct {
	tx    Transactor
	store EnvironmentDeleter
	rec   Recorder
}

// NewDeleteEnvironment returns the handler.
func NewDeleteEnvironment(tx Transactor, store EnvironmentDeleter, rec Recorder) *DeleteEnvironment {
	return &DeleteEnvironment{tx: tx, store: store, rec: rec}
}

// DeleteEnvironmentCommand names the environment to delete and the version
// of it the deletion is based on.
type DeleteEnvironmentCommand struct {
	Project  workspace.ProjectID
	Key      string
	Expected precondition.Version
	Request  governance.Request
}

// Handle deletes the environment. The project's row is locked first, so two
// deletions at once cannot remove its last two environments.
func (h *DeleteEnvironment) Handle(ctx context.Context, cmd DeleteEnvironmentCommand) error {
	now := h.rec.clock.Now()
	return h.tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.store.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		e, err := h.store.EnvironmentByKey(ctx, cmd.Project, cmd.Key, true)
		if err != nil {
			return notFound(err, "environment")
		}
		if err = precondition.Check(cmd.Expected, e.Version); err != nil {
			return err
		}
		count, err := h.store.CountEnvironments(ctx, cmd.Project)
		if err != nil {
			return err
		}
		if err := workspace.CanDeleteEnvironment(count); err != nil {
			return err
		}
		// The deletion is recorded before the rows go, so what it ends
		// elsewhere, such as the change requests proposed for the
		// environment, is found and closed with an entry of its own before
		// the cascade removes it.
		if err := h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: e.Project, Environment: e.ID, Action: "environment.deleted", Entity: environmentEntity(e),
			Before: environmentValues(e),
		}); err != nil {
			return err
		}
		return h.store.DeleteEnvironment(ctx, e.ID)
	})
}
