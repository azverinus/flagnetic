package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ProjectUpdater stores a project's changed name and description.
type ProjectUpdater interface {
	ProjectLocker
	UpdateProject(ctx context.Context, p workspace.Project) error
}

// UpdateProject renames a project or changes its description.
type UpdateProject struct {
	tx    Transactor
	store ProjectUpdater
	rec   Recorder
}

// NewUpdateProject returns the handler.
func NewUpdateProject(tx Transactor, store ProjectUpdater, rec Recorder) *UpdateProject {
	return &UpdateProject{tx: tx, store: store, rec: rec}
}

// UpdateProjectCommand is a change to a project.
type UpdateProjectCommand struct {
	Project workspace.ProjectID
	Change  workspace.ProjectChange
	Request governance.Request
}

// Handle applies the change and returns the project as it is after it. A
// change to the values the project already has stores and records nothing.
func (h *UpdateProject) Handle(ctx context.Context, cmd UpdateProjectCommand) (workspace.Project, error) {
	now := h.rec.clock.Now()
	var out workspace.Project
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.store.LockProject(ctx, cmd.Project)
		if err != nil {
			return err
		}
		after, changed, err := before.Change(cmd.Change, now)
		if err != nil || !changed {
			out = before
			return err
		}
		if err := h.store.UpdateProject(ctx, after); err != nil {
			return err
		}
		out = after
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: after.ID, Action: "project.updated", Entity: projectEntity(after),
			Before: projectValues(before), After: projectValues(after),
		})
	})
	return out, err
}
