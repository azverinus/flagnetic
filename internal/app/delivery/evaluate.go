package delivery

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// ErrNotFound is what a store answers for an entity it does not hold.
var ErrNotFound = errors.New("delivery: not found")

// Snapshotter runs a query of several statements on one consistent view.
type Snapshotter interface {
	Snapshot(ctx context.Context, fn func(ctx context.Context) error) error
}

// RulesetEnvironments reads the environment a ruleset is materialized for.
type RulesetEnvironments interface {
	// RulesetEnvironment returns the identifier and ruleset version of the
	// environment of project keyed key, or ErrNotFound.
	RulesetEnvironment(ctx context.Context, project workspace.ProjectID, key string) (workspace.EnvironmentID, int64, error)
}

// Declarations reads a project's unit types and attributes.
type Declarations interface {
	UnitTypes(ctx context.Context, project workspace.ProjectID) ([]workspace.UnitType, error)
	Attributes(ctx context.Context, project workspace.ProjectID) ([]workspace.Attribute, error)
}

// ConfiguredFlags reads a project's flags and segments and the flags'
// configurations in one environment.
type ConfiguredFlags interface {
	// ProjectFlags returns every flag of project, archived ones included.
	ProjectFlags(ctx context.Context, project workspace.ProjectID) ([]flags.Flag, error)
	Segments(ctx context.Context, project workspace.ProjectID) ([]flags.Segment, error)
	// EnvironmentConfigurations returns every flag's configuration in
	// environment.
	EnvironmentConfigurations(ctx context.Context, environment workspace.EnvironmentID) ([]flags.Configuration, error)
}

// Drafts checks an unsaved configuration of one flag, as the flags context
// would check replacing the saved one with it.
type Drafts interface {
	// Draft returns in as the configuration of project's flag keyed flag in
	// the environment keyed environment, or the refusal a replace would get.
	Draft(ctx context.Context, project workspace.ProjectID, flag, environment string, in flags.ConfigurationInput) (flags.Configuration, error)
}

// EvaluateDeps are what Evaluate runs on.
type EvaluateDeps struct {
	Snapshots    Snapshotter
	Environments RulesetEnvironments
	Declarations Declarations
	Flags        ConfiguredFlags
	Drafts       Drafts
	// Discoveries gathers the undeclared attributes of the contexts
	// evaluated; nil records none.
	Discoveries *Discoveries
	Clock       clock.Clock
}

// Evaluate evaluates an environment's ruleset for a context, as its SDKs
// would, with the full reason of every result: the tester of the console.
type Evaluate struct{ d EvaluateDeps }

// NewEvaluate returns the handler.
func NewEvaluate(d EvaluateDeps) *Evaluate { return &Evaluate{d: d} }

// EvaluateQuery is a context to evaluate in the environment keyed
// Environment of Project: every flag that is not archived, or those keyed
// by Flags when it is not nil, with Draft in place of its flag's saved
// configuration when it is not nil.
type EvaluateQuery struct {
	Project     workspace.ProjectID
	Environment string
	Context     eval.Context
	Flags       []string
	Draft       *Draft
}

// Draft is an unsaved configuration of the flag keyed Flag.
type Draft struct {
	Flag          string
	Configuration flags.ConfigurationInput
}

// Evaluation is what the environment's ruleset served a context.
type Evaluation struct {
	// RulesetVersion is the version of the saved ruleset, which a draft
	// does not move.
	RulesetVersion int64
	// Results are by flag key.
	Results map[string]delivery.Result
}

// errEnvironmentNotFound answers an environment key the project does not
// have.
var errEnvironmentNotFound = apperrors.NotFound("not_found", "the project has no environment with this key")

// Handle refuses an invalid context with invalid_context, then
// materializes the environment's ruleset from one snapshot, with the draft
// in place of its flag's configuration once checked, and evaluates it. It
// stores nothing, neither the draft nor an exposure; the context's
// undeclared attributes, key and type only, are noted as discovered.
func (h *Evaluate) Handle(ctx context.Context, q EvaluateQuery) (Evaluation, error) {
	if err := delivery.CheckContext(q.Context); err != nil {
		return Evaluation{}, err
	}
	var m delivery.Materials
	err := h.d.Snapshots.Snapshot(ctx, func(ctx context.Context) error {
		var err error
		m, err = h.materials(ctx, q.Project, q.Environment)
		if err != nil || q.Draft == nil {
			return err
		}
		draft, err := h.d.Drafts.Draft(ctx, q.Project, q.Draft.Flag, q.Environment, q.Draft.Configuration)
		if err != nil {
			return err
		}
		m = m.With(draft)
		return nil
	})
	if err != nil {
		return Evaluation{}, err
	}
	doc, err := delivery.Materialize(m)
	if err != nil {
		return Evaluation{}, err
	}
	rs, err := eval.Decode(doc)
	if err != nil {
		return Evaluation{}, err
	}
	results := delivery.Evaluate(rs, q.Context, q.Flags)
	if h.d.Discoveries != nil {
		h.d.Discoveries.Note(q.Project, h.d.Clock.Now(), delivery.ObserveUndeclared(rs, q.Context.Attributes))
	}
	return Evaluation{RulesetVersion: m.Version, Results: results}, nil
}

// materials reads what the ruleset of project's environment keyed
// environment is materialized from, in the snapshot ctx carries.
func (h *Evaluate) materials(ctx context.Context, project workspace.ProjectID, environment string) (delivery.Materials, error) {
	m := delivery.Materials{Environment: environment}
	envID, version, err := h.d.Environments.RulesetEnvironment(ctx, project, environment)
	if errors.Is(err, ErrNotFound) {
		return m, errEnvironmentNotFound
	}
	if err != nil {
		return m, err
	}
	m.Version = version
	return m, readMaterials(ctx, h.d.Declarations, h.d.Flags, project, envID, &m)
}
