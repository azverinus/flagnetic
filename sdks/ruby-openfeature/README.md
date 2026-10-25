# Flagnetic OpenFeature provider for Ruby

`flagnetic-openfeature` is an [OpenFeature](https://openfeature.dev) provider over the [Flagnetic Ruby SDK](../ruby/README.md), for a service already standardised on OpenFeature. It is a gem of its own, so `flagnetic` itself stays free of the OpenFeature dependency.

Requires Ruby 3.4 or later and `openfeature-sdk` 0.6.1 or a later 0.x release.

## Install

```bash
bundle add flagnetic-openfeature
```

## Wiring

```ruby
require "flagnetic/openfeature"

provider = Flagnetic::OpenFeature::Provider.new(
  ENV.fetch("FLAGNETIC_SDK_KEY"),
  base_url: "https://flags.example.com",
  unit_type: "user",  # the unit type of the targeting key
  ready_timeout: 2,   # seconds init waits for the first ruleset
  store: "/var/lib/myservice/flagnetic-ruleset.json" # any further option goes to Flagnetic::Client.new
)
OpenFeature::SDK.configure { |config| config.set_provider_and_wait(provider) }
client = OpenFeature::SDK.build_client

context = OpenFeature::SDK::EvaluationContext.new(targeting_key: "u-1001", plan: "pro")
client.fetch_boolean_value(flag_key: "new_checkout", default_value: false, evaluation_context: context)
```

- Boolean, string, number, integer, float and object evaluations go through the client's `detail`. Integers follow its `int` getter: a number that is not whole serves the default with `TYPE_MISMATCH`. Floats serve a whole number as a `Float`.
- The targeting key is the unit ID of `unit_type` (`user` by default); further unit types travel in the evaluation context's `units` attribute, as `units: { "anonymous" => "d-8f1c" }`. Every other attribute is an attribute of the Flagnetic context.
- `init` waits for the first ruleset up to `ready_timeout` and never fails: a provider at built-in defaults still serves them. `shutdown` closes the client, flushing what is queued.
- `provider.client` returns the wrapped `Flagnetic::Client`: local overrides, the recorder of an offline client (`offline:` for tests), flush and status.
- A `logger:` keeps receiving every log event of the client.

## Events

| When | Event |
|---|---|
| `init` returns | `PROVIDER_READY`, or `PROVIDER_ERROR` when the SDK key was refused; `PROVIDER_STALE` follows when only a last good ruleset is held |
| A ruleset is accepted, or a local override changes | `PROVIDER_CONFIGURATION_CHANGED` with the keys of the flags that may have changed |
| The client falls back to its last good ruleset | `PROVIDER_STALE` |
| The client is fresh again | `PROVIDER_READY` |
| The SDK key is refused | `PROVIDER_ERROR` (`GENERAL`), kept until the key is accepted again |

With `openfeature-sdk` releases before 0.7 the SDK itself sends `PROVIDER_READY` once `init` returns, so a key refused before then reads as ready until the client recovers.

## Reasons

Flagnetic reasons map to OpenFeature reasons the same way in every Flagnetic provider, in every language, fixed by [`conformance/openfeature/reasons.json`](../../conformance/openfeature/reasons.json):

| Flagnetic | OpenFeature |
|---|---|
| `rule`, `prerequisite_failed`, `targeting_match`, `override` | `TARGETING_MATCH` |
| `rollout`, `experiment` | `SPLIT` |
| `fallthrough` | `DEFAULT` |
| `off` | `DISABLED` |
| `error`, `fallback` | `ERROR` |
| `local_override` | `STATIC` |

A fallback is an `ERROR` whose error code is `FLAG_NOT_FOUND`, `TYPE_MISMATCH` or `GENERAL`, with the Flagnetic error code as its message. The Flagnetic reason itself travels in the flag metadata under `flagnetic_reason`, beside `stage`, `ruleset_version`, `variation` and `overridden` (`ruleset_version` and `variation` only when there is one). An evaluation an experiment decided is a `SPLIT` whose metadata carries the experiment block: `experiment_key`, `experiment_variation`, `experiment_unit_type`, `experiment_unit_id`, `experiment_inclusion_bucket`, `experiment_variation_bucket` and `experiment_override`.

## Track

```ruby
client.track("exposure", evaluation_context: context,
                         tracking_event_details: OpenFeature::SDK::TrackingEventDetails.new(flag: "checkout_v2"))
client.track("alias", tracking_event_details: OpenFeature::SDK::TrackingEventDetails.new(
  fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001"
))
```

`exposure` records the exposure of the most recent evaluation of that flag for that context, as the client's `exposure` does, and `alias` links two unit IDs, as `alias` does. Any other event name, or one missing an attribute, does nothing. An evaluation alone never records an exposure, unless the flag asks for automatic exposure.
