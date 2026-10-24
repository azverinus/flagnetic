# Implementing a Flagnetic SDK against the conformance suite

A Flagnetic SDK in a new language is right when it passes [`conformance/`](../../conformance) with no file, case or scenario skipped. Behaviour two SDKs must agree on is pinned there, never left to a judgement call: drift between SDKs does not fail loudly, it silently serves different values to the same user and biases experiments. This page is the order to work through it.

## 1. The evaluator

Port the evaluation algorithm documented on [`eval`](../../eval/README.md) (`go doc github.com/azverinus/flagnetic/eval`), then run the evaluator suite:

- **Fixtures** (`conformance/fixtures/<topic>/<name>.json`): decode `ruleset`, evaluate `flag` for `context` and compare with `expect` exactly, key for key, with no key missing and none extra. [`conformance/README.md`](../../conformance/README.md) lists every result key and when it is present. The evaluator always returns the true reason.
- **murmur3** (`conformance/fixtures/murmur3.json`): MurmurHash3 x86 32-bit, seed 0, over UTF-8 bytes, read unsigned; `bucket = hash % 10000`, and `Bucket(salt, unit_id)` hashes `"{salt}.{unit_id}"`. Hashing UTF-16 or reading the hash as signed fails these vectors.
- **RE2 vectors** (`conformance/fixtures/regex.json`): a `matches` condition must match, not match or refuse the pattern (`"invalid"`, evaluated as `inconsistent_ruleset`) exactly as Go `regexp` does. A pattern Go accepts must be accepted. If your platform's engine is not RE2, translate patterns or bring an RE2 implementation; a skip list is not an option.
- **Unsupported rules**: an operator, condition kind or serve kind your evaluator does not know is found when the ruleset is decoded and reported per flag, as `{kind, name}`. A fixture's `unsupported` key is the report it expects; without the key, the report must be empty. Evaluating such a flag is `inconsistent_ruleset` where evaluation reaches the construct.

## 2. The SDK surface

Wrap the evaluator in getters that never throw or panic: a missing flag, a type mismatch, an evaluator error or no ruleset at all serves the built-in default the caller passed, with reason `fallback` and an error code. Put every evaluator fixture through your getters too, as the Go SDK's `conformance_test.go` does, so the SDK layer cannot distort what the evaluator computed. Log `rule_unsupported` once per flag and ruleset version, naming the flag and its constructs.

## 3. The protocol

[`conformance/protocol`](../../conformance/protocol/README.md) holds the documents of `/sdk/v1` in exactly the shapes the server serves (their contract is [`api/sdk.yaml`](../../api/sdk.yaml)), and a step language a mock server in any language can script:

- **Degradation cases** (`degradation/cases.json`): what the fallback chain does with an empty, truncated, newer, lower-versioned or otherwise wrong document, and the answers served afterwards; `logs` names what must have been logged.
- **Scenarios** (`scenarios.json`, `versioning-scenarios.json`, `experiment-scenarios.json`, and `foreground-scenarios.json` for client SDKs on devices): the fallback chain, the store, a revoked key, the change stream, local overrides, offline mode, the events retry policy, evaluation summaries, schema versions, running experiments with explicit and automatic exposures, and the foreground stream of a device (the random wait before evaluating, the background close, the resume refetch). Run every scenario of every mode your SDK supports; `server` mode evaluates locally from the ruleset, `client` mode reads evaluate answers.
- **A client key's answers** carry `targeting_match` in place of the reasons targeting decided, and no rule identifier. A client-mode SDK reads and reports them as they are.
- **Errors** are problem documents in the management API's shape (`server/problems/`): `type`, `title`, `status`, a stable `code`, `detail`, the `request_id` of the request, and for a refused body `errors`, each with a JSON Pointer into the body, a `code` and the rule's `params`. Act on the status and `Retry-After` (and the `code` where it helps a log line), never on `title` or `detail`.
- **Events answers** list each refused event in `rejected` with a `code`. The codes are an open set: treat a code you do not know like any other rejection and never send that event again.

Your runner should fail when a scenario of a mode you support runs nowhere, as the Go SDK's `TestEveryServerScenarioIsCovered` does, and generate your section of the [behaviour matrix](behaviour-matrix.md) from what it ran.

## 4. OpenFeature

A provider maps Flagnetic reasons to OpenFeature reasons by [`conformance/openfeature/reasons.json`](../../conformance/openfeature/reasons.json), and carries the Flagnetic reason in the flag metadata under the key the file names, so dashboards agree across languages.

## 5. Compatibility

Declare the `schema_version` range your SDK reads and the SDK API path it speaks, send `Flagnetic-Schema-Version` on every document and events request, and add your row to the [compatibility table](compatibility.md).
