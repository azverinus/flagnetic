package governance

import (
	"context"
	"encoding/json"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/protection"
)

// Definitions is the flags context's view of flags' and segments'
// definitions, as a change request proposes them: a flag's variations and
// automatic exposure, a segment's conditions and private switch.
type Definitions interface {
	SegmentReferrers
	// ProposeFlag returns project's flag keyed flag and what making change
	// to it would leave, checked as that write would be, with the same
	// refusals.
	ProposeFlag(ctx context.Context, project workspace.ProjectID, flag string, change flags.FlagChange) (flags.Flag, flags.Flag, error)
	// CurrentFlag returns project's flag keyed flag, or not_found.
	CurrentFlag(ctx context.Context, project workspace.ProjectID, flag string) (flags.Flag, error)
	// ProposeSegment returns project's segment keyed segment and what
	// making change to it would leave, checked as that write would be,
	// with the same refusals.
	ProposeSegment(ctx context.Context, project workspace.ProjectID, segment string, change flags.SegmentChange,
	) (flags.Segment, flags.Segment, error)
	// CurrentSegment returns project's segment keyed segment, or not_found.
	CurrentSegment(ctx context.Context, project workspace.ProjectID, segment string) (flags.Segment, error)
	// ApplyFlag makes change to project's flag keyed flag, based on its
	// version baseVersion, in the unit of work ctx carries, checked again as
	// that write is: a refusal rolls the application back. req is who makes
	// the change, for its audit entry.
	ApplyFlag(ctx context.Context, project workspace.ProjectID, flag string, change flags.FlagChange, baseVersion int64,
		req governance.Request) error
	// ApplySegment makes change to project's segment keyed segment, as
	// ApplyFlag does to a flag.
	ApplySegment(ctx context.Context, project workspace.ProjectID, segment string, change flags.SegmentChange,
		baseVersion int64, req governance.Request) error
}

// SegmentReferrers reads which flags name a segment.
type SegmentReferrers interface {
	// SegmentReferrers returns the flags of project whose configurations
	// name the segment keyed segment in some environment, archived flags
	// included, each once and in the order of their identifiers, read in
	// the unit of work ctx carries.
	SegmentReferrers(ctx context.Context, project workspace.ProjectID, segment string) ([]flags.FlagID, error)
}

// ProtectedEnvironments reads which environments of a project are
// protected.
type ProtectedEnvironments interface {
	// Protected returns project's protected environments, read in the unit
	// of work ctx carries.
	Protected(ctx context.Context, project workspace.ProjectID) ([]workspace.EnvironmentID, error)
}

// Proposed is what an author proposes, as a write would give it: the one
// document the change request's kind and entity call for.
type Proposed struct {
	// Configuration is a flag's configuration in one environment.
	Configuration *flags.ConfigurationInput
	// Flag is a flag's definition: its variations and automatic exposure,
	// and nothing else.
	Flag *flags.FlagChange
	// Segment is a segment's definition: its conditions and private
	// switch, and nothing else.
	Segment *flags.SegmentChange
}

// Target is what a change request proposes for: its kind, and the keys of
// the entity it names.
type Target struct {
	Kind        governance.ChangeRequestKind
	Flag        string
	Environment string
	Segment     string
}

// TargetOf returns what cr proposes for.
func TargetOf(cr governance.ChangeRequest) Target {
	t := Target{Kind: cr.Kind}
	if cr.Flag != nil {
		t.Flag = cr.Flag.Key
	}
	if cr.Environment != nil {
		t.Environment = cr.Environment.Key
	}
	if cr.Segment != nil {
		t.Segment = cr.Segment.Key
	}
	return t
}

// segmentDefinition reports whether t is a segment's definition, and so
// names a segment and not a flag.
func (t Target) segmentDefinition() bool {
	return t.Kind == governance.KindDefinition && t.Segment != ""
}

