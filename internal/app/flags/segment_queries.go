package flags

import (
	"context"
	"errors"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// errSegmentNotFound answers a segment key the project does not have.
var errSegmentNotFound = apperrors.NotFound("not_found", "the project has no segment with this key")

// segmentNotFound answers ErrNotFound as not_found, any other error as it
// is.
func segmentNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return errSegmentNotFound
	}
	return err
}

// SegmentReader reads a project's segments.
type SegmentReader interface {
	// Segments returns the project's segments by key.
	Segments(ctx context.Context, project workspace.ProjectID) ([]flags.Segment, error)
	SegmentByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error)
	// SegmentUsages returns one reference per flag and environment whose
	// rules name the segment key, archived flags included, by flag key and
	// then environment order.
	SegmentUsages(ctx context.Context, project workspace.ProjectID, key string) ([]flags.Reference, error)
}

// ListSegments lists a project's segments.
type ListSegments struct{ store SegmentReader }

// NewListSegments returns the handler.
func NewListSegments(store SegmentReader) *ListSegments { return &ListSegments{store: store} }

// Handle returns project's segments by key.
func (h *ListSegments) Handle(ctx context.Context, project workspace.ProjectID) ([]flags.Segment, error) {
	return h.store.Segments(ctx, project)
}

// GetSegment reads one segment.
type GetSegment struct{ store SegmentReader }

// NewGetSegment returns the handler.
func NewGetSegment(store SegmentReader) *GetSegment { return &GetSegment{store: store} }

// Handle returns project's segment with key, or not_found.
func (h *GetSegment) Handle(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error) {
	s, err := h.store.SegmentByKey(ctx, project, key)
	return s, segmentNotFound(err)
}

// ListSegmentUsages reads where a segment is used.
type ListSegmentUsages struct{ store SegmentReader }

// NewListSegmentUsages returns the handler.
func NewListSegmentUsages(store SegmentReader) *ListSegmentUsages {
	return &ListSegmentUsages{store: store}
}

// Handle returns every flag and environment whose rules name project's
// segment key, or not_found for a segment the project does not have.
func (h *ListSegmentUsages) Handle(ctx context.Context, project workspace.ProjectID, key string) ([]flags.Reference, error) {
	if _, err := h.store.SegmentByKey(ctx, project, key); err != nil {
		return nil, segmentNotFound(err)
	}
	return h.store.SegmentUsages(ctx, project, key)
}
