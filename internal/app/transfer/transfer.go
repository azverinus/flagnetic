// Package transfer holds the use cases of the transfer context: a project's
// configuration leaving the platform as a project document.
package transfer

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Snapshotter runs a query of several statements on one consistent view, so
// what it answers never mixes the states before and after a write committed
// between its statements.
type Snapshotter interface {
	Snapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

// Projects reads projects.
type Projects interface {
	// Projects returns every project when all is set, else those of ids,
	// ordered by key.
	Projects(ctx context.Context, all bool, ids []workspace.ProjectID) ([]workspace.Project, error)
}

// Workspace reads what a project declares, which the workspace context owns.
type Workspace interface {
	// Environments returns the project's environments by sort order.
	Environments(ctx context.Context, project workspace.ProjectID) ([]workspace.Environment, error)
	// UnitTypes returns the project's unit types by key.
	UnitTypes(ctx context.Context, project workspace.ProjectID) ([]workspace.UnitType, error)
	// Attributes returns the project's attributes by key, the standard ones
	// included.
	Attributes(ctx context.Context, project workspace.ProjectID) ([]workspace.Attribute, error)
}

// Flags reads segments, flags and their configurations, which the flags
// context owns.
type Flags interface {
	// Segments returns the project's segments by key.
	Segments(ctx context.Context, project workspace.ProjectID) ([]flags.Segment, error)
	// ProjectFlags returns every flag of the project, archived ones included.
	ProjectFlags(ctx context.Context, project workspace.ProjectID) ([]flags.Flag, error)
	// ProjectConfigurations returns the configuration of every flag of the
	// project in every environment, by flag, each flag's by the
	// environments' order.
	ProjectConfigurations(ctx context.Context, project workspace.ProjectID) (map[flags.FlagID][]flags.EnvironmentConfiguration, error)
}

// Experiments reads experiments, which the experiments context owns.
type Experiments interface {
	// ProjectExperiments returns every experiment of the project, whatever
	// its status.
	ProjectExperiments(ctx context.Context, project workspace.ProjectID) ([]experiments.Experiment, error)
}

// Members reads who owns what, which the access context knows.
type Members interface {
	// MemberEmails returns the email address of each of members that exists.
	MemberEmails(ctx context.Context, members []access.MemberID) (map[access.MemberID]string, error)
}

// History tells how far a project's history has come, which the audit log
// of the governance context keeps.
type History interface {
	// LatestProjectEntry returns the identifier of the newest audit entry of
	// project, the zero identifier when it has none.
	LatestProjectEntry(ctx context.Context, project workspace.ProjectID) (governance.EntryID, error)
}

// errProjectNotFound answers a project deleted since the request found it.
var errProjectNotFound = apperrors.NotFound("not_found", "there is no such project")
