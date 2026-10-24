# Flagnetic SDK for Ruby

The Ruby SDK for [Flagnetic](../../README.md), server mode: it evaluates flags inside a Ruby process from a ruleset held in memory. It needs Ruby 3.3 or newer and depends on nothing outside the standard library.

## Client

`Flagnetic::Client` is the server-mode client. It downloads the ruleset with the server key, holds it in memory, follows every change through the change stream (polling while the stream is down) and evaluates locally, so a getter never waits on the network and never raises.

```ruby
require "flagnetic"

client = Flagnetic::Client.new(ENV.fetch("FLAGNETIC_SDK_KEY"), base_url: "https://flags.example.com")
client.ready(timeout: 5) # => :fresh, :last_good or :built_in

context = { units: { user: "u-1001" }, attributes: { plan: "pro" } }
client.bool("new_checkout", default: false, context: context)
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
| `platform:` | The platform in the `User-Agent` (`flagnetic-ruby/<version> (<platform>)`, default `"server"`). |
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

A ruleset from a newer server may hold constructs this SDK does not know. The client logs `rule_unsupported` once per flag and ruleset version, and the flag serves its default with the error code `inconsistent_ruleset`.

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
bundle exec rake test   # unit tests, every conformance fixture, the protocol scenarios and the p99 test
bundle exec rubocop
```
