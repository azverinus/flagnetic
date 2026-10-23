package experiments

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// GetExperiment reads an experiment.
type GetExperiment struct{ store Reader }

// NewGetExperiment returns the handler.
func NewGetExperiment(store Reader) *GetExperiment { return &GetExperiment{store: store} }

// Handle returns project's experiment with key, or not_found.
func (h *GetExperiment) Handle(ctx context.Context, project workspace.ProjectID, key string) (experiments.Experiment, error) {
	e, err := h.store.ExperimentByKey(ctx, project, key)
	return e, experimentNotFound(err)
}

// ExperimentPage asks for one page of a project's experiments, by key in
// byte order.
type ExperimentPage struct {
	// After is the key the page starts after, "" for the first page.
	After string
	Limit int
}

// ExperimentLister lists experiments.
type ExperimentLister interface {
	// Experiments returns up to limit experiments of project, ordered by
	// key in byte order, whose key is greater than after when it is not "".
	Experiments(ctx context.Context, project workspace.ProjectID, after string, limit int) ([]experiments.Experiment, error)
}

// ListExperiments lists a project's experiments.
type ListExperiments struct{ store ExperimentLister }

// NewListExperiments returns the handler.
func NewListExperiments(store ExperimentLister) *ListExperiments {
	return &ListExperiments{store: store}
}

// Handle returns one page of project's experiments, and the key to start the
// next page after, nil on the last page.
func (h *ListExperiments) Handle(ctx context.Context, project workspace.ProjectID, page ExperimentPage) ([]experiments.Experiment, *string, error) {
	list, err := h.store.Experiments(ctx, project, page.After, page.Limit+1)
	if err != nil {
		return nil, nil, err
	}
	if len(list) <= page.Limit {
		return list, nil, nil
	}
	list = list[:page.Limit]
	next := list[page.Limit-1].Key
	return list, &next, nil
}
