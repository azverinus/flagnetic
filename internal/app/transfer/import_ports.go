package transfer

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// Transactor runs a unit of work. An import is one: it applies whole or not
// at all, and a dry run is one that is rolled back.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// WorkspaceImport writes what the workspace context owns. Each method runs
// the context's validation and records the audit entry of the ordinary
// write, in the unit of work the context carries, and none moves a ruleset
// on.
type WorkspaceImport interface {
	// CreateProject creates a project holding only the standard
	// attributes; a key in use is already_exists.
	CreateProject(ctx context.Context, key, name, description string, req governance.Request) (workspace.Project, error)
	// CreateEnvironment creates an environment at sortOrder, protected or
	// not.
	CreateEnvironment(ctx context.Context, project workspace.ProjectID, key, name string, protected bool, sortOrder int,
		req governance.Request) (workspace.Environment, error)
	CreateUnitType(ctx context.Context, project workspace.ProjectID, key, name, description string,
		req governance.Request) (workspace.UnitType, error)
	CreateAttribute(ctx context.Context, project workspace.ProjectID, key, name, description string,
		typ workspace.AttributeType, req governance.Request) (workspace.Attribute, error)
}

// SegmentImport is a segment to create.
type SegmentImport struct {
	Project           workspace.ProjectID
	Key               string
	Name, Description string
	Private           bool
	Conditions        []flags.Condition
	Request           governance.Request
}

// FlagImport is a flag to create, switched off in every environment of its
// project. Owner is the member who owns it, Caller the member importing.
type FlagImport struct {
	Project           workspace.ProjectID
	Key               string
	Type              flags.Type
	Name, Description string
	Owner             *access.MemberID
	Caller            *access.MemberID
	Tags              []string
	Lifetime          flags.Lifetime
	ExpectedRemovalAt *time.Time
	AutomaticExposure bool
	Variations        *[]flags.VariationInput
	Request           governance.Request
}

// ConfigurationImport is the configuration to write whole for a flag in one
// environment, with the rule identifiers and rollout salts it names.
type ConfigurationImport struct {
	Project     workspace.ProjectID
	Flag        string
	Environment string
	Input       flags.ConfigurationInput
	Caller      *access.MemberID
	Request     governance.Request
}

// FlagsImport writes what the flags context owns, as WorkspaceImport does.
type FlagsImport interface {
	CreateSegment(ctx context.Context, in SegmentImport) error
	CreateFlag(ctx context.Context, in FlagImport) error
	// ArchiveFlag archives the flag keyed key; its configuration can still
	// be written.
	ArchiveFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error
	WriteConfiguration(ctx context.Context, in ConfigurationImport) error
}

// Owners resolves the owners a document names, who are email addresses.
type Owners interface {
	// OwnerByEmail returns the member with email, whatever its case, who
	// may own in project, and whether there is one.
	OwnerByEmail(ctx context.Context, project workspace.ProjectID, email string) (access.MemberID, bool, error)
}

// Rulesets marks the rulesets an import changed, in its unit of work.
type Rulesets interface {
	// Environment marks the ruleset of environment changed.
	Environment(ctx context.Context, environment workspace.EnvironmentID) error
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}
