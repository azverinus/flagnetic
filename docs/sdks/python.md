# Python SDK

One package on PyPI, released on its own tag:

| Package | Directory | What it is | Needs |
|---|---|---|---|
| `flagnetic` | [`sdks/python`](../../sdks/python/README.md) | The SDK: server mode, `flagnetic.testing`, and the [OpenFeature](https://openfeature.dev) provider `flagnetic.openfeature` behind the extra `openfeature` | Python ≥ 3.11 and nothing outside the standard library; `openfeature-sdk` ≥ 0.9 with the extra |

```bash
pip install flagnetic
pip install 'flagnetic[openfeature]' # only with OpenFeature
```

The package is pure Python with no runtime dependency, its regular expression engine included, so it installs the same on Alpine (musl) and on free-threaded builds. It carries type hints (`py.typed`) checked with `mypy --strict`.

Supported runtimes follow upstream support: the oldest Python still supported upstream and every later one. CI runs the SDK on Python 3.11 and the newest release, with and without the extra, and the provider also on `openfeature-sdk` 0.9.0. A version that reaches its end of life upstream is dropped in a minor release.

## Mode

The SDK runs in server mode only: it takes a server key (`fn_srv_…`), downloads the environment's whole [ruleset](ruleset.md) and evaluates every flag inside the process, with the true evaluation reason. A client key is a `ValueError` at construction. Getters never do I/O and never raise; a flag that cannot be served gives the default you pass, with reason `fallback`.

```python
import os

import flagnetic

client = flagnetic.Client(os.environ["FLAGNETIC_SDK_KEY"], "https://flags.example.com")
client.ready(timeout=5)  # Stage.FRESH, LAST_GOOD or BUILT_IN
client.get_bool(flagnetic.Context(units={"user": "u-1001"}), "new-checkout", False)
```

One daemon thread per client fetches the ruleset, holds the stream and sends events. Getters read the ruleset it holds without a lock, so they are as cheap in a coroutine as in a thread; `aready`, `aflush` and `aclose` wait on a worker thread so that start-up and shutdown never block an event loop. Under a pre-forking server (gunicorn, uWSGI) a client forked into a worker restarts its thread there by itself. Tests build offline clients with `flagnetic.testing`, which make no request and start no thread. The [package's README](../../sdks/python/README.md) covers every option, the log events, the test helpers and the OpenFeature provider.

## Propagation

| The client | A change reaches it within |
|---|---|
| holds the [ruleset stream](stream.md) (the default) | 2 seconds at the 99th percentile |
| runs with `stream=False`, or behind a proxy that blocks `text/event-stream` | 30 seconds, its poll interval without the stream |

While the stream is connected the client also polls every 5 minutes (`poll_interval=`).

## Compatibility

`flagnetic` 0.x reads ruleset `schema_version` 1, speaks `/sdk/v1` and works with every Flagnetic server from 0.1.0 on; see the [compatibility table](compatibility.md). `flagnetic.testing` and `flagnetic.openfeature` make no request of their own and follow the client they wrap.

## How it is tested

- **Evaluation**: every fixture of the [conformance suite](conformance.md), the RE2 vectors and MurmurHash3 vectors, with no case skipped, and a 1,000-flag ruleset evaluated under 1 ms at the 99th percentile.
- **Delivery and events**: every server-mode protocol scenario and degradation case against a mock server; the [behaviour matrix](behaviour-matrix.md) shows which passed. A forked child and an asyncio program are tested through the public API.
- **End to end**: on every pull request that changes the package, against two replicas on one PostgreSQL brought up by [`flagnetic-e2e up`](../../e2e/README.md): the client reaches `fresh`, serves every seeded case with its true reason, decides the private segment, sees a kill switch saved through one replica within 2 seconds while streaming from the other, appears in the SDK inventory as `flagnetic-python` and is refused once its key is revoked.

To run the smoke yourself, with Docker running:

```bash
go build -o /tmp/flagnetic-e2e ./e2e/cmd/flagnetic-e2e
rm -f /tmp/seed.json
FLAGNETIC_E2E_SEED=/tmp/seed.json /tmp/flagnetic-e2e up &
until [ -s /tmp/seed.json ]; do sleep 1; done # up writes the seed once both replicas serve
cd sdks/python && FLAGNETIC_E2E_SEED=/tmp/seed.json FLAGNETIC_E2E_CMD=/tmp/flagnetic-e2e uv run pytest e2e
```
