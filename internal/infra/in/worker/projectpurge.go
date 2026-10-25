package worker

import (
	"context"
	"time"

	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
)

// ProjectPurgeName names the project purge worker in logs, metrics and
// Tick.
const ProjectPurgeName = "project_purge"

// ProjectPurge returns the worker deleting, once an hour, the rows deleted
// projects left where their deletion could not cascade: nothing reads them,
// so the purge only keeps the tables small.
func ProjectPurge(h *appworkspace.PurgeDeletedProjects) Worker {
	return Worker{
		Name: ProjectPurgeName, Interval: time.Hour,
		// A large project's rows are purged a bounded slice per round; the
		// next round goes on where this one stopped.
		Timeout: 5 * time.Minute,
		Round: func(ctx context.Context) error {
			_, err := h.Handle(ctx)
			return err
		},
	}
}
