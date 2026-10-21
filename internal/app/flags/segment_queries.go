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

// SegmentReader reads one segment of a project and where it is used.
type SegmentReader interface {
	SegmentByKey(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error)
	// SegmentUsages returns one reference per flag and environment whose
	// rules name the segment key, archived flags included, by flag key and
	// then environment order.
	SegmentUsages(ctx context.Context, project workspace.ProjectID, key string) ([]flags.Reference, error)
}

// SegmentLister lists a project's segments with how much they are used.
type SegmentLister interface {
	// Segments returns the project's segments by key.
	Segments(ctx context.Context, project workspace.ProjectID) ([]flags.Segment, error)
	// SegmentUsageCounts returns, by segment key, how many flags' rules
	// name it, archived flags included; a key no rule names is absent.
	SegmentUsageCounts(ctx context.Context, project workspace.ProjectID) (map[string]int, error)
}

// ListedSegment is a segment with how many flags' rules name it.
type ListedSegment struct {
	Segment    flags.Segment
	UsageCount int
}

// ListSegments lists a project's segments.
type ListSegments struct {
	snapshots Snapshotter
	store     SegmentLister
}

// NewListSegments returns the handler.
func NewListSegments(snapshots Snapshotter, store SegmentLister) *ListSegments {
	return &ListSegments{snapshots: snapshots, store: store}
}

// Handle returns project's segments by key, each with how many flags'
// rules name it. The segments and the counts are read in one snapshot, so
// a count is never that of a segment deleted and created again in between,
// and the counts of the whole list cost one query.
func (h *ListSegments) Handle(ctx context.Context, project workspace.ProjectID) ([]ListedSegment, error) {
	var out []ListedSegment
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		ss, err := h.store.Segments(ctx, project)
		if err != nil {
			return err
		}
		counts, err := h.store.SegmentUsageCounts(ctx, project)
		if err != nil {
			return err
		}
		out = make([]ListedSegment, len(ss))
		for i, s := range ss {
			out[i] = ListedSegment{Segment: s, UsageCount: counts[s.Key]}
		}
		return nil
	})
	return out, err
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
type ListSegmentUsages struct {
	snapshots Snapshotter
	store     SegmentReader
}

// NewListSegmentUsages returns the handler.
func NewListSegmentUsages(snapshots Snapshotter, store SegmentReader) *ListSegmentUsages {
	return &ListSegmentUsages{snapshots: snapshots, store: store}
}

// Handle returns every flag and environment whose rules name project's
// segment key, or not_found for a segment the project does not have. The
// segment and its usages are read in one snapshot, so the usages are those
// of the segment found.
func (h *ListSegmentUsages) Handle(ctx context.Context, project workspace.ProjectID, key string) ([]flags.Reference, error) {
	var usages []flags.Reference
	err := h.snapshots.Snapshot(ctx, func(ctx context.Context) error {
		if _, err := h.store.SegmentByKey(ctx, project, key); err != nil {
			return segmentNotFound(err)
		}
		var err error
		usages, err = h.store.SegmentUsages(ctx, project, key)
		return err
	})
	return usages, err
}
