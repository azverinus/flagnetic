package flags

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// ImportStore is what the import writes segments, flags and configurations
// with.
type ImportStore interface {
	SegmentCreator
	SegmentUpdater
	FlagCreator
	FlagArchiver
	DefinitionUpdater
	ConfigurationReplacer
	SegmentDeleter
	FlagDeleter
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
// except that it moves no ruleset on but says whether the ordinary write
// would have, since the import does that once per environment when it is
// done, and that it takes what no client write may set: the rule
// identifiers and rollout salts a document names, as a revert takes the
// ones it restores. The import holds the project's row, so the version it
// reads of an entity is the one its write is based on.
type Importer struct {
	d        ImporterDeps
	segments *CreateSegment
	segment  *UpdateSegment
	flags    *CreateFlag
	update   *UpdateFlag
	archive  *ArchiveFlag
	restore  *RestoreFlag

	deleteSegment *DeleteSegment
	deleteFlag    *DeleteFlag
}

// NewImporter returns the importer.
func NewImporter(d ImporterDeps) *Importer {
	none := markedRulesets{}
	return &Importer{
		d: d,
		segments: NewCreateSegment(CreateSegmentDeps{
			Tx: d.Tx, Projects: d.Projects, Declarations: d.Declarations, Store: d.Store, Rulesets: none, Recorder: d.Recorder,
		}),
		segment: NewUpdateSegment(UpdateSegmentDeps{
			Tx: d.Tx, Projects: d.Projects, Declarations: d.Declarations, Store: d.Store, Rulesets: none, Recorder: d.Recorder,
		}),
		flags: NewCreateFlag(CreateFlagDeps{
			Tx: d.Tx, Projects: d.Projects, Environments: d.Environments, Store: d.Store, Owners: d.Owners, Rulesets: none,
			Recorder: d.Recorder,
		}),
		update: NewUpdateFlag(UpdateFlagDeps{
			Tx: d.Tx, Projects: d.Projects, Store: d.Store, Owners: d.Owners, Rulesets: none, Recorder: d.Recorder,
			Evaluations: d.Evaluations,
		}),
		archive: NewArchiveFlag(ArchiveFlagDeps{
			Tx: d.Tx, Projects: d.Projects, Store: d.Store, Rulesets: none, Recorder: d.Recorder, Evaluations: d.Evaluations,
		}),
		restore: NewRestoreFlag(ArchivalDeps{
			Tx: d.Tx, Projects: d.Projects, Store: d.Store, Rulesets: none, Recorder: d.Recorder, Evaluations: d.Evaluations,
		}),
		deleteSegment: NewDeleteSegment(DeleteSegmentDeps{
			Tx: d.Tx, Projects: d.Projects, Store: d.Store, Rulesets: none, Recorder: d.Recorder,
		}),
		deleteFlag: NewDeleteFlag(DeleteFlagDeps{Tx: d.Tx, Projects: d.Projects, Store: d.Store, Recorder: d.Recorder}),
	}
}

// Effect is what a write of the importer did.
type Effect struct {
	// Changed is false for a write that changed nothing, which stores and
	// records nothing.
	Changed bool
	// Rulesets is whether the ordinary write would have moved a ruleset on:
	// every environment's for a write to a segment or a flag's definition,
	// the configuration's own environment's for a configuration.
	Rulesets bool
}

// marks notes that a write moved a ruleset on.
type marks struct{ moved bool }

type marksKey struct{}

// marked returns ctx carrying a note for the rulesets a write moves on, and
// the note.
func marked(ctx context.Context) (context.Context, *marks) {
	m := &marks{}
	return context.WithValue(ctx, marksKey{}, m), m
}

// markedRulesets only notes, in the context the write was made in, that a
// ruleset moved: the import moves each one on once, when it is done.
type markedRulesets struct{}

func (markedRulesets) Project(ctx context.Context, _ workspace.ProjectID) error { return note(ctx) }

func (markedRulesets) Environment(ctx context.Context, _ workspace.EnvironmentID) error {
	return note(ctx)
}

func note(ctx context.Context) error {
	if m, ok := ctx.Value(marksKey{}).(*marks); ok {
		m.moved = true
	}
	return nil
}

// CreateSegment creates a segment as an ordinary request does.
func (i *Importer) CreateSegment(ctx context.Context, cmd CreateSegmentCommand) (Effect, error) {
	ctx, m := marked(ctx)
	_, err := i.segments.Handle(ctx, cmd)
	return Effect{Changed: err == nil, Rulesets: m.moved}, err
}

// UpdateSegment changes a segment as an ordinary request does, to the values
// change names.
func (i *Importer) UpdateSegment(ctx context.Context, project workspace.ProjectID, key string, change flags.SegmentChange,
	req governance.Request,
) (Effect, error) {
	before, err := i.d.Store.SegmentByKey(ctx, project, key)
	if err != nil {
		return Effect{}, segmentNotFound(err)
	}
	ctx, m := marked(ctx)
	after, err := i.segment.Handle(ctx, UpdateSegmentCommand{
		Project: project, Key: key, Change: change, Expected: precondition.On(before.Version), Request: req,
	})
	return Effect{Changed: err == nil && after.Version != before.Version, Rulesets: m.moved}, err
}

// CreateFlag creates a flag, switched off in every environment of its
// project as an ordinary request does.
func (i *Importer) CreateFlag(ctx context.Context, cmd CreateFlagCommand) (Effect, error) {
	ctx, m := marked(ctx)
	_, err := i.flags.Handle(ctx, cmd)
	return Effect{Changed: err == nil, Rulesets: m.moved}, err
}

// UpdateFlag changes a flag's definition as an ordinary request does.
func (i *Importer) UpdateFlag(ctx context.Context, cmd UpdateFlagCommand) (Effect, error) {
	before, err := i.d.Store.FlagByKey(ctx, cmd.Project, cmd.Key)
	if err != nil {
		return Effect{}, flagNotFound(err)
	}
	cmd.Expected = precondition.On(before.Version)
	ctx, m := marked(ctx)
	after, err := i.update.Handle(ctx, cmd)
	return Effect{Changed: err == nil && after.Flag.Version != before.Version, Rulesets: m.moved}, err
}

// ArchiveFlag archives a flag as an ordinary request does.
func (i *Importer) ArchiveFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (Effect, error) {
	return i.archival(ctx, project, key, req, i.archive.Handle)
}

// RestoreFlag restores an archived flag as an ordinary request does.
func (i *Importer) RestoreFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (Effect, error) {
	return i.archival(ctx, project, key, req, i.restore.Handle)
}

func (i *Importer) archival(ctx context.Context, project workspace.ProjectID, key string, req governance.Request,
	handle func(context.Context, FlagCommand) (FlagView, error),
) (Effect, error) {
	before, err := i.d.Store.FlagByKey(ctx, project, key)
	if err != nil {
		return Effect{}, flagNotFound(err)
	}
	ctx, m := marked(ctx)
	after, err := handle(ctx, FlagCommand{Project: project, Key: key, Expected: precondition.On(before.Version), Request: req})
	return Effect{Changed: err == nil && after.Flag.Version != before.Version, Rulesets: m.moved}, err
}

// DeleteSegment deletes a segment as an ordinary request does, refusing one a
// rule still names, which leaves every environment's ruleset.
func (i *Importer) DeleteSegment(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (Effect, error) {
	before, err := i.d.Store.SegmentByKey(ctx, project, key)
	if err != nil {
		return Effect{}, segmentNotFound(err)
	}
	ctx, m := marked(ctx)
	err = i.deleteSegment.Handle(ctx, SegmentCommand{Project: project, Key: key, Expected: precondition.On(before.Version), Request: req})
	return Effect{Changed: err == nil, Rulesets: m.moved}, err
}

// DeleteFlag deletes a flag with its configurations as an ordinary request
// does, archiving it first when it is not archived, with the checks of both:
// a flag another requires and one a draft or running experiment holds is not
// archived, and so not deleted.
func (i *Importer) DeleteFlag(ctx context.Context, project workspace.ProjectID, key string, req governance.Request) (Effect, error) {
	before, err := i.d.Store.FlagByKey(ctx, project, key)
	if err != nil {
		return Effect{}, flagNotFound(err)
	}
	ctx, m := marked(ctx)
	version := before.Version
	if before.ArchivedAt == nil {
		archived, err := i.archive.Handle(ctx, FlagCommand{Project: project, Key: key, Expected: precondition.On(version), Request: req})
		if err != nil {
			return Effect{}, err
		}
		version = archived.Flag.Version
	}
	err = i.deleteFlag.Handle(ctx, FlagCommand{Project: project, Key: key, Expected: precondition.On(version), Request: req})
	return Effect{Changed: err == nil, Rulesets: m.moved}, err
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
// flag comes back as it was. A configuration the flag already has is not
// written.
func (i *Importer) WriteConfiguration(ctx context.Context, cmd ImportConfigurationCommand) (Effect, error) {
	d := ConfigurationDeps{Tx: i.d.Tx, Projects: i.d.Projects, Store: i.d.Store, Rulesets: markedRulesets{}, Recorder: i.d.Recorder}
	c := cmd.Configuration
	ctx, m := marked(ctx)
	var changed flags.Changed
	_, err := writeConfigurationOf(ctx, d, c, func(flags.Flag) error { return nil },
		func(ctx context.Context, f flags.Flag, before flags.Configuration, now time.Time) (configurationChange, error) {
			r, err := referentsOf(ctx, i.d.Store, i.d.Declarations, c.Project, before.Environment)
			if err != nil {
				return configurationChange{}, err
			}
			after, ch, err := before.Import(f, cmd.Input, r, i.d.Recorder.mint(), flags.Write{By: c.Caller, Now: now})
			if err != nil {
				return configurationChange{}, err
			}
			changed = ch
			return configurationChange{after: after, changed: ch, action: "flag.configuration_updated"}, nil
		})
	return Effect{Changed: err == nil && changed.Any, Rulesets: m.moved}, err
}
