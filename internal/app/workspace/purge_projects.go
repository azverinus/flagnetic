package workspace

import (
	"context"
)

// ProjectPurgeStore deletes the rows deleted projects left in tables that
// have no foreign key to projects.
type ProjectPurgeStore interface {
	// TryLockProjectPurge takes, until the transaction ends, the lock that
	// lets one replica purge at a time; false when another holds it.
	TryLockProjectPurge(ctx context.Context) (bool, error)
	// PurgeDeletedProjects deletes at most limit rows of projects that no
	// longer exist.
	PurgeDeletedProjects(ctx context.Context, limit int) (int, error)
}

// ProjectPurgeBatch is how many rows one transaction of the purge deletes,
// so a large project's leftovers never hold locks or a transaction open for
// long.
const ProjectPurgeBatch = 1000

// PurgeDeletedProjects deletes what deleted projects left in tables without
// a foreign key to projects, which their deletion could not cascade to.
type PurgeDeletedProjects struct {
	tx    Transactor
	store ProjectPurgeStore
}

// NewPurgeDeletedProjects returns the handler.
func NewPurgeDeletedProjects(tx Transactor, store ProjectPurgeStore) *PurgeDeletedProjects {
	return &PurgeDeletedProjects{tx: tx, store: store}
}

// Handle purges in batches, each in a transaction of its own holding the
// purge lock, until a batch finds less than a full batch to delete, and
// returns how many rows it deleted. When another replica holds the lock it
// leaves the work to it.
func (h *PurgeDeletedProjects) Handle(ctx context.Context) (int, error) {
	purged := 0
	for {
		var n int
		held := true
		err := h.tx.Within(ctx, func(ctx context.Context) error {
			var err error
			if held, err = h.store.TryLockProjectPurge(ctx); err != nil || !held {
				return err
			}
			n, err = h.store.PurgeDeletedProjects(ctx, ProjectPurgeBatch)
			return err
		})
		purged += n
		if err != nil || !held || n < ProjectPurgeBatch {
			return purged, err
		}
	}
}
