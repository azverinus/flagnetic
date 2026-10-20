package openfeature

import (
	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// flagMetadata carries a Detail's Flagnetic reason, stage, ruleset version,
// variation and overridden flag, plus the experiment block when there is
// one, flattened to the scalar string, bool and int64 values FlagMetadata
// supports.
func flagMetadata(d flagnetic.Detail) of.FlagMetadata {
	m := of.FlagMetadata{
		reasonMetadataKey: string(d.Reason),
		"stage":           string(d.Stage),
		"ruleset_version": d.RulesetVersion,
		"variation":       d.Variation,
		"overridden":      d.Overridden,
	}
	if e := d.Experiment; e != nil {
		m["experiment_key"] = e.Key
		m["experiment_variation"] = e.Variation
		m["experiment_unit_type"] = e.UnitType
		m["experiment_unit_id"] = e.UnitID
		m["experiment_inclusion_bucket"] = int64(e.InclusionBucket)
		m["experiment_variation_bucket"] = int64(e.VariationBucket)
		m["experiment_override"] = e.Override
	}
	return m
}

// resolution turns a Detail into a ProviderResolutionDetail: reason fallback
// becomes an OpenFeature ERROR carrying the mapped ResolutionError, anything
// else the mapped reason with its variant.
func resolution(d flagnetic.Detail) of.ProviderResolutionDetail {
	if d.Reason == flagnetic.ReasonFallback {
		return of.ProviderResolutionDetail{
			ResolutionError: resolutionError(d.ErrorCode),
			Reason:          of.ErrorReason,
			FlagMetadata:    flagMetadata(d),
		}
	}
	return of.ProviderResolutionDetail{
		Variant:      d.Variation,
		Reason:       reasonOf(d.Reason),
		FlagMetadata: flagMetadata(d),
	}
}
