# eval: the Flagnetic evaluator

`github.com/azverinus/flagnetic/eval` computes a flag's value and evaluation reason from a ruleset document and a context. It is a pure function: no dependencies outside the Go standard library, no I/O, no clock, no exposures.

This module is the **reference implementation**. The server's remote evaluation and the Go SDK import it; SDKs in other languages implement the same algorithm and prove it with the conformance suite in [`../conformance`](../conformance): a fixture is the contract, this module is one way to meet it.

```go
rs, err := eval.Decode(document) // one ruleset document, schema_version 1
r := rs.Evaluate(eval.Context{
	Units:      map[string]string{"user": "u-1001"},
	Attributes: map[string]any{"plan": "pro", "app_version": "1.18.0"},
}, "new_checkout")
// r.Type, r.Value, r.Variation, r.Reason, r.RuleID, r.AutomaticExposure,
// r.Experiment, r.Bucket, r.OverrideInfluenced
all := rs.EvaluateAll(ctx) // every flag, prerequisites memoized within the call
```

## One scope

A ruleset document has one scope, `server`: the whole configuration of an environment, private segments and overrides included. Only a server SDK holding a server key receives it, and `Decode` refuses any other scope. A client SDK (a browser or a mobile app) never receives a ruleset: it posts its context to the server's remote evaluation and receives the served values, so private segments never leave the server.

`Decode` checks every experiment serve once: an experiment key, a unit type and both salts present; a traffic allocation within 0 to 10000; at least two weights, none negative, naming distinct variations of the flag and summing to exactly 10000. A flag whose evaluation reaches a serve breaking one of them is an `error` with `inconsistent_ruleset`, as an unsupported rule is, and an SDK serves the caller's default for it.

## The result shape

Every implementation produces the same result, key for key; [`../conformance/README.md`](../conformance/README.md) lists every key and when it is present. Two of them are the flag's rather than the evaluation's, and travel with every result of a known flag:

- `type`: the flag type, so a getter can check it against the type it was asked for.
- `automatic_exposure`: the flag's setting. With it, an SDK emits an exposure after every evaluation whose result carries `experiment`, with no second call, so the result alone says what to do.

Both are left out only when there is no flag to read them from: an error with `flag_not_found`. Every other result carries them, errors and reasons `off` and `prerequisite_failed` included.

## Buckets

A unit's bucket for a salt is

```
murmur3_32(utf8("{salt}.{unit_id}"), seed 0) mod 10000
```

on the unsigned hash, giving 0–9999; a unit is inside a percentage `p` (in basis points) when its bucket is below `p`. The formula equals ClickHouse `murmurHash3_32(concat(salt, '.', unit_id)) % 10000`, so any bucket can be recomputed next to the exported events. An implementation that hashes UTF-16 or reads the hash as signed gets different buckets and fails the murmur3 vectors.

The algorithm and the operator semantics are documented on the package (`go doc github.com/azverinus/flagnetic/eval`).

| File | What it holds |
|---|---|
| `ruleset.go` | the ruleset document, `Decode`, the index of flags and segments |
| `evaluate.go` | `Context`, `Result`, the evaluation steps, prerequisites, override influence |
| `condition.go` | every operator of every attribute type, `CheckOperator` |
| `semver.go` | semantic version parsing and SemVer 2.0.0 precedence |
| `bucket.go` | MurmurHash3 x86 32-bit and `Bucket` |

## Using the conformance suite from another SDK

An SDK walks `conformance/fixtures`, decodes each file's `ruleset` with its own evaluator, evaluates every case and compares the result with `expect` key for key, with no file or case skipped. Two files hold vectors instead of a ruleset: `murmur3.json` for the hash and buckets, `regex.json` for the `matches` operator. A fixture's `unsupported` key is the decode report its ruleset must produce. [`../conformance/README.md`](../conformance/README.md) describes the formats.

## Tests

```bash
go test ./...                    # conformance suite, property tests, fuzz seeds
go test -run XXX -fuzz FuzzParseSemver -fuzztime 30s .
go test -run XXX -fuzz FuzzConditionDecoding -fuzztime 30s .
```

- `conformance_test.go` runs every file under `conformance/fixtures`, checks each fixture's unsupported-rule report, and checks that `EvaluateAll` agrees with `Evaluate` on each case. It also checks `experiments/allocation-growth` against the invariant its expectations exist to pin: a unit included at one traffic allocation is included, with the same variation, at every higher one.
- `regex_test.go` checks every RE2 vector of `conformance/fixtures/regex.json` against Go `regexp` and the evaluator's `matches`; `go test -run TestRegexVectorsUpdate -update-regex .` regenerates the expectations.
- `property_test.go` checks bucket uniformity by chi-squared over 10,000 buckets on one million identifiers of four shapes (sequential base62, UUIDv4, UUIDv7, integers); rollout monotonicity over random salts and percentages; and fuzzes semantic version parsing and condition decoding.
