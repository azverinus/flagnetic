package transfer

import (
	"context"
	"errors"
	"strconv"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/protection"
)

// ImportNewProjectDeps are what ImportNewProject runs on.
type ImportNewProjectDeps struct {
	Tx          Transactor
	Workspace   WorkspaceImport
	Flags       FlagsImport
	Experiments ExperimentsImport
	Owners      Owners
	Rulesets    Rulesets
	Audit       AuditLog
	Clock       clock.Clock
	IDs         *id.Source
}

// ImportNewProject creates a project from a project document.
type ImportNewProject struct{ d ImportNewProjectDeps }

// NewImportNewProject returns the handler.
func NewImportNewProject(d ImportNewProjectDeps) *ImportNewProject { return &ImportNewProject{d: d} }

// ImportNewProjectCommand is the document to create a project from. Key and
// Name, when given, replace the document's. Caller is the member importing,
// nil for a token that acts for none.
type ImportNewProjectCommand struct {
	Document transfer.Document
	Key      *string
	Name     *string
	DryRun   bool
	Caller   *access.MemberID
	Request  governance.Request
}

// ImportResult is what an import did, or with a dry run what it would do.
type ImportResult struct {
	// Project is the project's key, name and description as they are, or
	// would be.
	Project  transfer.Project
	Mode     transfer.Mode
	Source   string
	DryRun   bool
	Changes  transfer.Changes
	Warnings []transfer.Warning
	// Latest is the newest audit entry of the project before the import, the
	// zero identifier when it has none or there was no project yet: the state
	// a later import can name as the one it is based on.
	Latest governance.EntryID
}

