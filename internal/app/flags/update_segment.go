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
		if err := lockProject(ctx, h.d.Projects, cmd.Project); err != nil {
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

// Propose returns project's segment keyed key, and the segment change would
// leave it as, checked as Handle checks it, with the same refusals; it
// stores and records nothing. It reads in the unit of work ctx carries,
// whose caller holds the project's lock.
func (h *UpdateSegment) Propose(ctx context.Context, project workspace.ProjectID, key string, change flags.SegmentChange,
) (flags.Segment, flags.Segment, error) {
	before, err := h.d.Store.SegmentByKey(ctx, project, key)
	if err != nil {
		return before, before, segmentNotFound(err)
	}
	decl, err := declarationsOf(ctx, h.d.Declarations, project)
	if err != nil {
		return before, before, err
	}
	after, _, err := before.Change(change, decl, h.d.Recorder.now())
	return before, after, err
}

// Current returns project's segment keyed key, or not_found.
func (h *UpdateSegment) Current(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error) {
	s, err := h.d.Store.SegmentByKey(ctx, project, key)
	return s, segmentNotFound(err)
}

// Apply makes change to project's segment keyed key, based on its version
// baseVersion, in the unit of work ctx carries, which joins a change
// request's application: checked again as Handle checks it and recorded as
// Handle records it. Whether the write may change a protected environment
// is the bypass ctx carries, as for every write.
func (h *UpdateSegment) Apply(ctx context.Context, project workspace.ProjectID, key string, change flags.SegmentChange,
	baseVersion int64, req governance.Request,
) error {
	_, err := h.Handle(ctx, UpdateSegmentCommand{
		Project: project, Key: key, Change: change, Expected: precondition.On(baseVersion), Request: req,
	})
	return err
}
