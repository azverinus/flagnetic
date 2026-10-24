package governance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// Transactor runs a unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// ProjectLocker locks a project's row for the unit of work. Every change
// request transition takes this lock first and no other, the lock every
// write of a project takes, so a change request is judged against the
// entity it proposes to change as it is when the transition commits.
type ProjectLocker interface {
	// LockProject locks the project's row, or answers ErrNotFound when
	// there is none.
	LockProject(ctx context.Context, project workspace.ProjectID) error
}

// ChangeRequestStore stores change requests.
type ChangeRequestStore interface {
	ChangeRequestReader
	CreateChangeRequest(ctx context.Context, cr governance.ChangeRequest) error
	// UpdateChangeRequest stores cr in place of the change request with
	// its identifier.
	UpdateChangeRequest(ctx context.Context, cr governance.ChangeRequest) error
}

// ChangeRequestReader reads change requests, with the keys of the entities
// they name and their authors' names as they are now.
type ChangeRequestReader interface {
	// ChangeRequest returns project's change request with changeRequest,
	// or ErrNotFound.
	ChangeRequest(ctx context.Context, project workspace.ProjectID, changeRequest governance.ChangeRequestID) (governance.ChangeRequest, error)
	// ChangeRequests returns at most limit change requests matching f,
	// older than after (unless zero), newest first.
	ChangeRequests(ctx context.Context, f ChangeRequestFilter, after governance.ChangeRequestID, limit int) ([]governance.ChangeRequest, error)
}

// Configurations is the flags context's view of a flag's configuration in
// one environment, as a change request proposes it.
type Configurations interface {
	// Propose returns project's flag keyed flag, its saved configuration in
	// the environment keyed environment, and what replacing it with in
	// would leave, checked as that write would be, with the same refusals,
	// once the rule identifiers and salts the saved configuration does not
	// hold are left out of in.
	Propose(ctx context.Context, project workspace.ProjectID, flag, environment string, in flags.ConfigurationInput,
	) (flags.Flag, flags.EnvironmentConfiguration, flags.Configuration, error)
	// Current returns project's flag keyed flag and its saved
	// configuration in the environment keyed environment, or not_found for
	// either.
	Current(ctx context.Context, project workspace.ProjectID, flag, environment string) (flags.Flag, flags.EnvironmentConfiguration, error)
}

// ChangeRequestDeps are what a change request's command handlers run on.
type ChangeRequestDeps struct {
	Tx             Transactor
	Projects       ProjectLocker
	Store          ChangeRequestStore
	Configurations Configurations
	Audit          AuditLog
	Clock          clock.Clock
	IDs            *id.Source
}

// now returns the instant a change is made at, to the microsecond in UTC as
// the database keeps it, so what a write answers is what a later read does.
func (d ChangeRequestDeps) now() time.Time { return d.Clock.Now().UTC().Truncate(time.Microsecond) }

// record writes the audit entry of action on cr, made at now in req, with cr
// before and after it.
func (d ChangeRequestDeps) record(ctx context.Context, now time.Time, req governance.Request, action string,
	before *governance.ChangeRequest, after governance.ChangeRequest, extra map[string]any,
) error {
	r := governance.Record{
		Project: after.Project, Action: action,
		Entity: governance.Entity{Kind: "change_request", ID: after.ID.String(), Key: after.Title},
	}
	if after.Environment != nil {
		r.Environment = after.Environment.ID
	}
	if before != nil {
		r.Before = changeRequestValues(*before)
	}
	values := changeRequestValues(after)
	for k, v := range extra {
		values[k] = v
	}
	r.After = values
	entry, err := governance.NewEntry(id.New[governance.Entry](d.IDs), now, req, r)
	if err != nil {
		return err
	}
	return d.Audit.Record(ctx, entry)
}

// lockProject takes project's row lock. The request found the project
// before the handler ran, so a project missing now was deleted in between,
// and is answered as the unknown project a moment later would have been.
func (d ChangeRequestDeps) lockProject(ctx context.Context, project workspace.ProjectID) error {
	err := d.Projects.LockProject(ctx, project)
	if errors.Is(err, ErrNotFound) {
		return errProjectNotFound
	}
	return err
}

var errProjectNotFound = apperrors.NotFound("not_found", "no project has this key")

// errChangeRequestNotFound answers a change request the project does not
// have.
var errChangeRequestNotFound = apperrors.NotFound("not_found", "the project has no change request with this identifier")

