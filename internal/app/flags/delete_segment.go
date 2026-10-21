package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// SegmentDeleter deletes a segment.
type SegmentDeleter interface {
	SegmentByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error)
	DeleteSegment(ctx context.Context, segment flags.SegmentID) error
}

// DeleteSegment deletes a segment.
type DeleteSegment struct {
	d     SegmentDeps
	store SegmentDeleter
}

// NewDeleteSegment returns the handler.
func NewDeleteSegment(d SegmentDeps, store SegmentDeleter) *DeleteSegment {
	return &DeleteSegment{d: d, store: store}
}

// SegmentCommand names one segment of a project and the version of it a
// write is based on.
type SegmentCommand struct {
	Project  workspace.ProjectID
	Key      string
	Expected precondition.Version
	Request  governance.Request
}

// Handle deletes the segment, which leaves every environment's ruleset.
func (h *DeleteSegment) Handle(ctx context.Context, cmd SegmentCommand) error {
	now := h.d.Recorder.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if _, err := h.d.Projects.LockProject(ctx, cmd.Project); err != nil {
			return err
		}
		s, err := h.store.SegmentByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return segmentNotFound(err)
		}
		if err = precondition.Check(cmd.Expected, s.Version); err != nil {
			return err
		}
		if err = h.store.DeleteSegment(ctx, s.ID); err != nil {
			return err
		}
		if err = h.d.Rulesets.Project(ctx, cmd.Project); err != nil {
			return err
		}
		return h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "segment.deleted", Entity: segmentEntity(s), Before: segmentValues(s),
		})
	})
}
