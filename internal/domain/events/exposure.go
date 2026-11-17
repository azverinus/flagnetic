package events

import (
	"fmt"
	"slices"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
)

// ExperimentFacts is as much of an experiment as an exposure is judged
// against: its status, the flag it runs on, the unit type it assigns, and the
// variations it weighs. A started experiment of any status has them, since a
// mobile app may flush its exposures days after the experiment stopped.
type ExperimentFacts struct {
	ID experiments.ExperimentID
	// Status is the experiment's status. A draft is not served to any unit,
	// so no SDK holds an exposure of one.
	Status   experiments.Status
	FlagKey  string
	UnitType string
	// Variations are the variations the experiment weighs.
	Variations []string
	// FlagVariations are all the variations of the experiment's flag, which
	// an override may serve whatever the experiment weighs.
	FlagVariations []string
}

// CheckExposure judges x against the experiment it names, nil when none
// exists, and returns why it is refused, or nil when it agrees. The checks
// run in a fixed order, so every SDK is told the same of the same exposure:
// the experiment exists and has started (a draft assigns no unit, so an
// exposure naming one was made up, and is refused as if no experiment were
// keyed so), the unit type is its own, the variation is one it
// weighs (an override exposure is checked against the flag's variations
// instead, since an override decides the variation whatever the weights),
// then the flag and the evaluation reason.
//
// Every exposure names a variation that exists: the variation is part of
// what is counted and kept for good, and a client key ships inside apps, so
// a variation taken as sent would let anyone holding one grow the counts
// with names of their own making.
//
// The detail names the unit type, flag, variation and status the exposure
// was judged against, so it is for a server key only; ForClient is what a
// client key is told.
func CheckExposure(x Exposure, experiment *ExperimentFacts) *Rejection {
	reject := func(code RejectionCode, detail string) *Rejection {
		return &Rejection{Code: code, Detail: detail}
	}
	switch {
	case experiment == nil || experiment.Status == experiments.StatusDraft:
		return reject(RejectUnknownExperiment, fmt.Sprintf("no experiment is keyed %q", x.ExperimentKey))
	case x.UnitType != experiment.UnitType:
		return reject(RejectUnitTypeMismatch,
			fmt.Sprintf("experiment %q assigns units of type %q, not %q", x.ExperimentKey, experiment.UnitType, x.UnitType))
	case !x.Override && !slices.Contains(experiment.Variations, x.VariationKey):
		return reject(RejectUnknownVariation,
			fmt.Sprintf("experiment %q has no variation %q", x.ExperimentKey, x.VariationKey))
	case x.Override && !slices.Contains(experiment.FlagVariations, x.VariationKey):
		return reject(RejectUnknownVariation,
			fmt.Sprintf("the flag of experiment %q has no variation %q", x.ExperimentKey, x.VariationKey))
	case x.FlagKey != experiment.FlagKey:
		return reject(RejectEventInvalid,
			fmt.Sprintf("flag_key is %q, but experiment %q is on flag %q", x.FlagKey, x.ExperimentKey, experiment.FlagKey))
	case x.Reason != string(eval.ReasonExperiment) && x.Reason != string(eval.ReasonOverride):
		msg := fmt.Sprintf("evaluation_reason is %q, not %q or %q", x.Reason, eval.ReasonExperiment, eval.ReasonOverride)
		return reject(RejectEventInvalid, msg)
	}
	return nil
}

// ForClient is the refusal a client key is told in place of r. A client key
// ships inside apps and browsers, where anyone can read it, and any finer
// answer, whatever its detail, would let it probe which experiments exist
// (a draft included), which unit type they assign, which variations they
// weigh and which flag they run on: every refusal of an exposure is
// answered as if no experiment were keyed so.
func (Rejection) ForClient(experimentKey string) Rejection {
	return Rejection{Code: RejectUnknownExperiment, Detail: fmt.Sprintf("no experiment is keyed %q", experimentKey)}
}

// RejectionCount is how many exposures of an experiment were refused for one
// reason.
type RejectionCount struct {
	Experiment experiments.ExperimentID
	// Reason is the rejection code of the refusal.
	Reason RejectionCode
	Count  int64
}
