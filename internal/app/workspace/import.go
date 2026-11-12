package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ImportStore is what the import writes a project's declarations with.
type ImportStore interface {
	ProjectCreator
	DeclarationKeys
	ProjectUpdater
	EnvironmentUpdater
	UnitTypeUpdater
	AttributeUpdater
	EnvironmentDeleter
	UnitTypeDeleter
	AttributeDeleter
}

// Importer writes the entities of a project document that the workspace
// context owns. It is the import's port into the context: every method runs
// the context's own validation, writes the entity and records the audit
// entry a direct write records, in the unit of work the caller's context
// carries, which also holds the project's row, so the version it reads of an
// entity is the one its write is based on. It moves no ruleset on: the
// import does that once per environment when it is done.
type Importer struct {
	tx        Transactor
	store     ImportStore
	configure EnvironmentConfigurer
	rec       Recorder
	project   *UpdateProject
	env       *UpdateEnvironment
	unitType  *UpdateUnitType
	attribute *UpdateAttribute

	deleteEnvironment *DeleteEnvironment
	deleteUnitType    *DeleteUnitType
	deleteAttribute   *DeleteAttribute
}

// ignoredRulesets marks nothing: the import moves each ruleset on once, when
// it is done, and learns from the port which writes would have moved them.
type ignoredRulesets struct{}

func (ignoredRulesets) Project(context.Context, workspace.ProjectID) error         { return nil }
func (ignoredRulesets) Environment(context.Context, workspace.EnvironmentID) error { return nil }

// NewImporter returns the importer writing to store and recording through
// rec, which has configure give an environment it creates in a project that
// has flags a configuration of each, and guard refuse deleting a unit type or
// an attribute something still names.
func NewImporter(tx Transactor, store ImportStore, configure EnvironmentConfigurer, guard DeclarationGuard, rec Recorder) *Importer {
	return &Importer{
		tx: tx, store: store, configure: configure, rec: rec,
		deleteEnvironment: NewDeleteEnvironment(tx, store, rec),
		deleteUnitType:    NewDeleteUnitType(tx, store, guard, ignoredRulesets{}, rec),
		deleteAttribute:   NewDeleteAttribute(tx, store, guard, ignoredRulesets{}, rec),
		project:           NewUpdateProject(tx, store, rec),
		env:               NewUpdateEnvironment(tx, store, rec),
		unitType:          NewUpdateUnitType(tx, store, rec),
		attribute:         NewUpdateAttribute(tx, store, rec),
	}
}

// UpdateProject changes a project's name and description as an ordinary
// request does, and returns the project as it is, and whether that changed
// anything.
func (i *Importer) UpdateProject(ctx context.Context, project workspace.ProjectID, name, description string,
	req governance.Request,
) (workspace.Project, bool, error) {
	before, err := i.store.LockProject(ctx, project)
	if err != nil {
		return workspace.Project{}, false, err
	}
	after, err := i.project.Handle(ctx, UpdateProjectCommand{
		Project: project, Change: workspace.ProjectChange{Name: &name, Description: &description},
		Expected: precondition.On(before.Version), Request: req,
	})
	return after, after.Version != before.Version, err
}

// UpdateEnvironment renames an environment as an ordinary request does, and
// says whether that changed anything. Whether it is protected is not an
// import's to change.
func (i *Importer) UpdateEnvironment(ctx context.Context, project workspace.ProjectID, key, name string,
	req governance.Request,
) (bool, error) {
	before, err := i.store.EnvironmentByKey(ctx, project, key, false)
	if err != nil {
		return false, notFound(err, "environment")
	}
	after, err := i.env.Handle(ctx, UpdateEnvironmentCommand{
		Project: project, Key: key, Change: workspace.EnvironmentChange{Name: &name},
		Expected: precondition.On(before.Version), Request: req,
	})
	return after.Version != before.Version, err
}

// UpdateUnitType renames a unit type or changes its description as an
// ordinary request does, and says whether that changed anything.
func (i *Importer) UpdateUnitType(ctx context.Context, project workspace.ProjectID, key, name, description string,
	req governance.Request,
) (bool, error) {
	before, err := i.store.UnitTypeByKey(ctx, project, key, false)
	if err != nil {
		return false, notFound(err, "unit type")
	}
	after, err := i.unitType.Handle(ctx, UpdateUnitTypeCommand{
		Project: project, Key: key, Change: workspace.UnitTypeChange{Name: &name, Description: &description},
		Expected: precondition.On(before.Version), Request: req,
	})
	return after.Version != before.Version, err
}

// UpdateAttribute renames an attribute or changes its description as an
// ordinary request does, and says whether that changed anything.
func (i *Importer) UpdateAttribute(ctx context.Context, project workspace.ProjectID, key, name, description string,
	req governance.Request,
) (bool, error) {
	before, err := i.store.AttributeByKey(ctx, project, key, false)
	if err != nil {
		return false, notFound(err, "attribute")
	}
	after, err := i.attribute.Handle(ctx, UpdateAttributeCommand{
		Project: project, Key: key, Change: workspace.AttributeChange{Name: &name, Description: &description},
		Expected: precondition.On(before.Version), Request: req,
	})
	return after.Version != before.Version, err
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
// keep protected, so the document's value is taken. Every flag the project
// has is configured in it, switched off, as written by by, the member
// importing.
func (i *Importer) CreateEnvironment(ctx context.Context, project workspace.ProjectID, key, name string, protected bool,
	sortOrder int, by *access.MemberID, req governance.Request,
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
	if err = i.configure.ConfigureEnvironment(ctx, e, by); err != nil {
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

// DeleteEnvironment deletes an environment as an ordinary request does,
// refusing the project's last and a protected one, with everything it holds.
func (i *Importer) DeleteEnvironment(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error {
	e, err := i.store.EnvironmentByKey(ctx, project, key, false)
	expected, err := versionOf(e.Version, err, "environment")
	if err != nil {
		return err
	}
	return i.deleteEnvironment.Handle(ctx, DeleteEnvironmentCommand{Project: project, Key: key, Expected: expected, Request: req})
}

// DeleteUnitType deletes a unit type as an ordinary request does, refusing
// one a rule, rollout, override or segment still names. It moves no ruleset
// on, though the ordinary deletion moves every environment's.
func (i *Importer) DeleteUnitType(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error {
	u, err := i.store.UnitTypeByKey(ctx, project, key, false)
	expected, err := versionOf(u.Version, err, "unit type")
	if err != nil {
		return err
	}
	return i.deleteUnitType.Handle(ctx, DeleteUnitTypeCommand{Project: project, Key: key, Expected: expected, Request: req})
}

// DeleteAttribute deletes an attribute as an ordinary request does, refusing
// a standard one and one a rule or segment still names. It moves no ruleset
// on, though the ordinary deletion moves every environment's.
func (i *Importer) DeleteAttribute(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error {
	a, err := i.store.AttributeByKey(ctx, project, key, false)
	expected, err := versionOf(a.Version, err, "attribute")
	if err != nil {
		return err
	}
	return i.deleteAttribute.Handle(ctx, DeleteAttributeCommand{Project: project, Key: key, Expected: expected, Request: req})
}

// versionOf returns the version an entity just read is deleted on, or the
// not-found answer of the read that failed.
func versionOf(version int64, err error, what string) (precondition.Version, error) {
	if err != nil {
		return precondition.None(), notFound(err, what)
	}
	return precondition.On(version), nil
}
