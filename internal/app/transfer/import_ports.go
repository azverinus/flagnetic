package transfer

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
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
	// UpdateProject writes the project's name and description and returns
	// the project as it is, and whether that changed anything.
	UpdateProject(ctx context.Context, project workspace.ProjectID, name, description string, req governance.Request,
	) (workspace.Project, bool, error)
	// CreateEnvironment creates an environment at sortOrder, protected or
	// not, configuring every flag the project has in it, as written by by.
	CreateEnvironment(ctx context.Context, project workspace.ProjectID, key, name string, protected bool, sortOrder int,
		by *access.MemberID, req governance.Request) (workspace.Environment, error)
	// UpdateEnvironment writes an environment's name, and says whether that
	// changed anything. Whether it is protected is never an import's to
	// change.
	UpdateEnvironment(ctx context.Context, project workspace.ProjectID, key, name string, req governance.Request) (bool, error)
	CreateUnitType(ctx context.Context, project workspace.ProjectID, key, name, description string,
		req governance.Request) (workspace.UnitType, error)
	UpdateUnitType(ctx context.Context, project workspace.ProjectID, key, name, description string, req governance.Request) (bool, error)
	CreateAttribute(ctx context.Context, project workspace.ProjectID, key, name, description string,
		typ workspace.AttributeType, req governance.Request) (workspace.Attribute, error)
	// UpdateAttribute writes an attribute's name and description; its type
	// never changes.
	UpdateAttribute(ctx context.Context, project workspace.ProjectID, key, name, description string, req governance.Request) (bool, error)
	// DeleteEnvironment deletes an environment with everything it holds,
	// refusing the project's last and a protected one.
	DeleteEnvironment(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error
	// DeleteUnitType and DeleteAttribute delete a declaration, refusing one
	// something still names with in_use. Neither moves a ruleset on, though the
	// ordinary deletion moves every environment's: the import does.
	DeleteUnitType(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error
	DeleteAttribute(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error
}

// Effect is what a write of an import port did: whether it changed anything,
// and whether the ordinary write would have moved a ruleset on, which the
// import does once per environment when it is done. A write to a segment or
// to a flag's definition moves every environment's on, a write of a
// configuration its own environment's.
type Effect struct {
	Changed  bool
	Rulesets bool
	// Environments are the environments whose ruleset the ordinary write
	// would have moved on, for a write that moves only some: a running
	// experiment's traffic allocation grown moves the ones holding its
	// rules.
	Environments []workspace.EnvironmentID
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

// SegmentUpdate is the values a segment is to have.
type SegmentUpdate struct {
	Project           workspace.ProjectID
	Key               string
	Name, Description string
	Private           bool
	Conditions        []flags.Condition
	Request           governance.Request
}

// FlagUpdate is the definition a flag is to have. Owner is the member who is
// to own it, nil to leave the owner as it is.
type FlagUpdate struct {
	Project           workspace.ProjectID
	Key               string
	Name, Description string
	Owner             *access.MemberID
	Tags              []string
	Lifetime          flags.Lifetime
	ExpectedRemovalAt *time.Time
	AutomaticExposure bool
	Variations        *[]flags.VariationInput
	Request           governance.Request
}

// FlagsImport writes what the flags context owns, as WorkspaceImport does.
type FlagsImport interface {
	CreateSegment(ctx context.Context, in SegmentImport) (Effect, error)
	UpdateSegment(ctx context.Context, in SegmentUpdate) (Effect, error)
	CreateFlag(ctx context.Context, in FlagImport) (Effect, error)
	UpdateFlag(ctx context.Context, in FlagUpdate) (Effect, error)
	// ArchiveFlag archives the flag keyed key; its configuration can still
	// be written.
	ArchiveFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (Effect, error)
	// RestoreFlag brings an archived flag back.
	RestoreFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (Effect, error)
	// WriteConfiguration writes the configuration whole; one the flag has
	// already is not written.
	WriteConfiguration(ctx context.Context, in ConfigurationImport) (Effect, error)
	// DeleteSegment deletes a segment, refusing one a rule still names with
	// in_use.
	DeleteSegment(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (Effect, error)
	// DeleteFlag deletes a flag with its configurations, archiving it first
	// when it is not archived. A flag another requires, or a draft or running
	// experiment holds, is in_use.
	DeleteFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (Effect, error)
}

// ExperimentImport is an experiment as a document gives it, to create or to
// change. Owner is the member the document's owner resolved to, nil for none:
// the importing member owns a new experiment then, and an existing one keeps
// its owner.
type ExperimentImport struct {
	Project           workspace.ProjectID
	Key               string
	Name              string
	Hypothesis        string
	Owner             *access.MemberID
	Caller            *access.MemberID
	Tags              []string
	Flag              string
	UnitType          string
	Variations        []experiments.Weight
	TrafficAllocation int
	// InclusionSalt and VariationSalt are the document's, "" for one it
	// leaves out: a new draft is given them, an existing experiment keeps
	// them.
	InclusionSalt string
	VariationSalt string
	Status        experiments.Status
	ExpectedEndAt *time.Time
	StartedAt     *time.Time
	StoppedAt     *time.Time
	ArchivedAt    *time.Time
	Decision      *experiments.Decision
	// Avoid are the salts the import gives rollouts, which no salt drawn for a
	// new draft may be.
	Avoid   []string
	Request governance.Request
}

// ExperimentsImport writes what the experiments context owns, as
// WorkspaceImport does. It creates an experiment exactly as the document has
// it, and changes one that exists only as an edit of its status allows,
// refusing frozen otherwise: it never moves an experiment through its status.
type ExperimentsImport interface {
	CreateExperiment(ctx context.Context, in ExperimentImport) (Effect, error)
	UpdateExperiment(ctx context.Context, in ExperimentImport) (Effect, error)
	// CheckSalts refuses two experiments of a project sharing a salt once
	// every experiment is written: until then one may take a salt another
	// gives up later in the same import.
	CheckSalts(ctx context.Context) error
	// DeleteExperiment deletes a draft experiment.
	DeleteExperiment(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) error
}

// SDKKeys tells which SDK keys an environment holds, which keep an import from
// deleting it.
type SDKKeys interface {
	// LiveSDKKeys returns the identifiers of the keys of environment that
	// still authenticate: neither revoked nor past the end of their overlap.
	LiveSDKKeys(ctx context.Context, environment workspace.EnvironmentID) ([]string, error)
}

// Owners resolves the owners a document names, who are email addresses.
type Owners interface {
	// OwnerByEmail returns the member with email, whatever its case, who
	// may own in project, and whether there is one.
	OwnerByEmail(ctx context.Context, project workspace.ProjectID, email string) (access.MemberID, bool, error)
}

// ProjectLocker locks a project's row for the unit of work, which an import
// holds from before it reads what the project has until it commits.
type ProjectLocker interface {
	// LockProject returns the project, and whether there is one.
	LockProject(ctx context.Context, project workspace.ProjectID) (workspace.Project, bool, error)
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
