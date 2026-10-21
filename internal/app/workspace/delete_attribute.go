package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// AttributeDeleter deletes an attribute.
type AttributeDeleter interface {
	AttributeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Attribute, error)
	DeleteAttribute(ctx context.Context, attribute workspace.AttributeID) error
}

// DeleteAttribute deletes an attribute.
type DeleteAttribute struct {
	tx    Transactor
	store AttributeDeleter
	rec   Recorder
}

// NewDeleteAttribute returns the handler.
func NewDeleteAttribute(tx Transactor, store AttributeDeleter, rec Recorder) *DeleteAttribute {
	return &DeleteAttribute{tx: tx, store: store, rec: rec}
}

// DeleteAttributeCommand names the attribute to delete.
type DeleteAttributeCommand struct {
	Project workspace.ProjectID
	Key     string
	Request governance.Request
}

// Handle deletes the attribute unless it is standard. Nothing refers to an
// attribute yet, so a declared one is deleted unconditionally.
func (h *DeleteAttribute) Handle(ctx context.Context, cmd DeleteAttributeCommand) error {
	now := h.rec.clock.Now()
	return h.tx.Within(ctx, func(ctx context.Context) error {
		attr, err := h.store.AttributeByKey(ctx, cmd.Project, cmd.Key, true)
		if err != nil {
			return notFound(err, "attribute")
		}
		if err := attr.CanDelete(); err != nil {
			return err
		}
		if err := h.store.DeleteAttribute(ctx, attr.ID); err != nil {
			return err
		}
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: attr.Project, Action: "attribute.deleted", Entity: attributeEntity(attr), Before: attributeValues(attr),
		})
	})
}
