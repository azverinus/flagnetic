package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ProjectCreator stores a new project and its defaults.
type ProjectCreator interface {
	// CreateProject refuses a key in use as already_exists.
	CreateProject(ctx context.Context, p workspace.Project) error
	CreateEnvironment(ctx context.Context, e workspace.Environment) error
	CreateUnitType(ctx context.Context, u workspace.UnitType) error
	CreateAttribute(ctx context.Context, a workspace.Attribute) error
}

// CreateProject creates a project with its default environments, unit types
// and standard attributes, so a new project is usable at once.
type CreateProject struct {
	tx    Transactor
	store ProjectCreator
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewCreateProject returns the handler.
func NewCreateProject(tx Transactor, store ProjectCreator, audit AuditLog, c clock.Clock, ids *id.Source) *CreateProject {
	return &CreateProject{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// CreateProjectCommand names the project to create.
type CreateProjectCommand struct {
	Key, Name, Description string
	Request                governance.Request
}

// Handle creates the project. Its creator receives no grant: only an
// instance admin creates projects, and they hold every permission in every
// project already.
func (h *CreateProject) Handle(ctx context.Context, cmd CreateProjectCommand) (workspace.Project, error) {
	now := h.clock.Now()
	p, err := workspace.NewProject(id.New[workspace.Project](h.ids), cmd.Key, cmd.Name, cmd.Description, now)
	if err != nil {
		return workspace.Project{}, err
	}
	defaults := workspace.DefaultsOf(p, ids{h.ids})
	entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, cmd.Request, governance.Record{
		Project: p.ID, Action: "project.created", Entity: projectEntity(p), After: projectValues(p),
	})
	if err != nil {
		return workspace.Project{}, err
	}
	if err := h.tx.Within(ctx, func(ctx context.Context) error { return h.create(ctx, p, defaults, entry) }); err != nil {
		return workspace.Project{}, err
	}
	return p, nil
}

// create stores p, its defaults and its audit entry in the unit of work ctx
// carries.
func (h *CreateProject) create(ctx context.Context, p workspace.Project, defaults workspace.ProjectDefaults, entry governance.Entry) error {
	if err := h.store.CreateProject(ctx, p); err != nil {
		return err
	}
	for _, e := range defaults.Environments {
		if err := h.store.CreateEnvironment(ctx, e); err != nil {
			return err
		}
	}
	for _, u := range defaults.UnitTypes {
		if err := h.store.CreateUnitType(ctx, u); err != nil {
			return err
		}
	}
	for _, a := range defaults.Attributes {
		if err := h.store.CreateAttribute(ctx, a); err != nil {
			return err
		}
	}
	return h.audit.Record(ctx, entry)
}

// ProjectUpdater stores a project's changed name and description.
type ProjectUpdater interface {
	ProjectLocker
	UpdateProject(ctx context.Context, p workspace.Project) error
}

// UpdateProject renames a project or changes its description.
type UpdateProject struct {
	tx    Transactor
	store ProjectUpdater
	audit AuditLog
	clock clock.Clock
	ids   *id.Source
}

// NewUpdateProject returns the handler.
func NewUpdateProject(tx Transactor, store ProjectUpdater, audit AuditLog, c clock.Clock, ids *id.Source) *UpdateProject {
	return &UpdateProject{tx: tx, store: store, audit: audit, clock: c, ids: ids}
}

// UpdateProjectCommand is a change to a project.
type UpdateProjectCommand struct {
	Project workspace.ProjectID
	Change  workspace.ProjectChange
	Request governance.Request
}

// Handle applies the change and returns the project as it is after it. A
// change to the values the project already has stores and records nothing.
func (h *UpdateProject) Handle(ctx context.Context, cmd UpdateProjectCommand) (workspace.Project, error) {
	now := h.clock.Now()
	var out workspace.Project
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		before, err := h.store.LockProject(ctx, cmd.Project)
		if err != nil {
			return err
		}
		after, changed, err := before.Change(cmd.Change, now)
		if err != nil || !changed {
			out = before
			return err
		}
		entry, err := governance.NewEntry(id.New[governance.Entry](h.ids), now, cmd.Request, governance.Record{
			Project: after.ID, Action: "project.updated", Entity: projectEntity(after),
			Before: projectValues(before), After: projectValues(after),
		})
		if err != nil {
			return err
		}
		if err := h.store.UpdateProject(ctx, after); err != nil {
			return err
		}
		out = after
		return h.audit.Record(ctx, entry)
	})
	return out, err
}

// FindProject finds a project by the key a request names.
type FindProject struct {
	store ProjectFinder
}

// NewFindProject returns the handler.
func NewFindProject(store ProjectFinder) *FindProject { return &FindProject{store: store} }

// Handle returns the project with key, or ErrNotFound.
func (h *FindProject) Handle(ctx context.Context, key string) (workspace.Project, error) {
	return h.store.ProjectByKey(ctx, key)
}

// ProjectLister lists projects.
type ProjectLister interface {
	// Projects returns every project when all is set, else those of ids,
	// ordered by key.
	Projects(ctx context.Context, all bool, ids []workspace.ProjectID) ([]workspace.Project, error)
}

// ListProjects lists the projects a caller works in.
type ListProjects struct {
	store ProjectLister
}

// NewListProjects returns the handler.
func NewListProjects(store ProjectLister) *ListProjects { return &ListProjects{store: store} }

// ListProjectsQuery says which projects to list: every one, or those of
// Only.
type ListProjectsQuery struct {
	All  bool
	Only []workspace.ProjectID
}

// Handle returns the projects, ordered by key.
func (h *ListProjects) Handle(ctx context.Context, q ListProjectsQuery) ([]workspace.Project, error) {
	return h.store.Projects(ctx, q.All, q.Only)
}

func projectEntity(p workspace.Project) governance.Entity {
	return governance.Entity{Kind: "project", ID: p.ID.String(), Key: p.Key}
}

// projectValues are what an audit entry records of a project.
func projectValues(p workspace.Project) map[string]any {
	return map[string]any{"key": p.Key, "name": p.Name, "description": p.Description}
}
