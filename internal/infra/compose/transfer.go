package compose

import (
	"context"
	"errors"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	apptransfer "github.com/azverinus/flagnetic/internal/app/transfer"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
)

// transferFlags gives the transfer context the import port of the flags
// context, which it may not import.
type transferFlags struct{ importer *appflags.Importer }

var _ apptransfer.FlagsImport = transferFlags{}

func effectOf(e appflags.Effect) apptransfer.Effect {
	return apptransfer.Effect{Changed: e.Changed, Rulesets: e.Rulesets}
}

func (f transferFlags) CreateSegment(ctx context.Context, in apptransfer.SegmentImport) (apptransfer.Effect, error) {
	e, err := f.importer.CreateSegment(ctx, appflags.CreateSegmentCommand{
		Project: in.Project, Key: in.Key, Name: in.Name, Description: in.Description, Private: in.Private,
		Conditions: in.Conditions, Request: in.Request,
	})
	return effectOf(e), err
}

func (f transferFlags) UpdateSegment(ctx context.Context, in apptransfer.SegmentUpdate) (apptransfer.Effect, error) {
	e, err := f.importer.UpdateSegment(ctx, in.Project, in.Key, flags.SegmentChange{
		Name: &in.Name, Description: &in.Description, Private: &in.Private, Conditions: &in.Conditions,
	}, in.Request)
	return effectOf(e), err
}

func (f transferFlags) CreateFlag(ctx context.Context, in apptransfer.FlagImport) (apptransfer.Effect, error) {
	e, err := f.importer.CreateFlag(ctx, appflags.CreateFlagCommand{
		Project: in.Project, Key: in.Key, Type: in.Type, Name: in.Name, Description: in.Description, Owner: in.Owner,
		Caller: in.Caller, Tags: in.Tags, Lifetime: in.Lifetime, ExpectedRemovalAt: in.ExpectedRemovalAt,
		AutomaticExposure: in.AutomaticExposure, Variations: in.Variations, Request: in.Request,
	})
	return effectOf(e), err
}

func (f transferFlags) UpdateFlag(ctx context.Context, in apptransfer.FlagUpdate) (apptransfer.Effect, error) {
	change := flags.FlagChange{
		Name: &in.Name, Description: &in.Description, Tags: &in.Tags, ExpectedRemovalAt: in.ExpectedRemovalAt,
		AutomaticExposure: &in.AutomaticExposure, Variations: in.Variations,
	}
	// A document that gives no lifetime leaves it as it is.
	if in.Lifetime != "" {
		change.Lifetime = &in.Lifetime
	}
	e, err := f.importer.UpdateFlag(ctx, appflags.UpdateFlagCommand{
		Project: in.Project, Key: in.Key, Change: change, Owner: in.Owner, Request: in.Request,
	})
	return effectOf(e), err
}

func (f transferFlags) ArchiveFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (apptransfer.Effect, error) {
	e, err := f.importer.ArchiveFlag(ctx, project, key, req)
	return effectOf(e), err
}

func (f transferFlags) RestoreFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (apptransfer.Effect, error) {
	e, err := f.importer.RestoreFlag(ctx, project, key, req)
	return effectOf(e), err
}

func (f transferFlags) WriteConfiguration(ctx context.Context, in apptransfer.ConfigurationImport) (apptransfer.Effect, error) {
	e, err := f.importer.WriteConfiguration(ctx, appflags.ImportConfigurationCommand{
		Configuration: appflags.ConfigurationCommand{
			Project: in.Project, Flag: in.Flag, Environment: in.Environment, Caller: in.Caller, Request: in.Request,
		},
		Input: in.Input,
	})
	return effectOf(e), err
}

// transferProjects gives the transfer context the project row lock of the
// workspace context.
type transferProjects struct{ projects *pgworkspace.Store }

var _ apptransfer.ProjectLocker = transferProjects{}

func (p transferProjects) LockProject(ctx context.Context, project workspace.ProjectID) (workspace.Project, bool, error) {
	locked, err := p.projects.LockProject(ctx, project)
	if errors.Is(err, appworkspace.ErrNotFound) {
		return workspace.Project{}, false, nil
	}
	return locked, err == nil, err
}

// transferOwners gives the transfer context the owners the access context
// knows by email.
type transferOwners struct {
	members   *pgaccess.Store
	ownership *appaccess.Ownership
}

var _ apptransfer.Owners = transferOwners{}

func (o transferOwners) OwnerByEmail(ctx context.Context, project workspace.ProjectID, email string) (access.MemberID, bool, error) {
	m, _, err := o.members.MemberByEmail(ctx, email)
	if errors.Is(err, appaccess.ErrNotFound) {
		return access.MemberID{}, false, nil
	}
	if err != nil {
		return access.MemberID{}, false, err
	}
	may, err := o.ownership.MayOwn(ctx, project, m.ID)
	return m.ID, may, err
}
