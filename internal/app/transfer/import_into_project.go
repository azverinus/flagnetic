package transfer

import (
	"context"
	"errors"
	"slices"
	"strings"

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

// ImportIntoProjectDeps are what ImportIntoProject runs on.
type ImportIntoProjectDeps struct {
	Tx          Transactor
	Projects    ProjectLocker
	Workspace   Workspace
	Flags       Flags
	Experiments Experiments
	Members     Members
	History     History

	WorkspaceImport   WorkspaceImport
	FlagsImport       FlagsImport
	ExperimentsImport ExperimentsImport
	SDKKeys           SDKKeys
	Owners            Owners
	Rulesets          Rulesets
	Audit             AuditLog
	Clock             clock.Clock
	IDs               *id.Source
}

// ImportIntoProject applies a project document to a project that exists.
type ImportIntoProject struct{ d ImportIntoProjectDeps }

// NewImportIntoProject returns the handler.
func NewImportIntoProject(d ImportIntoProjectDeps) *ImportIntoProject {
	return &ImportIntoProject{d: d}
}

// ImportIntoProjectCommand is the document to merge into Project. IfMatch,
// when given, is the entity tag of the project's state the import is based
// on, as an answer to a dry run gave it. Caller is the member importing.
type ImportIntoProjectCommand struct {
	Project workspace.ProjectID
	// Mode is merge, or replace, which also deletes what the document does not
	// name; the zero value is merge.
	Mode     transfer.Mode
	Document transfer.Document
	DryRun   bool
	IfMatch  *string
	Caller   *access.MemberID
	Request  governance.Request
}

// errVersionMismatch refuses an import based on a state the project has left.
var errVersionMismatch = apperrors.PreconditionFailed("version_mismatch",
	"the project changed since the state this import was based on; run the dry run again")

// entityTag is the tag a project's state is known by: its newest audit entry,
// which any change to the project moves on; "" for a project with none.
func entityTag(latest governance.EntryID) string {
	if latest.IsZero() {
		return ""
	}
	return latest.String()
}

// Handle merges the document into the project, whole or not at all, in one
// unit of work that holds the project's row from before the project is read:
// what the document names is created or updated, written through the context
// that owns it as an ordinary write is, each entity that differs recording
// its own audit entry, and the rest is left as it is. An import that changes
// nothing writes nothing, records nothing and moves no ruleset on; one that
// changes something records one entry for the import and moves each changed
// environment's ruleset on once. The unit of work bypasses protection in
// every environment. With a dry run the whole unit of work runs, so every
// check the database makes is made, and is then rolled back.
//
// IfMatch is judged under the project's row before anything is written. A
// document that breaks a rule is refused whole, with every fault found.
func (h *ImportIntoProject) Handle(ctx context.Context, cmd ImportIntoProjectCommand) (ImportResult, error) {
	var res ImportResult
	err := h.d.Tx.Within(protection.BypassEverywhere(ctx), func(ctx context.Context) error {
		project, found, err := h.d.Projects.LockProject(ctx, cmd.Project)
		if err != nil {
			return err
		}
		if !found {
			return errProjectNotFound
		}
		latest, err := h.d.History.LatestProjectEntry(ctx, project.ID)
		if err != nil {
			return err
		}
		if cmd.IfMatch != nil && *cmd.IfMatch != entityTag(latest) {
			return errVersionMismatch
		}
		snapshot, err := h.read(ctx, project)
		if err != nil {
			return err
		}
		imp := &mergeRun{
			h: h, cmd: cmd, doc: cmd.Document, snapshot: snapshot,
			run: run{owners: h.d.Owners, audit: h.d.Audit, clock: h.d.Clock, ids: h.d.IDs, req: cmd.Request, caller: cmd.Caller, project: project},
		}
		res, err = imp.apply(ctx)
		res.Latest = latest
		if err != nil {
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

// read returns what the project holds that a merge compares the document
// with, in the unit of work ctx carries.
func (h *ImportIntoProject) read(ctx context.Context, project workspace.Project) (transfer.Snapshot, error) {
	s := transfer.Snapshot{Project: project}
	reads := []func() (err error){
		func() (err error) { s.Environments, err = h.d.Workspace.Environments(ctx, project.ID); return },
		func() (err error) { s.UnitTypes, err = h.d.Workspace.UnitTypes(ctx, project.ID); return },
		func() (err error) { s.Attributes, err = h.d.Workspace.Attributes(ctx, project.ID); return },
		func() (err error) { s.Segments, err = h.d.Flags.Segments(ctx, project.ID); return },
		func() (err error) { s.Flags, err = h.d.Flags.ProjectFlags(ctx, project.ID); return },
		func() (err error) { s.Configurations, err = h.d.Flags.ProjectConfigurations(ctx, project.ID); return },
		func() (err error) { s.Experiments, err = h.d.Experiments.ProjectExperiments(ctx, project.ID); return },
	}
	for _, read := range reads {
		if err := read(); err != nil {
			return s, err
		}
	}
	owners := make([]access.MemberID, 0, len(s.Flags)+len(s.Experiments))
	for _, f := range s.Flags {
		owners = append(owners, f.Owner)
	}
	for _, e := range s.Experiments {
		owners = append(owners, e.Owner)
	}
	var err error
	s.Emails, err = h.d.Members.MemberEmails(ctx, owners)
	return s, err
}

// mergeRun is one import of a document into a project.
type mergeRun struct {
	run
	h        *ImportIntoProject
	cmd      ImportIntoProjectCommand
	doc      transfer.Document
	snapshot transfer.Snapshot

	// current is the project's document, eff the document as it applies to
	// it, and plan what differs between the two.
	current, eff transfer.Document
	plan         transfer.Plan

	// environments are the project's environments, and the ones created, by
	// key.
	environments map[string]workspace.Environment
	// owners are the members to own the flags the document gives a new owner,
	// by flag key, and experimentOwners the same for experiments.
	owners, experimentOwners map[string]*access.MemberID
	// known are the flags that exist once the flags are written, by key,
	// and touched the ones the import changed.
	known, touched map[string]bool
	// wrote is whether the import changed anything.
	wrote bool
	// moved are the environments whose ruleset moves on, and allMoved is
	// whether every one does.
	moved    map[string]bool
	allMoved bool
}

// apply writes the document in the unit of work ctx carries.
func (r *mergeRun) apply(ctx context.Context) (ImportResult, error) {
	r.current = transfer.FromSnapshot(r.snapshot)
	check := transfer.CheckMerge
	if r.mode() == transfer.ModeReplace {
		check = transfer.CheckReplace
	}
	if err := r.refuse(check(r.doc, r.current)); err != nil {
		return ImportResult{}, err
	}
	r.eff = transfer.Effective(r.current, r.doc)
	if err := r.resolve(ctx); err != nil {
		return ImportResult{}, err
	}
	r.plan = transfer.Compare(r.current, r.eff)
	r.environments = make(map[string]workspace.Environment, len(r.snapshot.Environments))
	for _, e := range r.snapshot.Environments {
		r.environments[e.Key] = e
	}
	r.known, r.touched, r.moved = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range r.snapshot.Flags {
		r.known[f.Key] = true
	}
	out := ImportResult{
		Project: transfer.Project{Key: r.project.Key, Name: r.project.Name, Description: r.project.Description},
		Mode:    r.mode(), Source: transfer.SourceDocument,
	}
	steps := []func(context.Context) error{
		r.updateProject, r.writeEnvironments, r.writeUnitTypes, r.writeAttributes, r.writeSegments, r.writeFlags,
		r.writeExperiments, r.configureFlags, r.archiveFlags,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return out, err
		}
	}
	if len(r.faults) > 0 {
		return out, r.invalid()
	}
	if r.mode() == transfer.ModeReplace {
		if err := r.deleteAbsent(ctx); err != nil {
			return out, err
		}
		if len(r.plan.Experiments.Created)+len(r.plan.Experiments.Updated) > 0 {
			if err := r.h.d.ExperimentsImport.CheckSalts(ctx); err != nil {
				return out, err
			}
		}
	}
	out.Project = transfer.Project{Key: r.project.Key, Name: r.project.Name, Description: r.project.Description}
	r.listFlagUpdates()
	out.Changes, out.Warnings = r.changes, r.warnings
	if !r.wrote {
		return out, nil
	}
	if err := r.moveRulesets(ctx); err != nil {
		return out, err
	}
	return out, r.record(ctx, r.mode())
}

// mode is how the document is applied.
func (r *mergeRun) mode() transfer.Mode {
	if r.cmd.Mode == transfer.ModeReplace {
		return transfer.ModeReplace
	}
	return transfer.ModeMerge
}

// resolve settles what the document says that the project overrules: an
// existing environment keeps its protection, a difference warned, and an
// owner who cannot own is not one, for an existing flag as for a new one.
func (r *mergeRun) resolve(ctx context.Context) error {
	r.owners, r.experimentOwners = map[string]*access.MemberID{}, map[string]*access.MemberID{}
	have := make(map[string]transfer.Environment, len(r.current.Environments))
	for _, e := range r.current.Environments {
		have[e.Key] = e
	}
	if r.eff.Environments != nil {
		r.eff.Environments = slices.Clone(r.eff.Environments)
		for i, e := range r.eff.Environments {
			was, ok := have[e.Key]
			if !ok || was.Protected == e.Protected {
				continue
			}
			r.warnings = append(r.warnings, transfer.Warning{
				Code: transfer.WarningProtectionKept, Pointer: transfer.Pointer("environments", i, "protected"),
				Params: map[string]any{"environment": e.Key, "protected": was.Protected},
				Detail: "the environment " + e.Key + " stays " + protectedWord(was.Protected) +
					": an import never changes whether an existing environment is protected",
			})
			r.eff.Environments[i].Protected = was.Protected
		}
	}
	existing := make(map[string]transfer.Flag, len(r.current.Flags))
	for _, f := range r.current.Flags {
		existing[f.Key] = f
	}
	if r.eff.Flags != nil {
		r.eff.Flags = slices.Clone(r.eff.Flags)
	}
	for i, f := range r.eff.Flags {
		at := transfer.Pointer("flags", i, "owner")
		was, ok := existing[f.Key]
		if !ok {
			owner, err := r.ownerOf(ctx, f.Owner, at)
			if err != nil {
				return err
			}
			r.owners[f.Key] = owner
			continue
		}
		if f.Owner == "" || strings.EqualFold(f.Owner, was.Owner) {
			r.eff.Flags[i].Owner = was.Owner
			continue
		}
		member, ok, err := r.lookupOwner(ctx, f.Owner)
		if err != nil {
			return err
		}
		if !ok {
			r.warnOwnerReplaced(f.Owner, at, "the flag keeps its owner")
			r.eff.Flags[i].Owner = was.Owner
			continue
		}
		r.owners[f.Key] = &member
	}
	return r.resolveExperimentOwners(ctx)
}

// resolveExperimentOwners settles the owners of the experiments as it settles
// the flags': a new experiment is owned by whoever the document's email
// resolves to, else by the importer, and an existing one keeps its owner
// unless the email resolves to someone who may own it.
func (r *mergeRun) resolveExperimentOwners(ctx context.Context) error {
	existing := make(map[string]transfer.Experiment, len(r.current.Experiments))
	for _, e := range r.current.Experiments {
		existing[e.Key] = e
	}
	if r.eff.Experiments != nil {
		r.eff.Experiments = slices.Clone(r.eff.Experiments)
	}
	for i, e := range r.eff.Experiments {
		at := transfer.Pointer("experiments", i, "owner")
		was, ok := existing[e.Key]
		if !ok {
			owner, err := r.ownerOf(ctx, e.Owner, at)
			if err != nil {
				return err
			}
			r.experimentOwners[e.Key] = owner
			continue
		}
		if e.Owner == "" || strings.EqualFold(e.Owner, was.Owner) {
			r.eff.Experiments[i].Owner = was.Owner
			continue
		}
		member, ok, err := r.lookupOwner(ctx, e.Owner)
		if err != nil {
			return err
		}
		if !ok {
			r.warnOwnerReplaced(e.Owner, at, "the experiment keeps its owner")
			r.eff.Experiments[i].Owner = was.Owner
			continue
		}
		r.experimentOwners[e.Key] = &member
	}
	return nil
}

// lookupOwner returns the member who has the address and may own in the project.
func (r *mergeRun) lookupOwner(ctx context.Context, email string) (access.MemberID, bool, error) {
	return r.h.d.Owners.OwnerByEmail(ctx, r.project.ID, email)
}

func protectedWord(protected bool) string {
	if protected {
		return "protected"
	}
	return "unprotected"
}

// updateProject writes the project's name and description the document
// holds.
func (r *mergeRun) updateProject(ctx context.Context) error {
	p := r.doc.Project
	if p == nil || r.isRefused("/project") || (p.Name == r.project.Name && p.Description == r.project.Description) {
		return nil
	}
	after, changed, err := r.h.d.WorkspaceImport.UpdateProject(ctx, r.project.ID, p.Name, p.Description, r.req)
	if ferr := r.fault(err, "/project"); ferr != nil || err != nil {
		return ferr
	}
	if changed {
		r.project, r.wrote = after, true
	}
	return nil
}

// writeEnvironments creates the environments the project lacks, after its
// others in the order of the document, and renames the ones that differ.
func (r *mergeRun) writeEnvironments(ctx context.Context) error {
	w := r.h.d.WorkspaceImport
	next := 0
	for _, e := range r.snapshot.Environments {
		next = max(next, e.SortOrder)
	}
	for _, c := range r.plan.Environments.Created {
		at := transfer.Pointer("environments", c.Index)
		if r.isRefused(at) {
			continue
		}
		e := r.eff.Environments[c.Index]
		next++
		created, err := w.CreateEnvironment(ctx, r.project.ID, e.Key, e.Name, e.Protected, next, r.caller, r.req)
		if ferr := r.fault(err, at); ferr != nil || err != nil {
			if ferr != nil {
				return ferr
			}
			continue
		}
		r.environments[e.Key] = created
		r.changes.Environments.Created = append(r.changes.Environments.Created, e.Key)
		r.wrote, r.moved[e.Key] = true, true
	}
	for _, u := range r.plan.Environments.Updated {
		at := transfer.Pointer("environments", u.Index)
		if r.isRefused(at) {
			continue
		}
		changed, err := w.UpdateEnvironment(ctx, r.project.ID, u.Key, r.eff.Environments[u.Index].Name, r.req)
		if ferr := r.fault(err, at); ferr != nil || err != nil {
			if ferr != nil {
				return ferr
			}
			continue
		}
		if changed {
			r.changes.Environments.Updated = append(r.changes.Environments.Updated, transfer.Update{Key: u.Key, Patch: u.Patch})
			r.wrote = true
		}
	}
	return nil
}

// writeUnitTypes creates and renames unit types.
func (r *mergeRun) writeUnitTypes(ctx context.Context) error {
	w := r.h.d.WorkspaceImport
	for _, c := range r.plan.UnitTypes.Created {
		at := transfer.Pointer("unit_types", c.Index)
		u := r.eff.UnitTypes[c.Index]
		if r.isRefused(at) {
			continue
		}
		_, err := w.CreateUnitType(ctx, r.project.ID, u.Key, u.Name, u.Description, r.req)
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err == nil {
			r.changes.UnitTypes.Created = append(r.changes.UnitTypes.Created, u.Key)
			r.wrote = true
		}
	}
	for _, c := range r.plan.UnitTypes.Updated {
		at := transfer.Pointer("unit_types", c.Index)
		u := r.eff.UnitTypes[c.Index]
		if r.isRefused(at) {
			continue
		}
		changed, err := w.UpdateUnitType(ctx, r.project.ID, u.Key, u.Name, u.Description, r.req)
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err == nil && changed {
			r.changes.UnitTypes.Updated = append(r.changes.UnitTypes.Updated, transfer.Update{Key: u.Key, Patch: c.Patch})
			r.wrote = true
		}
	}
	return nil
}

// writeAttributes declares and renames attributes; the standard ones are
// never listed in a plan.
func (r *mergeRun) writeAttributes(ctx context.Context) error {
	w := r.h.d.WorkspaceImport
	for _, c := range r.plan.Attributes.Created {
		at := transfer.Pointer("attributes", c.Index)
		a := r.eff.Attributes[c.Index]
		if r.isRefused(at) {
			continue
		}
		_, err := w.CreateAttribute(ctx, r.project.ID, a.Key, a.Name, a.Description, workspace.AttributeType(a.Type), r.req)
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err == nil {
			r.changes.Attributes.Created = append(r.changes.Attributes.Created, a.Key)
			r.wrote = true
		}
	}
	for _, c := range r.plan.Attributes.Updated {
		at := transfer.Pointer("attributes", c.Index)
		a := r.eff.Attributes[c.Index]
		if r.isRefused(at) {
			continue
		}
		changed, err := w.UpdateAttribute(ctx, r.project.ID, a.Key, a.Name, a.Description, r.req)
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err == nil && changed {
			r.changes.Attributes.Updated = append(r.changes.Attributes.Updated, transfer.Update{Key: a.Key, Patch: c.Patch})
			r.wrote = true
		}
	}
	return nil
}

// writeSegments creates and changes segments.
func (r *mergeRun) writeSegments(ctx context.Context) error {
	f := r.h.d.FlagsImport
	for _, c := range r.plan.Segments.Created {
		at := transfer.Pointer("segments", c.Index)
		s := r.eff.Segments[c.Index]
		if r.isRefused(at) {
			continue
		}
		effect, err := f.CreateSegment(ctx, SegmentImport{
			Project: r.project.ID, Key: s.Key, Name: s.Name, Description: s.Description, Private: s.Private,
			Conditions: transfer.FlagConditions(s.Conditions), Request: r.req,
		})
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err == nil {
			r.changes.Segments.Created = append(r.changes.Segments.Created, s.Key)
			r.took(effect)
		}
	}
	for _, c := range r.plan.Segments.Updated {
		at := transfer.Pointer("segments", c.Index)
		s := r.eff.Segments[c.Index]
		if r.isRefused(at) {
			continue
		}
		effect, err := f.UpdateSegment(ctx, SegmentUpdate{
			Project: r.project.ID, Key: s.Key, Name: s.Name, Description: s.Description, Private: s.Private,
			Conditions: orNone(transfer.FlagConditions(s.Conditions)), Request: r.req,
		})
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err == nil && effect.Changed {
			r.changes.Segments.Updated = append(r.changes.Segments.Updated, transfer.Update{Key: s.Key, Patch: c.Patch})
			r.took(effect)
		}
	}
	return nil
}

func orNone[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// took notes what a write of an import port did: that the import changed
// something, and which rulesets it moves on, for a write that is not a
// configuration's.
func (r *mergeRun) took(e Effect) {
	if !e.Changed {
		return
	}
	r.wrote = true
	if e.Rulesets {
		r.allMoved = true
	}
}

// writeFlags creates the flags the project lacks, and changes the
// definition of the ones that differ. An archived flag the document has
// restored is restored here, before its configurations are written; the
// archiving is the last step, once what requires a flag has stopped to.
func (r *mergeRun) writeFlags(ctx context.Context) error {
	f := r.h.d.FlagsImport
	for _, c := range r.plan.Flags.Created {
		at := transfer.Pointer("flags", c.Index)
		fl := r.eff.Flags[c.Index]
		if r.isRefused(at + "/key") {
			// Another flag of the document has the key. A fault the check
			// found in a configuration skips that configuration alone.
			continue
		}
		effect, err := f.CreateFlag(ctx, FlagImport{
			Project: r.project.ID, Key: fl.Key, Type: flags.Type(fl.Type), Name: fl.Name, Description: fl.Description,
			Owner: r.owners[fl.Key], Caller: r.caller, Tags: fl.Tags, Lifetime: flags.Lifetime(fl.Lifetime),
			ExpectedRemovalAt: fl.ExpectedRemovalAt, AutomaticExposure: fl.AutomaticExposure, Variations: fl.VariationInputs(),
			Request: r.req,
		})
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err != nil {
			continue
		}
		r.known[fl.Key] = true
		r.changes.Flags.Created = append(r.changes.Flags.Created, fl.Key)
		r.took(effect)
	}
	archived := map[string]bool{}
	for _, fl := range r.current.Flags {
		archived[fl.Key] = fl.Archived
	}
	for _, c := range r.plan.Flags.Updated {
		at := transfer.Pointer("flags", c.Index)
		fl := r.eff.Flags[c.Index]
		if r.isRefused(at) {
			continue
		}
		if archived[fl.Key] && !fl.Archived {
			effect, err := f.RestoreFlag(ctx, r.project.ID, fl.Key, r.req)
			if ferr := r.fault(err, at); ferr != nil {
				return ferr
			}
			if err != nil {
				continue
			}
			r.tookFlag(fl.Key, effect)
		}
		effect, err := f.UpdateFlag(ctx, FlagUpdate{
			Project: r.project.ID, Key: fl.Key, Name: fl.Name, Description: fl.Description, Owner: r.owners[fl.Key],
			Tags: orNone(fl.Tags), Lifetime: flags.Lifetime(fl.Lifetime), ExpectedRemovalAt: fl.ExpectedRemovalAt,
			AutomaticExposure: fl.AutomaticExposure, Variations: fl.VariationInputs(), Request: r.req,
		})
		if ferr := r.fault(err, at); ferr != nil {
			return ferr
		}
		if err == nil {
			r.tookFlag(fl.Key, effect)
		}
	}
	return nil
}

// tookFlag is took for a write to the flag keyed key, which then counts as
// updated.
func (r *mergeRun) tookFlag(key string, e Effect) {
	r.took(e)
	if e.Changed {
		r.touched[key] = true
	}
}

// writeExperiments creates the experiments the project lacks exactly as the
// document has them and changes the ones that differ as far as an edit of
// their status allows, before the configurations are written, since a rule
// serves an experiment only once it exists. An experiment whose flag the
// document names but the import refused is skipped, its fault being the
// flag's. The traffic allocation of a running experiment grown moves on the
// ruleset of the environments holding its rules.
func (r *mergeRun) writeExperiments(ctx context.Context) error {
	e := r.h.d.ExperimentsImport
	named := map[string]bool{}
	for _, f := range r.doc.Flags {
		named[f.Key] = true
	}
	avoid := saltsOf(r.eff)
	// write sends the experiment at index to the port, and reports whether it
	// was written. A refusal the document is to be told of is noted, and is not
	// an error.
	write := func(index int, create bool) (Effect, bool, error) {
		at := transfer.Pointer("experiments", index)
		ex := r.eff.Experiments[index]
		if r.isRefused(at) || (named[ex.Flag] && !r.known[ex.Flag]) {
			return Effect{}, false, nil
		}
		in := experimentImport(r.project.ID, ex, r.experimentOwners[ex.Key], r.caller, avoid, r.req)
		write := e.UpdateExperiment
		if create {
			write = e.CreateExperiment
		}
		effect, err := write(ctx, in)
		if ferr := r.fault(err, at); ferr != nil {
			return effect, false, ferr
		}
		return effect, err == nil, nil
	}
	for _, c := range r.plan.Experiments.Created {
		_, ok, err := write(c.Index, true)
		if err != nil {
			return err
		}
		if ok {
			r.changes.Experiments.Created = append(r.changes.Experiments.Created, c.Key)
			r.wrote = true
		}
	}
	for _, c := range r.plan.Experiments.Updated {
		effect, ok, err := write(c.Index, false)
		if err != nil {
			return err
		}
		if ok && effect.Changed {
			r.changes.Experiments.Updated = append(r.changes.Experiments.Updated, transfer.Update{Key: c.Key, Patch: c.Patch})
			r.wrote = true
			r.moveEnvironments(effect.Environments)
		}
	}
	if len(r.plan.Experiments.Created)+len(r.plan.Experiments.Updated) == 0 || r.mode() == transfer.ModeReplace {
		// A replace checks the salts once it has deleted what the document
		// lacks, since an experiment of the document may take the salt of a
		// draft that goes.
		return nil
	}
	return e.CheckSalts(ctx)
}

// moveEnvironments notes that the ruleset of each of the environments moves
// on.
func (r *mergeRun) moveEnvironments(ids []workspace.EnvironmentID) {
	for _, id := range ids {
		for key, env := range r.environments {
			if env.ID == id {
				r.moved[key] = true
			}
		}
	}
}

// configureFlags writes the configurations the document gives, of a flag it
// creates in every environment it names and of one it changes in the
// environments where the project's differs.
func (r *mergeRun) configureFlags(ctx context.Context) error {
	created := map[string]bool{}
	for _, c := range r.plan.Flags.Created {
		created[c.Key] = true
	}
	updated := map[string][]transfer.PatchOperation{}
	for _, c := range r.plan.Flags.Updated {
		updated[c.Key] = c.Patch
	}
	environments := map[string]int{}
	for i, e := range r.doc.Environments {
		environments[e.Key] = i
	}
	for i, fl := range r.eff.Flags {
		if !r.known[fl.Key] {
			continue
		}
		patch, isUpdated := updated[fl.Key]
		if !created[fl.Key] && !isUpdated {
			continue
		}
		for _, env := range slices.Sorted(mapKeysOf(r.doc.Flags[i].Configurations)) {
			at := transfer.Pointer("flags", i, "configurations", env)
			if !created[fl.Key] && !touches(patch, transfer.Pointer("configurations", env)) {
				continue
			}
			if j, named := environments[env]; named && r.isRefused(transfer.Pointer("environments", j)) {
				continue
			}
			target, ok := r.environments[env]
			if !ok || r.isRefused(at) {
				continue
			}
			effect, err := r.h.d.FlagsImport.WriteConfiguration(ctx, ConfigurationImport{
				Project: r.project.ID, Flag: fl.Key, Environment: env, Input: fl.Configurations[env].Input(), Caller: r.caller,
				Request: r.req,
			})
			if ferr := r.fault(err, at); ferr != nil {
				return ferr
			}
			if err != nil || !effect.Changed {
				continue
			}
			r.wrote, r.moved[target.Key] = true, r.moved[target.Key] || effect.Rulesets
			if !created[fl.Key] {
				r.touched[fl.Key] = true
			}
		}
	}
	return nil
}

// touches reports whether patch changes what is at pointer or under it.
func touches(patch []transfer.PatchOperation, pointer string) bool {
	return slices.ContainsFunc(patch, func(op transfer.PatchOperation) bool {
		return op.Path == pointer || strings.HasPrefix(op.Path, pointer+"/")
	})
}

func mapKeysOf[V any](m map[string]V) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// archiveFlags archives the flags the document archives, which are not
// archived yet.
func (r *mergeRun) archiveFlags(ctx context.Context) error {
	archived := map[string]bool{}
	for _, fl := range r.current.Flags {
		archived[fl.Key] = fl.Archived
	}
	created := map[string]bool{}
	for _, c := range r.plan.Flags.Created {
		created[c.Key] = true
	}
	for i, fl := range r.eff.Flags {
		if !fl.Archived || archived[fl.Key] || !r.known[fl.Key] || r.isRefused(transfer.Pointer("flags", i)) {
			continue
		}
		effect, err := r.h.d.FlagsImport.ArchiveFlag(ctx, r.project.ID, fl.Key, r.req)
		if ferr := r.fault(err, transfer.Pointer("flags", i)); ferr != nil {
			return ferr
		}
		if err != nil {
			continue
		}
		if created[fl.Key] {
			r.took(effect)
			continue
		}
		r.tookFlag(fl.Key, effect)
	}
	return nil
}

// listFlagUpdates lists the flags the import changed, with the patch each
// plan holds.
func (r *mergeRun) listFlagUpdates() {
	for _, c := range r.plan.Flags.Updated {
		if r.touched[c.Key] {
			r.changes.Flags.Updated = append(r.changes.Flags.Updated, transfer.Update{Key: c.Key, Patch: c.Patch})
		}
	}
}

// moveRulesets moves the ruleset of every environment the import changed
// on, once: every environment's when a segment or a flag's definition
// changed, else the ones whose configurations did and the new ones.
func (r *mergeRun) moveRulesets(ctx context.Context) error {
	keys := make([]string, 0, len(r.environments))
	for k := range r.environments {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !r.allMoved && !r.moved[k] {
			continue
		}
		if err := r.h.d.Rulesets.Environment(ctx, r.environments[k].ID); err != nil {
			return err
		}
	}
	return nil
}
