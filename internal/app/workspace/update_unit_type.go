package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// UnitTypeUpdater stores a unit type's changed name and description.
type UnitTypeUpdater interface {
	// UnitTypeByKey returns the unit type, locking its row when lock is set,
	// or ErrNotFound.
	UnitTypeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.UnitType, error)
	UpdateUnitType(ctx context.Context, u workspace.UnitType) error
}

// UpdateUnitType renames a unit type or changes its description.
type UpdateUnitType struct {
	tx    Transactor
	store UnitTypeUpdater
	rec   Recorder
}

// NewUpdateUnitType returns the handler.
func NewUpdateUnitType(tx Transactor, store UnitTypeUpdater, rec Recorder) *UpdateUnitType {
	return &UpdateUnitType{tx: tx, store: store, rec: rec}
}

// UpdateUnitTypeCommand is a change to the unit type Key of Project, based
// on the version Expected names.
type UpdateUnitTypeCommand struct {
	Project  workspace.ProjectID
	Key      string
	Change   workspace.UnitTypeChange
	Expected precondition.Version
	Request  governance.Request
}

// Handle applies the change, validated first and then judged against the
// version it was based on, and returns the unit type as it is after it; a
// change to the values it already has stores and records nothing.
func (h *UpdateUnitType) Handle(ctx context.Context, cmd UpdateUnitTypeCommand) (workspace.UnitType, error) {
	now := h.rec.clock.Now()
	var out workspace.UnitType
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.store.UnitTypeByKey(ctx, cmd.Project, cmd.Key, true)
		if err != nil {
			return notFound(err, "unit type")
		}
		after, changed, err := before.Change(cmd.Change, now)
		if err != nil {
			return err
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		if !changed {
			out = before
			return nil
		}
		if err := h.store.UpdateUnitType(ctx, after); err != nil {
			return err
		}
		out = after
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: after.Project, Action: "unit_type.updated", Entity: unitTypeEntity(after),
			Before: unitTypeValues(before), After: unitTypeValues(after),
		})
	})
	return out, err
}
