package compose

import (
	"context"
	"errors"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	apptransfer "github.com/azverinus/flagnetic/internal/app/transfer"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// transferFlags gives the transfer context the import port of the flags
// context, which it may not import.
type transferFlags struct{ importer *appflags.Importer }

var _ apptransfer.FlagsImport = transferFlags{}

func (f transferFlags) CreateSegment(ctx context.Context, in apptransfer.SegmentImport) error {
	_, err := f.importer.CreateSegment(ctx, appflags.CreateSegmentCommand{
		Project: in.Project, Key: in.Key, Name: in.Name, Description: in.Description, Private: in.Private,
		Conditions: in.Conditions, Request: in.Request,
	})
	return err
}

func (f transferFlags) CreateFlag(ctx context.Context, in apptransfer.FlagImport) error {
	_, err := f.importer.CreateFlag(ctx, appflags.CreateFlagCommand{
		Project: in.Project, Key: in.Key, Type: in.Type, Name: in.Name, Description: in.Description, Owner: in.Owner,
		Caller: in.Caller, Tags: in.Tags, Lifetime: in.Lifetime, ExpectedRemovalAt: in.ExpectedRemovalAt,
		AutomaticExposure: in.AutomaticExposure, Variations: in.Variations, Request: in.Request,
	})
	return err
}

func (f transferFlags) ArchiveFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error {
	// The flag was created by this import and is at its first version.
	return f.importer.ArchiveFlag(ctx, appflags.FlagCommand{Project: project, Key: key, Expected: precondition.On(1), Request: req})
}

func (f transferFlags) WriteConfiguration(ctx context.Context, in apptransfer.ConfigurationImport) error {
	return f.importer.WriteConfiguration(ctx, appflags.ImportConfigurationCommand{
		Configuration: appflags.ConfigurationCommand{
			Project: in.Project, Flag: in.Flag, Environment: in.Environment, Caller: in.Caller, Request: in.Request,
		},
		Input: in.Input,
	})
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
