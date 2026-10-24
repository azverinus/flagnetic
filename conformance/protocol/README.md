# Protocol fixtures

Everything an SDK's fallback chain, change stream and events retry policy is tested against: the documents of `/sdk/v1` in exactly the shapes the endpoints serve, the corrupt and newer documents the fallback chain must reject or tolerate, and a scenario list a mock server in any language can script. The evaluator has [`../fixtures`](../fixtures); this directory does the same for the protocol. A new degradation rule lands here first, and every SDK stays red until it passes.

The documents under `server/` are what the SDK API serves, byte for byte where it matters. Their contract is [`api/sdk.yaml`](../../api/sdk.yaml), which the server's adapter and a test client are generated from; a test checks that every exchange is an operation and status of it and every document one of its shapes. The server in this repository serves these documents and is tested against them: its protocol run (`internal/infra/in/http/sdk/protocol_test.go`) drives every exchange of `exchanges.json`, in order, against a real server on PostgreSQL and fails when a status, a header listed there or a body differs. It normalizes only what differs on every run: ruleset versions (the server takes them from its clock, while the documents keep small example numbers that SDKs only compare), rule IDs and rollout salts (minted by the server) and request IDs. `server/not-served-yet.json` lists the answers the documents pin that the server does not give yet, grouped by what it lacks, and what it answers instead; SDKs ignore it. `versioning-scenarios.json` (below) is written by hand.

## Layout

