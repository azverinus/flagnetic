package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ConfigurationCreator stores the configurations of a new environment.
type ConfigurationCreator interface {
	// ProjectFlags returns every flag of project, archived ones included.
	ProjectFlags(ctx context.Context, project workspace.ProjectID) ([]flags.Flag, error)
	CreateConfigurations(ctx context.Context, cs []flags.Configuration) error
}

// ConfigureEnvironment gives a new environment a configuration of every
// flag of its project, archived ones included, so every flag exists in every
// environment as a restored flag expects.
type ConfigureEnvironment struct {
	store ConfigurationCreator
}

// NewConfigureEnvironment returns the handler.
func NewConfigureEnvironment(store ConfigurationCreator) *ConfigureEnvironment {
	return &ConfigureEnvironment{store: store}
}

// ConfigureEnvironment configures every flag of the environment's project
// in it, switched off, as written by by, the member creating the
// environment (nil for a project token), in the unit of work ctx carries:
// the environment is new, so its ruleset starts at its first version and
// nothing is bumped; the environment's own audit entry records the creation.
func (h *ConfigureEnvironment) ConfigureEnvironment(ctx context.Context, e workspace.Environment, by *access.MemberID) error {
	fs, err := h.store.ProjectFlags(ctx, e.Project)
	if err != nil || len(fs) == 0 {
		return err
	}
	configs := make([]flags.Configuration, len(fs))
	for i, f := range fs {
		configs[i] = flags.InitialConfiguration(f, e.ID, by, e.CreatedAt)
	}
	return h.store.CreateConfigurations(ctx, configs)
}