// check returns invalid_change_request naming every field of t to fix: a
// flag's configuration names a flag and an environment, a definition one
// flag or one segment and no environment.
func (t Target) check() error {
	var violations []apperrors.Violation
	required := func(field, detail string) {
		violations = append(violations, apperrors.Violation{Pointer: "/" + field, Code: "required", Detail: detail})
	}
	notAllowed := func(field, detail string) {
		violations = append(violations, apperrors.Violation{Pointer: "/" + field, Code: "not_allowed", Detail: detail})
	}
	switch t.Kind {
	case governance.KindFlagConfiguration:
		if t.Flag == "" {
			required("flag", "a configuration is a flag's")
		}
		if t.Environment == "" {
			required("environment", "a configuration is in one environment")
		}
		if t.Segment != "" {
			notAllowed("segment", "a configuration is a flag's, not a segment's")
		}
	case governance.KindDefinition:
		switch {
		case t.Flag == "" && t.Segment == "":
			required("flag", "a definition is a flag's or a segment's")
		case t.Flag != "" && t.Segment != "":
			notAllowed("segment", "a definition is one flag's or one segment's")
		}
		if t.Environment != "" {
			notAllowed("environment", "a definition is applied in every environment")
		}
	default:
		violations = append(violations, apperrors.Violation{
			Pointer: "/kind", Code: "invalid_value", Detail: "a change request proposes a flag configuration or a definition",
			Params: map[string]any{"kinds": []string{string(governance.KindFlagConfiguration), string(governance.KindDefinition)}},
		})
	}
	if violations != nil {
		return apperrors.Invalid("invalid_change_request", "the change request is not valid", violations...)
	}
	return nil
}

// errNothingProposed refuses a change request whose proposal is not the
// document its kind and entity call for.
var errNothingProposed = apperrors.Invalid("invalid_change_request", "the change request is not valid", apperrors.Violation{
	Pointer: "/proposed", Code: "required", Detail: "a change request proposes the document its kind and entity call for",
})

// propose returns the subject of a change request proposing in for t in
// project, based on the entity as it is now, and the document it proposes,
// checked as writing it would be. held is the document a change request
// already proposes, nil for a new one: the rules it proposes keep the
// identifiers and salts they were given.
func (d ChangeRequestDeps) propose(ctx context.Context, project workspace.ProjectID, t Target, in Proposed,
	held governance.Document,
) (governance.Subject, governance.Document, error) {
	switch {
	case t.Kind == governance.KindFlagConfiguration && in.Configuration != nil:
		var heldRules []flags.RuleInput
		if held != nil {
			heldIn, err := configurationInputOf(held)
			if err != nil {
				return governance.Subject{}, nil, err
			}
			heldRules = heldIn.Rules
		}
		f, saved, proposed, err := d.Configurations.Propose(ctx, project, t.Flag, t.Environment, *in.Configuration, heldRules)
		if err != nil {
			return governance.Subject{}, nil, under(err, "/proposed")
		}
		return configurationProposal(f, saved, proposed)
	case t.segmentDefinition() && in.Segment != nil:
		before, after, err := d.Definitions.ProposeSegment(ctx, project, t.Segment, *in.Segment)
		if err != nil {
			return governance.Subject{}, nil, under(err, "/proposed")
		}
		subject, err := d.segmentSubject(ctx, before)
		if err != nil {
			return subject, nil, err
		}
		proposed, err := segmentDefinitionDocument(after)
		return subject, proposed, err
	case t.Kind == governance.KindDefinition && !t.segmentDefinition() && in.Flag != nil:
		before, after, err := d.Definitions.ProposeFlag(ctx, project, t.Flag, *in.Flag)
		if err != nil {
			return governance.Subject{}, nil, under(err, "/proposed")
		}
		subject, err := flagSubject(before)
		if err != nil {
			return subject, nil, err
		}
		proposed, err := flagDefinitionDocument(after)
		return subject, proposed, err
	}
	return governance.Subject{}, nil, errNothingProposed
}

