package eval

import (
	"encoding/json"
)

// Context is what a flag is evaluated for: unit IDs by unit type, and
// attributes. A context with no units is valid: attribute rules apply;
// rollouts, experiments and overrides are skipped. An empty unit ID counts
// as absent.
//
// Attribute values are JSON-shaped: string, bool, float64 (any Go number
// or json.Number is accepted), a list of strings ([]string or []any), or
// for dates an RFC 3339 string, integral Unix epoch milliseconds or a
// time.Time.
type Context struct {
	Units      map[string]string `json:"units"`
	Attributes map[string]any    `json:"attributes"`
}

func (c Context) unit(unitType string) (string, bool) {
	id := c.Units[unitType]
	return id, id != ""
}

// Reason is the evaluation reason of a result. fallback and local_override
// are produced by SDKs, never by the evaluator.
type Reason string

// Evaluation reasons.
const (
	ReasonOff                Reason = "off"
	ReasonOverride           Reason = "override"
	ReasonRule               Reason = "rule"
	ReasonRollout            Reason = "rollout"
	ReasonExperiment         Reason = "experiment"
	ReasonFallthrough        Reason = "fallthrough"
	ReasonPrerequisiteFailed Reason = "prerequisite_failed"
	ReasonError              Reason = "error"
)

// ErrorCode says why a result has reason error.
type ErrorCode string

// Error codes.
const (
	ErrorFlagNotFound        ErrorCode = "flag_not_found"
	ErrorPrerequisiteCycle   ErrorCode = "prerequisite_cycle"
	ErrorInconsistentRuleset ErrorCode = "inconsistent_ruleset"
)

// Result is what evaluating one flag for one context served, and why. Its JSON
// form is what the conformance fixtures compare, field for field.
type Result struct {
	// Type is the flag's type. It is empty only when there is no flag to
	// read it from: an error with ErrorFlagNotFound.
	Type string `json:"type"`
	// Value is the served variation's value; absent for errors.
	Value json.RawMessage `json:"value,omitempty"`
	// Variation is the served variation's key; absent for errors.
	Variation string `json:"variation,omitempty"`
	Reason    Reason `json:"reason"`
	// RuleID is the rule that served, for reasons rule, rollout and
	// experiment.
	RuleID string `json:"rule_id,omitempty"`
	// AutomaticExposure is the flag's setting: with it, an SDK emits an
	// exposure after every evaluation carrying Experiment, without a
	// second call. It travels with every result of a known flag, so the
	// result alone says what to do.
	AutomaticExposure bool `json:"automatic_exposure"`
	// Experiment is present with reason experiment, and with reason
	// override when the override's shadow evaluation lands in a running
	// experiment that includes the unit.
	Experiment *ExperimentDetail `json:"experiment,omitempty"`
	// Prerequisite is the flag-level prerequisite that failed, for reason
	// prerequisite_failed.
	Prerequisite string `json:"prerequisite,omitempty"`
	// Bucket is the unit's bucket for reason rollout.
	Bucket    *int      `json:"bucket,omitempty"`
	ErrorCode ErrorCode `json:"error_code,omitempty"`
	// OverrideInfluenced is true when an override decided the result, or
	// any prerequisite sub-evaluation consulted on the way was
	// override-influenced, whatever the final reason. Every exposure and
	// event derived from the result carries it.
	OverrideInfluenced bool `json:"override_influenced"`
}

// MarshalJSON encodes a result. Type and AutomaticExposure are the flag's,
// so both are left out when there is no flag to read them from. That is
// exactly a result ofFlag never stamped, and ErrorFlagNotFound marks it:
// flag returns that code only for a key its index does not hold, and every
// result it stamps keeps the flag it was stamped with.
func (r Result) MarshalJSON() ([]byte, error) {
	type fields Result
	if r.ErrorCode != ErrorFlagNotFound {
		return json.Marshal(fields(r))
	}
	return json.Marshal(struct {
		fields
		Type              *string `json:"type,omitempty"`
		AutomaticExposure *bool   `json:"automatic_exposure,omitempty"`
	}{fields(r), nil, nil})
}

// ExperimentDetail is everything an SDK needs to report an exposure: the
// one shape of experiment detail shared by local and remote evaluation.
type ExperimentDetail struct {
	Key string `json:"key"`
	// Variation is the served variation: the assignment, or the override's
	// variation when an override decided the result.
	Variation       string `json:"variation"`
	UnitType        string `json:"unit_type"`
	UnitID          string `json:"unit_id"`
	InclusionSalt   string `json:"inclusion_salt"`
	VariationSalt   string `json:"variation_salt"`
	InclusionBucket int    `json:"inclusion_bucket"`
	VariationBucket int    `json:"variation_bucket"`
	// Override equals the result's OverrideInfluenced; exposures carrying
	// it are excluded from analysis.
	Override bool `json:"override"`
}

