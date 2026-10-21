package management

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/flags"
)

// ListSegments answers the project's segments by key.
func (a *api) ListSegments(ctx context.Context, _ ListSegmentsRequestObject) (ListSegmentsResponseObject, error) {
	ss, err := a.h.ListSegments.Handle(ctx, projectID(ctx))
	if err != nil {
		return nil, err
	}
	return ListSegments200JSONResponse{Items: each(ss, segmentListItemOut)}, nil
}

// CreateSegment creates a segment.
func (a *api) CreateSegment(ctx context.Context, req CreateSegmentRequestObject) (CreateSegmentResponseObject, error) {
	b := req.Body
	s, err := a.h.CreateSegment.Handle(ctx, appflags.CreateSegmentCommand{
		Project: projectID(ctx), Key: b.Key, Name: b.Name, Description: valueOf(b.Description),
		Private: valueOr(b.Private, false), Conditions: conditionsIn(valueOr(b.Conditions, nil)), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CreateSegment201JSONResponse{Body: segmentOut(s), Headers: CreateSegment201ResponseHeaders{ETag: etag(s.Version)}}, nil
}

// GetSegment answers one segment.
func (a *api) GetSegment(ctx context.Context, req GetSegmentRequestObject) (GetSegmentResponseObject, error) {
	s, err := a.h.GetSegment.Handle(ctx, projectID(ctx), req.Segment)
	if err != nil {
		return nil, err
	}
	return GetSegment200JSONResponse{Body: segmentOut(s), Headers: GetSegment200ResponseHeaders{ETag: etag(s.Version)}}, nil
}

// UpdateSegment changes a segment.
func (a *api) UpdateSegment(ctx context.Context, req UpdateSegmentRequestObject) (UpdateSegmentResponseObject, error) {
	expected, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	change := flags.SegmentChange{Key: b.Key, Name: b.Name, Description: b.Description, Private: b.Private}
	if b.Conditions != nil {
		cs := conditionsIn(*b.Conditions)
		change.Conditions = &cs
	}
	s, err := a.h.UpdateSegment.Handle(ctx, appflags.UpdateSegmentCommand{
		Project: projectID(ctx), Key: req.Segment, Change: change, Expected: expected, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UpdateSegment200JSONResponse{Body: segmentOut(s), Headers: UpdateSegment200ResponseHeaders{ETag: etag(s.Version)}}, nil
}

// DeleteSegment deletes a segment.
func (a *api) DeleteSegment(ctx context.Context, req DeleteSegmentRequestObject) (DeleteSegmentResponseObject, error) {
	cmd, err := segmentCommand(ctx, req.Segment, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.h.DeleteSegment.Handle(ctx, cmd); err != nil {
		return nil, err
	}
	return DeleteSegment204Response{}, nil
}

// segmentCommand returns the command naming segment of the request's
// project, based on the version header names.
func segmentCommand(ctx context.Context, segment string, header *IfMatch) (appflags.SegmentCommand, error) {
	expected, err := ifMatch(header)
	return appflags.SegmentCommand{Project: projectID(ctx), Key: segment, Expected: expected, Request: requestOf(ctx)}, err
}

// ListSegmentUsages answers where a segment is used.
func (a *api) ListSegmentUsages(ctx context.Context, req ListSegmentUsagesRequestObject) (ListSegmentUsagesResponseObject, error) {
	refs, err := a.h.ListSegmentUsages.Handle(ctx, projectID(ctx), req.Segment)
	if err != nil {
		return nil, err
	}
	return ListSegmentUsages200JSONResponse{Items: each(refs, referenceOut)}, nil
}

func conditionsIn(in []Condition) []flags.Condition {
	if in == nil {
		return nil
	}
	return each(in, func(c Condition) flags.Condition {
		return flags.Condition{
			Kind: c.Kind, Attribute: valueOf(c.Attribute), Op: valueOf(c.Op), Value: c.Value,
			Segment: valueOf(c.Segment), Flag: valueOf(c.Flag), Variation: valueOf(c.Variation),
		}
	})
}

// optional returns s, or nil when it is empty, for a member a condition of
// another kind leaves out.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func conditionOut(c flags.Condition) Condition {
	return Condition{
		Kind: c.Kind, Attribute: optional(c.Attribute), Op: optional(c.Op), Value: c.Value,
		Segment: optional(c.Segment), Flag: optional(c.Flag), Variation: optional(c.Variation),
	}
}

func segmentOut(s flags.Segment) Segment {
	return Segment{
		Id: openapi_types.UUID(s.ID.Bytes()), Key: s.Key, Name: s.Name, Description: s.Description, Private: s.Private,
		Conditions: each(s.Conditions, conditionOut), CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Version: s.Version,
	}
}

func segmentListItemOut(l appflags.ListedSegment) SegmentListItem {
	s := segmentOut(l.Segment)
	return SegmentListItem{
		Id: s.Id, Key: s.Key, Name: s.Name, Description: s.Description, Private: s.Private, Conditions: s.Conditions,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Version: s.Version, UsageCount: l.UsageCount,
	}
}

func referenceOut(r flags.Reference) Reference {
	return Reference{Kind: r.Kind, Key: r.Key, Environment: optional(r.Environment)}
}
