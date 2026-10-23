@TestOn('vm')
// The VM only: this suite needs a `dart:io` mock server.
library;

// The client-mode refetch triggers against the mock server: setting the context, foreground, the interval, refresh and
// the SSE opt-in, all coalesced into one in-flight request. Durations are
// the protocol's divided by 1000, as in the other behaviour tests.
import 'dart:async';

import 'package:flagnetic/flagnetic.dart';
import 'package:flagnetic/src/client/options.dart';
import 'package:test/test.dart';

import 'support/mock_server.dart';

/// The context an anonymous visitor starts with.
const anonymous = FlagneticContext(units: {'anonymous': 'd-8f1c'});

/// The same visitor after signing in: the anonymous unit stays in the context.
const signedIn = FlagneticContext(
  units: {'anonymous': 'd-8f1c', 'user': 'u-1001'},
);

/// The evaluate response the fixtures hold, and the one a kill switch made.
Map<String, Object?> get evaluation =>
    protocolJson('server/evaluation.json')! as Map<String, Object?>;
Map<String, Object?> get afterKillSwitch =>
    protocolJson('server/evaluation-after-kill-switch.json')!
        as Map<String, Object?>;

/// The answers of `scenarios.json` for a document.
List<Object?> answersOf(String document) =>
    ((protocolJson('scenarios.json')! as Map<String, Object?>)['answers']!
            as Map<String, Object?>)[document]!
        as List<Object?>;