// configurationProposal returns the subject of a configuration change
// request on f's saved configuration, and proposed as its document.
func configurationProposal(f flags.Flag, saved flags.EnvironmentConfiguration, proposed flags.Configuration,
) (governance.Subject, governance.Document, error) {
	subject, err := subjectOf(f, saved)
	if err != nil {
		return subject, nil, err
	}
	doc, err := configurationDocument(proposed)
	return subject, doc, err
}

// current returns the subject cr names as it is now, as a change request
// based on it now would be.
func (d ChangeRequestDeps) current(ctx context.Context, cr governance.ChangeRequest) (governance.Subject, error) {
	t := TargetOf(cr)
	switch {
	case t.Kind == governance.KindFlagConfiguration:
		f, saved, err := d.Configurations.Current(ctx, cr.Project, t.Flag, t.Environment)
		if err != nil {
			return governance.Subject{}, err
		}
		return subjectOf(f, saved)
	case t.segmentDefinition():
		s, err := d.Definitions.CurrentSegment(ctx, cr.Project, t.Segment)
		if err != nil {
			return governance.Subject{}, err
		}
		return d.segmentSubject(ctx, s)
	default:
		f, err := d.Definitions.CurrentFlag(ctx, cr.Project, t.Flag)
		if err != nil {
			return governance.Subject{}, err
		}
		return flagSubject(f)
	}
}

// judged returns what cr is judged against when it is approved or applied:
// its entity's version now and, for a change request applied in every
// environment, the environments protected now.
func (d ChangeRequestDeps) judged(ctx context.Context, cr governance.ChangeRequest) (governance.Current, error) {
	subject, err := d.current(ctx, cr)
	if err != nil {
		return governance.Current{}, err
	}
	c := governance.Current{Version: subject.BaseVersion}
	if cr.Kind != governance.KindFlagConfiguration {
		c.Protected, err = d.Environments.Protected(ctx, cr.Project)
	}
	return c, err
}

// write has the context owning cr's entity write what cr proposes, in the
// unit of work ctx carries, allowed to change the environments cr's
// approval covers although they are protected, and no other; by is the
// member applying, nil for a project token or the system.
func (d ChangeRequestDeps) write(ctx context.Context, cr governance.ChangeRequest, by *access.MemberID,
	req governance.Request,
) error {
	ctx = applying(protection.Bypass(ctx, cr.Covers()...), cr.ID)
	t := TargetOf(cr)
	switch {
	case t.Kind == governance.KindFlagConfiguration:
		proposed, err := configurationInputOf(cr.Proposed)
		if err != nil {
			return err
		}
		return d.Proposals.Apply(ctx, cr.Project, t.Flag, t.Environment, proposed, cr.BaseVersion, by, req)
	case t.segmentDefinition():
		change, err := segmentChangeOf(cr.Proposed)
		if err != nil {
			return err
		}
		return d.Definitions.ApplySegment(ctx, cr.Project, t.Segment, change, cr.BaseVersion, req)
	default:
		change, err := flagChangeOf(cr.Proposed)
		if err != nil {
			return err
		}
		return d.Definitions.ApplyFlag(ctx, cr.Project, t.Flag, change, cr.BaseVersion, req)
	}
}

// proposedOf returns what cr proposes as its author would write it again.
func proposedOf(cr governance.ChangeRequest) (Proposed, error) {
	t := TargetOf(cr)
	switch {
	case t.Kind == governance.KindFlagConfiguration:
		in, err := configurationInputOf(cr.Proposed)
		return Proposed{Configuration: &in}, err
	case t.segmentDefinition():
		change, err := segmentChangeOf(cr.Proposed)
		return Proposed{Segment: &change}, err
	default:
		change, err := flagChangeOf(cr.Proposed)
		return Proposed{Flag: &change}, err
	}
}

// flagSubject returns the subject of a change request to f's definition.
func flagSubject(f flags.Flag) (governance.Subject, error) {
	base, err := flagDefinitionDocument(f)
	return governance.Subject{
		Kind: governance.KindDefinition, Flag: &governance.FlagRef{ID: f.ID, Key: f.Key}, Base: base, BaseVersion: f.Version,
	}, err
}

