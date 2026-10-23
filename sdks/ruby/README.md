# Flagnetic SDK for Ruby

The Ruby SDK for [Flagnetic](../../README.md), server mode: it evaluates flags inside a Ruby process from a ruleset held in memory. It needs Ruby 3.3 or newer and depends on nothing outside the standard library.

This release holds the evaluator, `Flagnetic::Eval`. It evaluates any ruleset document exactly as the reference evaluator in [`eval/`](../../eval) does, which every fixture of the [conformance suite](../../conformance) proves with no case skipped.

```ruby
require "flagnetic"

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

A server newer than the SDK may send an operator, a condition kind or a serve kind this evaluator does not know. Decoding reports them per flag, and evaluating a flag that reaches one answers `error` with `inconsistent_ruleset`, so a caller serves its default:

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
bundle exec rake test   # unit tests, every conformance fixture and the p99 test
bundle exec rubocop
```
