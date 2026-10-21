package workspace

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// EnvironmentCreator stores a new environment after its project's others.
type EnvironmentCreator interface {
	ProjectLocker
	// NextSortOrder returns the sort order after the project's others.
	NextSortOrder(ctx context.Context, project workspace.ProjectID) (int, error)
	// CreateEnvironment refuses a key the project uses as already_exists.
	CreateEnvironment(ctx context.Context, e workspace.Environment) error
}

// EnvironmentConfigurer configures what a project holds in one of its
// environments, such as every flag's configuration, as the environment is
// created.
type EnvironmentConfigurer interface {
	// ConfigureEnvironment configures e as written by by, the member
	// creating it (nil for a project token), in the unit of work ctx
	// carries.
	ConfigureEnvironment(ctx context.Context, e workspace.Environment, by *access.MemberID) error
}

// CreateEnvironment adds an environment to a project.
type CreateEnvironment struct {
	tx        Transactor
	store     EnvironmentCreator
	configure EnvironmentConfigurer
	rec       Recorder
}

// NewCreateEnvironment returns the handler, which has configure set the new
// environment up for what the project already holds.
func NewCreateEnvironment(tx Transactor, store EnvironmentCreator, configure EnvironmentConfigurer, rec Recorder) *CreateEnvironment {
	return &CreateEnvironment{tx: tx, store: store, configure: configure, rec: rec}
}

// CreateEnvironmentCommand names the environment to add. Caller is the
// member adding it, nil for a project token.
type CreateEnvironmentCommand struct {
	Project   workspace.ProjectID
	Key, Name string
	Caller    *access.MemberID
	Request   governance.Request
}

// Handle adds the environment after the project's others, under the
// project's row lock so two created at once never share a place and no flag
// created meanwhile misses a configuration in it.
func (h *CreateEnvironment) Handle(ctx context.Context, cmd CreateEnvironmentCommand) (workspace.Environment, error) {
	now := h.rec.clock.Now()
	var e workspace.Environment
	err := h.tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.store.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		next, err := h.store.NextSortOrder(ctx, cmd.Project)
		if err != nil {
			return err
		}
		e, err = workspace.NewEnvironment(id.New[workspace.Environment](h.rec.ids), cmd.Project, cmd.Key, cmd.Name, next, now)
		if err != nil {
			return err
		}
		if err := h.store.CreateEnvironment(ctx, e); err != nil {
			return err
		}
		// Every flag exists in every environment, so the new one starts
		// with each flag's configuration, switched off.
		if err := h.configure.ConfigureEnvironment(ctx, e, cmd.Caller); err != nil {
			return err
		}
		return h.rec.record(ctx, now, cmd.Request, governance.Record{
			Project: e.Project, Environment: e.ID, Action: "environment.created", Entity: environmentEntity(e),
			After: environmentValues(e),
		})
	})
	return e, err
}

func environmentEntity(e workspace.Environment) governance.Entity {
	return governance.Entity{Kind: "environment", ID: e.ID.String(), Key: e.Key}
}

// environmentValues are what an audit entry records of an environment.
func environmentValues(e workspace.Environment) map[string]any {
	return map[string]any{"key": e.Key, "name": e.Name, "sort_order": e.SortOrder}
}
