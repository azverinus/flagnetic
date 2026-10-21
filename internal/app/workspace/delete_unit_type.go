package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// UnitTypeDeleter deletes a unit type.
type UnitTypeDeleter interface {
	ProjectLocker
	UnitTypeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.UnitType, error)
	DeleteUnitType(ctx context.Context, unitType workspace.UnitTypeID) error
}

// DeleteUnitType deletes a unit type.
type DeleteUnitType struct {
	tx       Transactor
	store    UnitTypeDeleter
	guard    DeclarationGuard
	rulesets Rulesets
	rec      Recorder
}

// NewDeleteUnitType returns the handler, which has guard refuse a unit
// type something still names.
func NewDeleteUnitType(tx Transactor, store UnitTypeDeleter, guard DeclarationGuard, rulesets Rulesets, rec Recorder) *DeleteUnitType {
	return &DeleteUnitType{tx: tx, store: store, guard: guard, rulesets: rulesets, rec: rec}
}

// DeleteUnitTypeCommand names the unit type to delete and the version of it
// the deletion is based on.
type DeleteUnitTypeCommand struct {
	Project  workspace.ProjectID
	Key      string
	Expected precondition.Version
	Request  governance.Request
}

// Handle deletes the unit type unless a rule, rollout, override or segment
// still names it, which every environment's ruleset then no longer
// declares. It holds the project's lock, which every write naming a unit
// type takes too, so no write starts naming it between the check and the
// deletion.
func (h *DeleteUnitType) Handle(ctx context.Context, cmd DeleteUnitTypeCommand) error {
	now := h.rec.clock.Now()
	return h.tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.store.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		// A rename locks the unit type's row, not the project's, so the row is
		// locked here too: the version judged below is then the one a
		// rename in flight committed, never the one it is replacing.
		u, err := h.store.UnitTypeByKey(ctx, cmd.Project, cmd.Key, true)
		if err != nil {
			return notFound(err, "unit type")
		}
		if err = precondition.Check(cmd.Expected, u.Version); err != nil {
			return err
		}
		if err := h.guard.RefuseReferenced(ctx, cmd.Project, u.Key); err != nil {
			return err
		}
		if err := h.store.DeleteUnitType(ctx, u.ID); err != nil {
			return err
		}
		if err := h.rulesets.Project(ctx, cmd.Project); err != nil {
			return err
		}
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: u.Project, Action: "unit_type.deleted", Entity: unitTypeEntity(u), Before: unitTypeValues(u),
		})
	})
}
