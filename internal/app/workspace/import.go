package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ImportStore is what the import writes a project's declarations with.
type ImportStore interface {
	ProjectCreator
	DeclarationKeys
}

// Importer writes the entities of a project document that the workspace
// context owns. It is the import's port into the context: every method runs
// the context's own validation, writes the entity and records the audit
// entry a direct write records, in the unit of work the caller's context
// carries, which also holds the project's row. It moves no ruleset on: the
// import does that once per environment when it is done.
type Importer struct {
	store ImportStore
	rec   Recorder
}

// NewImporter returns the importer writing to store and recording through
// rec.
func NewImporter(store ImportStore, rec Recorder) *Importer {
	return &Importer{store: store, rec: rec}
}

// CreateProject creates a project holding only what every project holds, the
// standard attributes: the document adds the rest. A key in use is
// already_exists.
func (i *Importer) CreateProject(ctx context.Context, key, name, description string, req governance.Request) (workspace.Project, error) {
	now := i.rec.clock.Now()
	p, err := workspace.NewProject(id.New[workspace.Project](i.rec.ids), key, name, description, now)
	if err != nil {
		return workspace.Project{}, err
	}
	if err = i.store.CreateProject(ctx, p); err != nil {
		return workspace.Project{}, err
	}
	for _, a := range workspace.StandardAttributesOf(p, ids{i.rec.ids}) {
		if err = i.store.CreateAttribute(ctx, a); err != nil {
			return workspace.Project{}, err
		}
	}
	err = i.rec.record(ctx, p.CreatedAt, req, governance.Record{
		Project: p.ID, Action: "project.created", Entity: projectEntity(p), After: projectValues(p),
	})
	return p, err
}

// CreateEnvironment creates an environment of project at sortOrder,
// protected or not as the document says: a new environment has nothing to
// keep protected, so the document's value is taken.
func (i *Importer) CreateEnvironment(ctx context.Context, project workspace.ProjectID, key, name string, protected bool,
	sortOrder int, req governance.Request,
) (workspace.Environment, error) {
	now := i.rec.clock.Now()
	e, err := workspace.NewEnvironment(id.New[workspace.Environment](i.rec.ids), project, key, name, sortOrder, now)
	if err != nil {
		return workspace.Environment{}, err
	}
	e.Protected = protected
	if err = i.store.CreateEnvironment(ctx, e); err != nil {
		return workspace.Environment{}, err
	}
	err = i.rec.record(ctx, now, req, governance.Record{
		Project: project, Environment: e.ID, Action: "environment.created", Entity: environmentEntity(e), After: environmentValues(e),
	})
	return e, err
}

// CreateUnitType declares a unit type of project, refusing a key a unit type
// or attribute of the project has.
func (i *Importer) CreateUnitType(ctx context.Context, project workspace.ProjectID, key, name, description string,
	req governance.Request,
) (workspace.UnitType, error) {
	now := i.rec.clock.Now()
	u, err := workspace.NewUnitType(id.New[workspace.UnitType](i.rec.ids), project, key, name, description, now)
	if err != nil {
		return workspace.UnitType{}, err
	}
	if err = claimDeclarationKey(ctx, i.store, project, key); err != nil {
		return workspace.UnitType{}, err
	}
	if err = i.store.CreateUnitType(ctx, u); err != nil {
		return workspace.UnitType{}, err
	}
	err = i.rec.record(ctx, now, req, governance.Record{
		Project: project, Action: "unit_type.created", Entity: unitTypeEntity(u), After: unitTypeValues(u),
	})
	return u, err
}

// CreateAttribute declares an attribute of project, refusing a key a unit
// type or attribute of the project has, the standard attributes included.
func (i *Importer) CreateAttribute(ctx context.Context, project workspace.ProjectID, key, name, description string,
	typ workspace.AttributeType, req governance.Request,
) (workspace.Attribute, error) {
	now := i.rec.clock.Now()
	a, err := workspace.NewAttribute(id.New[workspace.Attribute](i.rec.ids), project, key, name, description, typ, now)
	if err != nil {
		return workspace.Attribute{}, err
	}
	if err = claimDeclarationKey(ctx, i.store, project, key); err != nil {
		return workspace.Attribute{}, err
	}
	if err = i.store.CreateAttribute(ctx, a); err != nil {
		return workspace.Attribute{}, err
	}
	err = i.rec.record(ctx, now, req, governance.Record{
		Project: project, Action: "attribute.created", Entity: attributeEntity(a), After: attributeValues(a),
	})
	return a, err
}
