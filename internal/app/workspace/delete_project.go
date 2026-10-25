package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ProjectDeleter deletes a project with everything in it.
type ProjectDeleter interface {
	ProjectLocker
	// DeleteProject deletes the project's row, and with it, by the
	// cascades of their foreign keys, every row that belongs to it.
	DeleteProject(ctx context.Context, project workspace.ProjectID) error
}

// DeleteProject deletes a project with everything in it.
type DeleteProject struct {
	tx    Transactor
	store ProjectDeleter
	rec   Recorder
}

// NewDeleteProject returns the handler.
func NewDeleteProject(tx Transactor, store ProjectDeleter, rec Recorder) *DeleteProject {
	return &DeleteProject{tx: tx, store: store, rec: rec}
}

// DeleteProjectCommand names the project to delete and the version of it
// the deletion is based on.
type DeleteProjectCommand struct {
	Project  workspace.ProjectID
	Expected precondition.Version
	Request  governance.Request
}

// Handle deletes the project in one unit of work, so its key is free for a
// new project once it commits. The project's row is locked first: every
// write in the project takes that lock, so none is half done when the rows
// go. The deletion is an entry of the instance, not of the project, since
// it is what the project's remaining entries are read against once the
// project is gone; it names the project's identifier, key and name. It
// ends the project's change requests too, which go with the rows, with no
// entry of their own: every one of them ends for the same reason.
func (h *DeleteProject) Handle(ctx context.Context, cmd DeleteProjectCommand) error {
	now := h.rec.clock.Now()
	return h.tx.Within(ctx, func(ctx context.Context) error {
		p, err := h.store.LockProject(ctx, cmd.Project)
		if err != nil {
			return err
		}
		if err = precondition.Check(cmd.Expected, p.Version); err != nil {
			return err
		}
		if err := h.rec.record(ctx, now, cmd.Request, governance.Record{
			Action: "project.deleted", Entity: projectEntity(p),
			Before: map[string]any{"id": p.ID.String(), "key": p.Key, "name": p.Name},
		}); err != nil {
			return err
		}
		return h.store.DeleteProject(ctx, p.ID)
	})
}
