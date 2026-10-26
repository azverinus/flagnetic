package experiments

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/precondition"
)

// Transitions are an experiment's transitions as a change request proposes,
// bases itself on and applies them: checked as the transition itself
// checks them, read, and run by the transition's own handler.
type Transitions struct {
	store  Reader
	start  *StartExperiment
	stop   *StopExperiment
	update *UpdateExperiment
}

// NewTransitions returns the transitions run by start, stop and update,
// reading experiments from store.
func NewTransitions(store Reader, start *StartExperiment, stop *StopExperiment, update *UpdateExperiment) *Transitions {
	return &Transitions{store: store, start: start, stop: stop, update: update}
}

// Current returns project's experiment keyed key, or not_found, read in the
// unit of work ctx carries.
func (t *Transitions) Current(ctx context.Context, project workspace.ProjectID, key string) (experiments.Experiment, error) {
	e, err := t.store.ExperimentByKey(ctx, project, key)
	return e, experimentNotFound(err)
}

// Propose returns project's experiment keyed key, or the refusal running tr
// on it now would get, writing nothing, so a change request that could
// never be applied is refused when it is proposed and not at review.
func (t *Transitions) Propose(ctx context.Context, project workspace.ProjectID, key string, tr experiments.Transition,
) (experiments.Experiment, error) {
	e, err := t.Current(ctx, project, key)
	if err != nil {
		return e, err
	}
	now := t.start.d.Recorder.now()
	switch tr.Action {
	case experiments.ActionStart:
		facts, factsErr := t.start.facts(ctx, e)
		if factsErr != nil {
			return e, factsErr
		}
		_, err = e.Start(facts, now)
	case experiments.ActionStop:
		_, err = e.Stop(*tr.Decision, now)
		err = under(err, "/decision")
	case experiments.ActionSetTrafficAllocation:
		_, err = e.GrowTrafficAllocation(*tr.TrafficAllocation, now)
	default:
		err = errUnknownTransition
	}
	return e, err
}

// Apply runs tr on project's experiment keyed key, based on its version
// baseVersion, in the unit of work ctx carries, through the handler a
// direct call runs, so the transition, the rules it rewrites and the
// rulesets it moves on commit with the change request's application: a
// refusal rolls it back. by is the member applying, nil for a project token
// or the system, recorded as the writer of the configurations a stop
// rewrites; req is who makes the change, for its audit entries.
func (t *Transitions) Apply(ctx context.Context, project workspace.ProjectID, key string, tr experiments.Transition, baseVersion int64,
	by *access.MemberID, req governance.Request,
) error {
	cmd := ExperimentCommand{Project: project, Key: key, Expected: precondition.On(baseVersion), Request: req}
	var err error
	switch tr.Action {
	case experiments.ActionStart:
		_, err = t.start.Handle(ctx, cmd)
	case experiments.ActionStop:
		_, err = t.stop.Handle(ctx, StopExperimentCommand{Experiment: cmd, Decision: *tr.Decision, Caller: by})
	case experiments.ActionSetTrafficAllocation:
		_, err = t.update.Handle(ctx, UpdateExperimentCommand{
			Project: project, Key: key, Change: experiments.Change{TrafficAllocation: tr.TrafficAllocation},
			Expected: cmd.Expected, Request: req,
		})
	default:
		err = errUnknownTransition
	}
	return err
}

// errUnknownTransition refuses a transition that is none of those a change
// request proposes.
var errUnknownTransition = apperrors.Invalid("invalid_experiment", "the transition is not valid", apperrors.Violation{
	Pointer: "/action", Code: "invalid_value", Detail: "a transition is start, stop or set_traffic_allocation",
})

// under returns err with its violations pointing under prefix, when it is
// a refusal naming fields; err as it is otherwise.
func under(err error, prefix string) error {
	if e, ok := apperrors.From(err); ok && len(e.Violations) > 0 {
		return e.Under(prefix)
	}
	return err
}
