# Flagnetic OpenFeature provider for Go

`github.com/azverinus/flagnetic/sdks/go/openfeature` is an [OpenFeature](https://openfeature.dev) provider over the [Flagnetic Go SDK](../README.md), for a service already standardised on OpenFeature. It is a module of its own, so the SDK itself stays free of the OpenFeature dependency.

## Install

```bash
go get github.com/azverinus/flagnetic/sdks/go/openfeature
```

## Wiring

```go
import (
	of "github.com/open-feature/go-sdk/openfeature"

	flagneticof "github.com/azverinus/flagnetic/sdks/go/openfeature"
)

provider, err := flagneticof.New(os.Getenv("FLAGNETIC_SDK_KEY"), "https://flags.example.com",
	flagneticof.WithUnitType("user"),            // the unit type of the targeting key
	flagneticof.WithReadyTimeout(2*time.Second), // how long Init waits for the first ruleset
	flagneticof.WithClientOptions(flagnetic.WithStore("/var/lib/myservice/flagnetic-ruleset.json")),
)
if err != nil {
	return err // a client SDK key: the Go SDK needs a server key
}
if err := of.SetProviderAndWait(ctx, provider); err != nil {
	return err
}
client := of.NewClient("checkout")

enabled, _ := client.BooleanValue(ctx, "new_checkout", false, of.NewEvaluationContext("u-1001", map[string]any{"plan": "pro"}))
```

- Boolean, string, integer, float and object evaluations go through the SDK's `Detail`. Integers follow the SDK's `Int`: a number that is not whole or does not fit in an `int64` serves the default with `TYPE_MISMATCH`.
- The targeting key is the unit ID of the unit type given with `WithUnitType` (`user` by default); further unit types travel in the evaluation context's `units` attribute, as `map[string]any{"units": map[string]any{"anonymous": "d-8f1c"}}`.
- `Init` waits for the first ruleset up to the ready timeout and never fails: a provider at built-in defaults still serves them. `Shutdown` closes the client, flushing what is queued.
- Events: ready from `Init`, `PROVIDER_CONFIGURATION_CHANGED` with the changed flag keys after every accepted ruleset, `PROVIDER_STALE` when the client falls back to its last good ruleset, and `PROVIDER_ERROR` when the SDK key is refused.
- `provider.Client()` returns the wrapped `*flagnetic.Client`.

## Reasons

Flagnetic reasons map to OpenFeature reasons the same way in every Flagnetic provider, in every language, fixed by [`conformance/openfeature/reasons.json`](../../../conformance/openfeature/reasons.json):

| Flagnetic | OpenFeature |
|---|---|
| `rule`, `prerequisite_failed`, `targeting_match`, `override` | `TARGETING_MATCH` |
| `rollout`, `experiment` | `SPLIT` |
| `fallthrough` | `DEFAULT` |
| `off` | `DISABLED` |
| `error`, `fallback` | `ERROR` |
| `local_override` | `STATIC` |

The Flagnetic reason itself travels in the flag metadata under `flagnetic_reason`, beside `stage`, `ruleset_version`, `variation` and `overridden`. An evaluation an experiment decided is a `SPLIT` whose metadata carries the experiment block: `experiment_key`, `experiment_variation`, `experiment_unit_type`, `experiment_unit_id`, `experiment_inclusion_bucket`, `experiment_variation_bucket` and `experiment_override`.

## Track

```go
client.Track(ctx, "exposure", evalCtx, of.NewTrackingEventDetails(0).Add("flag", "checkout_v2"))
client.Track(ctx, "alias", evalCtx, of.NewTrackingEventDetails(0).
	Add("fromUnitType", "anonymous").Add("fromUnitId", "d-8f1c").
	Add("toUnitType", "user").Add("toUnitId", "u-1001"))
```

`exposure` records the exposure of the most recent evaluation of that flag for that context, as the SDK's `Exposure` does, and `alias` links two unit IDs, as `Alias` does. Any other event name, or one missing an attribute, does nothing. An evaluation alone never records an exposure, unless the flag asks for automatic exposure.
