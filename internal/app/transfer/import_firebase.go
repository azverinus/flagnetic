package transfer

import (
	"context"
	"slices"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/transfer"
	"github.com/azverinus/flagnetic/internal/domain/transfer/firebase"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// DefaultFirebaseUnitType is the unit type percent rollouts bucket on when
// the import names none.
const DefaultFirebaseUnitType = "anonymous"

// ImportFirebaseTemplate merges a Firebase Remote Config template into one
// environment of a project. It is a merge like any other: the template is
// translated, inside the unit of work, into the project document that holds
// the flags and attributes it describes, and that document is applied.
type ImportFirebaseTemplate struct{ inner *ImportIntoProject }

// NewImportFirebaseTemplate returns the handler. It runs on the same
// dependencies as ImportIntoProject.
func NewImportFirebaseTemplate(d ImportIntoProjectDeps) *ImportFirebaseTemplate {
	return &ImportFirebaseTemplate{inner: NewImportIntoProject(d)}
}

// ImportFirebaseTemplateCommand is the template to merge into Environment of
// Project. UnitType is the unit type percent rollouts bucket on, the
// default one when empty. IfMatch is the entity tag of the project's state
// the import is based on, as for ImportIntoProjectCommand.
type ImportFirebaseTemplateCommand struct {
	Project     workspace.ProjectID
	Template    firebase.Template
	Environment string
	UnitType    string
	DryRun      bool
	IfMatch     *string
	Caller      *access.MemberID
	Request     governance.Request
}

// FirebaseFlag is a flag the import wrote, as the project stores it in the
// target environment.
type FirebaseFlag struct {
	Key          string
	Type         flags.Type
	Description  string
	Tags         []string
	Variations   []transfer.Variation
	On           bool
	OffVariation string
	Fallthrough  string
	Rules        []transfer.Rule
}

// FirebaseAttribute is an attribute the translation declares.
type FirebaseAttribute struct {
	Key  string
	Type workspace.AttributeType
}

// FirebaseImportResult is what an import of a template did, with the flags it
// left in the environment and the attributes it declared.
type FirebaseImportResult struct {
	ImportResult
	// Flags are those of the template, by key.
	Flags      []FirebaseFlag
	Attributes []FirebaseAttribute
}

// Handle translates the template from the project's state under the
// project's row and merges what it describes into the environment, whole or
// not at all: a new flag is on there and off elsewhere, and an existing one
// keeps its other environments, its prerequisites and its overrides. A
// template that cannot be translated, an environment or a unit type the
// project lacks is refused as an invalid document, every fault found.
func (h *ImportFirebaseTemplate) Handle(ctx context.Context, cmd ImportFirebaseTemplateCommand) (FirebaseImportResult, error) {
	unitType := cmd.UnitType
	if unitType == "" {
		unitType = DefaultFirebaseUnitType
	}
	var translated firebase.Result
	var stored []FirebaseFlag
	res, err := h.inner.Handle(ctx, ImportIntoProjectCommand{
		Project: cmd.Project, Mode: transfer.ModeMerge, DryRun: cmd.DryRun, IfMatch: cmd.IfMatch, Caller: cmd.Caller, Request: cmd.Request,
		source: transfer.SourceFirebase,
		prepare: func(_ context.Context, s transfer.Snapshot) (transfer.Document, []transfer.Warning, error) {
			if err := checkTarget(s, cmd.Environment, unitType); err != nil {
				return transfer.Document{}, nil, err
			}
			current := transfer.FromSnapshot(s)
			var err error
			translated, err = firebase.Translate(cmd.Template, firebaseCurrent(s, cmd.Environment), firebase.Target{UnitType: unitType})
			if err != nil {
				return transfer.Document{}, nil, err
			}
			return transfer.FirebaseDocument(current, translated, cmd.Environment), transfer.FirebaseWarnings(translated.Warnings), nil
		},
		after: func(ctx context.Context) error {
			var err error
			stored, err = h.read(ctx, cmd.Project, cmd.Environment, translated)
			return err
		},
	})
	if err != nil {
		return FirebaseImportResult{}, err
	}
	out := FirebaseImportResult{ImportResult: res, Flags: stored, Attributes: make([]FirebaseAttribute, len(translated.Attributes))}
	for i, a := range translated.Attributes {
		out.Attributes[i] = FirebaseAttribute{Key: a.Key, Type: a.Type}
	}
	return out, nil
}

// checkTarget refuses an environment or a unit type the project lacks, each
// pointing at the query parameter that names it.
func checkTarget(s transfer.Snapshot, environment, unitType string) error {
	var faults []apperrors.Violation
	if !slices.ContainsFunc(s.Environments, func(e workspace.Environment) bool { return e.Key == environment }) {
		faults = append(faults, apperrors.Violation{
			Pointer: "/query/environment", Code: "unknown_environment",
			Detail: "the project has no environment " + environment, Params: map[string]any{"environment": environment},
		})
	}
	if !slices.ContainsFunc(s.UnitTypes, func(u workspace.UnitType) bool { return u.Key == unitType }) {
		faults = append(faults, apperrors.Violation{
			Pointer: "/query/unit_type", Code: "unknown_unit_type",
			Detail: "the project has no unit type " + unitType, Params: map[string]any{"unit_type": unitType},
		})
	}
	if len(faults) == 0 {
		return nil
	}
	return apperrors.Invalid("invalid_document", "the Remote Config template cannot be imported there", faults...)
}

// firebaseCurrent is what the translation reads of the project: its flags
// with the rules each has in the environment, and its declarations.
func firebaseCurrent(s transfer.Snapshot, environment string) firebase.Current {
	cur := firebase.Current{UnitTypes: s.UnitTypes, Attributes: s.Attributes, Flags: make([]firebase.ExistingFlag, 0, len(s.Flags))}
	for _, f := range s.Flags {
		e := firebase.ExistingFlag{Flag: f}
		for _, c := range s.Configurations[f.ID] {
			if c.Environment == environment {
				e.Rules = c.Configuration.Rules
			}
		}
		cur.Flags = append(cur.Flags, e)
	}
	return cur
}

// read returns the flags of the translation as the project holds them now,
// in the unit of work the import ran in, so that a rule or a rollout given
// its identifier or salt by the write shows it.
func (h *ImportFirebaseTemplate) read(ctx context.Context, project workspace.ProjectID, environment string, res firebase.Result) ([]FirebaseFlag, error) {
	all, err := h.inner.d.Flags.ProjectFlags(ctx, project)
	if err != nil {
		return nil, err
	}
	configurations, err := h.inner.d.Flags.ProjectConfigurations(ctx, project)
	if err != nil {
		return nil, err
	}
	doc := transfer.FromSnapshot(transfer.Snapshot{Flags: all, Configurations: configurations})
	byKey := make(map[string]transfer.Flag, len(doc.Flags))
	for _, f := range doc.Flags {
		byKey[f.Key] = f
	}
	out := make([]FirebaseFlag, 0, len(res.Flags))
	for _, t := range res.Flags {
		f := byKey[t.Key]
		c := f.Configurations[environment]
		out = append(out, FirebaseFlag{
			Key: f.Key, Type: flags.Type(f.Type), Description: f.Description, Tags: nonNilStrings(f.Tags), Variations: f.Variations,
			On: c.On, OffVariation: c.OffVariation, Fallthrough: c.Fallthrough, Rules: nonNilRules(c.Rules),
		})
	}
	return out, nil
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func nonNilRules(in []transfer.Rule) []transfer.Rule {
	if in == nil {
		return []transfer.Rule{}
	}
	return in
}
