package flags

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// Definitions is a flag's and a segment's definition as a change request
// proposes, bases itself on and applies it: checked, read and written as a
// direct edit of the flag or the segment is.
type Definitions struct {
	flag      *UpdateFlag
	segment   *UpdateSegment
	referrers *SegmentReferrers
}

// NewDefinitions returns the definitions edited through flag and segment,
// with the flags naming a segment read through referrers.
func NewDefinitions(flag *UpdateFlag, segment *UpdateSegment, referrers *SegmentReferrers) *Definitions {
	return &Definitions{flag: flag, segment: segment, referrers: referrers}
}

// ProposeFlag returns project's flag keyed key and the flag change would
// leave it as, with the refusals editing it would get.
func (d *Definitions) ProposeFlag(ctx context.Context, project workspace.ProjectID, key string, change flags.FlagChange,
) (flags.Flag, flags.Flag, error) {
	return d.flag.Propose(ctx, project, key, change)
}

// CurrentFlag returns project's flag keyed key, or not_found.
func (d *Definitions) CurrentFlag(ctx context.Context, project workspace.ProjectID, key string) (flags.Flag, error) {
	return d.flag.Current(ctx, project, key)
}

// ApplyFlag makes change to project's flag keyed key, based on its version
// baseVersion, in the unit of work ctx carries.
func (d *Definitions) ApplyFlag(ctx context.Context, project workspace.ProjectID, key string, change flags.FlagChange,
	baseVersion int64, req governance.Request,
) error {
	return d.flag.Apply(ctx, project, key, change, baseVersion, req)
}

// ProposeSegment returns project's segment keyed key and the segment change
// would leave it as, with the refusals editing it would get.
func (d *Definitions) ProposeSegment(ctx context.Context, project workspace.ProjectID, key string, change flags.SegmentChange,
) (flags.Segment, flags.Segment, error) {
	return d.segment.Propose(ctx, project, key, change)
}

// CurrentSegment returns project's segment keyed key, or not_found.
func (d *Definitions) CurrentSegment(ctx context.Context, project workspace.ProjectID, key string) (flags.Segment, error) {
	return d.segment.Current(ctx, project, key)
}

// ApplySegment makes change to project's segment keyed key, based on its
// version baseVersion, in the unit of work ctx carries.
func (d *Definitions) ApplySegment(ctx context.Context, project workspace.ProjectID, key string, change flags.SegmentChange,
	baseVersion int64, req governance.Request,
) error {
	return d.segment.Apply(ctx, project, key, change, baseVersion, req)
}

// SegmentReferrers returns the flags of project whose rules name the
// segment key in some environment, archived flags included, each once.
func (d *Definitions) SegmentReferrers(ctx context.Context, project workspace.ProjectID, key string) ([]flags.FlagID, error) {
	return d.referrers.SegmentReferrers(ctx, project, key)
}
