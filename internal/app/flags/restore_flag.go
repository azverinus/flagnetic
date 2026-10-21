package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
)

// RestoreFlag brings an archived flag back to SDKs as it was archived.
type RestoreFlag struct{ d ArchivalDeps }

// NewRestoreFlag returns the handler.
func NewRestoreFlag(d ArchivalDeps) *RestoreFlag { return &RestoreFlag{d: d} }

// Handle restores the flag and returns it; a flag that is not archived stays
// as it is and nothing is recorded.
func (h *RestoreFlag) Handle(ctx context.Context, cmd FlagCommand) (FlagView, error) {
	return archival(ctx, h.d, cmd, "flag.restored", flags.Flag.Restore, nil)
}
