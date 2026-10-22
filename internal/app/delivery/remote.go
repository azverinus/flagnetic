package delivery

import (
	"context"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/shared/clock"
)

// EvaluateRemotely evaluates an environment's ruleset for a context an SDK
// posts, for an SDK that holds no ruleset: a client SDK, or a server SDK
// that prefers to ask.
type EvaluateRemotely struct {
	rulesets    Rulesets
	discoveries *Discoveries
	counts      *RemoteEvaluationCounts
	clock       clock.Clock
}

// NewEvaluateRemotely returns the handler, evaluating the rulesets this
// replica holds, noting the undeclared attributes of each context in
// discoveries and counting each evaluation in counts.
func NewEvaluateRemotely(rulesets Rulesets, discoveries *Discoveries, counts *RemoteEvaluationCounts, c clock.Clock) *EvaluateRemotely {
	return &EvaluateRemotely{rulesets: rulesets, discoveries: discoveries, counts: counts, clock: c}
}

// RemoteQuery is a context posted for remote evaluation, and the flags to
// evaluate: every flag when Flags is nil.
type RemoteQuery struct {
	Context delivery.RemoteContext
	Flags   []string
	// SDK is the SDK asking, as its User-Agent names it or else its context.
	SDK delivery.SDKIdentity
}

// appVersionAttribute is the standard attribute naming the version of the
// app a context comes from.
const appVersionAttribute = "app_version"

// RemoteEvaluation is what a remote evaluation answers.
type RemoteEvaluation struct {
	RulesetVersion int64
	Environment    string
	Protected      bool
	// Results are by flag key, masked for a client key.
	Results map[string]eval.Result
	// Ignored are the attributes left out of the evaluation, by name.
	Ignored []delivery.IgnoredAttribute
}

// Handle refuses a context over the limits with invalid_context, then
// evaluates the server ruleset of key's environment, private segments
// included, which never leaves the server: attributes typed against the
// declared ones, a flag asked for that the ruleset does not hold left out.
// A client key's results are masked so that it learns the values served
// and not how they were reached; a server key's carry the true reason. It
// records no exposure, an exposure being the SDK's explicit call; the
// context's undeclared attributes, key and type only, are noted as
// discovered, and the evaluations counted by their true reason, before the
// mask.
func (h *EvaluateRemotely) Handle(ctx context.Context, key delivery.SDKKey, q RemoteQuery) (RemoteEvaluation, error) {
	if err := delivery.CheckRemoteContext(q.Context); err != nil {
		return RemoteEvaluation{}, err
	}
	rs, err := h.rulesets.Ruleset(ctx, key.Environment)
	if err != nil {
		return RemoteEvaluation{}, err
	}
	evaluable := rs.Evaluable
	if evaluable == nil {
		if evaluable, err = eval.Decode(rs.Document); err != nil {
			return RemoteEvaluation{}, err
		}
	}
	attributes, ignored := delivery.TypeAttributes(evaluable, q.Context.Attributes)
	units := make(map[string]string, len(q.Context.Units))
	for _, u := range q.Context.Units {
		units[u.Type] = u.ID
	}
	var keys []string
	if q.Flags != nil {
		keys = make([]string, 0, len(q.Flags))
		for _, k := range q.Flags {
			if _, ok := evaluable.Flag(k); ok {
				keys = append(keys, k)
			}
		}
	}
	out := RemoteEvaluation{
		RulesetVersion: rs.Version, Protected: evaluable.Protected(), Ignored: ignored,
		Results: make(map[string]eval.Result),
	}
	if evaluable.Environment != nil {
		out.Environment = evaluable.Environment.Key
	}
	counted := make(map[string]eval.Result)
	for k, r := range delivery.Evaluate(evaluable, eval.Context{Units: units, Attributes: attributes}, keys) {
		counted[k] = r.Result
		if key.Kind == delivery.SDKKeyClient {
			r.Result = delivery.MaskForClient(r.Result)
		}
		out.Results[k] = r.Result
	}
	appVersion, _ := q.Context.Attributes[appVersionAttribute].(string)
	h.counts.Add(delivery.RemoteEvaluator{Key: key, SDK: q.SDK, AppVersion: appVersion}, rs.Version, counted)
	h.discoveries.Note(key.Project, h.clock.Now(), delivery.ObserveUndeclared(evaluable, q.Context.Attributes))
	return out, nil
}
