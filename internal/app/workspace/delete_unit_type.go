package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// UnitTypeDeleter deletes a unit type.
type UnitTypeDeleter interface {
	UnitTypeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.UnitType, error)
	DeleteUnitType(ctx context.Context, unitType workspace.UnitTypeID) error
}

// DeleteUnitType deletes a unit type.
type DeleteUnitType struct {
	tx    Transactor
	store UnitTypeDeleter
	rec   Recorder
}

// NewDeleteUnitType returns the handler.
func NewDeleteUnitType(tx Transactor, store UnitTypeDeleter, rec Recorder) *DeleteUnitType {
	return &DeleteUnitType{tx: tx, store: store, rec: rec}
}

// DeleteUnitTypeCommand names the unit type to delete.
type DeleteUnitTypeCommand struct {
	Project workspace.ProjectID
	Key     string
	Request governance.Request
}

// Handle deletes the unit type. Nothing refers to a unit type yet, so the
// deletion is unconditional.
func (h *DeleteUnitType) Handle(ctx context.Context, cmd DeleteUnitTypeCommand) error {
	now := h.rec.clock.Now()
	return h.tx.Within(ctx, func(ctx context.Context) error {
		u, err := h.store.UnitTypeByKey(ctx, cmd.Project, cmd.Key, true)
		if err != nil {
			return notFound(err, "unit type")
		}
		if err := h.store.DeleteUnitType(ctx, u.ID); err != nil {
			return err
		}
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: u.Project, Action: "unit_type.deleted", Entity: unitTypeEntity(u), Before: unitTypeValues(u),
		})
	})
}
