package experiments

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
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

// ExperimentFilter narrows a list of experiments; every field given applies.
type ExperimentFilter struct {
	// Status lists the experiments in that status; "" for any.
	Status experiments.Status
	// Flag lists the experiments comparing the flag with that key; "" for any.
	Flag string
	// Owner lists the experiments owned by that member; nil for any.
	Owner *access.MemberID
	// Tag lists the experiments carrying that tag, matched exactly; "" for any.
	Tag string
	// IncludeArchived lists archived experiments too. They are hidden
	// otherwise, unless Status asks for them.
	IncludeArchived bool
}

// ExperimentPage asks for one page of a project's experiments, by key in
// byte order.
type ExperimentPage struct {
	ExperimentFilter
	// After is the key the page starts after, "" for the first page.
	After string
	Limit int
}

// ExperimentLister lists experiments.
type ExperimentLister interface {
	// Experiments returns up to limit experiments of project matching
	// filter, ordered by key in byte order, whose key is greater than after
	// when it is not "".
	Experiments(ctx context.Context, project workspace.ProjectID, filter ExperimentFilter, after string, limit int) ([]experiments.Experiment, error)
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
	list, err := h.store.Experiments(ctx, project, page.ExperimentFilter, page.After, page.Limit+1)
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
