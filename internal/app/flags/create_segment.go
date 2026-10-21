package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Declarations reads what a project's conditions may name.
type Declarations interface {
	// UnitTypes returns the project's unit types.
	UnitTypes(ctx context.Context, project workspace.ProjectID) ([]workspace.UnitType, error)
	// Attributes returns the project's attributes.
	Attributes(ctx context.Context, project workspace.ProjectID) ([]workspace.Attribute, error)
}

// declarationsOf returns the declarations of project as conditions are
// checked against them.
func declarationsOf(ctx context.Context, d Declarations, project workspace.ProjectID) (flags.Declarations, error) {
	units, err := d.UnitTypes(ctx, project)
	if err != nil {
		return flags.Declarations{}, err
	}
	attributes, err := d.Attributes(ctx, project)
	return flags.NewDeclarations(units, attributes), err
}

// SegmentCreator stores a new segment.
type SegmentCreator interface {
	// CreateSegment refuses a key the project uses as already_exists.
	CreateSegment(ctx context.Context, s flags.Segment) error
}

// CreateSegmentDeps are what CreateSegment runs on.
type CreateSegmentDeps struct {
	Tx           Transactor
	Projects     ProjectLocker
	Declarations Declarations
	Store        SegmentCreator
	Rulesets     Rulesets
	Recorder     Recorder
}

// CreateSegment creates a segment.
type CreateSegment struct{ d CreateSegmentDeps }

// NewCreateSegment returns the handler.
func NewCreateSegment(d CreateSegmentDeps) *CreateSegment { return &CreateSegment{d: d} }

// CreateSegmentCommand is the segment to create.
type CreateSegmentCommand struct {
	Project           workspace.ProjectID
	Key               string
	Name, Description string
	Private           bool
	Conditions        []flags.Condition
	Request           governance.Request
}

// Handle creates the segment, checked against the project's declarations
// under its row lock, so no declaration it names is deleted meanwhile. Every
// environment's ruleset carries the project's segments, so each moves on.
func (h *CreateSegment) Handle(ctx context.Context, cmd CreateSegmentCommand) (flags.Segment, error) {
	now := h.d.Recorder.now()
	var s flags.Segment
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, h.d.Projects, cmd.Project); err != nil {
			return err
		}
		decl, err := declarationsOf(ctx, h.d.Declarations, cmd.Project)
		if err != nil {
			return err
		}
		s, err = flags.NewSegment(flags.SegmentInput{
			ID: id.New[flags.Segment](h.d.Recorder.ids), Project: cmd.Project, Key: cmd.Key, Name: cmd.Name,
			Description: cmd.Description, Private: cmd.Private, Conditions: cmd.Conditions, Declarations: decl, Now: now,
		})
		if err != nil {
			return err
		}
		if err = h.d.Store.CreateSegment(ctx, s); err != nil {
			return err
		}
		if err = h.d.Rulesets.Project(ctx, cmd.Project); err != nil {
			return err
		}
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "segment.created", Entity: segmentEntity(s), After: segmentValues(s),
		})
	})
	return s, err
}

func segmentEntity(s flags.Segment) governance.Entity {
	return governance.Entity{Kind: "segment", ID: s.ID.String(), Key: s.Key}
}

// segmentValues are what an audit entry records of a segment.
func segmentValues(s flags.Segment) map[string]any {
	conditions := make([]map[string]any, len(s.Conditions))
	for i, c := range s.Conditions {
		conditions[i] = conditionValues(c)
	}
	return map[string]any{
		"key": s.Key, "name": s.Name, "description": s.Description, "private": s.Private,
		"conditions": conditions, "version": s.Version,
	}
}

// conditionValues are a condition's fields that it has, as a ruleset
// carries them.
func conditionValues(c flags.Condition) map[string]any {
	out := map[string]any{"kind": c.Kind}
	for k, v := range map[string]string{
		"attribute": c.Attribute, "op": c.Op, "segment": c.Segment, "flag": c.Flag, "variation": c.Variation,
	} {
		if v != "" {
			out[k] = v
		}
	}
	if c.Value != nil {
		out["value"] = c.Value
	}
	return out
}