- `server/`: what the server answered.
  - `exchanges.json`: every exchange in order: the request (method, path, the headers that matter, the body) and the response (status, the headers an SDK acts on, the path of the body's file).
  - `ruleset.json`: the server-scope ruleset of a protected environment, private segment included; `ruleset-after-kill-switch.json` the next version; `ruleset-unprotected.json` an unprotected environment.
  - `evaluation.json`: a client key's evaluate response for the context below, with `ignored_attributes`; `evaluation-after-kill-switch.json` and `evaluation-unprotected.json` likewise.
  - `events-accepted.json`, `events-one-rejected.json`, `events-mixed.json`: 202 answers, the last with a duplicate, a dropped summary row and every per-event rejection code (`event_invalid`, `event_type_unknown`, `event_id_invalid`, `out_of_window`, `undeclared_unit_type`, `unknown_experiment`, `unit_type_mismatch`, `unknown_variation`). `events-exposure-rejected.json`: an exposure of an experiment the project does not have, rejected `unknown_experiment`; `events-exposure-unit-type-rejected.json`: an exposure of an experiment that exists whose unit type is not the experiment's, rejected `unit_type_mismatch` with a detail specific to a server key. The documents' events name the experiment `banner_test`, a draft on the flag `banner_text` assigning the unit type `user` and weighing `short` and `long`; a client key's details name none of that. The rejection codes are an open set: an SDK treats a code it does not know like any other rejection and never sends the event again.
  - `stream.txt`: a stream transcript byte for byte: `retry: 3000`, the current version at once, a heartbeat, the version a kill switch made. `stream-caught-up.txt`: a stream opened with `Last-Event-ID` equal to the current version hears heartbeats only.
  - `problems/<status>-<code>.json`: one problem document per 4xx code, plus 503 `ruleset_unavailable`. Problems have the management API's shape: `type` (`urn:flagnetic:problem:<code>`), `title`, `status`, a stable `code`, `detail`, the `request_id` of the request, `retry_after` with `Retry-After`, and for a refused body `errors`, each a violation `{pointer, code, detail, params}` whose JSON Pointer points into the request body (`422 invalid_context` uses `too_long` and `too_many` with `params.max`, and `duplicate` for a unit type given twice). The adapter's own refusals are `400 invalid_request`, `404 not_found`, `405 method_not_allowed`, `413 request_too_large` and `415 unsupported_media_type`; the SDK API's own codes are `sdk_key_missing`, `sdk_key_invalid`, `sdk_key_revoked`, `sdk_key_expired`, `key_kind_not_allowed`, `server_key_in_query`, `server_key_from_browser`, `origin_not_allowed`, `schema_version_unsupported`, `invalid_context`, `rate_limited`, `ruleset_unavailable` and `unavailable`. An SDK branches on the status, the `code` and `Retry-After`, never on `title` or `detail`.
- `degradation/`: documents derived from `server/` by one change each, and `cases.json`, which lists them with the outcome every SDK must reach.
- `scenarios.json`: the scenario list for mock servers.
- `versioning-scenarios.json`: scenarios the server cannot produce because only the SDKs decide them (versioning and compatibility), written by hand in the same step language and naming only documents the server produced. Every runner appends its `scenarios` to those of `scenarios.json`, and reads `context`, `defaults` and `answers` from `scenarios.json`.
- `experiment-scenarios.json`: running experiments, for a server key and a client key: an answer carrying the experiment block, an explicit exposure with its envelope, an evaluation that records no exposure, automatic exposure, and repeats sent once. Its documents are `server/ruleset-experiment.json` and `server/evaluation-experiment.json`, whose `answers` it carries; every runner appends its `scenarios` and merges its `answers` into those of `scenarios.json`.

Offline mode takes `server/ruleset.json` (server mode) or `server/evaluation.json` (client mode) as it is.

## What a client key learns

A client key ships inside apps and browsers, so it learns the values it is served and nothing about the targeting behind them. Its evaluate responses carry the reason `targeting_match` wherever targeting decided the value, in place of `rule`, `rollout`, `fallthrough` and `prerequisite_failed`, and never a rule identifier; `off`, `override`, `experiment`, `error` and the experiment block stay. Every evaluation document here, and every answer a client-mode SDK reads from one, has that shape. A server key's ruleset carries everything, and a server-mode SDK computes the true reason with its evaluator.

## Conventions

- Values in angle brackets stand for SDK keys, which change on every run: `<server key>`, `<client key>` and so on in `exchanges.json`; `<sdk key>` in `scenarios.json` is the key the SDK under test was built with.
- `<request id>` in a problem document stands for the request's ID, which changes on every request.
- Identifiers (rules, segments, events) are UUIDv7 in canonical form.
- A response's `body` is a path relative to this directory; no `body` means an empty body.
- `context` in `cases.json` and `scenarios.json` is the context of every evaluation: client-mode SDKs are built with it, server-mode SDKs pass it to every getter. `defaults` are the built-in defaults every getter is called with, each unlike anything the flag serves, so a `fallback` is visible.
- An answer is what a getter of `type` returns for `flag` with `default`: `value`, `variation` (absent when none was served), `reason`, and `experiment` when the value comes from a running experiment (absent means the answer carries no experiment block). A server-mode SDK computes it with its evaluator; a client-mode SDK reads it.

## Degradation cases

Each case of `degradation/cases.json` gives an `endpoint`, the document `held` (fresh) when the `response` arrives, the `outcome`, the `answers` the SDK serves afterwards and, where it matters, the `logs` the logger hook must have received by name: a ruleset holding an unsupported rule logs `rule_unsupported` once.

| Outcome | What the SDK does |
|---|---|
| `keep_held` | The response is a failure of the fallback chain: the held document keeps serving, the stage becomes `last_good`, `stale_since` is set, the next attempt is scheduled, and the held version stays the one revalidated. |
| `accept` | The document replaces the held one, the stage becomes `fresh`, and the answers follow the degradation rules. |
| `never_retry_rejected` | The batch was answered 202; rejected events are dropped and never sent again. |

The cases: an empty body, `{}`, truncated JSON, a `schema_version` above the one requested (absent `Flagnetic-Schema-Version` requests 1), a lower version, the other endpoint's document (a ruleset has `version` and `scope`; an evaluate response has `ruleset_version` and a `flags` object) are `keep_held`. An unknown field at any level is ignored. An unknown operator, condition kind or rule kind is an unsupported rule: an evaluation error (`inconsistent_ruleset`) where evaluation reaches it, so the flag answers its default with `fallback`, and a prerequisite's error is its dependent's. A flag of an unknown type is absent; an unknown evaluation reason is passed through with the served value; an unknown event type is rejected per event (`event_type_unknown`), and a rejection code the SDK does not know is a rejection like any other: the event is never sent again. The shared evaluator in `eval/` does not know flag types, so an SDK drops a flag of an unknown type before evaluating.

## Scenarios

`scenarios.json` holds `context`, `defaults`, `answers` (by document, and `built_in` for none: what every getter returns while that document serves) and `scenarios`; `versioning-scenarios.json` holds only `scenarios`. A scenario has a `mode` (`server`: the ruleset and local evaluation; `client`: remote evaluation), what it `covers`, what is `given` (`stored`: the last good document in the SDK's store at construction; `offline`: a document instead of a key; `options`) and `steps`, run in order.

A mock server consumes the list like this:

1. Build the SDK for the scenario's mode against the mock's base URL with the given options, store and document. Option names are language-neutral: `sse`, `poll_interval_ms` (the poll interval in every state), `flush_interval_ms`, `summary_interval_ms`, `send_summaries`, `send_exposures`, `allow_overrides`; map them to the SDK's own.
2. Run the steps. Each step holds one action and may hold an `expect`, checked once the action is done:
   - `request` + `respond`: the SDK's next request to that `path` must have the `method`, carry `headers` with these values and none of `absent_headers`, arrive within `delay_ms` (`min`–`max`, from the end of the previous step) and, for events, match `events`. The mock answers `respond`: a status, headers and body; `network_error` drops the connection; `stream` keeps the response open as the change stream. Requests to other paths meanwhile are queued for their own steps.
   - `stream_send`: write these bytes to the open stream. `stream_close`: end it from the server's side.
   - `advance_ms`: let time pass. `no_request`: let `for_ms` pass while the SDK sends nothing to `path` (any path when absent).
   - `call`: call the SDK's API: `evaluate` (the getter of `type` for `flag` with `default`, `times` times), `set_override`, `exposure`, `alias` (from the anonymous unit `d-8f1c` to the user `u-1001`), `flush`.
3. `events` in a request: `types` are the batch's event types in order; `same_ids_as_step` and `excludes_ids_of_step` compare its event IDs with the batch of another step (0-based); `summary_counts` are the rows of its evaluation summary in any order (`variation` null when none was served) and `summary_ruleset_version` its `ruleset_version`; `absent_strings` appear nowhere in the body; `exposures` are, in order, the fields each exposure event of the batch must carry with these values.
4. A 202 answer's `rejected[].event_id` holds the ID the server was sent when the document was produced: the mock replaces it with the ID of the event at `index` in the request it answers.

A passing run asserts, for every step: the expected request arrived within its window with its headers and body; each `expect` holds: `stage` from the SDK's stage API, `stale_since` set or unset from its status API, `answers_of` by calling every getter of `answers[answers_of]` with its default, `result` for every call of the step, `refused` from `set_override`, `logs` as the number of events of each name the logger hook received since construction, `recorded` as the offline recorder's events by type. After the last step no request may be left unanswered or unexpected.

Where the protocol leaves a choice, the scenarios decide, and every SDK must agree:

- `schema_unsupported` is logged once per client: a second document outside the SDK's `schema_version` range fails the fetch again without logging it again (`versioning-scenarios.json`), and every ruleset, evaluate and events request carries `Flagnetic-Schema-Version` with the highest schema the SDK reads. The change stream carries no document, and a browser `EventSource` cannot set headers, so stream requests need not send it.
- A failed fetch is retried by backoff alone; the poll timer waits while retries run and restarts when an attempt succeeds.
- A server key opens the stream with `Authorization`; a client key may use `Authorization` or `?sdk_key=`, so client-mode stream steps do not check either.
- An SDK may honour `Retry-After` or back off; the windows after a 429 allow both.
- Scenarios about the stream set `poll_interval_ms` to an hour, so no safety poll interleaves with their requests.

Durations are in the SDK's clock. A test that injects a clock advances it; one on real time may divide every duration of a scenario, options and windows alike, by one factor. Windows already allow a second of scheduling latency where a timer fires at a fixed time; backoff windows are full jitter, 0 up to the doubling bound: fetch retries from 1 s in server mode and 2 s in client mode to 5 minutes, 15 minutes while the key is refused; stream reconnects from 1 s to 30 s, 0–2 s after a server-side close.

## Not covered by documents

- Behaviour with no document of its own, which each SDK tests against its own mock server: flushing by size and interval, the offline queue surviving a restart, evaluate on foreground, a context change serving the previous values until the new response, poll intervals that differ while the stream is up or down, and a stream answering 401 or 403.
- 500 `internal` cannot be produced on purpose; the scenarios script a 500 without a body.