// Handle creates the project, whole or not at all: the document is checked
// as a document, then each entity is written through the context that owns
// it in one unit of work, in the order they name each other, each recording
// its own audit entry, followed by one entry for the import. Every fault of
// the document is reported at once, with a pointer into it: an entity the
// document check refuses is not written, and the others are, so that their
// contexts judge them too, in a unit of work that is then rolled back. The unit of work
// bypasses protection in every environment: a project made from a document
// has no ruleset to review. With a dry run the whole unit of work runs, so
// every check the database makes is made, and is then rolled back.
func (h *ImportNewProject) Handle(ctx context.Context, cmd ImportNewProjectCommand) (ImportResult, error) {
	d := cmd.Document
	if d.Project != nil {
		project := *d.Project
		if cmd.Key != nil {
			project.Key = *cmd.Key
		}
		if cmd.Name != nil {
			project.Name = *cmd.Name
		}
		d.Project = &project
	}
	imp := &importRun{
		h: h, cmd: cmd, doc: d,
		run: run{owners: h.d.Owners, audit: h.d.Audit, clock: h.d.Clock, ids: h.d.IDs, req: cmd.Request, caller: cmd.Caller},
	}
	if err := imp.checkDocument(); err != nil {
		return ImportResult{}, err
	}
	var res ImportResult
	err := h.d.Tx.Within(protection.BypassEverywhere(ctx), func(ctx context.Context) error {
		var err error
		if res, err = imp.apply(ctx); err != nil {
			return err
		}
		if cmd.DryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return ImportResult{}, err
	}
	res.DryRun = cmd.DryRun
	return res, nil
}

// importRun is one import of a document into a new project.
type importRun struct {
	run
	h   *ImportNewProject
	cmd ImportNewProjectCommand
	doc transfer.Document

	// environments are the environments created, by key.
	environments map[string]workspace.Environment
	// flags are the flags created, by key.
	flags map[string]bool
}

// checkDocument finds the faults of what the document alone says, and of
// the project for what the workspace context says of it. They are reported
// with the faults the contexts find in the entities, unless there is no
// project to write the entities into: then it refuses the document at once.
func (r *importRun) checkDocument() error {
	if err := r.refuse(transfer.CheckCreate(r.doc)); err != nil {
		return err
	}
	var project []apperrors.Violation
	if p := r.doc.Project; p != nil {
		_, err := workspace.NewProject(id.New[workspace.Project](r.h.d.IDs), p.Key, p.Name, p.Description, r.h.d.Clock.Now())
		if e, ok := apperrors.From(err); ok {
			project = e.Under("/project").Violations
		}
	}
	if r.doc.Project != nil && len(project) == 0 {
		return nil
	}
	return apperrors.Invalid("invalid_document", "the project document is not valid", append(r.faults, project...)...)
}

// apply writes the document in the unit of work ctx carries.
func (r *importRun) apply(ctx context.Context) (ImportResult, error) {
	d := r.doc
	req := r.cmd.Request
	p, err := r.h.d.Workspace.CreateProject(ctx, d.Project.Key, d.Project.Name, d.Project.Description, req)
	if e, ok := apperrors.From(err); ok && e.Kind == apperrors.KindConflict && len(r.faults) > 0 {
		// A document that is refused is refused for what it is, whichever
		// project it would have been written as.
		return ImportResult{}, r.invalid()
	}
	if err != nil {
		return ImportResult{}, err
	}
	r.project = p
	steps := []func(context.Context) error{r.declare, r.createSegments, r.createFlags, r.createExperiments, r.configureFlags}
	for _, step := range steps {
		if err = step(ctx); err != nil {
			return ImportResult{}, err
		}
	}
	if len(r.faults) > 0 {
		return ImportResult{}, r.invalid()
	}
	// Every environment now serves what it did not, so each moves on once,
	// whatever number of entities were written.
	for _, e := range d.Environments {
		if err = r.h.d.Rulesets.Environment(ctx, r.environments[e.Key].ID); err != nil {
			return ImportResult{}, err
		}
	}
	err = r.record(ctx, transfer.ModeCreate)
	return ImportResult{
		Project: *d.Project, Mode: transfer.ModeCreate, Source: transfer.SourceDocument, Changes: r.changes, Warnings: r.warnings,
	}, err
}

// declare creates the environments, unit types and attributes. A standard
// attribute the document declares as the type it has is already there.
func (r *importRun) declare(ctx context.Context) error {
	w, req, project := r.h.d.Workspace, r.cmd.Request, r.project.ID
	r.environments = make(map[string]workspace.Environment, len(r.doc.Environments))
	for i, e := range r.doc.Environments {
		if r.isRefused("/environments/" + strconv.Itoa(i)) {
			continue
		}
		created, err := w.CreateEnvironment(ctx, project, e.Key, e.Name, e.Protected, i+1, r.cmd.Caller, req)
		if ferr := r.fault(err, "/environments/"+strconv.Itoa(i)); ferr != nil {
			return ferr
		}
		if err == nil {
			r.environments[e.Key] = created
			r.changes.Environments.Created = append(r.changes.Environments.Created, e.Key)
		}
	}
	for i, u := range r.doc.UnitTypes {
		if r.isRefused("/unit_types/" + strconv.Itoa(i)) {
			continue
		}
		_, err := w.CreateUnitType(ctx, project, u.Key, u.Name, u.Description, req)
		if ferr := r.fault(err, "/unit_types/"+strconv.Itoa(i)); ferr != nil {
			return ferr
		}
		if err == nil {
			r.changes.UnitTypes.Created = append(r.changes.UnitTypes.Created, u.Key)
		}
	}
	for i, a := range r.doc.Attributes {
		if _, standard := workspace.StandardAttributeType(a.Key); standard || r.isRefused("/attributes/"+strconv.Itoa(i)) {
			continue
		}
		_, err := w.CreateAttribute(ctx, project, a.Key, a.Name, a.Description, workspace.AttributeType(a.Type), req)
		if ferr := r.fault(err, "/attributes/"+strconv.Itoa(i)); ferr != nil {
			return ferr
		}
		if err == nil {
			r.changes.Attributes.Created = append(r.changes.Attributes.Created, a.Key)
		}
	}
	return nil
}

func (r *importRun) createSegments(ctx context.Context) error {
	for i, s := range r.doc.Segments {
		if r.isRefused("/segments/" + strconv.Itoa(i)) {
			continue
		}
		_, err := r.h.d.Flags.CreateSegment(ctx, SegmentImport{
			Project: r.project.ID, Key: s.Key, Name: s.Name, Description: s.Description, Private: s.Private,
			Conditions: transfer.FlagConditions(s.Conditions), Request: r.cmd.Request,
		})
		if ferr := r.fault(err, "/segments/"+strconv.Itoa(i)); ferr != nil {
			return ferr
		}
		if err == nil {
			r.changes.Segments.Created = append(r.changes.Segments.Created, s.Key)
		}
	}
	return nil
}

// createFlags creates every flag, archived ones archived at once, so that
// the configurations written next are judged against flags as the document
// has them: a flag requiring an archived flag is refused as it would be
// anywhere.
func (r *importRun) createFlags(ctx context.Context) error {
	r.flags = make(map[string]bool, len(r.doc.Flags))
	for i, f := range r.doc.Flags {
		at := "/flags/" + strconv.Itoa(i)
		if r.isRefused(at + "/key") {
			// Another flag of the document has the key. A fault the check
			// found in a configuration skips that configuration alone.
			continue
		}
		owner, err := r.ownerOf(ctx, f.Owner, at+"/owner")
		if err != nil {
			return err
		}
		_, err = r.h.d.Flags.CreateFlag(ctx, FlagImport{
			Project: r.project.ID, Key: f.Key, Type: flags.Type(f.Type), Name: f.Name, Description: f.Description,
			Owner: owner, Caller: r.cmd.Caller, Tags: f.Tags, Lifetime: flags.Lifetime(f.Lifetime),
			ExpectedRemovalAt: f.ExpectedRemovalAt, AutomaticExposure: f.AutomaticExposure, Variations: f.VariationInputs(),
			Request: r.cmd.Request,
		})
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err != nil {
			continue
		}
		r.flags[f.Key] = true
		r.changes.Flags.Created = append(r.changes.Flags.Created, f.Key)
		if f.Archived {
			if _, err = r.h.d.Flags.ArchiveFlag(ctx, r.project.ID, f.Key, r.cmd.Request); err != nil {
				return err
			}
		}
	}
	return nil
}

// createExperiments creates every experiment exactly as the document has it,
// before the configurations are written, since a rule serves an experiment
// only once it exists. An experiment whose flag the document names but the
// import refused is skipped, its fault being the flag's.
func (r *importRun) createExperiments(ctx context.Context) error {
	named := map[string]bool{}
	for _, f := range r.doc.Flags {
		named[f.Key] = true
	}
	avoid := saltsOf(r.doc)
	for i, e := range r.doc.Experiments {
		at := transfer.Pointer("experiments", i)
		if r.isRefused(at) || (named[e.Flag] && !r.flags[e.Flag]) {
			continue
		}
		owner, err := r.ownerOf(ctx, e.Owner, at+"/owner")
		if err != nil {
			return err
		}
		_, err = r.h.d.Experiments.CreateExperiment(ctx, experimentImport(r.project.ID, e, owner, r.cmd.Caller, avoid, r.cmd.Request))
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err == nil {
			r.changes.Experiments.Created = append(r.changes.Experiments.Created, e.Key)
		}
	}
	if len(r.doc.Experiments) == 0 {
		return nil
	}
	return r.h.d.Experiments.CheckSalts(ctx)
}

// configureFlags writes each flag's configuration in each environment the
// document configures it in; an environment it does not configure it in
// keeps the initial one.
func (r *importRun) configureFlags(ctx context.Context) error {
	for i, f := range r.doc.Flags {
		if !r.flags[f.Key] {
			continue
		}
		for j, env := range r.doc.Environments {
			c, configured := f.Configurations[env.Key]
			at := transfer.Pointer("flags", i, "configurations", env.Key)
			if !configured || r.environments[env.Key].ID.IsZero() || r.isRefused(transfer.Pointer("environments", j)) || r.isRefused(at) {
				continue
			}
			_, err := r.h.d.Flags.WriteConfiguration(ctx, ConfigurationImport{
				Project: r.project.ID, Flag: f.Key, Environment: env.Key, Input: c.Input(), Caller: r.cmd.Caller, Request: r.cmd.Request,
			})
			if ferr := r.fault(err, at); ferr != nil {
				return ferr
			}
		}
	}
	return nil
}
