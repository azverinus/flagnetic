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

// DeleteFlag deletes an archived flag.
type DeleteFlag struct {
	tx       Transactor
	projects ProjectLocker
	store    FlagDeleter
	rec      Recorder
}

// NewDeleteFlag returns the handler.
func NewDeleteFlag(tx Transactor, projects ProjectLocker, store FlagDeleter, rec Recorder) *DeleteFlag {
	return &DeleteFlag{tx: tx, projects: projects, store: store, rec: rec}
}

// Handle deletes the flag with its configuration in every environment,
// recording both as they were. An archived flag is in no ruleset already,
// so deleting it moves none on.
func (h *DeleteFlag) Handle(ctx context.Context, cmd FlagCommand) error {
	now := h.rec.now()
	return h.tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.projects.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		f, err := h.store.FlagByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return flagNotFound(err)
		}
		if err = precondition.Check(cmd.Expected, f.Version); err != nil {
			return err
		}
		if err = f.CanDelete(); err != nil {
			return err
		}
		configs, err := h.store.Configurations(ctx, f.ID)
		if err != nil {
			return err
		}
		if err = h.store.DeleteFlag(ctx, f.ID); err != nil {
			return err
		}
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "flag.deleted", Entity: flagEntity(f),
			Before: flagValues(FlagView{Flag: f, Configurations: configs}),
		})
	})
}
