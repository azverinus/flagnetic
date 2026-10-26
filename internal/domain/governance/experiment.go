package governance

import (
	"slices"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// ExperimentRef names an experiment by identifier, and by its key, which
// never changes.
type ExperimentRef struct {
	ID  experiments.ExperimentID
	Key string
}

// diffOf returns the patch a change request of kind shows its reviewers:
// from base to proposed, or, for an experiment, from base to what the
// transition proposed leaves of it, which is the one kind whose proposal is
// not a document of what it changes.
func diffOf(kind ChangeRequestKind, base, proposed Document) []PatchOperation {
	if kind == KindExperiment {
		return Diff(base, experimentLeaves(base, proposed))
	}
	return Diff(base, proposed)
}

// experimentLeaves returns what the transition proposed leaves of base, an
// experiment's status, traffic allocation and decision with the rules
// serving it in each holding environment by environment key: a start runs
// the experiment; a stop records its decision and makes every holding rule
// serve the winner in place of the experiment, keeping its identifier,
// description and conditions, or removes it when the decision names none;
// a traffic allocation change sets the allocation. The documents are JSON
// values as encoding/json decodes them; a base or a transition not of that
// shape is left as it is.
func experimentLeaves(base, proposed Document) Document {
	state, ok := cloneDocument(base).(map[string]any)
	transition, isTransition := proposed.(map[string]any)
	if !ok || !isTransition {
		return base
	}
	switch transition["action"] {
	case experiments.ActionStart:
		state["status"] = string(experiments.StatusRunning)
	case experiments.ActionSetTrafficAllocation:
		if allocation, set := transition["traffic_allocation"]; set {
			state["traffic_allocation"] = allocation
		}
	case experiments.ActionStop:
		state["status"] = string(experiments.StatusStopped)
		decision, decided := transition["decision"].(map[string]any)
		if !decided {
			break
		}
		state["decision"] = cloneDocument(decision)
		winner, won := decision["winner_variation"].(string)
		rules, _ := state["rules"].(map[string]any)
		for env, held := range rules {
			list, _ := held.([]any)
			out := make([]any, 0, len(list))
			for _, r := range list {
				if rule, isRule := r.(map[string]any); isRule && won && winner != "" {
					rule["serve"] = map[string]any{"kind": flags.ServeVariation, "variation": winner}
					out = append(out, rule)
				}
			}
			rules[env] = out
		}
	}
	return state
}

// cloneDocument returns a deep copy of d, so a document derived from it
// never changes it.
func cloneDocument(d Document) Document {
	switch v := d.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = cloneDocument(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = cloneDocument(e)
		}
		return out
	}
	return d
}

// sameEnvironments reports whether a and b hold the same environments,
// whatever their order.
func sameEnvironments(a, b []workspace.EnvironmentID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, e := range a {
		if !slices.Contains(b, e) {
			return false
		}
	}
	return true
}
