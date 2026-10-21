package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// FlagDeleter deletes a flag with its configurations.
type FlagDeleter interface {
	FlagReader
	DeleteFlag(ctx context.Context, flag flags.FlagID) error
}

// DeleteFlagDeps are what DeleteFlag runs on.
type DeleteFlagDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    FlagDeleter
	Recorder Recorder
}

// DeleteFlag deletes an archived flag.
type DeleteFlag struct{ d DeleteFlagDeps }

// NewDeleteFlag returns the handler.
func NewDeleteFlag(d DeleteFlagDeps) *DeleteFlag { return &DeleteFlag{d: d} }

// Handle deletes the flag with its configuration in every environment,
// recording both as they were. An archived flag is in no ruleset already,
// so deleting it moves none on.
func (h *DeleteFlag) Handle(ctx context.Context, cmd FlagCommand) error {
	now := h.d.Recorder.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.d.Projects.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		f, err := h.d.Store.FlagByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return flagNotFound(err)
		}
		if err = precondition.Check(cmd.Expected, f.Version); err != nil {
			return err
		}
		if err = f.CanDelete(); err != nil {
			return err
		}
		configs, err := h.d.Store.Configurations(ctx, f.ID)
		if err != nil {
			return err
		}
		if err = h.d.Store.DeleteFlag(ctx, f.ID); err != nil {
			return err
		}
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "flag.deleted", Entity: flagEntity(f),
			Before: flagValues(FlagView{Flag: f, Configurations: configs}),
		})
	})
}
