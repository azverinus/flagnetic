@TestOn('vm')
// The VM only: this suite needs a `dart:io` mock server.
library;

// Client mode against a mock server: construction, the store, remote
// evaluation, the fallback chain, the getters, ready and close, and the
// API-boundary catch-all. Scenarios observe the public API, the logger hook
// and the requests the mock receives; durations are the protocol's divided by
// 1000 (see scaledOptions).
import 'dart:async';

import 'package:flagnetic/flagnetic.dart';
import 'package:flagnetic/src/client/options.dart';
import 'package:test/test.dart';

import 'support/mock_server.dart';

final scenarios = protocolJson('scenarios.json')! as Map<String, Object?>;
final context = contextOf(scenarios['context']);
final answers = scenarios['answers']! as Map<String, Object?>;
List<Object?> answersOf(String document) => answers[document]! as List<Object?>;

Map<String, Object?> document(String path) =>
    protocolJson(path)! as Map<String, Object?>;

final evaluation = document('server/evaluation.json');
final afterKillSwitch = document('server/evaluation-after-kill-switch.json');

/// A client against [server] with scaled durations, closed at tear-down.
FlagneticClient clientOf(
  MockServer server, {
  String sdkKey = clientKey,
  FlagneticContext? of,
  FlagneticStore? store,
  LogRecorder? logs,
  Duration pollInterval = const Duration(hours: 1),
  Map<String, Object?>? bootstrap,
  String? platform,
  String? appVersion,
  int storedUnitSets = 4,
  Duration requestTimeout = const Duration(seconds: 5),
}) {
  final client = Flagnetic.client(
    sdkKey,
    baseUrl: server.url,
    context: of ?? context,
    options: scaledOptions(
      store: store,
      logger: logs?.call,
      pollInterval: pollInterval,
      bootstrap: bootstrap,
      platform: platform,
      appVersion: appVersion,
      storedUnitSets: storedUnitSets,
      requestTimeout: requestTimeout,
    ),
  );
  addTearDown(client.close);
  return client;
}

Future<MockServer> startServer() async {
  final server = await MockServer.start();
  addTearDown(server.close);
  return server;
}

