package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
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
	rec   Recorder
}

// NewCreateProject returns the handler.
func NewCreateProject(tx Transactor, store ProjectCreator, rec Recorder) *CreateProject {
	return &CreateProject{tx: tx, store: store, rec: rec}
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
	now := h.rec.clock.Now()
	p, err := workspace.NewProject(id.New[workspace.Project](h.rec.ids), cmd.Key, cmd.Name, cmd.Description, now)
	if err != nil {
		return workspace.Project{}, err
	}
	defaults := workspace.DefaultsOf(p, ids{h.rec.ids})
	if err := h.tx.Within(ctx, func(ctx context.Context) error { return h.create(ctx, p, defaults, cmd.Request) }); err != nil {
		return workspace.Project{}, err
	}
	return p, nil
}

// create stores p, its defaults and its audit entry in the unit of work ctx
// carries.
func (h *CreateProject) create(ctx context.Context, p workspace.Project, defaults workspace.ProjectDefaults, req governance.Request) error {
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
	return h.rec.record(ctx, p.CreatedAt, req, governance.Record{
		Project: p.ID, Action: "project.created", Entity: projectEntity(p), After: projectValues(p),
	})
}

func projectEntity(p workspace.Project) governance.Entity {
	return governance.Entity{Kind: "project", ID: p.ID.String(), Key: p.Key}
}

// projectValues are what an audit entry records of a project.
func projectValues(p workspace.Project) map[string]any {
	return map[string]any{"key": p.Key, "name": p.Name, "description": p.Description}
}
