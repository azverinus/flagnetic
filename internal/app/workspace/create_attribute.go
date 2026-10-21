package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// AttributeCreator stores a new attribute.
type AttributeCreator interface {
	DeclarationKeys
	CreateAttribute(ctx context.Context, attr workspace.Attribute) error
}

// CreateAttribute declares an attribute in a project.
type CreateAttribute struct {
	tx       Transactor
	store    AttributeCreator
	rulesets Rulesets
	rec      Recorder
}

// NewCreateAttribute returns the handler.
func NewCreateAttribute(tx Transactor, store AttributeCreator, rulesets Rulesets, rec Recorder) *CreateAttribute {
	return &CreateAttribute{tx: tx, store: store, rulesets: rulesets, rec: rec}
}

// CreateAttributeCommand names the attribute to declare.
type CreateAttributeCommand struct {
	Project                workspace.ProjectID
	Key, Name, Description string
	Type                   workspace.AttributeType
	Request                governance.Request
}

// Handle declares the attribute.
func (h *CreateAttribute) Handle(ctx context.Context, cmd CreateAttributeCommand) (workspace.Attribute, error) {
	now := h.rec.clock.Now()
	attr, err := workspace.NewAttribute(id.New[workspace.Attribute](h.rec.ids), cmd.Project, cmd.Key, cmd.Name, cmd.Description, cmd.Type, now)
	if err != nil {
		return workspace.Attribute{}, err
	}
	err = h.tx.Within(ctx, func(ctx context.Context) error { return h.create(ctx, attr, cmd.Request) })
	if err != nil {
		return workspace.Attribute{}, err
	}
	return attr, nil
}

// create stores attr under its project's lock unless the key is taken, in the
// unit of work ctx carries.
func (h *CreateAttribute) create(ctx context.Context, attr workspace.Attribute, req governance.Request) error {
	if err := claimDeclarationKey(ctx, h.store, attr.Project, attr.Key); err != nil {
		return err
	}
	if err := h.store.CreateAttribute(ctx, attr); err != nil {
		return err
	}
	// Every environment's ruleset declares the project's unit types and
	// attributes, so a new one changes them all.
	if err := h.rulesets.Project(ctx, attr.Project); err != nil {
		return err
	}
	return h.rec.record(ctx, attr.CreatedAt, req, governance.Record{
		Project: attr.Project, Action: "attribute.created", Entity: attributeEntity(attr), After: attributeValues(attr),
	})
}

func attributeEntity(attr workspace.Attribute) governance.Entity {
	return governance.Entity{Kind: "attribute", ID: attr.ID.String(), Key: attr.Key}
}

// attributeValues are what an audit entry records of an attribute.
func attributeValues(attr workspace.Attribute) map[string]any {
	return map[string]any{"key": attr.Key, "type": attr.Type, "name": attr.Name, "description": attr.Description, "standard": attr.Standard}
}
