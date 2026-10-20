package flagnetic

import (
	"time"

	"github.com/azverinus/flagnetic/eval"
)

// Context is the set of attributes describing what an evaluation is for:
// unit IDs by unit type, and attributes. Units may be empty; then only
// attribute rules apply, and rollouts, experiments and overrides are
// skipped.
//
// Attribute values are string, number, bool, a list of strings, or for
// semantic versions and dates a string; the attribute type the project
// declares decides the comparison.
type Context struct {
	Units      map[string]string `json:"units,omitempty"`
	Attributes map[string]any    `json:"attributes,omitempty"`
}

func (c Context) eval() eval.Context {
	return eval.Context{Units: c.Units, Attributes: c.Attributes}
}

// Reason is the evaluation reason of a Detail, with its wire name.
type Reason string

// Evaluation reasons. The evaluator produces the first seven; the SDK
// produces local_override and fallback. No getter returns the evaluator's
// error reason: an evaluator error serves the built-in default with reason
// fallback and the evaluator's error code.
const (
	ReasonRule               Reason = "rule"
	ReasonRollout            Reason = "rollout"
	ReasonExperiment         Reason = "experiment"
	ReasonOverride           Reason = "override"
	ReasonPrerequisiteFailed Reason = "prerequisite_failed"
	ReasonOff                Reason = "off"
	ReasonFallthrough        Reason = "fallthrough"
	ReasonLocalOverride      Reason = "local_override"
	ReasonFallback           Reason = "fallback"
)

// Stage is the step of the fallback chain a client evaluates against.
type Stage string

// Stages of the fallback chain.
const (
	// StageBuiltIn: no ruleset was ever received; getters serve built-in
	// defaults.
	StageBuiltIn Stage = "built_in"
	// StageLastGood: the held ruleset came from an earlier success and the
	// most recent attempt failed.
	StageLastGood Stage = "last_good"
	// StageFresh: the most recent attempt succeeded, a 304 included. An
	// offline client is always fresh.
	StageFresh Stage = "fresh"
)

// ErrorCode says why a Detail has reason fallback.
type ErrorCode string

// Error codes of a fallback. The first four are the evaluator's.
const (
	ErrorFlagNotFound        ErrorCode = "flag_not_found"
	ErrorServerOnly          ErrorCode = "server_only"
	ErrorPrerequisiteCycle   ErrorCode = "prerequisite_cycle"
	ErrorInconsistentRuleset ErrorCode = "inconsistent_ruleset"
	// ErrorTypeMismatch: the flag's type differs from the getter's.
	ErrorTypeMismatch ErrorCode = "type_mismatch"
	// ErrorNoRuleset: stage built_in, nothing to evaluate against.
	ErrorNoRuleset ErrorCode = "no_ruleset"
	// ErrorGeneral: an internal failure caught at the API boundary.
	ErrorGeneral ErrorCode = "general"
)

// Detail is a served value and why it was served.
type Detail struct {
	// Value is the served value: bool, string, float64 or json.RawMessage
	// by flag type, or the built-in default as passed with reason fallback.
	Value     any    `json:"value"`
	Reason    Reason `json:"reason"`
	Variation string `json:"variation,omitempty"`
	// RuleID is the rule that served, for reasons rule, rollout and
	// experiment.
	RuleID string `json:"rule_id,omitempty"`
	// ErrorCode is set with reason fallback.
	ErrorCode ErrorCode `json:"error_code,omitempty"`
	// RulesetVersion and ReceivedAt describe the ruleset evaluated against;
	// zero at stage built_in. The age of a last good ruleset is observable
	// through ReceivedAt.
	RulesetVersion int64     `json:"ruleset_version"`
	ReceivedAt     time.Time `json:"received_at"`
	Stage          Stage     `json:"stage"`
	// Overridden is true when a platform override decided the value,
	// directly or through a prerequisite (the evaluator's
	// override_influenced). A local override is reason local_override
	// instead.
	Overridden bool `json:"overridden"`
	// Experiment is present when the value came from a running experiment,
	// including an override landing in one.
	Experiment *Experiment `json:"experiment,omitempty"`
}

// Experiment is the experiment block of a Detail, in the one shape shared
// by local and remote evaluation.
type Experiment struct {
	Key string `json:"key"`
	// Variation is the served variation: the assignment, or the override's
	// variation when an override decided the value.
	Variation       string `json:"variation"`
	UnitType        string `json:"unit_type"`
	UnitID          string `json:"unit_id"`
	InclusionSalt   string `json:"inclusion_salt"`
	VariationSalt   string `json:"variation_salt"`
	InclusionBucket int    `json:"inclusion_bucket"`
	VariationBucket int    `json:"variation_bucket"`
	// Override is true when an override decided the value; exposures
	// carrying it are excluded from analysis.
	Override bool `json:"override"`
}

func experimentOf(e *eval.ExperimentDetail) *Experiment {
	if e == nil {
		return nil
	}
	return &Experiment{
		Key:             e.Key,
		Variation:       e.Variation,
		UnitType:        e.UnitType,
		UnitID:          e.UnitID,
		InclusionSalt:   e.InclusionSalt,
		VariationSalt:   e.VariationSalt,
		InclusionBucket: e.InclusionBucket,
		VariationBucket: e.VariationBucket,
		Override:        e.Override,
	}
}
