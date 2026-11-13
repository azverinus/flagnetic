package sinks

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ListSinks answers a project's sinks.
type ListSinks struct{ d Deps }

// NewListSinks returns the query.
func NewListSinks(d Deps) *ListSinks { return &ListSinks{d: d} }

// Handle returns project's sinks, the oldest first, each with how far behind
// it is.
func (h *ListSinks) Handle(ctx context.Context, project workspace.ProjectID) ([]sinks.Sink, error) {
	list, err := h.d.Store.Sinks(ctx, project)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i], err = h.d.judged(ctx, list[i]); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// GetSink answers one sink.
type GetSink struct{ d Deps }

// NewGetSink returns the query.
func NewGetSink(d Deps) *GetSink { return &GetSink{d: d} }

// Handle returns project's sink with sink, or not_found.
func (h *GetSink) Handle(ctx context.Context, project workspace.ProjectID, sink sinks.ID) (sinks.Sink, error) {
	return h.d.read(ctx, project, sink)
}

// read returns project's sink with sink as it is now, judged, or not_found.
func (d Deps) read(ctx context.Context, project workspace.ProjectID, sink sinks.ID) (sinks.Sink, error) {
	s, err := sinkOf(ctx, d.Store, project, sink)
	if err != nil {
		return s, err
	}
	return d.judged(ctx, s)
}

// judged sets how far behind s is and how it stands.
func (d Deps) judged(ctx context.Context, s sinks.Sink) (sinks.Sink, error) {
	s, err := d.withLag(ctx, s)
	s.Status = s.Health(d.LagWarning)
	return s, err
}

// withLag sets how far behind s is: the age of the oldest row any of its
// streams has not delivered. A disabled sink has none, since it is not
// expected to deliver.
func (d Deps) withLag(ctx context.Context, s sinks.Sink) (sinks.Sink, error) {
	if !s.Enabled {
		return s, nil
	}
	lag, _, _, err := worstLag(ctx, d.Rows, d.now(), s)
	if err != nil {
		return s, err
	}
	s.Lag = &lag
	return s, nil
}
