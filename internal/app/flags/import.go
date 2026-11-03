package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ImportStore is what the import writes segments, flags and configurations
// with.
type ImportStore interface {
	SegmentCreator
	FlagCreator
	FlagArchiver
	ConfigurationReplacer
}

// ImporterDeps are what the Importer runs on.
type ImporterDeps struct {
	Tx           Transactor
	Projects     ProjectLocker
	Environments Environments
	Declarations Declarations
	Store        ImportStore
	Owners       Owners
	Recorder     Recorder
	Evaluations  LastEvaluations
}

// Importer writes the entities of a project document that the flags context
// owns. It is the import's port into the context: each method is the
// ordinary write of the entity, so it runs the same validation and records
// the same audit entry, in the unit of work the caller's context carries,
// except that it moves no ruleset on, since the import does that once per
// environment when it is done, and that it takes what no client write may
// set: the rule identifiers and rollout salts a document names, as a revert
// takes the ones it restores.
type Importer struct {
	d        ImporterDeps
	segments *CreateSegment
	flags    *CreateFlag
	archive  *ArchiveFlag
}

// NewImporter returns the importer.
func NewImporter(d ImporterDeps) *Importer {
	none := ignoredRulesets{}
	return &Importer{
		d: d,
		segments: NewCreateSegment(CreateSegmentDeps{
			Tx: d.Tx, Projects: d.Projects, Declarations: d.Declarations, Store: d.Store, Rulesets: none, Recorder: d.Recorder,
		}),
		flags: NewCreateFlag(CreateFlagDeps{
			Tx: d.Tx, Projects: d.Projects, Environments: d.Environments, Store: d.Store, Owners: d.Owners, Rulesets: none,
			Recorder: d.Recorder,
		}),
		archive: NewArchiveFlag(ArchiveFlagDeps{
			Tx: d.Tx, Projects: d.Projects, Store: d.Store, Rulesets: none, Recorder: d.Recorder, Evaluations: d.Evaluations,
		}),
	}
}

// ignoredRulesets marks nothing: the import marks every ruleset it changed
// once, when it is done.
type ignoredRulesets struct{}

func (ignoredRulesets) Project(context.Context, workspace.ProjectID) error         { return nil }
func (ignoredRulesets) Environment(context.Context, workspace.EnvironmentID) error { return nil }

// CreateSegment creates a segment as an ordinary request does.
func (i *Importer) CreateSegment(ctx context.Context, cmd CreateSegmentCommand) (flags.Segment, error) {
	return i.segments.Handle(ctx, cmd)
}

// CreateFlag creates a flag, switched off in every environment of its
// project as an ordinary request does.
func (i *Importer) CreateFlag(ctx context.Context, cmd CreateFlagCommand) (FlagView, error) {
	return i.flags.Handle(ctx, cmd)
}

// ArchiveFlag archives a flag as an ordinary request does.
func (i *Importer) ArchiveFlag(ctx context.Context, cmd FlagCommand) error {
	_, err := i.archive.Handle(ctx, cmd)
	return err
}

// ImportConfigurationCommand is the configuration a document gives a flag
// in one environment, to write whole in place of the one Configuration
// names.
type ImportConfigurationCommand struct {
	Configuration ConfigurationCommand
	Input         flags.ConfigurationInput
}

// WriteConfiguration writes the configuration whole, keeping the rule
// identifiers and rollout salts Input names and giving the rest, recorded as
// a configuration updated. An archived flag is configured too, since a
// document holds the configuration of every flag it names, and a restored
// flag comes back as it was.
func (i *Importer) WriteConfiguration(ctx context.Context, cmd ImportConfigurationCommand) error {
	d := ConfigurationDeps{Tx: i.d.Tx, Projects: i.d.Projects, Store: i.d.Store, Rulesets: ignoredRulesets{}, Recorder: i.d.Recorder}
	c := cmd.Configuration
	_, err := writeConfigurationOf(ctx, d, c, func(flags.Flag) error { return nil },
		func(ctx context.Context, f flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
			r, err := referentsOf(ctx, i.d.Store, i.d.Declarations, c.Project, before.Environment)
			if err != nil {
				return configurationChange{}, err
			}
			after, changed, err := before.Import(f, cmd.Input, r, i.d.Recorder.mint(), flags.Write{By: c.Caller, Now: now})
			if err != nil {
				return configurationChange{}, err
			}
			return configurationChange{after: after, changed: changed, action: "flag.configuration_updated"}, nil
		})
	return err
}
