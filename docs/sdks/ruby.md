---
title: "Ruby SDK"
description: "The flagnetic gem with its Rails integration and test helper, and the flagnetic-openfeature provider."
---

# Ruby SDK

Two gems, released on their own tags:

| Gem | Directory | What it is | Needs |
|---|---|---|---|
| `flagnetic` | [`sdks/ruby`](../../sdks/ruby/README.md) | The SDK: server mode, a Rails integration loaded only under Rails, and `Flagnetic::Testing` | Ruby ≥ 3.3; Rails ≥ 8.0 for the integration; nothing outside the standard library |
| `flagnetic-openfeature` | [`sdks/ruby-openfeature`](../../sdks/ruby-openfeature/README.md) | An [OpenFeature](https://openfeature.dev) provider over `flagnetic` | Ruby ≥ 3.4, `openfeature-sdk` ≥ 0.6.1 within 0.x, `flagnetic` within one minor (0.1.x) |

```bash
bundle add flagnetic
bundle add flagnetic-openfeature # only with OpenFeature
```

Supported runtimes follow upstream support: the oldest Ruby and Rails still supported upstream and every later one. CI runs the SDK on Ruby 3.3, 3.4 and 4.0, the Rails integration on Rails 8.0 and 8.1, and the provider on Ruby 3.4 and 4.0. A version that reaches its end of life upstream is dropped in a minor release.

## Mode

The SDK runs in server mode only: it takes a server key (`fn_srv_…`), downloads the environment's whole [ruleset](ruleset.md) and evaluates every flag inside the process, with the true evaluation reason. A client key is refused at construction. Getters never touch the network and never raise; a flag that cannot be served gives the default you pass, with reason `fallback`.

```ruby
client = Flagnetic::Client.new(ENV.fetch("FLAGNETIC_SDK_KEY"), base_url: "https://flags.example.com")
client.ready(timeout: 5) # => :fresh, :last_good or :built_in
client.bool("new_checkout", default: false, context: { units: { user: "u-1001" } })
```

Under Rails the gem builds `Flagnetic.client` itself from `config.flagnetic`, `FLAGNETIC_SDK_KEY` and `FLAGNETIC_BASE_URL`, or credentials, so no initializer is needed; controllers and views get `flagnetic_context`. Tests build offline clients with `Flagnetic::Testing`, which make no request. The [gem's README](../../sdks/ruby/README.md) covers every option, the log events, the Rails settings and the test helpers.

## Quickstart

From an empty Gemfile to a served flag, an exposure and a clean shutdown. You need a Flagnetic server and a server key (`fn_srv_…`, see [SDK keys](keys.md)); the examples read both from the environment.

**1. Install.**

```bash
bundle add flagnetic
```

**2. Create the client with the key.** One client per process, shared by every thread. `ready` waits for the first ruleset and returns the stage it reached; the client works either way, serving your defaults until a ruleset arrives.

```ruby
require "flagnetic"

client = Flagnetic::Client.new(ENV.fetch("FLAGNETIC_SDK_KEY"), base_url: ENV.fetch("FLAGNETIC_BASE_URL"))
client.ready(timeout: 5) # => :fresh, :last_good or :built_in
```

**3. Evaluate a flag with a context.** The context names who the evaluation is for: units (unit type to unit ID) decide rollouts and experiments, attributes feed rules. A getter never waits on the network and never raises; a flag it cannot serve returns your default.

```ruby
context = { units: { user: "u-1001" }, attributes: { plan: "pro" } }

client.bool("new_checkout", default: false, context: context) # => true
client.detail("new_checkout", default: false, context: context).reason # => :fallthrough
```

**4. Report an exposure.** Evaluating records nothing. When the person actually sees what an experiment decided, say so; `exposure` sends the experiment block of the latest evaluation of that flag for that context.

```ruby
variant = client.string("pricing", default: "standard", context: context)
client.exposure("pricing", context: context)
```

**5. Shut down.** `close` sends what is queued and stops the background thread; call it when the process ends (the Rails integration does it for you).

```ruby
client.close(timeout: 5)
```

Under Rails none of the above needs code: see [Rails](../../sdks/ruby/README.md#rails). The [package README](../../sdks/ruby/README.md) has every option; the [behaviour matrix](behaviour-matrix.md) shows what the SDK is tested against.

## Propagation

| The client | A change reaches it within |
|---|---|
| holds the [ruleset stream](stream.md) (the default) | 2 seconds at the 99th percentile |
| runs with `stream: false`, or behind a proxy that blocks `text/event-stream` | 30 seconds, its poll interval without the stream |

While the stream is connected the client also polls every 5 minutes (`poll_interval:`). The stream and every other request go through `Net::HTTP`, so the process's `http_proxy`, `https_proxy` and `no_proxy` apply.

## Compatibility

`flagnetic` 0.x reads ruleset `schema_version` 1, speaks `/sdk/v1` and works with every Flagnetic server from 0.1.0 on; see the [compatibility table](compatibility.md). `flagnetic-openfeature` makes no request of its own and follows the `flagnetic` it runs with. Before 1.0 a new minor of `flagnetic` may break the provider, so the provider requires one minor of it; a provider release that admits the next minor follows each `flagnetic` minor release.

## How it is tested

- **Evaluation**: every fixture of the [conformance suite](conformance.md), the RE2 vectors and MurmurHash3 vectors, with no case skipped, and a 1,000-flag ruleset evaluated under 1 ms at the 99th percentile.
- **Delivery and events**: every server-mode protocol scenario against a mock server; the [behaviour matrix](behaviour-matrix.md) shows which passed.
- **Packages**: on every pull request that changes either gem, `gem build --strict` and a comparison of the files each gem packs with the files the repository tracks for it; the provider runs its tests against both the `flagnetic` of the working tree and the one released on RubyGems.
- **End to end**: on every pull request that changes either gem, against two replicas on one PostgreSQL brought up by [`flagnetic-e2e up`](../../e2e/README.md): the client reaches `fresh`, serves every seeded case with its true reason, decides the private segment, sees a kill switch saved through one replica within 2 seconds while streaming from the other, appears in the SDK inventory as `flagnetic-ruby` and is refused once its key is revoked.

To run the smoke yourself, with Docker running:

```bash
go build -o /tmp/flagnetic-e2e ./e2e/cmd/flagnetic-e2e
rm -f /tmp/seed.json
FLAGNETIC_E2E_SEED=/tmp/seed.json /tmp/flagnetic-e2e up &
until [ -s /tmp/seed.json ]; do sleep 1; done # up writes the seed once both replicas serve
cd sdks/ruby && FLAGNETIC_E2E_SEED=/tmp/seed.json FLAGNETIC_E2E_COMMAND=/tmp/flagnetic-e2e bundle exec rake e2e
```