// changeRequestOf returns project's change request with changeRequest from
// store, or not_found.
func changeRequestOf(ctx context.Context, store ChangeRequestReader, project workspace.ProjectID,
	changeRequest governance.ChangeRequestID,
) (governance.ChangeRequest, error) {
	cr, err := store.ChangeRequest(ctx, project, changeRequest)
	if errors.Is(err, ErrNotFound) {
		return cr, errChangeRequestNotFound
	}
	return cr, err
}

// ChangeRequestCommand names a change request, the version of it a
// transition is based on, and who takes it.
type ChangeRequestCommand struct {
	Project       workspace.ProjectID
	ChangeRequest governance.ChangeRequestID
	Expected      precondition.Version
	By            governance.Taker
	Request       governance.Request
}

// transition runs one transition of the change request cmd names: under the
// project's lock it reads it, has change decide what it becomes, judges the
// version the transition was based on, and stores and records the change
// request as action when it changed.
func transition(ctx context.Context, d ChangeRequestDeps, cmd ChangeRequestCommand, action string,
	change func(ctx context.Context, cr governance.ChangeRequest, now time.Time) (governance.ChangeRequest, bool, error),
) (governance.ChangeRequest, error) {
	now := d.now()
	var out governance.ChangeRequest
	err := d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := d.lockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := changeRequestOf(ctx, d.Store, cmd.Project, cmd.ChangeRequest)
		if err != nil {
			return err
		}
		after, changed, err := change(ctx, before, now)
		if err != nil {
			return err
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		out = after
		if !changed {
			return nil
		}
		if err = d.Store.UpdateChangeRequest(ctx, after); err != nil {
			return err
		}
		if err = d.record(ctx, now, cmd.Request, action, &before, after, nil); err != nil {
			return err
		}
		out, err = changeRequestOf(ctx, d.Store, cmd.Project, cmd.ChangeRequest)
		return err
	})
	return out, err
}

// subjectOf returns the subject of a configuration change request: the flag
// and environment proposed for, and the configuration it is based on.
func subjectOf(f flags.Flag, saved flags.EnvironmentConfiguration) (governance.Subject, error) {
	base, err := configurationDocument(saved.Configuration)
	if err != nil {
		return governance.Subject{}, err
	}
	return governance.Subject{
		Kind: governance.KindFlagConfiguration, Flag: &governance.FlagRef{ID: f.ID, Key: f.Key},
		Environment: &governance.EnvironmentRef{ID: saved.Configuration.Environment, Key: saved.Environment},
		Base:        base, BaseVersion: saved.Configuration.Version,
	}, nil
}

// changeRequestValues are what an audit entry records of a change request:
// everything a read answers but the author's name, which is read from the
// actor.
func changeRequestValues(cr governance.ChangeRequest) map[string]any {
	author := map[string]any{"kind": "member"}
	if cr.Author.Token != nil {
		author["kind"], author["id"] = "token", cr.Author.Token.String()
	} else if cr.Author.Member != nil {
		author["id"] = cr.Author.Member.String()
	}
	diff := make([]map[string]any, len(cr.Diff))
	for i, op := range cr.Diff {
		diff[i] = PatchOperationValues(op)
	}
	out := map[string]any{
		"kind": cr.Kind, "state": cr.State, "title": cr.Title, "description": cr.Description,
		"base_version": cr.BaseVersion, "proposed": cr.Proposed, "diff": diff, "author": author, "version": cr.Version,
	}
	if cr.Flag != nil {
		out["flag"] = cr.Flag.Key
	}
	if cr.Environment != nil {
		out["environment"] = cr.Environment.Key
	}
	return out
}

// PatchOperationValues returns op as RFC 6902 writes it: with a value for an
// add or a replace, null included, and none for a remove.
func PatchOperationValues(op governance.PatchOperation) map[string]any {
	out := map[string]any{"op": op.Op, "path": op.Path}
	if op.Op != "remove" {
		out["value"] = op.Value
	}
	return out
}

// configurationDoc is the document a configuration change request bases
// itself on and proposes: what the configuration serves and what stays on
// the server with it, without who wrote it or when, which a proposal does
// not decide. It is the shape the API answers `proposed` in, so the paths of
// the diff name the fields a client reads.
type configurationDoc struct {
	On            bool              `json:"on"`
	OffVariation  string            `json:"off_variation"`
	Fallthrough   string            `json:"fallthrough"`
	Prerequisites []prerequisiteDoc `json:"prerequisites"`
	Overrides     []overrideDoc     `json:"overrides"`
	Rules         []ruleDoc         `json:"rules"`
}

type prerequisiteDoc struct {
	Flag      string `json:"flag"`
	Variation string `json:"variation"`
}

