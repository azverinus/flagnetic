package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// AttributeUpdater stores an attribute's changed name and description.
type AttributeUpdater interface {
	// AttributeByKey returns the attribute, locking its row when lock is set,
	// or ErrNotFound.
	AttributeByKey(ctx context.Context, project workspace.ProjectID, key string, lock bool) (workspace.Attribute, error)
	UpdateAttribute(ctx context.Context, attr workspace.Attribute) error
}

// UpdateAttribute renames an attribute or changes its description.
type UpdateAttribute struct {
	tx    Transactor
	store AttributeUpdater
	rec   Recorder
}

// NewUpdateAttribute returns the handler.
func NewUpdateAttribute(tx Transactor, store AttributeUpdater, rec Recorder) *UpdateAttribute {
	return &UpdateAttribute{tx: tx, store: store, rec: rec}
}

// UpdateAttributeCommand is a change to the attribute Key of Project,
// based on the version Expected names.
type UpdateAttributeCommand struct {
	Project  workspace.ProjectID
	Key      string
	Change   workspace.AttributeChange
	Expected precondition.Version
	Request  governance.Request
}

// Handle applies the change, validated first and then judged against the
// version it was based on, and returns the attribute as it is after it; a
// change to the values it already has stores and records nothing.
func (h *UpdateAttribute) Handle(ctx context.Context, cmd UpdateAttributeCommand) (workspace.Attribute, error) {
	now := h.rec.clock.Now()
	var out workspace.Attribute
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.store.AttributeByKey(ctx, cmd.Project, cmd.Key, true)
		if err != nil {
			return notFound(err, "attribute")
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
		if err := h.store.UpdateAttribute(ctx, after); err != nil {
			return err
		}
		out = after
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: after.Project, Action: "attribute.updated", Entity: attributeEntity(after),
			Before: attributeValues(before), After: attributeValues(after),
		})
	})
	return out, err
}