// segmentSubject returns the subject of a change request to s's
// definition, with the flags naming s now.
func (d ChangeRequestDeps) segmentSubject(ctx context.Context, s flags.Segment) (governance.Subject, error) {
	base, err := segmentDefinitionDocument(s)
	if err != nil {
		return governance.Subject{}, err
	}
	referrers, err := d.Definitions.SegmentReferrers(ctx, s.Project, s.Key)
	if err != nil {
		return governance.Subject{}, err
	}
	return governance.Subject{
		Kind: governance.KindDefinition, Segment: &governance.SegmentRef{ID: s.ID, Key: s.Key}, Base: base,
		BaseVersion: s.Version, BaseReferrers: referrers,
	}, nil
}

// flagDefinitionDoc is the document a change request to a flag's definition
// bases itself on and proposes: what every environment's ruleset carries of
// the flag, its variations and automatic exposure, with the names and
// descriptions of the variations, which stay on the server. It is the shape
// the API answers `proposed` in.
type flagDefinitionDoc struct {
	AutomaticExposure bool           `json:"automatic_exposure"`
	Variations        []variationDoc `json:"variations"`
}

type variationDoc struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
}

// segmentDefinitionDoc is the document a change request to a segment's
// definition bases itself on and proposes: its conditions, and whether they
// stay on the server.
type segmentDefinitionDoc struct {
	Conditions []conditionDoc `json:"conditions"`
	Private    bool           `json:"private"`
}

// flagDefinitionDocument returns f's definition as the document a change
// request holds.
func flagDefinitionDocument(f flags.Flag) (governance.Document, error) {
	d := flagDefinitionDoc{AutomaticExposure: f.AutomaticExposure, Variations: make([]variationDoc, len(f.Variations))}
	for i, v := range f.Variations {
		d.Variations[i] = variationDoc{Key: v.Key, Value: v.Value, Name: v.Name, Description: v.Description}
	}
	return asDocument(d)
}

// segmentDefinitionDocument returns s's definition as the document a change
// request holds.
func segmentDefinitionDocument(s flags.Segment) (governance.Document, error) {
	d := segmentDefinitionDoc{Conditions: make([]conditionDoc, len(s.Conditions)), Private: s.Private}
	for i, c := range s.Conditions {
		d.Conditions[i] = conditionDoc(c)
	}
	return asDocument(d)
}

// flagChangeOf returns the change to a flag's definition doc proposes, every
// variation with its name and description.
func flagChangeOf(doc governance.Document) (flags.FlagChange, error) {
	var d flagDefinitionDoc
	if err := fromDocument(doc, &d); err != nil {
		return flags.FlagChange{}, err
	}
	variations := make([]flags.VariationInput, len(d.Variations))
	for i, v := range d.Variations {
		name, description := v.Name, v.Description
		variations[i] = flags.VariationInput{Key: v.Key, Value: v.Value, Name: &name, Description: &description}
	}
	return flags.FlagChange{AutomaticExposure: &d.AutomaticExposure, Variations: &variations}, nil
}

// segmentChangeOf returns the change to a segment's definition doc
// proposes.
func segmentChangeOf(doc governance.Document) (flags.SegmentChange, error) {
	var d segmentDefinitionDoc
	if err := fromDocument(doc, &d); err != nil {
		return flags.SegmentChange{}, err
	}
	conditions := make([]flags.Condition, len(d.Conditions))
	for i, c := range d.Conditions {
		conditions[i] = flags.Condition(c)
	}
	return flags.SegmentChange{Conditions: &conditions, Private: &d.Private}, nil
}

// asDocument returns v decoded into an any as JSON values are, so documents
// compare and diff alike however they were made.
func asDocument(v any) (governance.Document, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(b, &out)
}

// fromDocument decodes doc into v.
func fromDocument(doc governance.Document, v any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
