@TestOn('vm')
// The VM only: this suite needs a `dart:io` mock server.
library;

// A `Deprecation` header on an answer is logged once per operation, with the
// `Sunset` when the answer has one, and never fails or changes the call.
import 'dart:async';
import 'dart:convert';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

import 'support/mock_server.dart';

const anonymous = FlagneticContext(units: {'anonymous': 'd-8f1c'});
const deprecated = {'Deprecation': '@1760000000'};
const sunset = 'Wed, 11 Nov 2026 00:00:00 GMT';

Map<String, Object?> get evaluation =>
    protocolJson('server/evaluation.json')! as Map<String, Object?>;

Future<({FlagneticClient client, MockServer server, LogRecorder logs})> start({
  bool? stream = false,
}) async {
  final server = await MockServer.start();
  final logs = LogRecorder();
  final client = Flagnetic.client(
    clientKey,
    baseUrl: server.url,
    context: anonymous,
    options: scaledOptions(
      logger: logs.call,
      stream: stream,
      pollInterval: const Duration(hours: 1),
    ),
  );
  addTearDown(() async {
    await client.close();
    await server.close();
  });
  return (client: client, server: server, logs: logs);
}

void main() {
  test(
    'an evaluate answer with Deprecation and Sunset is logged once',
    () async {
      final run = await start();
      await (await run.server.next()).respond(
        200,
        body: jsonEncode(evaluation),
        headers: {
          'Content-Type': 'application/json',
          ...deprecated,
          'Sunset': sunset,
        },
      );
      await eventually(() => run.client.stage == Stage.fresh);
      expect(run.client.getBool('checkout', false), isNotNull);

      expect(run.logs.fieldsOf(LogEvents.operationDeprecated), [
        {
          'operation': 'evaluate',
          'deprecation': '@1760000000',
          'sunset': sunset,
        },
      ]);
      expect(
        run.logs.events
            .singleWhere((e) => e.event == LogEvents.operationDeprecated)
            .level,
        LogLevel.warn,
      );

      // The same operation answers deprecated again: no second line.
      final again = run.client.refresh();
      await (await run.server.next()).respond(
        200,
        body: jsonEncode(evaluation),
        headers: {'Content-Type': 'application/json', ...deprecated},
      );
      await again;
      expect(run.logs.count(LogEvents.operationDeprecated), 1);
    },
  );

  test(
    'without a Sunset the log has none, and the call still succeeds',
    () async {
      final run = await start();
      await (await run.server.next()).respond(
        200,
        body: jsonEncode(evaluation),
        headers: {'Content-Type': 'application/json', ...deprecated},
      );
      await eventually(() => run.client.stage == Stage.fresh);
      expect(run.logs.fieldsOf(LogEvents.operationDeprecated), [
        {'operation': 'evaluate', 'deprecation': '@1760000000'},
      ]);
    },
  );

  test('an error answer carries the header too and is logged', () async {
    final run = await start();
    await (await run.server.next()).respond(503, headers: deprecated);
    await eventually(
      () => run.logs.count(LogEvents.operationDeprecated) == 1,
      reason: 'the deprecation log',
    );
    expect(run.logs.fieldsOf(LogEvents.fetchFailed), isNotEmpty);
  });

  test('an answer without the header logs nothing', () async {
    final run = await start();
    await (await run.server.next()).respondJson(evaluation);
    await eventually(() => run.client.stage == Stage.fresh);
    expect(run.logs.count(LogEvents.operationDeprecated), 0);
  });

  test('an empty Deprecation header logs nothing', () async {
    final run = await start();
    await (await run.server.next()).respond(
      200,
      body: jsonEncode(evaluation),
      headers: {'Content-Type': 'application/json', 'Deprecation': ''},
    );
    await eventually(() => run.client.stage == Stage.fresh);
    expect(run.logs.count(LogEvents.operationDeprecated), 0);
  });

  test('an events answer is logged under events, once', () async {
    final run = await start();
    await (await run.server.next()).respondJson(evaluation);
    await eventually(() => run.client.stage == Stage.fresh);
    run.client.getBool('checkout', false);

    for (var i = 0; i < 2; i++) {
      final flushed = run.client.flush();
      await (await run.server.next(path: '/sdk/v1/events')).respond(
        202,
        body: '{"accepted":0,"rejected":[]}',
        headers: {
          'Content-Type': 'application/json',
          ...deprecated,
          'Sunset': sunset,
        },
      );
      await flushed;
      run.client.getBool('checkout', false);
    }
    expect(run.logs.fieldsOf(LogEvents.operationDeprecated), [
      {'operation': 'events', 'deprecation': '@1760000000', 'sunset': sunset},
    ]);
  });

  test('a stream answer is logged under stream', () async {
    final run = await start(stream: true);
    final stream = await (await run.server.next(path: '/sdk/v1/stream'))
        .respondStream(
          headers: {...eventStreamHeaders, ...deprecated, 'Sunset': sunset},
        );
    await eventually(
      () => run.logs.count(LogEvents.operationDeprecated) == 1,
      reason: 'the deprecation log',
    );
    expect(run.logs.fieldsOf(LogEvents.operationDeprecated), [
      {'operation': 'stream', 'deprecation': '@1760000000', 'sunset': sunset},
    ]);
    unawaited(stream.closeStream());
  });
}
