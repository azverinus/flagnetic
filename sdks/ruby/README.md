# Flagnetic SDK for Ruby

The Ruby SDK for [Flagnetic](../../README.md), server mode: it evaluates flags inside a Ruby process from a ruleset held in memory. It depends on nothing outside the standard library.

## Install

```bash
bundle add flagnetic
```

| Runtime | Supported |
|---|---|
| Ruby | 3.3 and later (CI: 3.3, 3.4, 4.0) |
| Rails, for the integration | 8.0 and later (CI: 8.0, 8.1) |
| Flagnetic server | 0.1.0 and later: the gem reads ruleset `schema_version` 1 over `/sdk/v1` ([compatibility](../../docs/sdks/compatibility.md)) |

The oldest Ruby and Rails still supported upstream set the floor; one that reaches its end of life is dropped in a minor release.

## Quick start

```ruby
require "flagnetic"

client = Flagnetic::Client.new(ENV.fetch("FLAGNETIC_SDK_KEY"), base_url: "https://flags.example.com")
client.ready(timeout: 5)

if client.bool("new_checkout", default: false, context: { units: { user: "u-1001" } })
  # the new checkout
end
```

Under Rails no initializer is needed: set `FLAGNETIC_SDK_KEY` and `FLAGNETIC_BASE_URL` and use `Flagnetic.client` (see [Rails](#rails)). In tests, `Flagnetic::Testing` serves the values you set without a server (see [Testing](#testing)).

## Propagation

A change saved in Flagnetic, a kill switch included, reaches a client holding the change stream (the default) within **2 seconds at the 99th percentile**, whichever replica the change and the client each go through. With `stream: false`, or behind a proxy that blocks `text/event-stream`, it arrives at the next poll, within 30 seconds. Every pull request that changes this gem checks the 2 seconds against two real replicas.

## Client

`Flagnetic::Client` is the server-mode client. It downloads the ruleset with the server key, holds it in memory, follows every change through the change stream (polling while the stream is down) and evaluates locally, so a getter never waits on the network and never raises.

```ruby
require "flagnetic"

client = Flagnetic::Client.new(ENV.fetch("FLAGNETIC_SDK_KEY"), base_url: "https://flags.example.com")
client.ready(timeout: 5) # => :fresh, :last_good or :built_in

context = { units: { user: "u-1001" }, attributes: { plan: "pro" } }
client.bool("new_checkout", default: false, context: context)
client.int("max_items", default: 10, context: context) # a number flag, whole numbers only
detail = client.detail("new_checkout", default: false, context: context)
detail.value  # => true
detail.reason # => :rule

client.close
```

A client key (`fn_cli_...`) is refused at construction with an `ArgumentError`. `base_url` is the only required option besides the key.

| Option | Meaning |
|---|---|
| `stream:` | `false` turns the change stream off; the ruleset is then polled every 30 s (default `true`). |
| `poll_interval:` | Seconds between polls while the stream is up (default 300, at least 30). |
| `request_timeout:` | Seconds per request (default 10). |
| `store:` | Path of a file keeping the last good ruleset across restarts. It is read at construction and rewritten on every accepted ruleset. |
| `logger:` | A callable receiving `(level, event, fields)`, or a standard `Logger`. Default: warnings and errors to `$stderr`. |
| `platform:` | The platform in the `User-Agent` (`flagnetic-ruby/<version> (<platform>)`) and the events envelope, and an exposure's `platform` when the context names none (default `"server"`). |
| `app_version:` | The application's version, sent with every evaluation summary and as an exposure's `app_version` when the context names none. |
| `flush_interval:` | Seconds between sends of the exposure and alias queue (default 2); 500 queued events are sent at once. |
| `summary_interval:` | Seconds between evaluation summaries (default 60). |
| `send_summaries:` | `false` stops counting and sending evaluation summaries. |
| `send_exposures:` | `false` makes `exposure` and `alias` send nothing (logged as `exposures_disabled`). |
| `offline:`, `offline_file:` | A ruleset document (JSON text, a `Hash` or a callable returning one) or the path of one. No request is ever made, the stage is `:fresh`, and `reload` re-reads the source. |
| `allow_overrides:` | Enables local overrides. |

`stage` is `:fresh` while the latest attempt succeeded (a `304` included), `:last_good` while the held ruleset is older than the latest attempt (it came from an earlier success or from the store) and `:built_in` before any ruleset was received. `status` adds `stale_since`. `on_change` and `on_stage` register blocks and return a callable that removes them.

Every request, the change stream included, goes through `Net::HTTP`, so the proxy configured for the process (`http_proxy`, `https_proxy`, `no_proxy`) applies.

After `fork` the child starts its own background thread on its first call; `after_fork` starts it at once, so a worker that has not served a request yet already follows the stream.

Local overrides replace a flag's value inside this process and are refused in a protected environment:

```ruby
client = Flagnetic::Client.new(key, base_url: url, allow_overrides: true)
client.local_overrides.set("new_checkout", true) # raises LocalOverridesNotAllowed without allow_overrides
client.local_overrides.remove("new_checkout")
```

## Events

The client reports what it served so that experiments can be analysed:

```ruby
context = { units: { user: "u-1001" }, attributes: { platform: "linux", app_version: "1.18.0" } }
client.string("pricing", default: "standard", context: context)
client.exposure("pricing", context: context) # the unit saw the behaviour the flag decided
client.alias("anonymous", "d-8f1c", "user", "u-1001") # after sign-up
client.flush(timeout: 5)
```

- **Evaluation summaries.** Every evaluation is counted per flag, variation and reason (`fallback` and `local_override` included, never a unit ID or a value) and sent every `summary_interval` as one or more `evaluation_summary` events of at most 1,000 rows, with the version of the ruleset held at the interval's end. A report that fails merges into the next one; one older than an hour is dropped.
- **Exposures.** Evaluating records nothing on its own. `exposure` sends the experiment block of the latest evaluation of that flag for that context, or nothing when it carried none (`exposure_skipped` at debug says why). A flag with automatic exposure sends one as it is evaluated. A repeat for the same experiment, unit and variation is sent once per `flush_interval`.
- **Aliases.** `alias` links the unit ID used before sign-up to the one after it; it changes no context.
- **Delivery.** Events go gzipped to `POST /sdk/v1/events` in batches of at most 500, each with a UUIDv7 event ID kept across retries, so the server drops a resend as a duplicate. An evaluation summary that is still unanswered when its flush ends is merged into the next window and sent under new IDs. A 429, a 5xx or a network error is retried with backoff (evaluation summaries wait for `Retry-After`, capped at five minutes); a 401 or 403 logs `key_revoked` and keeps the events; an event the server rejects is logged as `queue_dropped` and never sent again. The queue holds 10,000 events and drops the oldest beyond that.
- `flush(timeout:)` sends what is queued and the current window and waits at most `timeout` seconds; `close(timeout:)` flushes first and then stops the background thread, both within the one `timeout`. A forked child starts with an empty queue and window.

An offline client sends nothing: it hands every summary, exposure and alias to its `recorder`.

Log events have stable names (`Flagnetic::EVENTS`); alert on the name, never on the text:

| Event | Level | Fields |
|---|---|---|
| `ruleset_loaded` | info | `source` (`fetch`, `store`, `offline`), `ruleset_version`, `flags` |
| `fetch_failed` | warn (error offline) | `cause`, `stage` (`source` offline) |
| `sse_connected` | info | |
| `sse_dropped` | info after a server close, warn otherwise | `cause` |
| `stage_changed` | info to `fresh`, warn otherwise | `from`, `to` |
| `key_revoked` | error, once per source until it recovers | `status`, `source` (`fetch`, `stream`) |
| `schema_unsupported` | error, once | `schema_version`, `supported` |
| `rule_unsupported` | error, once per flag and ruleset version | `flag`, `constructs`, `ruleset_version` |
| `override_set` | warn | `flag` |
| `override_refused` | warn | `flag`, `reason` (`not_allowed`, `protected`), `environment` |
| `local_overrides_allowed` | warn | |
| `type_mismatch` | warn, once per flag | `flag`, `flag_type`, `requested_type` |
| `evaluation_failed` | error | `flag`, `cause` |
| `listener_failed` | error | `cause` |
| `store_failed` | error | `path`, `cause` |
| `worker_failed` | error | `cause` |
| `exposure_skipped` | debug | `flag`, `cause` (`no_experiment`, `no_detail`, `repeat`) |
| `exposures_disabled` | warn | |
| `ingest_failed` | warn | `kind`, `events`, `retry_in`, `status`, `error` |
| `client_not_configured` | warn | `rails_env`: the Rails integration found no SDK key and base URL and serves built-in defaults |
| `queue_dropped` | warn | `kind` (`exposure_or_alias`, `evaluation_summary`), `cause` (`queue_full`, `client_error`, `refused_by_the_platform`, `window_older_than_one_hour`) and what was dropped |

A ruleset from a newer server may hold constructs this SDK does not know. The client logs `rule_unsupported` once per flag and ruleset version, and the flag serves its default with the error code `inconsistent_ruleset`.

## Flagnetic.client

`Flagnetic.client` is the process-wide client: assign one with `Flagnetic.client = ...`, or set `Flagnetic.client_builder` to a callable that builds it on first use, once. It raises `Flagnetic::NotConfigured` until either is set.

## Rails

Under Rails the gem loads a railtie (nothing of it loads without Rails) that sets `Flagnetic.client` up without an initializer. The SDK key and base URL come from the first of `config.flagnetic.sdk_key` and `config.flagnetic.base_url`, the `FLAGNETIC_SDK_KEY` and `FLAGNETIC_BASE_URL` environment variables, and credentials (`flagnetic.sdk_key`, `flagnetic.base_url`). Without both, the client serves built-in defaults and logs `client_not_configured`.

A server or job process builds the client at boot and waits up to `ready_timeout` seconds for the ruleset; `rails console`, `rails runner` and rake build it on first use, so a task that never asks for a flag makes no request. Every forked child (Puma cluster mode, Unicorn) starts its own background thread at once, and every process closes its client at exit, which flushes what it queued. Local overrides are allowed in every environment but production, where no setting enables them.

| `config.flagnetic.` | |
|---|---|
| `sdk_key`, `base_url` | See above. |
| `offline_file` | A ruleset document to serve instead; no request is made. |
| `start` | `nil` (default): at boot in a server or job process, on first use in console, runner and rake. `true`: at boot everywhere. `false`: on first use. |
| `ready_timeout`, `close_timeout` | Seconds the boot waits for the ruleset and the exit flush may take (default 5 each). |
| `allow_overrides` | `false` disables local overrides outside production too. |
| `logger` | The client's `logger:` (default `Rails.logger`). |
| `client_options` | Any other `Flagnetic::Client.new` option, as a `Hash`. |
| `platform`, `app_version` | Attributes of every request context, and the client's `platform:` and `app_version:`. |
| `user_unit_type`, `user_id` | The signed-in user's unit type, and a callable receiving the controller that returns the ID (default `current_user&.id`, else `session[:user_id]`). |
| `anonymous_unit_type`, `anonymous_cookie` | The unit type of a random ID kept in a cookie (default name `flagnetic_anonymous_id`). |
| `attributes` | A callable receiving the controller and returning more attributes. |

Controllers and views get `flagnetic_context`, the current request's context, and `flagnetic_anonymous_id`, the visitor's anonymous ID (set in an HTTP-only cookie on the first request). No unit type is set by default. Signing in keeps the anonymous ID beside the user's, so an experiment spanning sign-up keeps its variation; call `Flagnetic.client.alias` at sign-up to link the two.

```ruby
config.flagnetic.user_unit_type = "user"
config.flagnetic.anonymous_unit_type = "anonymous"

# in a controller or view
Flagnetic.client.bool("new_checkout", default: false, context: flagnetic_context)
```

## Testing

`Flagnetic::Testing` builds offline clients for tests: they make no request and keep what they would have sent in a recorder.

```ruby
require "flagnetic/testing"

client = Flagnetic::Testing.offline(values: { "new_checkout" => true, "banner" => "spring" })
client = Flagnetic::Testing.offline(File.read("ruleset.json")) # or a Hash, or file: "ruleset.json"

client.set("banner", "autumn") # served to everyone as fallthrough; a new key is added
client.reload                  # back to the source

client.recorder.summary              # running counts per flag, variation and reason
client.recorder.evaluation_summaries # what flush would have sent
client.recorder.exposures            # what exposure and automatic exposure recorded
client.recorder.aliases
client.recorder.reset
```

`with_flags` and `with_ruleset` make such a client `Flagnetic.client` for a block and restore the previous one afterwards, even when the block raises. They are process-wide, so tests running in parallel threads must not use them. Include the helpers in RSpec or Minitest:

```ruby
RSpec.configure { |config| config.include Flagnetic::Testing::Helpers }

class ActiveSupport::TestCase
  include Flagnetic::Testing::Helpers
end

with_flags(new_checkout: true) do |client|
  get "/checkout"
  assert client.recorder.summary.any? { |row| row.flag == "new_checkout" }
end

with_ruleset(file: "test/fixtures/flags.json") do |client|
  post "/checkout"
  assert_equal ["checkout_v2"], client.recorder.exposures.map(&:experiment_key)
end
```

## OpenFeature

[`flagnetic-openfeature`](../ruby-openfeature/README.md) is an OpenFeature provider over this gem, in a gem of its own so that `flagnetic` stays free of the dependency. It needs Ruby 3.4 or later and `openfeature-sdk` 0.6.1 or a later 0.x release; exposures and aliases go through `track`.

```bash
bundle add flagnetic-openfeature
```

```ruby
require "flagnetic/openfeature"

provider = Flagnetic::OpenFeature::Provider.new(ENV.fetch("FLAGNETIC_SDK_KEY"), base_url: "https://flags.example.com")
OpenFeature::SDK.configure { |config| config.set_provider_and_wait(provider) }
```

## Evaluator

`Flagnetic::Eval` evaluates any ruleset document exactly as the reference evaluator in [`eval/`](../../eval) does, which every fixture of the [conformance suite](../../conformance) proves with no case skipped.

```ruby
ruleset = Flagnetic::Eval::Ruleset.decode(File.read("ruleset.json"))

context = { units: { user: "u-1001" }, attributes: { plan: "pro", app_version: "1.42.0" } }
result = ruleset.evaluate("new_checkout", context)
result.value     # => true
result.variation # => "on"
result.reason    # => :rule
result.rule_id   # => "r1"
```

`Ruleset.decode` raises `Flagnetic::Eval::DecodeError` only for a document the evaluator cannot read: malformed JSON, a field of the wrong JSON type, an unsupported `schema_version` or a scope other than `server`. `evaluate` never raises: a problem is a result with reason `error` and an `error_code` (`flag_not_found`, `prerequisite_cycle`, `inconsistent_ruleset`).

Evaluation records nothing. An exposure is an explicit call, never a side effect of evaluating.

## Unsupported rules

A server newer than the SDK may send an operator, a condition kind or a serve kind this evaluator does not know. Decoding reports them per flag, and evaluating a flag that reaches one answers `error` with `inconsistent_ruleset`, so the flag serves the fallback:

```ruby
ruleset.unsupported_rules
# => { "new_checkout" => [#<struct Flagnetic::Eval::Construct kind=:operator, name="sounds_like">] }
```

Every other flag evaluates normally. The fix is to upgrade the SDK.

## Regular expressions

A `matches` condition is RE2 syntax with RE2 semantics, as Go's `regexp` reads it, and not the dialect of Ruby's own engine. A pattern is parsed as RE2 and translated, so `^` and `$` are not line anchors, `\s` is `[\t\n\f\r ]`, lookarounds and backreferences are refused and `(?i)` folds case by Go's rules, `\p{...}` included. A pattern RE2 refuses makes the condition `inconsistent_ruleset`. A match that takes longer than 0.1 s makes the condition false.

The case folding orbits and property names come from Go's `unicode` package, and `lib/flagnetic/eval/unicode_tables.rb` is generated from it:

```bash
go run ./sdks/ruby/script/_unicode_tables > sdks/ruby/lib/flagnetic/eval/unicode_tables.rb
```

## Development

```bash
bundle install
bundle exec rake test   # unit tests, every conformance fixture, the protocol scenarios, the RSpec helpers and the p99 test
bundle exec rubocop
(cd rails && bundle install && bundle exec rake test)  # the Rails integration; RAILS_VERSION=8.0 for the older major
```

`bundle exec rake e2e` runs the smoke against two real replicas brought up by `flagnetic-e2e up`; [docs/sdks/ruby.md](../../docs/sdks/ruby.md#how-it-is-tested) shows how.