void main() {
  group('construction', () {
    test('a server key fails loudly', () {
      expect(
        () => Flagnetic.client(
          'fn_srv_TESTKEY0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
          baseUrl: Uri.parse('http://127.0.0.1:1'),
          context: context,
        ),
        throwsArgumentError,
      );
    });

    test('anything else constructs, even without a base URL', () async {
      final logs = LogRecorder();
      final client = Flagnetic.client(
        'not-a-key',
        context: context,
        options: ClientOptions(logger: logs.call),
      );
      addTearDown(client.close);
      expect(client.stage, Stage.builtIn);
      expect(client.getBool('checkout', false), isFalse);
      await eventually(() => logs.count(LogEvents.fetchFailed) == 1);
    });

    test('the in-memory store gets, sets and removes by key', () {
      final store = InMemoryStore({'a': '1'});
      expect(store.get('a'), '1');
      store.set('b', '2');
      expect(store.get('b'), '2');
      store.remove('a');
      store.remove('missing');
      expect(store.get('a'), isNull);
      expect(store.entries, {'b': '2'});
    });
  });

  group('the store', () {
    test('last good serves before any network call', () async {
      final store = await storeHolding(evaluation, context);
      final server = await startServer();
      final logs = LogRecorder();
      final client = clientOf(server, store: store, logs: logs);
      // Synchronously, before the first request can have been sent.
      expect(client.stage, Stage.lastGood);
      expectAnswers(client, answersOf('server/evaluation.json'));
      final detail = client.getDetail('checkout', false);
      expect(detail.stage, Stage.lastGood);
      expect(detail.rulesetVersion, 1);
      expect(detail.receivedAt, isNotNull);
      expect(logs.fieldsOf(LogEvents.valuesLoaded).single['source'], 'store');

      final exchange = await server.next();
      expect(client.stage, Stage.lastGood);
      await exchange.respondJson(afterKillSwitch);
      await eventually(() => client.stage == Stage.fresh);
      expectAnswers(
        client,
        answersOf('server/evaluation-after-kill-switch.json'),
      );
    });

    test('a store answering with futures loads before the request', () async {
      final seeded = await storeHolding(evaluation, context);
      final server = await startServer();
      final client = clientOf(server, store: _AsyncStore(seeded));
      expect(client.stage, Stage.builtIn);
      await server.next();
      expect(client.stage, Stage.lastGood);
      expectAnswers(client, answersOf('server/evaluation.json'));
    });

    test('a 200 persists values, units, ruleset version and received-at '
        'under the unit set, whatever order the units are listed in', () async {
      final store = InMemoryStore();
      final server = await startServer();
      final first = clientOf(server, store: store);
      await (await server.next()).respondJson(evaluation);
      await eventually(() => first.stage == Stage.fresh);
      final receivedAt = first.getDetail('checkout', false).receivedAt;
      await first.close();

      final reordered = FlagneticContext(
        units: {
          for (final type in context.units.keys.toList().reversed)
            type: context.units[type]!,
        },
      );
      final second = clientOf(server, store: store, of: reordered);
      expect(second.stage, Stage.lastGood);
      final detail = second.getDetail('checkout', false);
      expect(detail.rulesetVersion, 1);
      expect(detail.receivedAt, receivedAt);
      expectAnswers(second, answersOf('server/evaluation.json'));

      final other = clientOf(
        server,
        store: store,
        of: const FlagneticContext(units: {'anonymous': 'someone-else'}),
      );
      expect(other.stage, Stage.builtIn);
    });

    test('what another SDK key stored is not served, whatever version it '
        'carries', () async {
      const otherKey = 'fn_cli_OTHERKEY0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
      final ahead = {...evaluation, 'ruleset_version': 9};
      final store = await storeHolding(ahead, context);
      final server = await startServer();

      // Another environment's values are another environment's, and so is
      // its ruleset version: a lower one here is still this key's fresh
      // answer, not a stale document.
      final client = clientOf(server, sdkKey: otherKey, store: store);
      expect(client.stage, Stage.builtIn);
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);
      expect(client.getDetail('checkout', false).rulesetVersion, 1);
      expectAnswers(client, answersOf('server/evaluation.json'));
      expect(
        store.entries[clientKeys.lastGood(context.units)],
        isNotNull,
        reason: "the other key's entry is left alone",
      );
      expect(
        store.entries[StoreKeys(otherKey).lastGood(context.units)],
        isNotNull,
        reason: 'and this key persists its own',
      );
    });

    test('the store keeps a bounded number of unit sets', () async {
      final store = InMemoryStore();
      final server = await startServer();
      FlagneticContext device(int i) =>
          FlagneticContext(units: {'anonymous': 'd-$i'});
      for (var i = 1; i <= 3; i++) {
        final client = clientOf(
          server,
          store: store,
          of: device(i),
          storedUnitSets: 2,
        );
        await (await server.next()).respondJson(evaluation);
        await eventually(() => client.stage == Stage.fresh);
        await client.close();
      }
      Stage startsAt(int i) => clientOf(
        server,
        store: store,
        of: device(i),
        storedUnitSets: 2,
      ).stage;
      expect(startsAt(1), Stage.builtIn, reason: 'the oldest set is evicted');
      expect(startsAt(2), Stage.lastGood);
      expect(startsAt(3), Stage.lastGood);
    });
  });

  group('bootstrap and offline', () {
    test('bootstrap values load as fresh and ready resolves at once', () async {
      final server = await startServer();
      final client = clientOf(server, bootstrap: evaluation);
      expect(client.stage, Stage.fresh);
      expectAnswers(client, answersOf('server/evaluation.json'));
      expect(await client.ready(const Duration(hours: 1)), Stage.fresh);
      // The client still fetches.
      await (await server.next()).respondJson(afterKillSwitch);
      await eventually(
        () => client.getBool('checkout', true) == false,
        reason: 'the fetched values replace the bootstrap',
      );
    });

    test('a refused bootstrap falls through to the store', () async {
      final store = await storeHolding(evaluation, context);
      final server = await startServer();
      final logs = LogRecorder();
      final client = clientOf(
        server,
        store: store,
        logs: logs,
        bootstrap: const {'schema_version': 1},
      );
      expect(client.stage, Stage.lastGood);
      expect(logs.fieldsOf(LogEvents.fetchFailed).first['source'], 'bootstrap');
    });

    test('offline mode: no request, fresh, events recorded, set and reload '
        'fire the change callback', () async {
      final server = await startServer();
      var source = evaluation;
      final client = Flagnetic.client(
        '',
        baseUrl: server.url,
        context: context,
        options: ClientOptions(offline: OfflineSource.read(() => source)),
      );
      addTearDown(client.close);
      expect(client.stage, Stage.fresh);
      expect(await client.ready(const Duration(hours: 1)), Stage.fresh);
      expectAnswers(client, answersOf('server/evaluation.json'));

      final changes = <List<String>>[];
      client.onChange(changes.add);
      client.offline!.set('checkout', false);
      expect(client.getDetail('checkout', true).value, isFalse);
      expect(client.getDetail('checkout', true).reason, 'targeting_match');
      client.offline!.set('brand_new', 'hello');
      expect(client.getDetail('brand_new', '').value, 'hello');
      expect(client.getDetail('brand_new', '').reason, 'targeting_match');

      source = afterKillSwitch;
      client.offline!.reload();
      expectAnswers(
        client,
        answersOf('server/evaluation-after-kill-switch.json'),
      );
      expect(changes, [
        ['checkout'],
        ['brand_new'],
        ['brand_new', 'new_search'],
      ]);

      client.alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-8f1c',
        toUnitType: 'user',
        toUnitId: 'u-1001',
      );
      final alias = client.offline!.recorded.single;
      expect(alias, containsPair('type', 'alias'));
      expect(alias['event_id'], matches(RegExp(r'^[0-9a-f]{8}-[0-9a-f]{4}-7')));
      expect(alias, containsPair('to_unit_id', 'u-1001'));

      await client.flush();
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(server.received, isEmpty);
    });

    test('an offline source that does not read is built_in', () {
      final logs = LogRecorder();
      final client = Flagnetic.client(
        '',
        context: context,
        options: ClientOptions(
          logger: logs.call,
          offline: OfflineSource.read(() => '{"truncated'),
        ),
      );
      expect(client.stage, Stage.builtIn);
      expect(client.getBool('checkout', false), isFalse);
      expect(logs.count(LogEvents.fetchFailed), 1);
    });
  });

  group('remote evaluation', () {
    test('posts the context with the protocol headers, adding the '
        'predeclared attributes the context does not set', () async {
      final server = await startServer();
      clientOf(
        server,
        of: const FlagneticContext(
          units: {'anonymous': 'd-8f1c'},
          attributes: {'platform': 'from-context', 'country': 'DE'},
        ),
        platform: 'android',
        appVersion: '1.42.0',
      );
      final request = await server.next();
      expect(request.method, 'POST');
      expect(request.path, '/sdk/v1/evaluate');
      expect(request.headers['authorization'], 'Bearer $clientKey');
      expect(request.headers['content-type'], 'application/json');
      expect(request.headers.containsKey('content-encoding'), isFalse);
      expect(request.headers['accept-encoding'], contains('gzip'));
      expect(request.headers['flagnetic-schema-version'], '1');
      expect(
        request.headers['user-agent'],
        'flagnetic-dart/$sdkVersion (android)',
      );
      expect(request.json, {
        'context': {
          'units': {'anonymous': 'd-8f1c'},
          'attributes': {
            'platform': 'from-context',
            'app_version': '1.42.0',
            'sdk': 'flagnetic-dart',
            'sdk_version': sdkVersion,
            'country': 'DE',
          },
        },
      });
    });

    test('a 200 replaces values, goes fresh, fires the change callback with '
        'the changed keys and resolves ready', () async {
      final server = await startServer();
      final logs = LogRecorder();
      final client = clientOf(
        server,
        logs: logs,
        pollInterval: const Duration(milliseconds: 20),
      );
      final changes = <List<String>>[];
      final stages = <Stage>[];
      client
        ..onChange(changes.add)
        ..onStage(stages.add);
      final ready = client.ready(const Duration(hours: 1));

      await (await server.next()).respondJson(evaluation);
      expect(await ready, Stage.fresh);
      expectAnswers(client, answersOf('server/evaluation.json'));
      expect(changes.single, [
        'banner_text',
        'checkout',
        'max_items',
        'new_search',
        'theme',
      ]);

      await (await server.next()).respondJson(afterKillSwitch);
      await eventually(() => changes.length == 2);
      expect(changes.last, ['checkout', 'new_search']);

      await (await server.next()).respondJson(afterKillSwitch);
      await server.next();
      expect(changes, hasLength(2), reason: 'nothing changed');
      expect(stages, [Stage.fresh]);
      expect(logs.count(LogEvents.valuesLoaded), 3);
    });

    test('ignored attributes are logged once per name', () async {
      final server = await startServer();
      final logs = LogRecorder();
      clientOf(
        server,
        logs: logs,
        pollInterval: const Duration(milliseconds: 5),
      );
      for (var i = 0; i < 3; i++) {
        await (await server.next()).respondJson(evaluation);
      }
      await eventually(() => logs.count(LogEvents.valuesLoaded) == 3);
      expect(
        logs.fieldsOf(LogEvents.attributeIgnored),
        unorderedEquals([
          {'name': 'favourite_colour', 'code': 'undeclared'},
          {'name': 'plan', 'code': 'type_mismatch'},
        ]),
      );
    });
  });

  group('the fallback chain', () {
    final failures = <String, FutureOr<void> Function(Exchange)>{
      'timeout': (_) {}, // never answered within the request timeout
      'network error': (e) => e.dropConnection(),
      '500': (e) => e.respond(500),
      '503': (e) => e.respond(
        503,
        body: protocolBytes('server/problems/503-ruleset_unavailable.json'),
        headers: {'Content-Type': 'application/problem+json'},
      ),
      '429': (e) => e.respond(
        429,
        body: protocolBytes('server/problems/429-rate_limited.json'),
        headers: {'Retry-After': '1'},
      ),
      'corrupt JSON': (e) => e.respond(200, body: '{"schema_version": 1,'),
      'empty body': (e) => e.respond(200),
      '{}': (e) => e.respondJson(<String, Object?>{}),
      'a ruleset document': (e) =>
          e.respond(200, body: protocolBytes('server/ruleset.json')),
      'unsupported schema': (e) => e.respond(
        200,
        body: protocolBytes(
          'degradation/evaluation-schema-version-above-requested.json',
        ),
      ),
    };
    for (final MapEntry(key: name, value: fail) in failures.entries) {
      test(
        '$name: fetch_failed, values kept, fresh becomes last_good',
        () async {
          final server = await startServer();
          final logs = LogRecorder();
          final client = clientOf(
            server,
            logs: logs,
            pollInterval: const Duration(milliseconds: 10),
            requestTimeout: const Duration(milliseconds: 200),
          );
          final stages = <Stage>[];
          client.onStage(stages.add);
          await (await server.next()).respondJson(evaluation);
          await eventually(() => client.stage == Stage.fresh);

          await fail(await server.next());
          await eventually(() => client.stage == Stage.lastGood);
          expect(logs.count(LogEvents.fetchFailed), 1);
          expect(client.status().staleSince, isNotNull);
          expectAnswers(client, answersOf('server/evaluation.json'));
          expect(stages, [Stage.fresh, Stage.lastGood]);
          expect(client.getDetail('checkout', false).stage, Stage.lastGood);
          expect(logs.fieldsOf(LogEvents.stageChanged).last, {
            'from': 'fresh',
            'to': 'last_good',
          });

          // A retry follows by backoff, and a success recovers.
          await (await server.next()).respondJson(afterKillSwitch);
          await eventually(() => client.stage == Stage.fresh);
          expect(client.status().staleSince, isNull);
        },
      );
    }

    test('an unsupported schema is logged once', () async {
      final server = await startServer();
      final logs = LogRecorder();
      clientOf(server, logs: logs);
      for (var i = 0; i < 3; i++) {
        await (await server.next()).respond(
          200,
          body: protocolBytes(
            'degradation/evaluation-schema-version-above-requested.json',
          ),
        );
      }
      await eventually(() => logs.count(LogEvents.fetchFailed) == 3);
      expect(logs.count(LogEvents.schemaUnsupported), 1);
    });

    test('a failure without values keeps built_in', () async {
      final server = await startServer();
      final client = clientOf(server);
      await (await server.next()).respond(500);
      await eventually(() => client.status().staleSince != null);
      expect(client.stage, Stage.builtIn);
      expectAnswers(client, answersOf('built_in'));
    });

    test(
      'retries back off with full jitter from 2 s, capped at 5 min',
      () async {
        final server = await startServer();
        clientOf(server);
        var answered = DateTime.now();
        final waits = <Duration>[];
        for (var i = 0; i < 12; i++) {
          final exchange = await server.next();
          waits.add(exchange.arrivedAt.difference(answered));
          answered = DateTime.now();
          await exchange.respond(500);
        }
        // Bounds 2, 4, 8 … ms, capped at 300 ms: the seconds / 1000.
        for (final (i, wait) in waits.skip(1).indexed) {
          final bound = Duration(milliseconds: i < 8 ? 2 << i : 300);
          expect(
            wait,
            lessThanOrEqualTo(bound + const Duration(milliseconds: 100)),
            reason: 'retry ${i + 1}',
          );
        }
      },
    );

    for (final status in [401, 403]) {
      test('$status: key_revoked once, values kept, retried at the 15 min '
          'cap', () async {
        final server = await startServer();
        final logs = LogRecorder();
        final client = clientOf(
          server,
          logs: logs,
          pollInterval: const Duration(milliseconds: 10),
        );
        await (await server.next()).respondJson(evaluation);
        await eventually(() => client.stage == Stage.fresh);
        for (var i = 0; i < 12; i++) {
          await (await server.next(
            timeout: const Duration(seconds: 2),
          )).respond(status, body: '{"code":"sdk_key_revoked"}');
        }
        await eventually(() => logs.count(LogEvents.fetchFailed) == 12);
        expect(logs.count(LogEvents.keyRevoked), 1);
        expect(
          logs.events.firstWhere((e) => e.event == 'key_revoked').level,
          LogLevel.error,
        );
        expect(logs.fieldsOf(LogEvents.keyRevoked).single, {
          'status': status,
          'code': 'sdk_key_revoked',
        });
        expect(client.stage, Stage.lastGood);
        expectAnswers(client, answersOf('server/evaluation.json'));
      });
    }
  });

  group('getters', () {
    test('are total: missing flag, type mismatch, built_in and an omitted '
        'flag serve the default with fallback', () async {
      final server = await startServer();
      final logs = LogRecorder();
      final client = clientOf(server, logs: logs);
      final builtIn = client.getDetail('checkout', false);
      expect(
        [builtIn.value, builtIn.reason, builtIn.stage, builtIn.errorCode],
        [false, 'fallback', Stage.builtIn, 'no_values'],
      );

      final partial = {
        ...evaluation,
        'flags': {
          'checkout': (evaluation['flags']! as Map)['checkout'],
          'broken': {'type': 'boolean', 'reason': 'error', 'error_code': 'x'},
          'wrong_value': {'type': 'boolean', 'value': 'yes', 'reason': 'rule'},
        },
      };
      await (await server.next()).respondJson(partial);
      await eventually(() => client.stage == Stage.fresh);

      expect(client.getBool('checkout', false), isTrue);
      expect(client.getString('banner_text', 'd'), 'd', reason: 'omitted');
      expect(client.getDetail('banner_text', 'd').errorCode, 'flag_not_found');
      expect(client.getString('checkout', 'd'), 'd');
      expect(client.getNumber('checkout', 7), 7);
      expect(client.getJson('checkout', const {'a': 1}), {'a': 1});
      expect(client.getDetail('checkout', 'd').errorCode, 'type_mismatch');
      expect(client.getBool('broken', true), isTrue);
      expect(client.getDetail('broken', true).errorCode, 'x');
      expect(client.getBool('wrong_value', true), isTrue);
      expect(logs.count(LogEvents.typeMismatch), 1, reason: 'once per key');
    });

    test('a JSON value is the caller\'s copy', () async {
      final server = await startServer();
      final client = clientOf(server);
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);
      (client.getJson('theme', null)! as Map<String, Object?>)['mode'] = 'x';
      expect(client.getJson('theme', null), {'mode': 'light'});
    });
  });

  group('ready, stage and close', () {
    test('ready with a timeout returns the stage reached', () async {
      final server = await startServer();
      final client = clientOf(server);
      final ready = client.ready(const Duration(milliseconds: 50));
      final exchange = await server.next();
      expect(await ready, Stage.builtIn);
      await exchange.respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);
      expect(await client.ready(const Duration(hours: 1)), Stage.fresh);
    });

    test('a failure does not resolve ready early', () async {
      final server = await startServer();
      final client = clientOf(server);
      var resolved = false;
      unawaited(
        client
            .ready(const Duration(milliseconds: 300))
            .then((_) => resolved = true),
      );
      await (await server.next()).respond(500);
      await server.next();
      expect(resolved, isFalse);
    });

    test('the stage getter, callback, log and detail agree', () async {
      final store = await storeHolding(evaluation, context);
      final server = await startServer();
      final logs = LogRecorder();
      final client = clientOf(
        server,
        store: store,
        logs: logs,
        pollInterval: const Duration(milliseconds: 10),
      );
      final seen = <(Stage, Stage, Stage)>[];
      client.onStage(
        (stage) => seen.add((
          stage,
          client.stage,
          client.getDetail('checkout', false).stage,
        )),
      );
      await (await server.next()).respondJson(evaluation);
      await (await server.next()).respond(500);
      await eventually(() => seen.length == 2);
      expect(seen, [
        (Stage.fresh, Stage.fresh, Stage.fresh),
        (Stage.lastGood, Stage.lastGood, Stage.lastGood),
      ]);
      expect(
        logs
            .fieldsOf(LogEvents.stageChanged)
            .map((f) => '${f['from']}>${f['to']}'),
        ['built_in>last_good', 'last_good>fresh', 'fresh>last_good'],
      );
    });

    test('close stops timers and returns promptly during a request', () async {
      final server = await startServer();
      final client = clientOf(server);
      await server.next(); // left in flight
      final watch = Stopwatch()..start();
      await client.close();
      expect(watch.elapsed, lessThan(const Duration(seconds: 2)));
      expect(await client.ready(const Duration(hours: 1)), Stage.builtIn);
      final before = server.received.length;
      await Future<void>.delayed(const Duration(milliseconds: 100));
      expect(server.received.length, before);
      expect(client.getBool('checkout', false), isFalse);
      await client.close();
    });

    test('close during backoff stops retrying', () async {
      final server = await startServer();
      final client = clientOf(server);
      await (await server.next()).respond(500);
      await eventually(() => client.status().staleSince != null);
      await client.close();
      final before = server.received.length;
      await Future<void>.delayed(const Duration(milliseconds: 100));
      expect(server.received.length, before);
    });
  });

  group('the API-boundary catch-all', () {
    test('a store throwing on every call: defaults, then fresh values, logs, '
        'no exception', () async {
      for (final store in [_ThrowingStore(), _FailingAsyncStore()]) {
        final server = await startServer();
        final logs = LogRecorder();
        final client = clientOf(server, store: store, logs: logs);
        expect(client.stage, Stage.builtIn);
        expectAnswers(client, answersOf('built_in'));
        await (await server.next()).respondJson(evaluation);
        await eventually(() => client.stage == Stage.fresh);
        expectAnswers(client, answersOf('server/evaluation.json'));
        await client.close();
        expect(logs.count(LogEvents.storeFailed), greaterThanOrEqualTo(2));
      }
    });

    test('a corrupted cache entry: built_in and a log, then fresh', () async {
      final corruptions = <String Function(String)>[
        (_) => 'garbage',
        (_) => '{}',
        (_) => '[1, 2]',
        (entry) =>
            entry.replaceFirst('"schema_version":1', '"schema_version":9'),
        (entry) => entry.replaceFirst('"d-8f1c"', '"someone-else"'),
        (entry) => entry.replaceFirst('"flags":{', '"flags":{"x":1,'),
      ];
      for (final (i, corrupt) in corruptions.indexed) {
        final store = await storeHolding(evaluation, context);
        for (final key in store.entries.keys.toList()) {
          store.entries[key] = corrupt(store.entries[key]!);
        }
        final server = await startServer();
        final logs = LogRecorder();
        final client = clientOf(server, store: store, logs: logs);
        expect(client.stage, Stage.builtIn, reason: 'corruption $i');
        expectAnswers(client, answersOf('built_in'));
        expect(logs.count(LogEvents.storeFailed), 1, reason: 'corruption $i');
        await (await server.next()).respondJson(evaluation);
        await eventually(() => client.stage == Stage.fresh);
        await client.close();
      }
    });

    test('a malformed response: last good and a log', () async {
      final store = await storeHolding(evaluation, context);
      final server = await startServer();
      final logs = LogRecorder();
      final client = clientOf(server, store: store, logs: logs);
      await (await server.next()).respond(200, body: [0xff, 0xfe, 0x00]);
      await eventually(() => logs.count(LogEvents.fetchFailed) == 1);
      expect(client.stage, Stage.lastGood);
      expectAnswers(client, answersOf('server/evaluation.json'));
    });

    test('throwing listeners and a throwing logger never escape', () async {
      final server = await startServer();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: context,
        options: scaledOptions(logger: (_, _, _) => throw StateError('log')),
      );
      addTearDown(client.close);
      client
        ..onChange((_) => throw StateError('change'))
        ..onStage((_) => throw StateError('stage'));
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);
      expect(client.getString('checkout', 'x'), 'x');
    });

    test(
      'an attribute JSON cannot encode is a failure, not an exception',
      () async {
        final server = await startServer();
        final logs = LogRecorder();
        final client = clientOf(
          server,
          logs: logs,
          of: FlagneticContext(
            units: const {'anonymous': 'd-1'},
            attributes: {'odd': Object()},
          ),
        );
        await eventually(() => logs.count(LogEvents.fetchFailed) >= 1);
        expect(client.stage, Stage.builtIn);
        expect(server.received, isEmpty);
      },
    );
  });
}

final class _ThrowingStore implements FlagneticStore {
  @override
  String? get(String key) => throw StateError('get');

  @override
  void set(String key, String value) => throw StateError('set');

  @override
  void remove(String key) => throw StateError('remove');
}

final class _FailingAsyncStore implements FlagneticStore {
  @override
  Future<String?> get(String key) => Future.error(StateError('get'));

  @override
  Future<void> set(String key, String value) => Future.error(StateError('set'));

  @override
  Future<void> remove(String key) => Future.error(StateError('remove'));
}

final class _AsyncStore implements FlagneticStore {
  _AsyncStore(this._inner);

  final InMemoryStore _inner;

  @override
  Future<String?> get(String key) async {
    await Future<void>.delayed(const Duration(milliseconds: 20));
    return _inner.get(key);
  }

  @override
  Future<void> set(String key, String value) async => _inner.set(key, value);

  @override
  Future<void> remove(String key) async => _inner.remove(key);
}
