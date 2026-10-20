# SDK versioning and compatibility

Which server an SDK build can talk to, and what an SDK version promises. A build shipped years ago keeps working: the server keeps serving every SDK API path a released SDK speaks and every document schema it shipped under that path, and every SDK treats a document it cannot read as a failure of the fallback chain, never as "everything off".

Every SDK is in its 0.x line: until 1.0.0 a minor release may still change the SDK's own API, while the protocol between an SDK and the server only grows.

## Compatibility table

One row per SDK line: an SDK major version, or the version within a major from which a new `schema_version` range applies. `N.x` covers every `N.y.z`; `≥ N.y.z` covers `N.y.z` and later within major `N`, and where two rows cover a version the later one applies. Server is the lowest Flagnetic server release that serves the path and the range.

| SDK | Version | `schema_version` | SDK API path | Server |
|---|---|---|---|---|
| Go | 0.x | 1 | `/sdk/v1` | ≥ 0.1.0 |

The table names the core module of each SDK. The Go OpenFeature provider and `flagnetictest` make no request of their own: they speak through the SDK they depend on, so its row is theirs. The evaluator module (`eval`) reads `schema_version` 1 and makes no request at all.

## What a key learns

A **server key** belongs in a backend. It fetches the whole ruleset of its environment, private segments included, and the SDK evaluates locally with the true evaluation reason: `rule`, `rollout`, `experiment`, `override`, `prerequisite_failed`, `off`, `fallthrough`, and the SDK's own `fallback` and `local_override`.

A **client key** ships inside apps and browsers, so it learns served values only. Its evaluate answers carry the value, the variation, the experiment block an exposure needs, and one of these reasons:

| Reason | When |
|---|---|
| `targeting_match` | targeting decided the value: a matched rule, a rollout, the fallthrough or a failed prerequisite |
| `off` | the flag is off |
| `override` | an override for this unit decided the value |
| `experiment` | a running experiment assigned the value |
| `error` | the flag could not be evaluated; the SDK serves its built-in default as `fallback` |

A client key never learns which rule, segment or prerequisite produced a value, nor a rule identifier. The Go SDK refuses a client key at construction.

## Versioning policy

- **Semantic versioning per SDK, independent numbers.** Each SDK releases on its own tag with its own version; the same number in two SDKs promises nothing about each other. The Go modules are tagged `eval/vX.Y.Z`, `sdks/go/vX.Y.Z` and `sdks/go-openfeature/vX.Y.Z`.
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
| Unknown flag type | The flag is absent. |
| Unknown evaluation reason | Passed through with the served value. |
| Unknown event type (sent to the server) | Rejected per event with `event_type_unknown`; the rest of the batch is accepted and the rejected event is never sent again. |
| `schema_version` outside the SDK's range | A failure of the fallback chain, as above. |

## What the Go SDK declares

`sdks/go/version.go` holds `Version`, which every request's User-Agent (`flagnetic-go/<version> (<os>)`) and every events envelope report and which release-please keeps equal to the module's tag, and the unexported `schema_version` range and SDK API path the SDK uses for every request. A test of the SDK fails when the range is not the one the evaluator reads, and the release workflow checks `Version` against the tag.

When a release changes what an SDK reads or speaks, add its row here in the same change, with the first server release that serves it:

```markdown
| Go | ≥ 1.4.0 | 1–2 | `/sdk/v1` | ≥ 1.2.0 |
| Go | 2.x | 2 | `/sdk/v2` | ≥ 2.0.0 |
```

The first widens the range within a major; the second is a new SDK API path, so a new major. Rows are never removed: an old line stays in the table as long as a build of it may be running.
