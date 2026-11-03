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
	Tx        Transactor
	Workspace WorkspaceImport
	Flags     FlagsImport
	Owners    Owners
	Rulesets  Rulesets
	Audit     AuditLog
	Clock     clock.Clock
	IDs       *id.Source
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
}

// errDryRun ends the unit of work of a dry run, which rolls it back.
var errDryRun = errors.New("transfer: a dry run ends in a rollback")

// Handle creates the project, whole or not at all: the document is checked
// as a document, then each entity is written through the context that owns
// it in one unit of work, in the order they name each other, each recording
// its own audit entry, followed by one entry for the import. Every fault of
// the document is reported at once, with a pointer into it. The unit of work
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
	run := &importRun{h: h, cmd: cmd, doc: d}
	if err := run.checkDocument(); err != nil {
		return ImportResult{}, err
	}
	var res ImportResult
	err := h.d.Tx.Within(protection.BypassEverywhere(ctx), func(ctx context.Context) error {
		var err error
		if res, err = run.apply(ctx); err != nil {
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
	h   *ImportNewProject
	cmd ImportNewProjectCommand
	doc transfer.Document

	project  workspace.Project
	changes  transfer.Changes
	warnings []transfer.Warning
	faults   []apperrors.Violation
	// environments are the environments created, by key.
	environments map[string]workspace.Environment
	// flags are the flags created, by key.
	flags map[string]bool
}

// checkDocument refuses the document for what it alone says, and the
// project for what the workspace context says of it, together, so a single
// answer reports both.
func (r *importRun) checkDocument() error {
	var violations []apperrors.Violation
	if err := transfer.CheckCreate(r.doc); err != nil {
		if e, ok := apperrors.From(err); ok {
			violations = append(violations, e.Violations...)
		}
	}
	if p := r.doc.Project; p != nil {
		_, err := workspace.NewProject(id.New[workspace.Project](r.h.d.IDs), p.Key, p.Name, p.Description, r.h.d.Clock.Now())
		if e, ok := apperrors.From(err); ok {
			violations = append(violations, e.Under("/project").Violations...)
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return apperrors.Invalid("invalid_document", "the project document is not valid", violations...)
}

// fault records err, a refusal of the entity at pointer, and goes on with
// the rest of the document so that every fault is reported; it returns any
// other error, which ends the import. A refusal is a fault the document
// has: invalid, with its violations pointing into the entity.
func (r *importRun) fault(err error, pointer string) error {
	if err == nil {
		return nil
	}
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid {
		return err
	}
	if len(e.Violations) == 0 {
		r.faults = append(r.faults, apperrors.Violation{Pointer: pointer, Code: e.Code, Detail: e.Detail})
		return nil
	}
	r.faults = append(r.faults, e.Under(pointer).Violations...)
	return nil
}

// apply writes the document in the unit of work ctx carries.
func (r *importRun) apply(ctx context.Context) (ImportResult, error) {
	d := r.doc
	req := r.cmd.Request
	p, err := r.h.d.Workspace.CreateProject(ctx, d.Project.Key, d.Project.Name, d.Project.Description, req)
	if err != nil {
		return ImportResult{}, err
	}
	r.project = p
	steps := []func(context.Context) error{r.declare, r.createSegments, r.createFlags, r.configureFlags}
	for _, step := range steps {
		if err = step(ctx); err != nil {
			return ImportResult{}, err
		}
	}
	if len(r.faults) > 0 {
		return ImportResult{}, apperrors.Invalid("invalid_document", "the project document is not valid", r.faults...)
	}
	// Every environment now serves what it did not, so each moves on once,
	// whatever number of entities were written.
	for _, e := range d.Environments {
		if err = r.h.d.Rulesets.Environment(ctx, r.environments[e.Key].ID); err != nil {
			return ImportResult{}, err
		}
	}
	err = r.record(ctx)
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
		created, err := w.CreateEnvironment(ctx, project, e.Key, e.Name, e.Protected, i+1, req)
		if ferr := r.fault(err, "/environments/"+strconv.Itoa(i)); ferr != nil {
			return ferr
		}
		if err == nil {
			r.environments[e.Key] = created
			r.changes.Environments.Created = append(r.changes.Environments.Created, e.Key)
		}
	}
	for i, u := range r.doc.UnitTypes {
		_, err := w.CreateUnitType(ctx, project, u.Key, u.Name, u.Description, req)
		if ferr := r.fault(err, "/unit_types/"+strconv.Itoa(i)); ferr != nil {
			return ferr
		}
		if err == nil {
			r.changes.UnitTypes.Created = append(r.changes.UnitTypes.Created, u.Key)
		}
	}
	for i, a := range r.doc.Attributes {
		if _, standard := workspace.StandardAttributeType(a.Key); standard {
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
		err := r.h.d.Flags.CreateSegment(ctx, SegmentImport{
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
		owner, err := r.ownerOf(ctx, f.Owner, at+"/owner")
		if err != nil {
			return err
		}
		err = r.h.d.Flags.CreateFlag(ctx, FlagImport{
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
			if err = r.h.d.Flags.ArchiveFlag(ctx, r.project.ID, f.Key, r.cmd.Request); err != nil {
				return err
			}
		}
	}
	return nil
}

// configureFlags writes each flag's configuration in each environment the
// document configures it in; an environment it does not configure it in
// keeps the initial one.
func (r *importRun) configureFlags(ctx context.Context) error {
	for i, f := range r.doc.Flags {
		if !r.flags[f.Key] {
			continue
		}
		for _, env := range r.doc.Environments {
			c, configured := f.Configurations[env.Key]
			if !configured || r.environments[env.Key].ID.IsZero() {
				continue
			}
			err := r.h.d.Flags.WriteConfiguration(ctx, ConfigurationImport{
				Project: r.project.ID, Flag: f.Key, Environment: env.Key, Input: c.Input(), Caller: r.cmd.Caller, Request: r.cmd.Request,
			})
			if ferr := r.fault(err, transfer.Pointer("flags", i, "configurations", env.Key)); ferr != nil {
				return ferr
			}
		}
	}
	return nil
}

// ownerOf returns the member to own an entity whose owner the document
// names as email. Members do not travel, so the address resolves only to
// someone who may own in the project now; otherwise the importing member
// owns it, and the import says so. A document that names no owner is the
// importer's.
func (r *importRun) ownerOf(ctx context.Context, email, pointer string) (*access.MemberID, error) {
	if email != "" {
		member, ok, err := r.h.d.Owners.OwnerByEmail(ctx, r.project.ID, email)
		if err != nil {
			return nil, err
		}
		if ok {
			return &member, nil
		}
		r.warnings = append(r.warnings, transfer.Warning{
			Code: transfer.WarningOwnerReplaced, Pointer: pointer, Params: map[string]any{"email": email},
			Detail: "the owner " + email + " cannot own in this project, so the importing member owns it",
		})
	}
	return r.cmd.Caller, nil
}

// record writes the entry saying what the import did, after the entries of
// each entity it wrote.
func (r *importRun) record(ctx context.Context) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](r.h.d.IDs), r.h.d.Clock.Now(), r.cmd.Request, governance.Record{
		Project: r.project.ID, Action: "project.imported",
		Entity: governance.Entity{Kind: "project", ID: r.project.ID.String(), Key: r.project.Key},
		After:  map[string]any{"mode": transfer.ModeCreate, "source": transfer.SourceDocument, "counts": r.changes.Counts()},
	})
	if err != nil {
		return err
	}
	return r.h.d.Audit.Record(ctx, entry)
}
