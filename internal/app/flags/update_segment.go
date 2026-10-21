package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// SegmentUpdater stores a segment's changes.
type SegmentUpdater interface {
	// SegmentByKey returns project's segment with key, or ErrNotFound.
	SegmentByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error)
	UpdateSegment(ctx context.Context, s flags.Segment) error
}

// UpdateSegmentDeps are what UpdateSegment runs on.
type UpdateSegmentDeps struct {
	Tx           Transactor
	Projects     ProjectLocker
	Declarations Declarations
	Store        SegmentUpdater
	Rulesets     Rulesets
	Recorder     Recorder
}

// UpdateSegment changes a segment.
type UpdateSegment struct{ d UpdateSegmentDeps }

// NewUpdateSegment returns the handler.
func NewUpdateSegment(d UpdateSegmentDeps) *UpdateSegment { return &UpdateSegment{d: d} }

// UpdateSegmentCommand is a change to the segment Key of Project, based on
// the version Expected names.
type UpdateSegmentCommand struct {
	Project  workspace.ProjectID
	Key      string
	Change   flags.SegmentChange
	Expected precondition.Version
	Request  governance.Request
}

// Handle applies the change, validated first and then judged against the
// version it was based on, and returns the segment as it is after it; a
// change to the values it already has stores and records nothing. A change
// to its conditions or private switch moves every environment's ruleset on.
func (h *UpdateSegment) Handle(ctx context.Context, cmd UpdateSegmentCommand) (flags.Segment, error) {
	now := h.d.Recorder.now()
	var out flags.Segment
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.d.Projects.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		before, err := h.d.Store.SegmentByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return segmentNotFound(err)
		}
		decl, err := declarationsOf(ctx, h.d.Declarations, cmd.Project)
		if err != nil {
			return err
		}
		after, changed, err := before.Change(cmd.Change, decl, now)
		if err != nil {
			return err
		}
		if err = precondition.Check(cmd.Expected, before.Version); err != nil {
			return err
		}
		out = after
		if !changed.Any {
			return nil
		}
		if err = h.d.Store.UpdateSegment(ctx, after); err != nil {
			return err
		}
		if changed.Ruleset {
			if err = h.d.Rulesets.Project(ctx, cmd.Project); err != nil {
				return err
			}
		}
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "segment.updated", Entity: segmentEntity(after),
			Before: segmentValues(before), After: segmentValues(after),
		})
	})
	return out, err
}
