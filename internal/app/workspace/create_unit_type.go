package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// DeclarationKeys tells whether a project's unit types or attributes use a
// key. No constraint spans the two, so a caller asks under the project's row
// lock.
type DeclarationKeys interface {
	ProjectLocker
	DeclarationKeyTaken(ctx context.Context, project workspace.ProjectID, key string) (bool, error)
}

// claimDeclarationKey locks project and refuses key when one of its unit
// types or attributes has it, in the unit of work ctx carries.
func claimDeclarationKey(ctx context.Context, store DeclarationKeys, project workspace.ProjectID, key string) error {
	if _, err := store.LockProject(ctx, project); err != nil {
		return err
	}
	taken, err := store.DeclarationKeyTaken(ctx, project, key)
	if err != nil {
		return err
	}
	if taken {
		return workspace.ErrDeclarationKeyTaken
	}
	return nil
}

// UnitTypeCreator stores a new unit type.
type UnitTypeCreator interface {
	DeclarationKeys
	CreateUnitType(ctx context.Context, u workspace.UnitType) error
}

// CreateUnitType declares a unit type in a project.
type CreateUnitType struct {
	tx       Transactor
	store    UnitTypeCreator
	rulesets Rulesets
	rec      Recorder
}

// NewCreateUnitType returns the handler.
func NewCreateUnitType(tx Transactor, store UnitTypeCreator, rulesets Rulesets, rec Recorder) *CreateUnitType {
	return &CreateUnitType{tx: tx, store: store, rulesets: rulesets, rec: rec}
}

// CreateUnitTypeCommand names the unit type to declare.
type CreateUnitTypeCommand struct {
	Project                workspace.ProjectID
	Key, Name, Description string
	Request                governance.Request
}

// Handle declares the unit type.
func (h *CreateUnitType) Handle(ctx context.Context, cmd CreateUnitTypeCommand) (workspace.UnitType, error) {
	now := h.rec.clock.Now()
	u, err := workspace.NewUnitType(id.New[workspace.UnitType](h.rec.ids), cmd.Project, cmd.Key, cmd.Name, cmd.Description, now)
	if err != nil {
		return workspace.UnitType{}, err
	}
	err = h.tx.Within(ctx, func(ctx context.Context) error { return h.create(ctx, u, cmd.Request) })
	if err != nil {
		return workspace.UnitType{}, err
	}
	return u, nil
}

// create stores u under its project's lock unless the key is taken, in the
// unit of work ctx carries.
func (h *CreateUnitType) create(ctx context.Context, u workspace.UnitType, req governance.Request) error {
	if err := claimDeclarationKey(ctx, h.store, u.Project, u.Key); err != nil {
		return err
	}
	if err := h.store.CreateUnitType(ctx, u); err != nil {
		return err
	}
	// Every environment's ruleset declares the project's unit types and
	// attributes, so a new one changes them all.
	if err := h.rulesets.Project(ctx, u.Project); err != nil {
		return err
	}
	return h.rec.record(ctx, u.CreatedAt, req, governance.Record{
		Project: u.Project, Action: "unit_type.created", Entity: unitTypeEntity(u), After: unitTypeValues(u),
	})
}

func unitTypeEntity(u workspace.UnitType) governance.Entity {
	return governance.Entity{Kind: "unit_type", ID: u.ID.String(), Key: u.Key}
}

// unitTypeValues are what an audit entry records of a unit type.
func unitTypeValues(u workspace.UnitType) map[string]any {
	return map[string]any{"key": u.Key, "name": u.Name, "description": u.Description}
}
