package governance

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// transitionDoc is what an experiment change request proposes: one
// transition, as the API answers `proposed`. A stop carries its decision; a
// growth the traffic allocation it grows to.
type transitionDoc struct {
	Action            string       `json:"action"`
	Decision          *decisionDoc `json:"decision,omitempty"`
	TrafficAllocation *int         `json:"traffic_allocation,omitempty"`
}

// decisionDoc is a stop's decision, as an experiment's is answered: the
// winner is null for a removal.
type decisionDoc struct {
	Outcome         string  `json:"outcome"`
	WinnerVariation *string `json:"winner_variation"`
	Note            string  `json:"note"`
}

// experimentStateDoc is what an experiment change request is based on and
// diffed from: the experiment's status, traffic allocation and decision,
// and the rules serving it in each environment holding one, by environment
// key, which is what a transition changes of the rulesets reviewers guard.
type experimentStateDoc struct {
	Status            string               `json:"status"`
	TrafficAllocation int                  `json:"traffic_allocation"`
	Decision          *decisionDoc         `json:"decision,omitempty"`
	Rules             map[string][]ruleDoc `json:"rules"`
}

// checkTransition returns invalid_change_request naming what t lacks: a
// known action, a stop's decision, a growth's traffic allocation. What the
// transition is refused for beyond that is the experiments context's.
func checkTransition(t experiments.Transition) error {
	var v *apperrors.Violation
	switch t.Action {
	case experiments.ActionStart:
	case experiments.ActionStop:
		if t.Decision == nil {
			v = &apperrors.Violation{Pointer: "/proposed/decision", Code: "required", Detail: "a stop records a decision"}
		}
	case experiments.ActionSetTrafficAllocation:
		if t.TrafficAllocation == nil {
			v = &apperrors.Violation{
				Pointer: "/proposed/traffic_allocation", Code: "required", Detail: "a growth names the traffic allocation it grows to",
			}
		}
	default:
		v = &apperrors.Violation{
			Pointer: "/proposed/action", Code: "invalid_value", Detail: "a transition is start, stop or set_traffic_allocation",
			Params: map[string]any{"actions": []string{
				experiments.ActionStart, experiments.ActionStop, experiments.ActionSetTrafficAllocation,
			}},
		}
	}
	if v != nil {
		return apperrors.Invalid("invalid_change_request", "the change request is not valid", *v)
	}
	return nil
}

// experimentSubject returns the subject of a change request proposing a
// transition of e, with the rules serving it now and the environments
// holding them.
func (d ChangeRequestDeps) experimentSubject(ctx context.Context, e experiments.Experiment) (governance.Subject, error) {
	held, err := d.Holders.HoldingConfigurations(ctx, e.Flag, e.Key)
	if err != nil {
		return governance.Subject{}, err
	}
	state := experimentStateDoc{
		Status: string(e.Status), TrafficAllocation: e.TrafficAllocation, Decision: decisionDocOf(e.Decision),
		Rules: make(map[string][]ruleDoc, len(held)),
	}
	holders := make([]workspace.EnvironmentID, 0, len(held))
	for _, ec := range held {
		holders = append(holders, ec.Configuration.Environment)
		rules := []ruleDoc{}
		for _, r := range ec.Configuration.Rules {
			if r.Serve.Kind == flags.ServeExperiment && r.Serve.Experiment == e.Key {
				rules = append(rules, ruleDocOf(r))
			}
		}
		state.Rules[ec.Environment] = rules
	}
	base, err := asDocument(state)
	return governance.Subject{
		Kind: governance.KindExperiment, Flag: &governance.FlagRef{ID: e.Flag, Key: e.FlagKey},
		Experiment: &governance.ExperimentRef{ID: e.ID, Key: e.Key}, Base: base, BaseVersion: e.Version, BaseHolders: holders,
	}, err
}

// decisionDocOf returns d as an experiment change request's documents hold
// it, nil for none.
func decisionDocOf(d *experiments.Decision) *decisionDoc {
	if d == nil {
		return nil
	}
	out := &decisionDoc{Outcome: string(d.Outcome), Note: d.Note}
	if d.WinnerVariation != "" {
		winner := d.WinnerVariation
		out.WinnerVariation = &winner
	}
	return out
}

// transitionDocument returns t as the document a change request proposes.
func transitionDocument(t experiments.Transition) (governance.Document, error) {
	d := transitionDoc{Action: t.Action, TrafficAllocation: t.TrafficAllocation}
	if t.Action == experiments.ActionStop {
		d.Decision = decisionDocOf(t.Decision)
	}
	if t.Action != experiments.ActionSetTrafficAllocation {
		d.TrafficAllocation = nil
	}
	return asDocument(d)
}

// experimentTransitionOf returns the transition doc proposes.
func experimentTransitionOf(doc governance.Document) (experiments.Transition, error) {
	var d transitionDoc
	if err := fromDocument(doc, &d); err != nil {
		return experiments.Transition{}, err
	}
	t := experiments.Transition{Action: d.Action, TrafficAllocation: d.TrafficAllocation}
	if d.Decision != nil {
		t.Decision = &experiments.Decision{Outcome: experiments.Outcome(d.Decision.Outcome), Note: d.Decision.Note}
		if d.Decision.WinnerVariation != nil {
			t.Decision.WinnerVariation = *d.Decision.WinnerVariation
		}
	}
	return t, nil
}