// Evaluate computes one flag for a context. It never reads a clock, never
// records an exposure and never fails: problems are results with reason
// error.
func (rs *Ruleset) Evaluate(ctx Context, flag string) Result {
	return rs.newEvaluation(ctx).flag(flag)
}

// EvaluateAll evaluates every flag of the ruleset for a context, keyed by
// flag key. Prerequisite results are memoized within the call.
func (rs *Ruleset) EvaluateAll(ctx Context) map[string]Result {
	e := rs.newEvaluation(ctx)
	out := make(map[string]Result, len(rs.Flags))
	for _, f := range rs.Flags {
		out[f.Key] = e.flag(f.Key)
	}
	return out
}

// evaluation is the state of one Evaluate or EvaluateAll call: the flags
// on the current prerequisite path, and every flag result computed so far.
type evaluation struct {
	idx      *index
	ctx      Context
	visiting map[string]bool
	memo     map[string]Result
	computed int // flag computations, for tests of memoization
}

func (rs *Ruleset) newEvaluation(ctx Context) *evaluation {
	return &evaluation{
		idx:      rs.index,
		ctx:      ctx,
		visiting: map[string]bool{},
		memo:     map[string]Result{},
	}
}

func errorResult(code ErrorCode, influenced bool) Result {
	return Result{Reason: ReasonError, ErrorCode: code, OverrideInfluenced: influenced}
}

// ofFlag stamps a result with the flag it is about. Every step below
// leaves Type and AutomaticExposure alone, so one place fills them in for
// every reason a known flag can carry.
func ofFlag(f *compiledFlag, r Result) Result {
	r.Type, r.AutomaticExposure = f.Type, f.AutomaticExposure
	return r
}

// flag evaluates a flag by key, steps 1–7. A flag reached again while it is
// on the current prerequisite path is a cycle.
func (e *evaluation) flag(key string) Result {
	if r, ok := e.memo[key]; ok {
		return r
	}
	f, ok := e.idx.flags[key]
	if !ok {
		return errorResult(ErrorFlagNotFound, false)
	}
	if e.visiting[key] {
		return ofFlag(f, errorResult(ErrorPrerequisiteCycle, false))
	}
	e.visiting[key] = true
	r := ofFlag(f, e.compute(f))
	delete(e.visiting, key)
	e.memo[key] = r
	e.computed++
	return r
}

func (e *evaluation) compute(f *compiledFlag) Result {
	// 2. Off.
	if !f.On {
		return e.serve(f, f.OffVariation, Result{Reason: ReasonOff})
	}
	// 3. Overrides, then the same steps in shadow for experiment detail.
	for _, o := range f.Overrides {
		if id, ok := e.ctx.unit(o.UnitType); !ok || id != o.UnitID {
			continue
		}
		r := e.serve(f, o.Variation, Result{Reason: ReasonOverride, OverrideInfluenced: true})
		if r.Reason == ReasonError {
			return r
		}
		if shadow := e.body(f); shadow.Reason == ReasonExperiment {
			x := *shadow.Experiment
			x.Variation = r.Variation
			x.Override = true
			r.Experiment = &x
		}
		return r
	}
	// 4–7.
	return e.body(f)
}

