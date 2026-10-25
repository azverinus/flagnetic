// Package projectrow records, for the life of a transaction, the projects
// whose row it locked FOR UPDATE, the lock every write of a project takes so
// that the project's writes run one at a time. The row itself cannot tell:
// a transaction that only inserted a row referencing the project leaves its
// id in the project row through the key share lock of the foreign key
// check, which keeps no other write out.
package projectrow

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
)

type heldKey struct{}

func held(ctx context.Context) (map[workspace.ProjectID]bool, bool) {
	return pgxtx.Local(ctx, heldKey{}, func() map[workspace.ProjectID]bool { return map[workspace.ProjectID]bool{} })
}

// MarkHeld records that the transaction ctx carries has locked project's
// row FOR UPDATE. Outside a transaction a lock ends with its statement, and
// nothing is recorded.
func MarkHeld(ctx context.Context, project workspace.ProjectID) {
	if h, ok := held(ctx); ok {
		h[project] = true
	}
}

// Held reports whether the transaction ctx carries has locked project's row
// FOR UPDATE.
func Held(ctx context.Context, project workspace.ProjectID) bool {
	h, _ := held(ctx)
	return h[project]
}
