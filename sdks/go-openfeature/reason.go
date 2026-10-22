package openfeature

import (
	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// reasonMetadataKey is the flag metadata key carrying the Flagnetic
// evaluation reason, which OpenFeature's coarser reason cannot tell apart:
// a rule and a failed prerequisite are both TARGETING_MATCH.
const reasonMetadataKey = "flagnetic_reason"

// reasons maps every Flagnetic evaluation reason to its OpenFeature reason,
// as conformance/openfeature/reasons.json fixes it for every provider in
// every language, so dashboards agree across languages. targeting_match is
// what a client key is told in place of the reasons targeting decided, and
// error is the evaluator's reason a getter turns into fallback.
var reasons = map[flagnetic.Reason]of.Reason{
	flagnetic.ReasonRule:               of.TargetingMatchReason,
	flagnetic.ReasonPrerequisiteFailed: of.TargetingMatchReason,
	"targeting_match":                  of.TargetingMatchReason,
	flagnetic.ReasonOverride:           of.TargetingMatchReason,
	flagnetic.ReasonRollout:            of.SplitReason,
	flagnetic.ReasonExperiment:         of.SplitReason,
	flagnetic.ReasonFallthrough:        of.DefaultReason,
	flagnetic.ReasonOff:                of.DisabledReason,
	"error":                            of.ErrorReason,
	flagnetic.ReasonFallback:           of.ErrorReason,
	flagnetic.ReasonLocalOverride:      of.StaticReason,
}

// reasonOf maps a Flagnetic evaluation reason to an OpenFeature one. A
// reason a newer server adds maps to UNKNOWN rather than being
// misrepresented as one of the known ones; the Flagnetic reason itself
// still travels in the flag metadata.
func reasonOf(r flagnetic.Reason) of.Reason {
	if mapped, ok := reasons[r]; ok {
		return mapped
	}
	return of.UnknownReason
}

// resolutionError maps a fallback's error code to an OpenFeature
// ResolutionError: FLAG_NOT_FOUND and TYPE_MISMATCH keep their identity,
// everything else (prerequisite_cycle, inconsistent_ruleset, no_ruleset,
// general) is GENERAL.
func resolutionError(code flagnetic.ErrorCode) of.ResolutionError {
	switch code {
	case flagnetic.ErrorFlagNotFound:
		return of.NewFlagNotFoundResolutionError(string(code))
	case flagnetic.ErrorTypeMismatch:
		return of.NewTypeMismatchResolutionError(string(code))
	default:
		return of.NewGeneralResolutionError(string(code))
	}
}