// body runs steps 4 to 6: prerequisites, rules and the fallthrough.
func (e *evaluation) body(f *compiledFlag) Result {
	influenced := false

	// 4. Flag-level prerequisites in order; the first failure serves the
	// off variation.
	for _, p := range f.Prerequisites {
		held, err := e.prerequisite(p.Flag, p.Variation, &influenced)
		if err != nil {
			return errorResult(*err, influenced)
		}
		if !held {
			return e.serve(f, f.OffVariation, Result{Reason: ReasonPrerequisiteFailed, Prerequisite: p.Flag, OverrideInfluenced: influenced})
		}
	}

	// 5. Rules in order; conditions in order, stopping at the first false.
	for i, rule := range f.Rules {
		matched, err := e.conditions(f.rules[i], &influenced)
		if err != nil {
			return errorResult(*err, influenced)
		}
		if !matched {
			continue
		}
		if f.invalidServes[i] {
			return errorResult(ErrorInconsistentRuleset, influenced)
		}
		s := rule.Serve
		switch s.Kind {
		case ServeVariation:
			return e.serve(f, s.Variation, Result{Reason: ReasonRule, RuleID: rule.ID, OverrideInfluenced: influenced})
		case ServeRollout:
			id, ok := e.ctx.unit(s.UnitType)
			if !ok {
				continue
			}
			b := Bucket(s.Salt, id)
			if b >= s.Percentage {
				continue
			}
			return e.serve(f, s.Variation, Result{Reason: ReasonRollout, RuleID: rule.ID, Bucket: &b, OverrideInfluenced: influenced})
		case ServeExperiment:
			id, ok := e.ctx.unit(s.UnitType)
			if !ok {
				continue
			}
			inclusion := Bucket(s.InclusionSalt, id)
			if inclusion >= s.TrafficAllocation {
				continue
			}
			variationBucket := Bucket(s.VariationSalt, id)
			assigned, ok := assign(s.Weights, variationBucket)
			if !ok {
				return errorResult(ErrorInconsistentRuleset, influenced)
			}
			r := e.serve(f, assigned, Result{Reason: ReasonExperiment, RuleID: rule.ID, OverrideInfluenced: influenced})
			if r.Reason == ReasonError {
				return r
			}
			r.Experiment = &ExperimentDetail{
				Key:             s.Experiment,
				Variation:       assigned,
				UnitType:        s.UnitType,
				UnitID:          id,
				InclusionSalt:   s.InclusionSalt,
				VariationSalt:   s.VariationSalt,
				InclusionBucket: inclusion,
				VariationBucket: variationBucket,
				Override:        influenced,
			}
			return r
		default:
			return errorResult(ErrorInconsistentRuleset, influenced)
		}
	}

	// 6. Fallthrough.
	return e.serve(f, f.Fallthrough, Result{Reason: ReasonFallthrough, OverrideInfluenced: influenced})
}

// assign walks weights in list order; variation i covers
// [sum(w_0..w_{i-1}), sum(w_0..w_i)).
func assign(weights []Weight, bucket int) (string, bool) {
	upper := 0
	for _, w := range weights {
		upper += w.Weight
		if bucket < upper {
			return w.Variation, true
		}
	}
	return "", false
}

// serve fills r with a variation of f, or turns it into
// inconsistent_ruleset when the variation is missing (step 7).
func (e *evaluation) serve(f *compiledFlag, variation string, r Result) Result {
	v, ok := f.variations[variation]
	if !ok {
		return errorResult(ErrorInconsistentRuleset, r.OverrideInfluenced)
	}
	r.Value, r.Variation = v, variation
	return r
}

// prerequisite evaluates another flag for the same context. It holds when
// that flag's reason is neither off nor error and it serves the required
// variation. An error in the prerequisite is a defect of the ruleset and is
// returned, so it surfaces on the dependent flag instead of silently
// serving its off variation. Influence is accumulated into influenced.
func (e *evaluation) prerequisite(key, variation string, influenced *bool) (bool, *ErrorCode) {
	f, ok := e.idx.flags[key]
	if !ok || !hasVariation(f, variation) {
		return false, errCode(ErrorInconsistentRuleset)
	}
	r := e.flag(key)
	*influenced = *influenced || r.OverrideInfluenced
	if r.Reason == ReasonError {
		return false, errCode(r.ErrorCode)
	}
	return r.Reason != ReasonOff && r.Variation == variation, nil
}

func hasVariation(f *compiledFlag, key string) bool {
	_, ok := f.variations[key]
	return ok
}

// conditions evaluates an AND-set in order and stops at the first false
// condition; later conditions are not evaluated and cannot influence the
// result.
func (e *evaluation) conditions(conds []compiledCondition, influenced *bool) (bool, *ErrorCode) {
	for _, c := range conds {
		held, err := e.condition(c, influenced)
		if err != nil || !held {
			return false, err
		}
	}
	return true, nil
}

func (e *evaluation) condition(c compiledCondition, influenced *bool) (bool, *ErrorCode) {
	if c.err != nil {
		return false, errCode(ErrorInconsistentRuleset)
	}
	switch c.kind {
	case ConditionAttribute:
		var v any
		if c.unit {
			if id, ok := e.ctx.unit(c.attribute); ok {
				v = id
			}
		} else {
			v = e.ctx.Attributes[c.attribute]
		}
		return c.match(v), nil
	case ConditionInSegment, ConditionNotInSegment:
		s, ok := e.idx.segments[c.segment]
		if !ok {
			return false, errCode(ErrorInconsistentRuleset)
		}
		in, err := e.conditions(s.conditions, influenced)
		if err != nil {
			return false, err
		}
		return in == (c.kind == ConditionInSegment), nil
	default: // prerequisite
		return e.prerequisite(c.flag, c.variation, influenced)
	}
}

func errCode(c ErrorCode) *ErrorCode { return &c }
