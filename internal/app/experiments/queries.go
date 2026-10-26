package experiments

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
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
	// PastExpectedEnd lists only the running experiments past their
	// expected end, from the UTC day after it.
	PastExpectedEnd bool
	// AwaitingCodeRemoval lists only the stopped experiments whose flag an
	// SDK evaluated after the stop, on one of the last
	// AwaitingCodeRemovalDays UTC days, in some environment.
	AwaitingCodeRemoval bool
}

// AwaitingCodeRemovalDays is how many UTC days, today included, an SDK's
// evaluation of a stopped experiment's flag keeps it awaiting code removal: a
// week covers a release cycle, so code that is gone from every deployed
// version stops being reported within one.
const AwaitingCodeRemovalDays = 7

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
	// filter at now, ordered by key in byte order, whose key is greater
	// than after when it is not "".
	Experiments(ctx context.Context, project workspace.ProjectID, filter ExperimentFilter, now time.Time, after string, limit int,
	) ([]experiments.Experiment, error)
}

// ListExperiments lists a project's experiments.
type ListExperiments struct {
	store ExperimentLister
	clock clock.Clock
}

// NewListExperiments returns the handler.
func NewListExperiments(store ExperimentLister, c clock.Clock) *ListExperiments {
	return &ListExperiments{store: store, clock: c}
}

// Handle returns one page of project's experiments, and the key to start the
// next page after, nil on the last page.
func (h *ListExperiments) Handle(ctx context.Context, project workspace.ProjectID, page ExperimentPage) ([]experiments.Experiment, *string, error) {
	list, err := h.store.Experiments(ctx, project, page.ExperimentFilter, h.clock.Now().UTC(), page.After, page.Limit+1)
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

// GetAssignment answers how an experiment treats one unit.
type GetAssignment struct{ store Reader }

// NewGetAssignment returns the handler.
func NewGetAssignment(store Reader) *GetAssignment { return &GetAssignment{store: store} }

// Handle returns unitID's assignment in project's experiment with key, from
// the experiment's own fields whatever its status, or not_found. It reads no
// event and records nothing.
func (h *GetAssignment) Handle(ctx context.Context, project workspace.ProjectID, key, unitID string) (experiments.Assignment, error) {
	e, err := h.store.ExperimentByKey(ctx, project, key)
	if err != nil {
		return experiments.Assignment{}, experimentNotFound(err)
	}
	return e.Assign(unitID), nil
}
