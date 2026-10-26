package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// SegmentDeleter deletes a segment no rule names.
type SegmentDeleter interface {
	SegmentByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error)
	// SegmentUsages returns one reference per flag and environment whose
	// rules name the segment key, archived flags included.
	SegmentUsages(ctx context.Context, project workspace.ProjectID, key string) ([]flags.Reference, error)
	DeleteSegment(ctx context.Context, segment flags.SegmentID) error
}

// DeleteSegmentDeps are what DeleteSegment runs on.
type DeleteSegmentDeps struct {
	Tx       Transactor
	Projects ProjectLocker
	Store    SegmentDeleter
	Rulesets Rulesets
	Recorder Recorder
}

// DeleteSegment deletes a segment.
type DeleteSegment struct{ d DeleteSegmentDeps }

// NewDeleteSegment returns the handler.
func NewDeleteSegment(d DeleteSegmentDeps) *DeleteSegment { return &DeleteSegment{d: d} }

// SegmentCommand names one segment of a project and the version of it a
// write is based on.
type SegmentCommand struct {
	Project  workspace.ProjectID
	Key      string
	Expected precondition.Version
	Request  governance.Request
}

// Handle deletes the segment, which leaves every environment's ruleset. A
// segment a rule names, an archived flag's included, is not deleted: the
// rule would name nothing, and a restored flag would come back broken.
func (h *DeleteSegment) Handle(ctx context.Context, cmd SegmentCommand) error {
	now := h.d.Recorder.now()
	return h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if err := lockProject(ctx, h.d.Projects, cmd.Project); err != nil {
			return err
		}
		s, err := h.d.Store.SegmentByKey(ctx, cmd.Project, cmd.Key)
		if err != nil {
			return segmentNotFound(err)
		}
		if err = precondition.Check(cmd.Expected, s.Version); err != nil {
			return err
		}
		usages, err := h.d.Store.SegmentUsages(ctx, cmd.Project, s.Key)
		if err != nil {
			return err
		}
		if len(usages) > 0 {
			return flags.ErrInUse(usages)
		}
		// The deletion is recorded before the row goes, so what it ends
		// elsewhere, such as the change requests proposed for the segment,
		// is found and closed with an entry of its own before the cascade
		// removes it.
		if err = h.d.Recorder.record(ctx, now, cmd.Request, governance.Record{
			Project: cmd.Project, Action: "segment.deleted", Entity: segmentEntity(s), Before: segmentValues(s),
		}); err != nil {
			return err
		}
		if err = h.d.Store.DeleteSegment(ctx, s.ID); err != nil {
			return err
		}
		return h.d.Rulesets.Project(ctx, cmd.Project)
	})
}
