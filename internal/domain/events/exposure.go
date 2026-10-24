package events

import (
	"fmt"
	"slices"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
)

// ExperimentFacts is as much of an experiment as an exposure is judged
// against: the flag it runs on, the unit type it assigns, and the variations
// it weighs. An experiment of any status has them, since a mobile app may
// flush its exposures days after the experiment stopped.
type ExperimentFacts struct {
	ID         experiments.ExperimentID
	FlagKey    string
	UnitType   string
	Variations []string
}

// CheckExposure judges x against the experiment it names, nil when none
// exists, and returns why it is refused, or nil when it agrees. The checks
// run in a fixed order, so every SDK is told the same of the same exposure:
// the experiment exists, the unit type is its own, the variation is one it
// weighs (an override exposure is not checked against the weights, since an
// override decides the variation whatever the weights), then the flag and the
// evaluation reason.
//
// A client key ships inside apps and browsers, so what it learns of an
// experiment is its existence by the key it already holds and nothing more:
// with detailed false the detail names no unit type, flag, variation or
// status. The code is the same for both kinds of key.
func CheckExposure(x Exposure, experiment *ExperimentFacts, detailed bool) *Rejection {
	reject := func(code RejectionCode, client, server string) *Rejection {
		if detailed {
			return &Rejection{Code: code, Detail: server}
		}
		return &Rejection{Code: code, Detail: client}
	}
	switch {
	case experiment == nil:
		return reject(RejectUnknownExperiment, fmt.Sprintf("no experiment is keyed %q", x.ExperimentKey),
			fmt.Sprintf("no experiment is keyed %q", x.ExperimentKey))
	case x.UnitType != experiment.UnitType:
		return reject(RejectUnitTypeMismatch, "the unit type is not the experiment's",
			fmt.Sprintf("experiment %q assigns units of type %q, not %q", x.ExperimentKey, experiment.UnitType, x.UnitType))
	case !x.Override && !slices.Contains(experiment.Variations, x.VariationKey):
		return reject(RejectUnknownVariation, "the variation is not one of the experiment's",
			fmt.Sprintf("experiment %q has no variation %q", x.ExperimentKey, x.VariationKey))
	case x.FlagKey != experiment.FlagKey:
		return reject(RejectEventInvalid, "the flag is not the experiment's",
			fmt.Sprintf("flag_key is %q, but experiment %q is on flag %q", x.FlagKey, x.ExperimentKey, experiment.FlagKey))
	case x.Reason != string(eval.ReasonExperiment) && x.Reason != string(eval.ReasonOverride):
		msg := fmt.Sprintf("evaluation_reason is %q, not %q or %q", x.Reason, eval.ReasonExperiment, eval.ReasonOverride)
		return reject(RejectEventInvalid, msg, msg)
	}
	return nil
}

// RejectionCount is how many exposures of an experiment were refused for one
// reason.
type RejectionCount struct {
	Experiment experiments.ExperimentID
	// Reason is the rejection code of the refusal.
	Reason RejectionCode
	Count  int64
}
