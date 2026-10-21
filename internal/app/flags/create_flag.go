package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// FlagCreator stores a new flag with its configurations.
type FlagCreator interface {
	// CreateFlag refuses a key the project uses as already_exists.
	CreateFlag(ctx context.Context, f flags.Flag) error
	CreateConfigurations(ctx context.Context, cs []flags.Configuration) error
}

// CreateFlagDeps are what CreateFlag runs on.
type CreateFlagDeps struct {
	Tx           Transactor
	Projects     ProjectLocker
	Environments Environments
	Store        FlagCreator
	Owners       Owners
	Rulesets     Rulesets
	Recorder     Recorder
}

// CreateFlag creates a flag, switched off in every environment of its
// project.
type CreateFlag struct{ d CreateFlagDeps }

// NewCreateFlag returns the handler.
func NewCreateFlag(d CreateFlagDeps) *CreateFlag { return &CreateFlag{d: d} }

// CreateFlagCommand is the flag to create. Owner is the member the request
// names, nil when it names none; Caller is the member making the request,
// nil for a project token.
type CreateFlagCommand struct {
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

// Handle creates the flag and its configuration in every environment, which
// changes every environment's ruleset: SDKs now receive a flag they did not.
func (h *CreateFlag) Handle(ctx context.Context, cmd CreateFlagCommand) (FlagView, error) {
	now := h.d.Recorder.now()
	var out FlagView
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, h.d.Projects, cmd.Project); err != nil {
			return err
		}
		owner, err := h.owner(ctx, cmd)
		if err != nil {
			return err
		}
		f, err := flags.NewFlag(flags.FlagInput{
			ID: id.New[flags.Flag](h.d.Recorder.ids), Project: cmd.Project, Key: cmd.Key, Type: cmd.Type,
			Name: cmd.Name, Description: cmd.Description, Owner: owner,
			Tags: cmd.Tags, Lifetime: cmd.Lifetime, ExpectedRemovalAt: cmd.ExpectedRemovalAt,
			AutomaticExposure: cmd.AutomaticExposure, Variations: cmd.Variations, CreatedBy: cmd.Caller, Now: now,
		})
		if err != nil {
			return err
		}
		envs, err := h.d.Environments.Environments(ctx, cmd.Project)
		if err != nil {
			return err
		}
		out = FlagView{Flag: f}
		configs := make([]flags.Configuration, len(envs))
		for i, e := range envs {
			configs[i] = flags.InitialConfiguration(f, e.ID, cmd.Caller, now)
			out.Configurations = append(out.Configurations, flags.EnvironmentConfiguration{Environment: e.Key, Configuration: configs[i]})
		}
		if err := h.d.Store.CreateFlag(ctx, f); err != nil {
			return err
		}
		if err := h.d.Store.CreateConfigurations(ctx, configs); err != nil {
			return err
		}
		if err := h.d.Rulesets.Project(ctx, cmd.Project); err != nil {
			return err
		}
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "flag.created", Entity: flagEntity(f), After: flagValues(out),
		})
	})
	return out, err
}

// owner returns the owner cmd names, else its caller, and whether they may
// own the flag. A project token acts for no member, so a flag it creates
// without naming an owner has none, which the flag refuses: a pipeline is
// nobody to ask before deleting it.
func (h *CreateFlag) owner(ctx context.Context, cmd CreateFlagCommand) (flags.Owner, error) {
	member := cmd.Owner
	if member == nil {
		member = cmd.Caller
	}
	if member == nil {
		return flags.Owner{}, nil
	}
	eligible, err := h.d.Owners.MayOwn(ctx, cmd.Project, *member)
	return flags.Owner{Member: *member, Eligible: eligible}, err
}
