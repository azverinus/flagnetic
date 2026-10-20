# Conformance suite

Language-agnostic fixtures that every Flagnetic evaluator must pass: the Go module in [`eval/`](../eval) and every SDK that evaluates locally, in any language. Evaluator drift between SDKs does not fail loudly, it silently biases experiments; this directory is what makes it fail loudly.

A change to evaluation lands here first, as a fixture. Every implementation runs the whole directory with no skip list, so every SDK stays red until it passes.

## Layout

- `fixtures/murmur3.json`: hash vectors `{input, hash, bucket}`. Every hash was computed by ClickHouse `murmurHash3_32(input)`, and `bucket = hash % 10000`. Reproduce them with `clickhouse local --query "SELECT line, murmurHash3_32(line) FROM file('inputs.txt', 'LineAsString')"`. An implementation must reproduce `hash` from the UTF-8 bytes of `input`; where `input` contains a `.`, `Bucket(salt, unit_id)` split at the first `.` must reproduce `bucket`.
- `fixtures/<topic>/<name>.json`: one ruleset and its cases. The file's `name` equals its path without `.json`.

## Fixture shape

```json
{
  "name": "semver/parsing",
  "ruleset": { "schema_version": 1, "version": 1, "scope": "server", "...": "a ruleset document" },
  "cases": [
    { "name": "two components equal three",
      "context": { "units": { "user": "u1" }, "attributes": { "app_version": "1.17" } },
      "flag": "case_02",
      "expect": { "type": "boolean", "value": true, "variation": "true", "reason": "rule",
                  "rule_id": "r1", "automatic_exposure": false, "override_influenced": false } }
  ]
}
```

A runner decodes `ruleset`, evaluates `flag` for `context`, and compares its result with `expect` **exactly**: every key in `expect` must be present with an equal JSON value, and the result must have no other key. The result keys are:

| Key | Present when |
|---|---|
| `type`, `automatic_exposure` | the flag is known: every result but an `error` with `flag_not_found` |
| `value`, `variation` | a variation is served (absent for `error`) |
| `reason` | always: `off`, `override`, `rule`, `rollout`, `experiment`, `fallthrough`, `prerequisite_failed`, `error` |
| `rule_id` | reason `rule`, `rollout` or `experiment` |
| `experiment` | reason `experiment`, and reason `override` when the shadow evaluation lands in a running experiment: `key`, `variation`, `unit_type`, `unit_id`, `inclusion_salt`, `variation_salt`, `inclusion_bucket`, `variation_bucket`, `override` |
| `prerequisite` | reason `prerequisite_failed`: the flag-level prerequisite that failed |
| `bucket` | reason `rollout` |
| `error_code` | reason `error`: `flag_not_found`, `server_only`, `prerequisite_cycle`, `inconsistent_ruleset` |
| `override_influenced` | always |

Values compare as JSON (the number `5` equals `5.0`). Fixtures carry the ruleset's `schema_version`; when it is bumped, old fixtures stay.

The evaluator always returns the true reason. Which reasons a caller may see is decided where a result leaves the server, never in the evaluator or its fixtures.

## Semantics the fixtures pin

Where prose leaves a choice, the fixture decides, and every implementation must agree.

- An absent, `null` or wrong-typed attribute makes every operator false except `not_exists`, which is true. An unparseable semver or date in a context is wrong-typed; in a condition value it makes the condition false. A condition value of the wrong JSON type, an operator not defined for the type, or a regex outside RE2 is `inconsistent_ruleset`.
- Semver: optional lower-case `v`; one to three dot-separated integers of any size, leading zeros ignored (`1.05` equals `1.5.0`); pre-release and build identifiers are non-empty `[0-9A-Za-z-]` runs; pre-release numeric identifiers compare numerically, leading zeros ignored.
- Dates: RFC 3339 with an offset, or integral Unix epoch milliseconds (a JSON number) in contexts; comparison is strict on instants.
- `matches` searches anywhere in the string unless the pattern is anchored.
- An empty unit ID is an absent unit.
- A prerequisite holds when its reason is neither `off` nor `error` and it serves the required variation, so a prerequisite that itself failed its own prerequisite still serves its off variation. An `error` in a prerequisite (a cycle, a dangling reference) becomes the dependent flag's error instead of a silent `prerequisite_failed`.
- An override's shadow experiment detail carries the served (override's) variation, the shadow's buckets and `override: true`.
- `type` and `automatic_exposure` are the flag's, not the evaluation's: they are the same for every reason a known flag can carry, `off`, `prerequisite_failed` and an `error` other than `flag_not_found` included. An unknown flag has neither.
- Growing traffic allocation never moves a unit that was already included: inclusion is a prefix test on a bucket that does not depend on the allocation, and the variation bucket is independent of inclusion. `experiments/allocation-growth` runs the same 200 unit IDs against one experiment at allocations 2000, 5000 and 10000; every unit the fixture includes at one allocation is included with the same variation at every higher one.

## Running

- Go: `cd eval && go test ./...`
- An SDK in another language: its test suite walks `conformance/fixtures` and compares the same way, with no file or case skipped.
