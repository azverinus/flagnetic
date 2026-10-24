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

## Development

```bash
uv sync
uv run ruff check . && uv run ruff format --check . && uv run mypy
uv run pytest
```

The tests run the shared conformance suite in `conformance/` against the evaluator, play the protocol scenarios and degradation cases against a mock server through the public API, and measure that evaluating one flag of a 1,000-flag ruleset stays under a millisecond at the 99th percentile.
