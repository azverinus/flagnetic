# Ruby SDK

Two gems, released on their own tags:

| Gem | Directory | What it is | Needs |
|---|---|---|---|
| `flagnetic` | [`sdks/ruby`](../../sdks/ruby/README.md) | The SDK: server mode, a Rails integration loaded only under Rails, and `Flagnetic::Testing` | Ruby ≥ 3.3; Rails ≥ 8.0 for the integration; nothing outside the standard library |
| `flagnetic-openfeature` | [`sdks/ruby-openfeature`](../../sdks/ruby-openfeature/README.md) | An [OpenFeature](https://openfeature.dev) provider over `flagnetic` | Ruby ≥ 3.4, `openfeature-sdk` ≥ 0.6.1 within 0.x |

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

## Propagation

| The client | A change reaches it within |
|---|---|
| holds the [ruleset stream](stream.md) (the default) | 2 seconds at the 99th percentile |
| runs with `stream: false`, or behind a proxy that blocks `text/event-stream` | 30 seconds, its poll interval without the stream |

While the stream is connected the client also polls every 5 minutes (`poll_interval:`). The stream and every other request go through `Net::HTTP`, so the process's `http_proxy`, `https_proxy` and `no_proxy` apply.

## Compatibility

`flagnetic` 0.x reads ruleset `schema_version` 1, speaks `/sdk/v1` and works with every Flagnetic server from 0.1.0 on; see the [compatibility table](compatibility.md). `flagnetic-openfeature` makes no request of its own and follows the `flagnetic` it runs with.

## How it is tested

- **Evaluation**: every fixture of the [conformance suite](conformance.md), the RE2 vectors and MurmurHash3 vectors, with no case skipped, and a 1,000-flag ruleset evaluated under 1 ms at the 99th percentile.
- **Delivery and events**: every server-mode protocol scenario against a mock server; the [behaviour matrix](behaviour-matrix.md) shows which passed.
- **End to end**: on every pull request that changes either gem, against two replicas on one PostgreSQL brought up by [`flagnetic-e2e up`](../../e2e/README.md): the client reaches `fresh`, serves every seeded case with its true reason, decides the private segment, sees a kill switch saved through one replica within 2 seconds while streaming from the other, appears in the SDK inventory as `flagnetic-ruby` and is refused once its key is revoked.

To run the smoke yourself, with Docker running:

```bash
go build -o /tmp/flagnetic-e2e ./e2e/cmd/flagnetic-e2e
FLAGNETIC_E2E_SEED=/tmp/seed.json /tmp/flagnetic-e2e up &
cd sdks/ruby && FLAGNETIC_E2E_SEED=/tmp/seed.json FLAGNETIC_E2E_COMMAND=/tmp/flagnetic-e2e bundle exec rake e2e
```