void main() {
  group('setting the context', () {
    test('serves the previous values as last_good until the response, then '
        'completes with the new ones and fires the diff', () async {
      final server = await MockServer.start();
      final changes = <List<String>>[];
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(pollInterval: const Duration(hours: 1)),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      client.onChange(changes.add);
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);
      changes.clear();

      final settled = client.setContext(signedIn);
      // Previous values, at last_good: no flash of built-in defaults.
      expect(client.stage, Stage.lastGood);
      expectAnswers(client, answersOf('server/evaluation.json'));
      expect(client.context.units, signedIn.units);

      final exchange = await server.next();
      // Still the previous values while the request is out.
      expectAnswers(client, answersOf('server/evaluation.json'));
      expect((exchange.json! as Map<String, Object?>)['context'], {
        'units': {'anonymous': 'd-8f1c', 'user': 'u-1001'},
        'attributes': {'sdk': sdkName, 'sdk_version': sdkVersion},
      });
      await exchange.respondJson(afterKillSwitch);

      await settled;
      expect(client.stage, Stage.fresh);
      expectAnswers(
        client,
        answersOf('server/evaluation-after-kill-switch.json'),
      );
      expect(changes, [
        ['checkout', 'new_search'],
      ]);
    });

    test('a failed attempt still completes the future', () async {
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(pollInterval: const Duration(hours: 1)),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);

      final settled = client.setContext(signedIn);
      await (await server.next()).respond(500);
      await settled.timeout(const Duration(seconds: 2));
      expect(client.stage, Stage.lastGood);
      expectAnswers(client, answersOf('server/evaluation.json'));
    });

    test('values that answered the previous context are not served, and stay '
        'that unit set\'s last good', () async {
      final server = await MockServer.start();
      final store = InMemoryStore();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(
          store: store,
          pollInterval: const Duration(hours: 1),
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      // The context changes while the first request is in flight.
      final first = await server.next();
      final settled = client.setContext(signedIn);
      await first.respondJson(evaluation);

      final second = await server.next();
      expect(
        ((second.json! as Map<String, Object?>)['context']!
            as Map<String, Object?>)['units'],
        signedIn.units,
        reason: 'the refetch carries the new context',
      );
      await second.respondJson(afterKillSwitch);
      await settled;
      expectAnswers(
        client,
        answersOf('server/evaluation-after-kill-switch.json'),
      );
      await eventually(
        () => store.entries.containsKey(lastGoodKeyOf(anonymous)),
        reason: 'the anonymous unit set keeps its last good',
      );
      expect(
        store.entries[lastGoodKeyOf(anonymous)],
        contains('"ruleset_version":1'),
      );
    });
  });

  group('the refetch triggers', () {
    test('refresh fetches now and completes with the answer', () async {
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(pollInterval: const Duration(hours: 1)),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);

      final settled = client.refresh();
      await (await server.next()).respondJson(afterKillSwitch);
      await settled;
      expectAnswers(
        client,
        answersOf('server/evaluation-after-kill-switch.json'),
      );
    });

    test('the interval runs while foregrounded and stops in the background, '
        'where foreground starts it again', () async {
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(
          pollInterval: const Duration(milliseconds: 40),
          minimumRefetchGap: Duration.zero,
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      await (await server.next()).respondJson(evaluation);
      // The interval keeps fetching while the app is foregrounded.
      await (await server.next()).respondJson(evaluation);

      client.background();
      expect(client.isForeground, isFalse);
      final quiet = server.received.length;
      await Future<void>.delayed(const Duration(milliseconds: 200));
      expect(
        server.received.length,
        quiet,
        reason: 'nothing runs in the background',
      );

      client.foreground();
      expect(client.isForeground, isTrue);
      await (await server.next()).respondJson(evaluation);
      await (await server.next()).respondJson(evaluation);
    });

    test('the poll interval never falls below its floor', () async {
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(
          // A millisecond is below the floor the options declare (30 ms
          // here, 30 s divided by 1000).
          pollInterval: const Duration(milliseconds: 1),
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      final first = await server.next();
      await first.respondJson(evaluation);
      final second = await server.next();
      expect(
        second.arrivedAt.difference(first.arrivedAt).inMilliseconds,
        greaterThanOrEqualTo(25),
      );
    });

    test(
      'a foreground within the minimum refetch gap fetches nothing',
      () async {
        final server = await MockServer.start();
        final client = Flagnetic.client(
          clientKey,
          baseUrl: server.url,
          context: anonymous,
          options: scaledOptions(
            pollInterval: const Duration(hours: 1),
            minimumRefetchGap: const Duration(seconds: 5),
          ),
        );
        addTearDown(() async {
          await client.close();
          await server.close();
        });
        await (await server.next()).respondJson(evaluation);
        await eventually(() => client.stage == Stage.fresh);

        final before = server.received.length;
        client
          ..foreground()
          ..foreground()
          ..foreground();
        await Future<void>.delayed(const Duration(milliseconds: 100));
        expect(server.received.length, before, reason: 'throttled');

        // refresh and setContext are the app asking, and are never throttled.
        final settled = client.refresh();
        await (await server.next()).respondJson(evaluation);
        await settled;
      },
    );

    test('triggers during an in-flight request coalesce into one', () async {
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(
          pollInterval: const Duration(hours: 1),
          minimumRefetchGap: Duration.zero,
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      final first = await server.next();
      // Four triggers while the first request is still out.
      final settled = Future.wait([
        client.refresh(),
        client.refresh(),
        client.setContext(signedIn),
      ]);
      client.foreground();
      await first.respondJson(evaluation);

      await (await server.next()).respondJson(afterKillSwitch);
      await settled;
      expect(server.received.length, 2, reason: 'one follow-up, not four');
      await Future<void>.delayed(const Duration(milliseconds: 100));
      expect(server.received.length, 2);
    });
  });

  group('the SSE opt-in', () {
    test('is off by default: no stream is opened', () async {
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(pollInterval: const Duration(hours: 1)),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);
      await Future<void>.delayed(const Duration(milliseconds: 100));
      expect(
        server.received.map((e) => e.path),
        everyElement('/sdk/v1/evaluate'),
      );
    });

    test('opens the stream first and evaluates after a ruleset event above '
        'the held version', () async {
      final server = await MockServer.start();
      final logs = LogRecorder();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(
          sse: true,
          logger: logs.call,
          pollInterval: const Duration(hours: 1),
          streamSilenceTimeout: const Duration(seconds: 30),
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      final opened = await server.next();
      expect(opened.method, 'GET');
      expect(opened.path, '/sdk/v1/stream');
      expect(opened.headers['authorization'], 'Bearer $clientKey');
      expect(opened.headers.containsKey('last-event-id'), isFalse);
      final stream = await opened.respondStream();

      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);
      expect(logs.count(LogEvents.sseConnected), 1);

      // The held version: nothing to fetch.
      await stream.send(
        'retry: 3000\n\nevent: ruleset\nid: 1\n'
        'data: {"version":1}\n\n',
      );
      await stream.send(': hb\n\n');
      final quiet = server.received.length;
      await Future<void>.delayed(const Duration(milliseconds: 100));
      expect(server.received.length, quiet);

      // A kill switch: one evaluate.
      await stream.send('event: ruleset\nid: 2\ndata: {"version":2}\n\n');
      await (await server.next()).respondJson(afterKillSwitch);
      await eventually(
        () => servesAnswers(
          client,
          answersOf('server/evaluation-after-kill-switch.json'),
        ),
        reason: 'the kill switch is served',
      );
    });

    test('a stream the server closes reconnects with Last-Event-ID and '
        'evaluates again', () async {
      final server = await MockServer.start();
      final logs = LogRecorder();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(
          sse: true,
          logger: logs.call,
          pollInterval: const Duration(hours: 1),
          streamSilenceTimeout: const Duration(seconds: 30),
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      final stream = await (await server.next()).respondStream();
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);
      await stream.closeStream();

      final again = await server.next();
      expect(again.path, '/sdk/v1/stream');
      expect(
        again.headers['last-event-id'],
        '1',
        reason: 'revalidate from the held ruleset version',
      );
      await again.respondStream();
      // Versions may have been missed while the stream was down.
      await (await server.next()).respondJson(afterKillSwitch);
      await eventually(
        () => servesAnswers(
          client,
          answersOf('server/evaluation-after-kill-switch.json'),
        ),
      );
      expect(logs.count(LogEvents.sseDropped), 1);
      expect(logs.fieldsOf(LogEvents.sseDropped).single['cause'], 'closed');
    });

    test('the background suspends the stream and the foreground opens it '
        'again', () async {
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(
          sse: true,
          pollInterval: const Duration(hours: 1),
          streamSilenceTimeout: const Duration(seconds: 30),
          minimumRefetchGap: Duration.zero,
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      final stream = await (await server.next()).respondStream();
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);

      client.background();
      // Nothing runs there: not the interval, and not the stream either.
      await stream.send('event: ruleset\nid: 2\ndata: {"version":2}\n\n');
      final quiet = server.received.length;
      await Future<void>.delayed(const Duration(milliseconds: 200));
      expect(server.received.length, quiet);
      expectAnswers(client, answersOf('server/evaluation.json'));

      // The foreground opens the stream again and evaluates once.
      client.foreground();
      final reopened = await server.next();
      expect(reopened.path, '/sdk/v1/stream');
      await reopened.respondStream();
      await (await server.next()).respondJson(afterKillSwitch);
      await eventually(
        () => servesAnswers(
          client,
          answersOf('server/evaluation-after-kill-switch.json'),
        ),
      );
    });

    test('a background and a foreground while the stream connects leave one '
        'stream listened to', () async {
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: scaledOptions(
          sse: true,
          pollInterval: const Duration(hours: 1),
          streamSilenceTimeout: const Duration(seconds: 30),
          minimumRefetchGap: Duration.zero,
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      await (await server.next(path: '/sdk/v1/stream')).respondStream();
      await (await server.next(
        path: '/sdk/v1/evaluate',
      )).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);

      // The app goes to the background and back twice before the stream it
      // reopened the first time is answered.
      client
        ..background()
        ..foreground();
      final given = await server.next(path: '/sdk/v1/stream');
      client
        ..background()
        ..foreground();
      final kept = await server.next(path: '/sdk/v1/stream');
      final stale = await given.respondStream();
      final held = await kept.respondStream();
      // Answers the refetches of both returns.
      for (;;) {
        try {
          await (await server.next(
            path: '/sdk/v1/evaluate',
            timeout: const Duration(milliseconds: 300),
          )).respondJson(evaluation);
        } on TimeoutException {
          break;
        }
      }

      int evaluates() =>
          server.received.where((e) => e.path == '/sdk/v1/evaluate').length;
      final before = evaluates();
      await stale.send('event: ruleset\nid: 5\ndata: {"version":5}\n\n');
      await Future<void>.delayed(const Duration(milliseconds: 200));
      expect(evaluates(), before, reason: 'the given-up stream is not read');

      await held.send('event: ruleset\nid: 5\ndata: {"version":5}\n\n');
      await (await server.next(
        path: '/sdk/v1/evaluate',
      )).respondJson(afterKillSwitch);
      await eventually(
        () => servesAnswers(
          client,
          answersOf('server/evaluation-after-kill-switch.json'),
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 100));
      expect(evaluates(), before + 1, reason: 'one change, one evaluate');
    });

    test(
      'a stream refused with 401 stops, logs once and keeps serving',
      () async {
        final server = await MockServer.start();
        final logs = LogRecorder();
        final client = Flagnetic.client(
          clientKey,
          baseUrl: server.url,
          context: anonymous,
          options: scaledOptions(
            sse: true,
            logger: logs.call,
            pollInterval: const Duration(hours: 1),
            streamSilenceTimeout: const Duration(seconds: 30),
          ),
        );
        addTearDown(() async {
          await client.close();
          await server.close();
        });
        await (await server.next()).respond(401);
        // The stream did not connect, so the fetch loop starts anyway.
        await (await server.next()).respondJson(evaluation);
        await eventually(() => client.stage == Stage.fresh);
        expectAnswers(client, answersOf('server/evaluation.json'));
        await Future<void>.delayed(const Duration(milliseconds: 100));
        expect(logs.count(LogEvents.keyRevoked), 1);
        expect(
          server.received.where((e) => e.path == '/sdk/v1/stream').length,
          1,
          reason: 'a refused key never reconnects',
        );
      },
    );
  });

  group('the anonymous unit ID helper', () {
    test('generates one, persists it and reads it back', () async {
      final store = InMemoryStore();
      final first = await anonymousUnitId(store, 'visitor');
      expect(first, isNotEmpty);
      expect(store.entries[anonymousUnitIdKey('visitor')], first);
      expect(await anonymousUnitId(store, 'visitor'), first);
      // One ID per unit type the app names; the SDK invents no unit type.
      expect(await anonymousUnitId(store, 'device'), isNot(first));
    });

    test('a store that throws still answers, with a log', () async {
      final logs = LogRecorder();
      final id = await anonymousUnitId(
        _ThrowingStore(),
        'visitor',
        logger: logs.call,
      );
      expect(id, isNotEmpty);
      expect(logs.count(LogEvents.storeFailed), 2);
    });
  });

  group('kill-switch propagation', () {
    test('a stream event reaches onChange within 2 s, in real time', () async {
      // Unscaled (no scaledOptions): the 2 s target is the SDK's own, and
      // the number this prints unconditionally is what CI puts in the job
      // summary.
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: anonymous,
        options: ClientOptions(
          sse: true,
          pollInterval: const Duration(hours: 1),
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      final stream = await (await server.next()).respondStream();
      await (await server.next()).respondJson(evaluation);
      await eventually(() => client.stage == Stage.fresh);

      final changed = Completer<List<String>>();
      client.onChange((keys) {
        if (!changed.isCompleted) changed.complete(keys);
      });
      final started = DateTime.now();
      await stream.send('event: ruleset\nid: 2\ndata: {"version":2}\n\n');
      await (await server.next()).respondJson(afterKillSwitch);
      final keys = await changed.future.timeout(const Duration(seconds: 2));
      final elapsed = DateTime.now().difference(started);
      // ignore: avoid_print
      print('kill_switch_propagation_ms=${elapsed.inMilliseconds}');
      expect(keys, isNotEmpty);
    });
  });
}

/// The store key of a context's unit set, as the client writes it.
String lastGoodKeyOf(FlagneticContext context) =>
    clientKeys.lastGood(context.units);

final class _ThrowingStore implements FlagneticStore {
  @override
  Never get(String key) => throw StateError('no store');

  @override
  Never set(String key, String value) => throw StateError('no store');

  @override
  Never remove(String key) => throw StateError('no store');
}
