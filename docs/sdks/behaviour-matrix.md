# SDK behaviour matrix

Which protocol scenario each SDK passes. The scenarios are those of [`conformance/protocol`](../../conformance/protocol/README.md): `scenarios.json`, then `experiment-scenarios.json`, `foreground-scenarios.json`, `page-scenarios.json` and `versioning-scenarios.json`. A server-mode SDK runs every `server` scenario, a client-mode SDK every `client` scenario of where it runs, each with no skip list.

Each SDK's column is built from the report its own protocol runner writes: the scenarios that passed in that run, and nothing else. A scenario is never marked because of its mode or because a runner is meant to cover it, so the page can neither run ahead of the runners nor fall behind them. CI runs each runner with a report, rebuilds that SDK's column with `.github/scripts/behaviour-matrix.sh` and fails when the committed page differs.

To rebuild a column, run the SDK's tests with `FLAGNETIC_SCENARIO_REPORT` naming a file, then hand that file to the script:

```bash
FLAGNETIC_SCENARIO_REPORT="$PWD/go.json" go test -count=1 -run 'TestProtocol' ./sdks/go
.github/scripts/behaviour-matrix.sh Go go.json
(cd sdks/ruby && FLAGNETIC_SCENARIO_REPORT="$PWD/ruby.json" bundle exec rake test)
.github/scripts/behaviour-matrix.sh Ruby sdks/ruby/ruby.json
(cd sdks/js && FLAGNETIC_SCENARIO_REPORT="$PWD/js.json" pnpm test)
.github/scripts/behaviour-matrix.sh JavaScript sdks/js/js.json
(cd sdks/python && FLAGNETIC_SCENARIO_REPORT="$PWD/python.json" uv run pytest tests/test_protocol.py)
.github/scripts/behaviour-matrix.sh Python sdks/python/python.json
rm -f sdks/dart/dart.json
(cd sdks/dart && FLAGNETIC_SCENARIO_REPORT="$PWD/dart.json" dart test test/protocol_test.dart && FLAGNETIC_SCENARIO_REPORT="$PWD/dart.json" dart test -p chrome test/protocol_web_test.dart)
.github/scripts/behaviour-matrix.sh Dart sdks/dart/dart.json
```

The Dart runner plays the device scenarios on the VM and the page scenarios in Chrome, and both runs add to the same report.

<!-- generated:matrix:begin -->
| Scenario | Mode | Go | Ruby | JavaScript | Python | Dart |
|---|---|---|---|---|---|---|
| server: the fallback chain keeps the held document through every failure | server | ✓ | ✓ | ✓ | ✓ |  |
| server: a stored last good document serves before the first request | server | ✓ | ✓ | ✓ | ✓ |  |
| server: a revoked key keeps the last good and retries at a capped backoff | server | ✓ | ✓ | ✓ | ✓ |  |
| server: a stream event above the held version triggers a fetch | server | ✓ | ✓ | ✓ | ✓ |  |
| server: a dropped stream reconnects with Last-Event-ID and fetches | server | ✓ | ✓ | ✓ | ✓ |  |
| server: 45 seconds of silence reconnect the stream | server | ✓ | ✓ | ✓ | ✓ |  |
| server: a fetch below the event's version is retried after 1, 2 and 4 seconds and never downgrades | server | ✓ | ✓ | ✓ | ✓ |  |
| server: If-None-Match revalidates and 304 keeps the document fresh | server | ✓ | ✓ | ✓ | ✓ |  |
| server: local overrides are refused in a protected environment | server | ✓ | ✓ | ✓ | ✓ |  |
| server: local overrides are refused without the option | server | ✓ | ✓ | ✓ | ✓ |  |
| server: a local override serves local_override, emits no exposure and is counted | server | ✓ | ✓ | ✓ | ✓ |  |
| server: offline mode makes no request | server | ✓ | ✓ | ✓ | ✓ |  |
| server: events are retried on 5xx and 429 with the same IDs and never after a rejection | server | ✓ | ✓ | ✓ | ✓ |  |
| server: the evaluation summary counts every evaluation, fallback included | server | ✓ | ✓ | ✓ | ✓ |  |
| client: the fallback chain keeps the held document through every failure | client |  |  | ✓ |  | ✓ |
| client: a stored last good document serves before the first request | client |  |  | ✓ |  | ✓ |
| client: a revoked key keeps the last good and retries at a capped backoff | client |  |  | ✓ |  | ✓ |
| client: a stream event above the held version triggers a fetch | client |  |  | ✓ |  | ✓ |
| client: a dropped stream reconnects with Last-Event-ID and fetches | client |  |  | ✓ |  | ✓ |
| client: 45 seconds of silence reconnect the stream | client |  |  | ✓ |  | ✓ |
| client: local overrides are refused in a protected environment | client |  |  | ✓ |  | ✓ |
| client: local overrides are refused without the option | client |  |  | ✓ |  | ✓ |
| client: a local override serves local_override, emits no exposure and is counted | client |  |  | ✓ |  | ✓ |
| client: offline mode makes no request | client |  |  | ✓ |  | ✓ |
| client: events are retried on 5xx and 429 with the same IDs and never after a rejection | client |  |  | ✓ |  | ✓ |
| client: the evaluation summary counts every evaluation, fallback included | client |  |  | ✓ |  | ✓ |
| server: an answer carries the experiment block | server | ✓ | ✓ | ✓ | ✓ |  |
| server: an explicit exposure is sent with its envelope | server | ✓ | ✓ | ✓ | ✓ |  |
| server: an evaluation alone records no exposure | server | ✓ | ✓ | ✓ | ✓ |  |
| server: automatic exposure follows the flag's setting | server | ✓ | ✓ | ✓ | ✓ |  |
| server: repeated exposures of one assignment are sent once | server | ✓ | ✓ | ✓ | ✓ |  |
| client: an answer carries the experiment block | client |  |  | ✓ |  | ✓ |
| client: an explicit exposure is sent with its envelope | client |  |  | ✓ |  | ✓ |
| client: automatic exposure follows the flag's setting | client |  |  | ✓ |  | ✓ |
| client: an evaluation alone records no exposure | client |  |  | ✓ |  | ✓ |
| client: repeated exposures of one assignment are sent once | client |  |  | ✓ |  | ✓ |
| client: a ruleset event waits up to 3 seconds before the evaluate it asks for | client |  |  |  |  | ✓ |
| client: events during the wait share one evaluate | client |  |  |  |  | ✓ |
| client: the background closes the stream and the foreground refetches | client |  |  |  |  | ✓ |
| client: a resume within 10 seconds of the last fetch evaluates nothing | client |  |  |  |  | ✓ |
| client: with the stream off the poll is the bound | client |  |  |  |  | ✓ |
| client: a page made visible again refetches, at most once per 10 seconds | client |  |  | ✓ |  | ✓ |
| client: the network coming back refetches, at most once per 10 seconds | client |  |  | ✓ |  | ✓ |
| client: a hidden or closing page hands its events to a beacon | client |  |  | ✓ |  | ✓ |
| server: a schema_version outside the supported range is a failure logged once | server | ✓ | ✓ | ✓ | ✓ |  |
| client: a schema_version outside the supported range is a failure logged once | client |  |  | ✓ |  | ✓ |
<!-- generated:matrix:end -->
