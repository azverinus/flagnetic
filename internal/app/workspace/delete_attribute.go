package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// AttributeDeleter deletes an attribute.
type AttributeDeleter interface {
	ProjectLocker
	AttributeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Attribute, error)
	DeleteAttribute(ctx context.Context, attribute workspace.AttributeID) error
}

// DeleteAttribute deletes an attribute.
type DeleteAttribute struct {
	tx       Transactor
	store    AttributeDeleter
	guard    DeclarationGuard
	rulesets Rulesets
	rec      Recorder
}

// NewDeleteAttribute returns the handler, which has guard refuse an
// attribute something still names.
func NewDeleteAttribute(tx Transactor, store AttributeDeleter, guard DeclarationGuard, rulesets Rulesets, rec Recorder) *DeleteAttribute {
	return &DeleteAttribute{tx: tx, store: store, guard: guard, rulesets: rulesets, rec: rec}
}

// DeleteAttributeCommand names the attribute to delete and the version of
// it the deletion is based on.
type DeleteAttributeCommand struct {
	Project  workspace.ProjectID
	Key      string
	Expected precondition.Version
	Request  governance.Request
}

// Handle deletes the attribute unless it is standard or a rule or segment
// still names it, which every environment's ruleset then no longer
// declares. It holds the project's lock, which every write naming an
// attribute takes too, so no write starts naming it between the check and
// the deletion.
func (h *DeleteAttribute) Handle(ctx context.Context, cmd DeleteAttributeCommand) error {
	now := h.rec.clock.Now()
	return h.tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.store.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		// A rename locks the attribute's row, not the project's, so the row is
		// locked here too: the version judged below is then the one a
		// rename in flight committed, never the one it is replacing.
		attr, err := h.store.AttributeByKey(ctx, cmd.Project, cmd.Key, true)
		if err != nil {
			return notFound(err, "attribute")
		}
		if err = precondition.Check(cmd.Expected, attr.Version); err != nil {
			return err
		}
		if err := attr.CanDelete(); err != nil {
			return err
		}
		if err := h.guard.RefuseReferenced(ctx, cmd.Project, attr.Key); err != nil {
			return err
		}
		if err := h.store.DeleteAttribute(ctx, attr.ID); err != nil {
			return err
		}
		if err := h.rulesets.Project(ctx, cmd.Project); err != nil {
			return err
		}
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: attr.Project, Action: "attribute.deleted", Entity: attributeEntity(attr), Before: attributeValues(attr),
		})
	})
}
