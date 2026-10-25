# flagnetic

Python SDK for [Flagnetic](https://github.com/azverinus/flagnetic), self-hosted feature flags and experiments.

```bash
pip install flagnetic
```

It needs Python 3.11 or later and installs no dependency.

## Quick start

```python
import flagnetic

client = flagnetic.Client("<server SDK key>", "https://flags.example.com")
client.ready(timeout=5)  # fresh, last_good or built_in; never raises

context = flagnetic.Context(units={"user": "u-1001"}, attributes={"plan": "pro"})
if client.get_bool(context, "new-checkout", False):
    ...

client.close()
```

The getters never block and never raise: they read the ruleset the client holds and serve the default you pass when
they cannot serve the flag. One background thread fetches the ruleset with `If-None-Match`, holds the ruleset stream
(a kill switch reaches the client within two seconds), polls while the stream is down, and backs off with jitter.
`store="/var/lib/app/flagnetic.json"` keeps the last good ruleset across restarts.

## Experiments and events

The same thread reports an evaluation summary every minute (counts per flag, variation and reason, never a unit ID or
a value) and sends exposures and aliases every two seconds, retrying with the same event IDs so the server drops a
resend. An evaluation alone is not an exposure: call `exposure` where the unit actually meets the behaviour, or turn
on automatic exposure for the flag.

```python
price = client.get_string(context, "pricing", "Built-in price")
client.exposure(context, "pricing")  # reports the assignment just served
client.alias("anonymous", "d-8f1c", "user", "u-1001")  # after sign-up
client.flush(timeout=5)  # close() flushes too
```

## Pre-forking servers and asyncio

Under gunicorn or uWSGI, build the client before the workers fork, or in each worker: a forked worker restarts the
thread by itself, refetches and reports its own events. No hook is needed.

The getters never block, so they are safe in a coroutine. Start-up and shutdown wait on a worker thread:

```python
stage = await client.aready(timeout=5)
await client.aflush()
await client.aclose()
```

## Tests and local overrides

`flagnetic.testing` builds offline clients that make no request and start no thread:

```python
from flagnetic import testing

client = testing.from_values({"new-checkout": True})  # or from_document(...), from_file(path)
client.set("new-checkout", False)  # change listeners fire
client.reload()  # back to what it was built from
client.recorder.exposures()  # what it would have reported
```

`Client(..., allow_overrides=True)` enables `client.local_overrides.set(key, value)`, served with reason
`local_override`; without the option `set` raises `LocalOverridesNotAllowed`, and a protected environment refuses and
drops every local override.

## OpenFeature

```bash
pip install 'flagnetic[openfeature]'
```

The extra installs `openfeature-sdk` 0.9 or later and the provider in `flagnetic.openfeature`. It takes the client's
arguments and options and builds the client itself:

```python
from openfeature import api
from openfeature.evaluation_context import EvaluationContext
from openfeature.track import TrackingEventDetails
from flagnetic.openfeature import Provider

provider = Provider("<server SDK key>", "https://flags.example.com", unit_type="user")
api.set_provider_and_wait(provider)  # api.set_provider before openfeature-sdk 0.10
client = api.get_client()

context = EvaluationContext("u-1001", {"plan": "pro", "units": {"account": "a-17"}})
price = client.get_string_value("pricing", "Built-in price", context)
client.track("exposure", context, TrackingEventDetails().add("flag", "pricing"))
client.track(
    "alias",
    tracking_event_details=TrackingEventDetails()
    .add("fromUnitType", "anonymous").add("fromUnitId", "d-8f1c")
    .add("toUnitType", "user").add("toUnitId", "u-1001"),
)
```

The targeting key is the unit ID of `unit_type`; the `units` attribute adds further units by unit type; every other
attribute is an attribute of the context. Reasons map as in every Flagnetic provider:

| Flagnetic | OpenFeature |
|---|---|
| `rule`, `prerequisite_failed`, `override`, `targeting_match` | `TARGETING_MATCH` |
| `rollout`, `experiment` | `SPLIT` |
| `fallthrough` | `DEFAULT` |
| `off` | `DISABLED` |
| `local_override` | `STATIC` |
| `fallback` | `ERROR`, with `FLAG_NOT_FOUND`, `TYPE_MISMATCH` or `GENERAL` |

The flag metadata carries the Flagnetic reason as `flagnetic_reason`, with `stage`, `ruleset_version`, `variation`,
`overridden` and, for an experiment, its `experiment_*` fields. The provider emits `PROVIDER_CONFIGURATION_CHANGED`
with the changed flag keys, `PROVIDER_STALE` while the client serves its last good ruleset, `PROVIDER_READY` once it
is fresh again and `PROVIDER_ERROR` when the SDK key is revoked. Initialization waits up to `ready_timeout` seconds
(default 10) for the first fetch and never fails. `provider.client` is the wrapped client, for local overrides,
`flush` and the recorder of an offline provider (`Provider("", "", offline=document)`).

## Development

```bash
uv sync --extra openfeature
uv run ruff check . && uv run ruff format --check . && uv run mypy
uv run pytest
```

The tests run the shared conformance suite in `conformance/` against the evaluator, play the protocol scenarios and degradation cases against a mock server through the public API, and measure that evaluating one flag of a 1,000-flag ruleset stays under a millisecond at the 99th percentile.