type overrideDoc struct {
	UnitType  string  `json:"unit_type"`
	UnitID    string  `json:"unit_id"`
	Variation string  `json:"variation"`
	Note      *string `json:"note"`
}

type ruleDoc struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Conditions  []conditionDoc `json:"conditions"`
	Serve       serveDoc       `json:"serve"`
}

type conditionDoc struct {
	Kind      string `json:"kind"`
	Attribute string `json:"attribute,omitempty"`
	Op        string `json:"op,omitempty"`
	Value     any    `json:"value,omitempty"`
	Segment   string `json:"segment,omitempty"`
	Flag      string `json:"flag,omitempty"`
	Variation string `json:"variation,omitempty"`
}

type serveDoc struct {
	Kind       string `json:"kind"`
	Variation  string `json:"variation,omitempty"`
	UnitType   string `json:"unit_type,omitempty"`
	Percentage *int   `json:"percentage,omitempty"`
	Salt       string `json:"salt,omitempty"`
}

// configurationDocument returns c as the document a change request holds,
// decoded into an any as JSON values are, so documents compare and diff
// alike however they were made.
func configurationDocument(c flags.Configuration) (governance.Document, error) {
	d := configurationDoc{
		On: c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough,
		Prerequisites: make([]prerequisiteDoc, len(c.Prerequisites)), Overrides: make([]overrideDoc, len(c.Overrides)),
		Rules: make([]ruleDoc, len(c.Rules)),
	}
	for i, p := range c.Prerequisites {
		d.Prerequisites[i] = prerequisiteDoc{Flag: p.Flag, Variation: p.Variation}
	}
	for i, o := range c.Overrides {
		d.Overrides[i] = overrideDoc{UnitType: o.UnitType, UnitID: o.UnitID, Variation: o.Variation}
		if o.Note != "" {
			note := o.Note
			d.Overrides[i].Note = &note
		}
	}
	for i, r := range c.Rules {
		rd := ruleDoc{
			ID: r.ID.String(), Description: r.Description, Conditions: make([]conditionDoc, len(r.Conditions)),
			Serve: serveDoc{Kind: r.Serve.Kind, Variation: r.Serve.Variation},
		}
		for j, cond := range r.Conditions {
			rd.Conditions[j] = conditionDoc(cond)
		}
		if r.Serve.Kind == flags.ServeRollout {
			percentage := r.Serve.Percentage
			rd.Serve.UnitType, rd.Serve.Percentage, rd.Serve.Salt = r.Serve.UnitType, &percentage, r.Serve.Salt
		}
		d.Rules[i] = rd
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(b, &out)
}

// configurationInputOf returns the configuration doc proposes as a write
// gives it whole, every rule with its identifier and every rollout with its
// salt.
func configurationInputOf(doc governance.Document) (flags.ConfigurationInput, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return flags.ConfigurationInput{}, err
	}
	var d configurationDoc
	if err = json.Unmarshal(b, &d); err != nil {
		return flags.ConfigurationInput{}, err
	}
	in := flags.ConfigurationInput{
		On: d.On, OffVariation: d.OffVariation, Fallthrough: d.Fallthrough,
		Prerequisites: make([]flags.Prerequisite, len(d.Prerequisites)), Overrides: make([]flags.OverrideInput, len(d.Overrides)),
		Rules: make([]flags.RuleInput, len(d.Rules)),
	}
	for i, p := range d.Prerequisites {
		in.Prerequisites[i] = flags.Prerequisite{Flag: p.Flag, Variation: p.Variation}
	}
	for i, o := range d.Overrides {
		in.Overrides[i] = flags.OverrideInput{UnitType: o.UnitType, UnitID: o.UnitID, Variation: o.Variation, Note: o.Note}
	}
	for i, r := range d.Rules {
		ri := flags.RuleInput{
			Description: r.Description, Conditions: make([]flags.Condition, len(r.Conditions)),
			Serve: flags.ServeInput{
				Kind: r.Serve.Kind, Variation: r.Serve.Variation, UnitType: r.Serve.UnitType, Percentage: r.Serve.Percentage,
				Salt: r.Serve.Salt,
			},
		}
		if ruleID, err := id.Parse[flags.Rule](r.ID); err == nil {
			ri.ID = &ruleID
		}
		for j, cond := range r.Conditions {
			ri.Conditions[j] = flags.Condition(cond)
		}
		in.Rules[i] = ri
	}
	return in, nil
}

// under returns err with its violations pointing under prefix, when it is
// a refusal naming fields; err as it is otherwise.
func under(err error, prefix string) error {
	if e, ok := apperrors.From(err); ok && len(e.Violations) > 0 {
		return e.Under(prefix)
	}
	return err
}
