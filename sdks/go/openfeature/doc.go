// Package openfeature is a thin OpenFeature provider over the Flagnetic Go
// SDK: a team already on OpenFeature adds this provider beside the SDK. It
// is its own Go module so the SDK module stays free of third-party
// dependencies.
//
// Flagnetic reasons map to OpenFeature reasons as every Flagnetic provider
// maps them: rule, prerequisite_failed, targeting_match and override are
// TARGETING_MATCH; rollout and experiment SPLIT; fallthrough DEFAULT; off
// DISABLED; error and fallback ERROR; local_override STATIC. The Flagnetic
// reason itself travels in the flag metadata under flagnetic_reason.
//
// New builds and owns the wrapped *flagnetic.Client; Client returns it, so
// the application can call Exposure and Alias natively wherever
// OpenFeature's Track has no reach.
//
//	provider, err := openfeature.New(sdkKey, baseURL, openfeature.WithUnitType("user"))
//	of.SetProviderAndWait(context.Background(), provider)
//	client := of.NewClient("checkout")
//	client.BooleanValueDetails(ctx, "new_checkout", false, of.NewEvaluationContext("u_1", nil))
//
// (aliasing the upstream SDK's own "openfeature" package as of avoids the
// name clash with this package.)
package openfeature
