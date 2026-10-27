# SDK versioning and compatibility

Which server an SDK build can talk to, and what an SDK version promises. A build shipped years ago keeps working: the server keeps serving every SDK API path a released SDK speaks and every document schema it shipped under that path, and every SDK treats a document it cannot read as a failure of the fallback chain, never as "everything off".

Every SDK is in its 0.x line: until 1.0.0 a minor release may still change the SDK's own API, while the protocol between an SDK and the server only grows.

## What 0.1 promises

Flagnetic 0.1.0 is the first release, a developer preview. From it on:

- **The SDK API and the ruleset format only grow.** `/sdk/v1`, its headers, answers and problem codes, and the ruleset document change only additively, under the degradation rules below, so an SDK released with 0.1 keeps working with every later 0.x and 1.x server. Anything an older SDK could not read is served only to an SDK that asks for it, as a new `schema_version` or under a new SDK API path beside `/sdk/v1`.
- **Upgrades only migrate forward, without data loss.** Migrations are never edited once released and there are no down migrations; upgrading a server keeps every flag, segment, key and audit entry. Take a database backup before an upgrade and upgrade all replicas together; 0.1 does not promise that an older version runs against a database a newer one migrated, nor that two versions serve side by side (see [Upgrades](../install.md#upgrades)).
- **The management API and the console may still change.** `/api/v1` and the console may change incompatibly in a 0.x minor release; every such change is marked as breaking in the release notes.

What 0.1 does not have yet: experiments (every exposure an SDK posts is rejected `unknown_experiment` until they exist), governance beyond roles and the audit log: protected environments, change requests, scheduled changes and webhooks (`environment.protected` is `false` everywhere, so local overrides work in every environment), single sign-on, and SDKs other than Go and its OpenFeature provider. It needs PostgreSQL 16 or later.

### Experiments

0.1 rejects every exposure `unknown_experiment`, since it has no experiments. A server with experiments accepts an exposure of an experiment the project has, in any status, and rejects the others per event with `unknown_experiment`, `unit_type_mismatch`, `unknown_variation` or `event_invalid` (see [Events](events.md#the-batch-and-each-event)); the batch is answered `202` either way, so an SDK built against 0.1 sends exposures unchanged and needs no upgrade. Its ruleset carries a running experiment's rules, which the SDKs already evaluate: the experiment scenarios of the [behaviour matrix](behaviour-matrix.md) prove it for each.

### Protected environments

0.1 serves `environment.protected` as `false` in every ruleset and evaluate answer, so local overrides work everywhere. A server with protected environments serves the real value: an environment an owner protects, and the production environment of every project created on that server, answer `true`, and a server SDK ignores local overrides there, as it always promised. Environments that existed before the upgrade stay unprotected until an owner protects them, so an upgrade alone changes nothing an SDK does. Protecting or unprotecting an environment moves its ruleset version, so SDKs pick the change up like any other. A test that sets local overrides against a protected environment should use an unprotected one, such as development, or the SDK's test client.

## Compatibility table

One row per SDK line: an SDK major version, or the version within a major from which a new `schema_version` range applies. `N.x` covers every `N.y.z`; `≥ N.y.z` covers `N.y.z` and later within major `N`, and where two rows cover a version the later one applies. Server is the lowest Flagnetic server release that serves the path and the range.

| SDK | Version | `schema_version` | SDK API path | Server |
|---|---|---|---|---|
| Dart | 0.x | 1 | `/sdk/v1` | ≥ 0.1.0 |
| Go | 0.x | 1 | `/sdk/v1` | ≥ 0.1.0 |
| JavaScript | 0.x | 1 | `/sdk/v1` | ≥ 0.1.0 |
| Python | 0.x | 1 | `/sdk/v1` | ≥ 0.1.0 |
| Ruby | 0.x | 1 | `/sdk/v1` | ≥ 0.1.0 |

The table names the core package of each SDK. The Go OpenFeature provider, `flagnetictest`, the Ruby gem `flagnetic-openfeature` and `Flagnetic::Testing` make no request of their own: they speak through the SDK they depend on, so its row is theirs; so do the React bindings and OpenFeature providers inside the `flagnetic` npm package, whose server mode and browser client speak the same path and range, and `flagnetic.testing` and the OpenFeature provider of the PyPI package `flagnetic`; and the Flutter package `flagnetic_flutter`, whose widgets, store and Dev Tools speak through the pub.dev package `flagnetic`. The evaluator module (`eval`) reads `schema_version` 1 and makes no request at all.

## What a key learns

A ruleset document has a single scope, `server`, and only a server key receives one; an SDK refuses a document of any other scope (`wrong_scope`).

A **server key** belongs in a backend. It fetches the whole ruleset of its environment, private segments included, and the SDK evaluates locally with the true evaluation reason: `rule`, `rollout`, `experiment`, `override`, `prerequisite_failed`, `off`, `fallthrough`, and the SDK's own `fallback` and `local_override`.

A **client key** ships inside apps and browsers, so it never receives a ruleset: a client SDK evaluates remotely, posting its context to `POST /sdk/v1/evaluate`, and learns served values only. Its evaluate answers carry the value, the variation, the experiment block an exposure needs, and one of these reasons:

| Reason | When |
|---|---|
| `targeting_match` | targeting decided the value: a matched rule, a rollout, the fallthrough or a failed prerequisite |
| `off` | the flag is off |
| `override` | an override for this unit decided the value |
| `experiment` | a running experiment assigned the value |
| `error` | the flag could not be evaluated; the SDK serves its built-in default as `fallback` |

A client key never learns which rule, segment or prerequisite produced a value, nor a rule identifier. The Go, Ruby and Python SDKs and the JavaScript server mode evaluate locally and refuse a client key at construction, and the JavaScript browser client and the Dart SDK refuse a server key.

## Versioning policy

- **Semantic versioning per SDK, independent numbers.** Each SDK releases on its own tag with its own version; the same number in two SDKs promises nothing about each other. The Go modules are tagged `eval/vX.Y.Z`, `sdks/go/vX.Y.Z` and `sdks/go-openfeature/vX.Y.Z`, the npm package `flagnetic` `sdks/js/vX.Y.Z`, the Ruby gems `sdks/ruby/vX.Y.Z` and `sdks/ruby-openfeature/vX.Y.Z`, the PyPI package `flagnetic` `sdks/python/vX.Y.Z`, the pub.dev packages `flagnetic` `sdks/dart/vX.Y.Z` and `flagnetic_flutter` `sdks/flutter/vX.Y.Z`.
- **A new SDK API path prefix is a new SDK major version.** An SDK moving from `/sdk/v1` to `/sdk/v2` releases a new major, and the table gains a row for it. The server serves `/sdk/v2` alongside `/sdk/v1` for as long as a released SDK speaks `/sdk/v1`.
- **`schema_version` changes only when structure changes.** Within `/sdk/v1` every change is additive and leaves `schema_version` alone. A new `schema_version` appears only when a change cannot be read under the degradation rules below; an SDK that reads it widens its range and the table gains a row.
- **What an SDK sends.** Every request for a document (the ruleset, remote evaluation) and every events batch carries `Flagnetic-Schema-Version` with the highest `schema_version` the SDK reads. The server answers with a document of that schema or a lower one it still produces, and 406 `schema_version_unsupported` when it produces none that low, so a new server keeps sending an old build the schema it asked for.
- **What an SDK accepts.** A document whose `schema_version` is outside the SDK's range is a failure of the fallback chain: the held document keeps serving, the stage becomes `last_good`, the next attempt is scheduled, and `schema_unsupported` is logged once per client.

### Degradation rules

Every SDK follows these rules, so an additive change never needs a new `schema_version`. `conformance/protocol/degradation/cases.json` holds a document for each, which every SDK's protocol runner serves:

| In a document | What the SDK does |
|---|---|
| Unknown field, at any level | Ignored. |
| Unknown operator, condition kind or serve kind (an unsupported rule) | Logged as `rule_unsupported` once per flag and ruleset version; where evaluation reaches it, the getter serves the built-in default with reason `fallback` and `inconsistent_ruleset`. Every other flag evaluates normally. |
| Experiment serve breaking its invariants (a missing key, unit type or salt; a traffic allocation outside 0 to 10000; fewer than two weights, a negative one, two naming the same variation or one naming a variation the flag lacks, or weights not summing to 10000) | Found when the document is decoded; where evaluation reaches it, the getter serves the built-in default with reason `fallback` and `inconsistent_ruleset`. Every other flag evaluates normally. |
| Unknown flag type | The flag is absent. |
| Unknown evaluation reason | Passed through with the served value. |
| Unknown event type (sent to the server) | Rejected per event with `event_type_unknown`; the rest of the batch is accepted and the rejected event is never sent again. |
| Unknown rejection code (in an events answer) | A rejection like any other: the event is never sent again. The codes are an open set a later server may add to. |
| `schema_version` outside the SDK's range | A failure of the fallback chain, as above. |

The same holds the other way round. The server ignores what it does not know in a request, so a later SDK that sends a new optional member or query parameter is answered as if it had not: a member at any level of a request body (the evaluate body and its `context`, the events envelope and its `sdk`, each event, each summary row) and a query parameter of any operation. Context attributes keep their own rule: one the project does not declare is listed in `ignored_attributes` and has no effect.

## What the Go SDK declares

`sdks/go/version.go` holds `Version`, which every request's User-Agent (`flagnetic-go/<version> (<os>)`) and every events envelope report and which release-please keeps equal to the module's tag, and the unexported `schema_version` range and SDK API path the SDK uses for every request. A test of the SDK fails when the range is not the one the evaluator reads, and the release workflow checks `Version` against the tag.

## What the Ruby SDK declares

`sdks/ruby/lib/flagnetic/version.rb` holds `Flagnetic::VERSION`, which every request's User-Agent (`flagnetic-ruby/<version> (<platform>)`) and every events envelope report and which release-please keeps equal to the gem's tag, and `SCHEMA_VERSION_MIN`, `SCHEMA_VERSION_MAX` and `SDK_API_PATH`. The gem's tests fail when the Ruby row of the table above that covers `VERSION` names another range or path, when the highest schema is not the one its evaluator reads, and when two rows of one major name different paths.

## What the JavaScript SDK declares

`sdks/js/src/version.ts` holds `VERSION`, which the server mode's User-Agent (`flagnetic-js/<version> (<platform>)`), the browser client's context and every events envelope report and which release-please keeps equal to `package.json` and the tag, and `SCHEMA_VERSION_MIN`, `SCHEMA_VERSION_MAX` and `SDK_API_PATH`, exported from every entry point. A test of the SDK fails when the last JavaScript row of the table above is not that range and path, and the release workflow checks `VERSION` against the tag.

## What the Python SDK declares

`sdks/python/src/flagnetic/_version.py` holds `flagnetic.__version__`, which every request's User-Agent (`flagnetic-python/<version> (<platform>)`) and every events envelope report, which the package's metadata reads, and which release-please keeps equal to the tag; the `schema_version` it reads and the SDK API path stay internal. A test of the SDK fails when the last Python row of the table above is not that schema and path, and the release workflow checks `__version__` against the tag.

## What the Dart SDK declares

`sdks/dart/lib/src/client/options.dart` holds `sdkVersion`, which every request's User-Agent (`flagnetic-dart/<version> (<platform>)`), the context's `sdk_version` attribute and every events envelope report and which release-please keeps equal to `pubspec.yaml` and the tag, and `schemaVersionMin`, `schemaVersionMax` and `sdkApiPath`. A test of the SDK fails when the last Dart row of the table above is not that range and path, and the publishing workflow checks `sdkVersion` against the tag.

## Adding a row

When a release changes what an SDK reads or speaks, add its row here in the same change, with the first server release that serves it:

```markdown
| Go | ≥ 1.4.0 | 1–2 | `/sdk/v1` | ≥ 1.2.0 |
| Go | 2.x | 2 | `/sdk/v2` | ≥ 2.0.0 |
```

The first widens the range within a major; the second is a new SDK API path, so a new major. Rows are never removed: an old line stays in the table as long as a build of it may be running.
