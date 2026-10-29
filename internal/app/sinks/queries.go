package sinks

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ListSinks answers a project's sinks.
type ListSinks struct{ store Store }

// NewListSinks returns the query.
func NewListSinks(store Store) *ListSinks { return &ListSinks{store: store} }

// Handle returns project's sinks, the oldest first.
func (h *ListSinks) Handle(ctx context.Context, project workspace.ProjectID) ([]sinks.Sink, error) {
	return h.store.Sinks(ctx, project)
}

// GetSink answers one sink.
type GetSink struct{ store Store }

// NewGetSink returns the query.
func NewGetSink(store Store) *GetSink { return &GetSink{store: store} }

// Handle returns project's sink with sink, or not_found.
func (h *GetSink) Handle(ctx context.Context, project workspace.ProjectID, sink sinks.ID) (sinks.Sink, error) {
	return sinkOf(ctx, h.store, project, sink)
}
