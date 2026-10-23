@TestOn('vm')
// The VM only: this suite needs a `dart:io` mock server.
library;

// The persisted event queue of client mode: exposures,
// aliases and evaluation summaries recorded on the device, surviving a
// restart and flushed once with stable event IDs when the network returns.
import 'dart:convert';

import 'package:flagnetic/flagnetic.dart';
import 'package:flagnetic/src/client/options.dart';
import 'package:test/test.dart';

import 'support/mock_server.dart';

/// The context every client here is built with.
const testContext = FlagneticContext(
  units: {'anonymous': 'd-8f1c'},
  attributes: {'country': 'DE'},
);

/// A client serving [document], with the first evaluate already answered.
/// It never polls again and never flushes on its own, so a test drives it.
Future<({FlagneticClient client, MockServer server, LogRecorder logs})> serving(
  Map<String, Object?> document, {
  InMemoryStore? store,
  MockServer? on,
  bool sendExposures = true,
  bool sendSummaries = true,
  int eventBatchSize = 100,
  int eventQueueCapacity = 1000,
  int exposureMemory = 1000,
  Duration retryBackoff = const Duration(milliseconds: 2),
  Duration flushInterval = const Duration(hours: 1),
  FlagneticContext context = testContext,
}) async {
  final server = on ?? await MockServer.start();
  final logs = LogRecorder();
  final client = Flagnetic.client(
    clientKey,
    baseUrl: server.url,
    context: context,
    options: scaledOptions(
      store: store ?? InMemoryStore(),
      logger: logs.call,
      pollInterval: const Duration(hours: 1),
      platform: 'android',
      appVersion: '1.42.0',
      flushInterval: flushInterval,
      retryBackoff: retryBackoff,
      eventBatchSize: eventBatchSize,
      eventQueueCapacity: eventQueueCapacity,
      exposureMemory: exposureMemory,
      sendExposures: sendExposures,
      sendSummaries: sendSummaries,
    ),
  );
  await (await server.next()).respondJson(document);
  await eventually(() => client.stage == Stage.fresh, reason: 'fresh values');
  return (client: client, server: server, logs: logs);
}

/// Evaluates `checkout` and records that the unit saw what its variation
/// decides: an exposure reports the most recent detail.
void expose(FlagneticClient client) {
  client
    ..getBool('checkout', false)
    ..exposure('checkout');
}

void main() {
  group('exposures', () {
    test('one carries the exposure fields and rides in the envelope '
        'envelope', () async {
      final run = await serving(experimentValues(), sendSummaries: false);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expose(run.client);
      final flushed = run.client.flush();
      final exchange = await run.server.next();
      expect(exchange.method, 'POST');
      expect(exchange.path, '/sdk/v1/events');
      expect(exchange.headers['authorization'], 'Bearer $clientKey');
      expect(exchange.headers['content-type'], 'application/json');

      final envelope = exchange.json! as Map<String, Object?>;
      expect(envelope['schema_version'], 1);
      expect(envelope['sdk'], {
        'name': sdkName,
        'version': sdkVersion,
        'platform': 'android',
      });
      expect(envelope['sent_at'], isA<String>());

      final event = eventsOf(exchange).single;
      expect(event['type'], 'exposure');
      expect(
        event['event_id'],
        matches(RegExp(r'^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab]')),
      );
      expect(event['occurred_at'], isA<String>());
      expect(event['experiment_key'], 'checkout_v2');
      expect(event['flag_key'], 'checkout');
      expect(event['variation_key'], 'treatment');
      expect(event['unit_type'], 'anonymous');
      expect(event['unit_id'], 'd-8f1c');
      expect(event['platform'], 'android');
      expect(event['app_version'], '1.42.0');
      expect(event['evaluation_reason'], 'experiment');
      expect(event['ruleset_version'], 42);
      expect(event['override'], isFalse);

      await acceptBatch(exchange);
      await flushed;
    });

    test('an override-influenced assignment is reported and marked', () async {
      final run = await serving(
        experimentValues(reason: Reasons.override, override: true),
        sendSummaries: false,
      );
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expose(run.client);
      final flushed = run.client.flush();
      final exchange = await run.server.next();
      final event = eventsOf(exchange).single;
      expect(event['evaluation_reason'], 'override');
      expect(event['override'], isTrue);
      await acceptBatch(exchange);
      await flushed;
    });

    test('nothing is recorded without values, for a flag with no experiment '
        'block, or for an unknown flag', () async {
      final server = await MockServer.start();
      final logs = LogRecorder();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: testContext,
        options: scaledOptions(
          logger: logs.call,
          pollInterval: const Duration(hours: 1),
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });

      // Stage built_in: nothing is held, so nothing is reported.
      expect(client.stage, Stage.builtIn);
      client.exposure('checkout');
      expect(logs.fieldsOf(LogEvents.exposureSkipped), [
        {'flag': 'checkout', 'cause': 'no_values'},
      ]);

      await (await server.next()).respondJson(experimentValues());
      await eventually(() => client.stage == Stage.fresh);

      // A flag the document serves without an experiment, and one it does
      // not serve at all: a `fallback` never produces an exposure.
      client
        ..exposure('banner')
        ..exposure('missing');
      expect(logs.fieldsOf(LogEvents.exposureSkipped).skip(1), [
        {'flag': 'banner', 'cause': 'no_experiment'},
        {'flag': 'missing', 'cause': 'no_experiment'},
      ]);

      await client.flush();
      expect(
        server.received.where((e) => e.path == '/sdk/v1/events'),
        isEmpty,
        reason: 'nothing to send',
      );
    });

    test('at stage last_good the cached experiment block and ruleset version '
        'are reported', () async {
      final store = await storeHolding(experimentValues(), testContext);
      final server = await MockServer.start();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: testContext,
        options: scaledOptions(
          store: store,
          pollInterval: const Duration(hours: 1),
          platform: 'android',
          sendSummaries: false,
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      await eventually(() => client.stage == Stage.lastGood);

      // The first evaluate is still in flight: a cached assignment is a
      // valid assignment and is reported from the store's document.
      expose(client);
      final flushed = client.flush();
      final exchange = await server.next(); // the events batch or the fetch
      final events = exchange.path == '/sdk/v1/events'
          ? exchange
          : await (() async {
              await exchange.respond(500);
              return server.next();
            })();
      final event = eventsOf(events).single;
      expect(event['experiment_key'], 'checkout_v2');
      expect(event['ruleset_version'], 42);
      await acceptBatch(events);
      await flushed;
    });

    test('automatic exposure records on every evaluation carrying an '
        'experiment, and a repeat of one assignment does not', () async {
      final run = await serving(
        experimentValues(automaticExposure: true),
        sendSummaries: false,
      );
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expect(run.client.getBool('checkout', false), isTrue);
      expect(run.client.getBool('checkout', false), isTrue);
      run.client
        ..exposure('checkout')
        // A flag without an experiment emits nothing even with the getter.
        ..getString('banner', 'none');

      final flushed = run.client.flush();
      final exchange = await run.server.next();
      expect(
        eventsOf(exchange),
        hasLength(1),
        reason: 'one exposure per (experiment, unit, variation)',
      );
      expect(eventsOf(exchange).single['type'], 'exposure');
      await acceptBatch(exchange);
      await flushed;
    });

    test('evaluation of a flag without automatic exposure records '
        'nothing', () async {
      final run = await serving(experimentValues(), sendSummaries: false);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expect(run.client.getBool('checkout', false), isTrue);
      await run.client.flush();
      expect(
        run.server.received.where((e) => e.path == '/sdk/v1/events'),
        isEmpty,
        reason: 'exposure is an explicit call',
      );
    });

    test('the suppression set is bounded: the oldest assignment is forgotten '
        'and its exposure recorded again', () async {
      final document = experimentValues(automaticExposure: true);
      final flags = document['flags']! as Map<String, Object?>;
      // A second flag with an experiment of its own, so two assignments
      // compete for a set holding one.
      flags['paywall'] = {
        'type': 'boolean',
        'value': true,
        'variation': 'on',
        'reason': 'experiment',
        'automatic_exposure': true,
        'experiment': {
          'key': 'paywall_v3',
          'variation': 'on',
          'unit_type': 'anonymous',
          'unit_id': 'd-8f1c',
          'inclusion_salt': 'b4e0d1a29c3f5687',
          'variation_salt': '5d2c7f10a8b3e694',
          'inclusion_bucket': 1204,
          'variation_bucket': 7781,
          'override': false,
        },
      };
      final run = await serving(
        document,
        sendSummaries: false,
        exposureMemory: 1,
      );
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      run.client
        ..getBool('checkout', false)
        ..getBool('paywall', false)
        // `checkout`'s assignment was forgotten to make room, so this
        // records again: the platform's first exposure is authoritative.
        ..getBool('checkout', false);

      final flushed = run.client.flush();
      final exchange = await run.server.next();
      expect(
        [for (final event in eventsOf(exchange)) event['experiment_key']],
        ['checkout_v2', 'paywall_v3', 'checkout_v2'],
      );
      await acceptBatch(exchange);
      await flushed;
    });

    test('sendExposures off records nothing and warns at '
        'construction', () async {
      final run = await serving(
        experimentValues(automaticExposure: true),
        sendExposures: false,
        sendSummaries: false,
      );
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expect(run.logs.count(LogEvents.exposuresOff), 1);
      expect(run.client.getBool('checkout', false), isTrue);
      run.client.exposure('checkout');
      await run.client.flush();
      expect(
        run.server.received.where((e) => e.path == '/sdk/v1/events'),
        isEmpty,
      );
      expect(run.logs.fieldsOf(LogEvents.exposureSkipped), [
        {'flag': 'checkout', 'cause': 'exposures_off'},
        {'flag': 'checkout', 'cause': 'exposures_off'},
      ]);
    });
  });

  group('aliases', () {
    test('an alias is enqueued and the context is unchanged', () async {
      final run = await serving(experimentValues());
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      run.client.alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-8f1c',
        toUnitType: 'user',
        toUnitId: 'u-1001',
      );
      expect(run.client.context.units, testContext.units);

      final flushed = run.client.flush();
      final exchange = await run.server.next();
      final event = eventsOf(exchange).single;
      expect(event['type'], 'alias');
      expect(event['from_unit_type'], 'anonymous');
      expect(event['from_unit_id'], 'd-8f1c');
      expect(event['to_unit_type'], 'user');
      expect(event['to_unit_id'], 'u-1001');
      expect(event['occurred_at'], isA<String>());
      await acceptBatch(exchange);
      await flushed;
      expect(run.client.context.units, testContext.units);
    });
  });

  group('the retry policy', () {
    test('a batch is retried whole on 500 and 429 with the same event '
        'IDs', () async {
      final run = await serving(experimentValues(), sendSummaries: false);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expose(run.client);
      run.client.alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-8f1c',
        toUnitType: 'user',
        toUnitId: 'u-1001',
      );
      final flushed = run.client.flush();

      final first = await run.server.next();
      final ids = eventIdsOf(first);
      expect(ids, hasLength(2));
      await first.respond(500);
      await flushed;

      final second = await run.server.next();
      expect(eventIdsOf(second), ids, reason: 'the same batch, unchanged');
      await second.respond(
        429,
        headers: {'Retry-After': '60', 'Content-Type': 'application/json'},
        body: jsonEncode({'code': 'rate_limited'}),
      );

      // Retry-After names a minute; the SDK backs off on its own instead,
      // so exposures are not held back for minutes.
      final third = await run.server.next();
      expect(eventIdsOf(third), ids);
      await acceptBatch(third);

      expect(run.logs.count(LogEvents.eventsFailed), 2);
      await run.client.flush();
      expect(
        run.server.received.where((e) => e.path == '/sdk/v1/events'),
        hasLength(3),
        reason: 'an accepted batch is never sent again',
      );
    });

    test('rejected events are never sent again', () async {
      final run = await serving(experimentValues(), sendSummaries: false);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expose(run.client);
      run.client.alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-8f1c',
        toUnitType: 'user',
        toUnitId: '',
      );
      final flushed = run.client.flush();
      final first = await run.server.next();
      final ids = eventIdsOf(first);
      await acceptBatch(
        first,
        rejected: [
          {
            'index': 1,
            'event_id': ids[1],
            'code': 'event_invalid',
            'detail': 'to_unit_id is missing',
          },
        ],
      );
      await flushed;
      expect(run.logs.fieldsOf(LogEvents.queueDropped).single, {
        'cause': 'refused_by_the_platform',
        'events': 1,
        'codes': ['event_invalid'],
      });

      run.client.alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-8f1c',
        toUnitType: 'user',
        toUnitId: 'u-1001',
      );
      final again = run.client.flush();
      final second = await run.server.next();
      expect(
        eventIdsOf(second).toSet().intersection(ids.toSet()),
        isEmpty,
        reason: 'a rejection is final',
      );
      await acceptBatch(second);
      await again;
    });

    test('another 4xx drops the batch and logs it', () async {
      final run = await serving(experimentValues(), sendSummaries: false);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expose(run.client);
      final flushed = run.client.flush();
      await (await run.server.next()).respond(
        400,
        headers: {'Content-Type': 'application/problem+json'},
        body: jsonEncode({'code': 'malformed_body'}),
      );
      await flushed;

      expect(run.logs.fieldsOf(LogEvents.queueDropped).single, {
        'cause': 'client_error',
        'events': 1,
        'status': 400,
      });
      await run.client.flush();
      expect(
        run.server.received.where((e) => e.path == '/sdk/v1/events'),
        hasLength(1),
        reason: 'sending it again would fail the same way',
      );
    });

    test('a refused key is surfaced once and the batch waits', () async {
      final run = await serving(
        experimentValues(),
        sendSummaries: false,
        retryBackoff: const Duration(seconds: 30),
      );
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expose(run.client);
      final flushed = run.client.flush();
      await (await run.server.next()).respond(
        401,
        headers: {'Content-Type': 'application/problem+json'},
        body: jsonEncode({'code': 'sdk_key_revoked'}),
      );
      await flushed;

      expect(run.logs.fieldsOf(LogEvents.keyRevoked).single, {
        'status': 401,
        'code': 'sdk_key_revoked',
      });
      final failed = run.logs.fieldsOf(LogEvents.eventsFailed).single;
      expect(failed['status'], 401);
      expect(
        (failed['retry_in_ms']! as int),
        lessThanOrEqualTo(
          scaledOptions().unauthorizedBackoffCap.inMilliseconds,
        ),
      );
      expect(run.client.stage, Stage.fresh, reason: 'values keep serving');
    });
  });

  group('the queue', () {
    test('it is bounded and drops the oldest with queue_dropped', () async {
      final store = InMemoryStore();
      final run = await serving(
        experimentValues(),
        store: store,
        eventQueueCapacity: 3,
        eventBatchSize: 100,
      );
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      for (var i = 0; i < 5; i++) {
        run.client.alias(
          fromUnitType: 'anonymous',
          fromUnitId: 'd-8f1c',
          toUnitType: 'user',
          toUnitId: 'u-$i',
        );
      }
      expect(run.logs.fieldsOf(LogEvents.queueDropped), [
        {'cause': 'queue_full', 'events': 1, 'capacity': 3},
        {'cause': 'queue_full', 'events': 1, 'capacity': 3},
      ]);

      final flushed = run.client.flush();
      final exchange = await run.server.next();
      expect(
        [for (final e in eventsOf(exchange)) e['to_unit_id']],
        ['u-2', 'u-3', 'u-4'],
        reason: 'the oldest go first',
      );
      await acceptBatch(exchange);
      await flushed;
    });

    test('a batch carries at most the batch size, and the rest '
        'follows', () async {
      final run = await serving(experimentValues(), eventBatchSize: 2);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      for (var i = 0; i < 3; i++) {
        run.client.alias(
          fromUnitType: 'anonymous',
          fromUnitId: 'd-8f1c',
          toUnitType: 'user',
          toUnitId: 'u-$i',
        );
      }
      // Reaching the batch size flushes on its own.
      final first = await run.server.next();
      expect(eventsOf(first), hasLength(2));
      await acceptBatch(first);

      final second = await run.server.next();
      expect(eventsOf(second), hasLength(1));
      await acceptBatch(second);
    });

    test('events survive a restart and go out once with the IDs they were '
        'recorded under', () async {
      final store = InMemoryStore();
      final first = await serving(
        experimentValues(),
        store: store,
        sendSummaries: false,
        retryBackoff: const Duration(seconds: 30),
      );

      expose(first.client);
      first.client.alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-8f1c',
        toUnitType: 'user',
        toUnitId: 'u-1001',
      );
      final flushed = first.client.flush();
      final offline = await first.server.next();
      final ids = eventIdsOf(offline);
      await offline.respond(500);
      await flushed;

      // The app is killed: what the platform never answered is on disk.
      await first.client.close();
      await first.server.close();
      expect(store.entries, contains(clientKeys.events));
      expect(
        (jsonDecode(store.entries[clientKeys.events]!) as List<Object?>),
        hasLength(2),
      );

      final second = await serving(
        experimentValues(),
        store: store,
        sendSummaries: false,
      );
      addTearDown(() async {
        await second.client.close();
        await second.server.close();
      });
      final resent = second.client.flush();
      final exchange = await second.server.next();
      expect(
        eventIdsOf(exchange),
        ids,
        reason: 'the same events, so the platform counts them once',
      );
      await acceptBatch(exchange);
      await resent;
      await eventually(
        () => !store.entries.containsKey(clientKeys.events),
        reason: 'an accepted queue leaves nothing behind',
      );

      await second.client.flush();
      expect(
        second.server.received.where((e) => e.path == '/sdk/v1/events'),
        hasLength(1),
        reason: 'flushed once',
      );
    });

    test('the interval flushes while the app is foregrounded, and the '
        'background does once', () async {
      final run = await serving(
        experimentValues(),
        sendSummaries: false,
        flushInterval: const Duration(milliseconds: 30),
      );
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expose(run.client);
      final onInterval = await run.server.next(path: '/sdk/v1/events');
      await acceptBatch(onInterval);

      // Nothing runs in the background: the interval stops there, and going
      // there flushes what is queued best effort.
      run.client
        ..alias(
          fromUnitType: 'anonymous',
          fromUnitId: 'd-8f1c',
          toUnitType: 'user',
          toUnitId: 'u-1001',
        )
        ..background();
      final onBackground = await run.server.next(path: '/sdk/v1/events');
      expect(eventsOf(onBackground).single['type'], 'alias');
      await acceptBatch(onBackground);

      await Future<void>.delayed(const Duration(milliseconds: 120));
      expect(
        run.server.received.where((e) => e.path == '/sdk/v1/events'),
        hasLength(2),
        reason: 'the interval does not run in the background',
      );
    });

    test('close flushes what is queued', () async {
      final run = await serving(experimentValues(), sendSummaries: false);
      addTearDown(run.server.close);

      expose(run.client);
      final closed = run.client.close();
      final exchange = await run.server.next();
      expect(eventsOf(exchange), hasLength(1));
      await acceptBatch(exchange);
      await closed;
    });

    test('a store throwing on every call leaves the client usable', () async {
      final server = await MockServer.start();
      final logs = LogRecorder();
      final client = Flagnetic.client(
        clientKey,
        baseUrl: server.url,
        context: testContext,
        options: scaledOptions(
          store: _ThrowingStore(),
          logger: logs.call,
          pollInterval: const Duration(hours: 1),
        ),
      );
      addTearDown(() async {
        await client.close();
        await server.close();
      });
      await (await server.next()).respondJson(experimentValues());
      await eventually(() => client.stage == Stage.fresh);

      expose(client);
      client.alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-8f1c',
        toUnitType: 'user',
        toUnitId: 'u-1001',
      );
      final flushed = client.flush();
      final exchange = await server.next();
      expect(
        eventsOf(exchange).map((e) => e['type']),
        containsAll(['exposure', 'alias']),
        reason: 'the queue keeps working in memory',
      );
      await acceptBatch(exchange);
      await flushed;
      expect(logs.count(LogEvents.storeFailed), greaterThan(0));
      expect(logs.count(LogEvents.internalError), 0);
    });
  });

  group('evaluation summaries', () {
    test('every evaluation is counted per flag, variation and reason, '
        'fallback included, with no unit ID in the body', () async {
      final run = await serving(experimentValues());
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      run.client
        ..getBool('checkout', false)
        ..getBool('checkout', false)
        ..getString('banner', 'none')
        // Absent: fallback with no variation.
        ..getString('missing', 'none')
        // A type mismatch is a fallback too.
        ..getBool('banner', false);

      final flushed = run.client.flush();
      final exchange = await run.server.next();
      final event = eventsOf(exchange).single;
      expect(event['type'], 'evaluation_summary');
      expect(event['ruleset_version'], 42);
      expect(event['app_version'], '1.42.0');
      expect(event['interval_start'], isA<String>());
      expect(event['interval_end'], isA<String>());
      expect(event['counts'], [
        {'flag': 'banner', 'variation': null, 'reason': 'fallback', 'count': 1},
        {
          'flag': 'banner',
          'variation': 'short',
          'reason': 'targeting_match',
          'count': 1,
        },
        {
          'flag': 'checkout',
          'variation': 'treatment',
          'reason': 'experiment',
          'count': 2,
        },
        {
          'flag': 'missing',
          'variation': null,
          'reason': 'fallback',
          'count': 1,
        },
      ]);
      final body = utf8.decode(exchange.rawBody);
      expect(body, isNot(contains('d-8f1c')));
      expect(body, isNot(contains('DE')));
      await acceptBatch(exchange);
      await flushed;
    });

    test('summaries ride in the same batch as exposures and aliases', () async {
      final run = await serving(experimentValues());
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      run.client
        ..getBool('checkout', false)
        ..exposure('checkout')
        ..alias(
          fromUnitType: 'anonymous',
          fromUnitId: 'd-8f1c',
          toUnitType: 'user',
          toUnitId: 'u-1001',
        );
      final flushed = run.client.flush();
      final exchange = await run.server.next();
      expect(eventsOf(exchange).map((e) => e['type']), [
        'exposure',
        'alias',
        'evaluation_summary',
      ]);
      await acceptBatch(exchange);
      await flushed;
    });

    test('a window whose report never arrived merges into the '
        'next', () async {
      final run = await serving(
        experimentValues(),
        retryBackoff: const Duration(seconds: 30),
      );
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      run.client.getBool('checkout', false);
      final first = run.client.flush();
      final failed = await run.server.next();
      final firstId = eventIdsOf(failed).single;
      final start = (eventsOf(failed).single['interval_start']! as String);
      await failed.respond(500);
      await first;

      // The next window closes with another evaluation; the unanswered one
      // merges into it and reports under a new event ID.
      run.client.getBool('checkout', false);
      await eventually(
        () => run.server.received.length > 2,
        reason: 'the retry went out',
      );
      final merged = run.server.received.last;
      final event = eventsOf(merged).single;
      expect(event['event_id'], isNot(firstId));
      expect(event['interval_start'], start, reason: 'from where it began');
      expect(event['counts'], [
        {
          'flag': 'checkout',
          'variation': 'treatment',
          'reason': 'experiment',
          'count': 2,
        },
      ]);
      await acceptBatch(merged);
    });

    test('sendSummaries off sends no summary', () async {
      final run = await serving(experimentValues(), sendSummaries: false);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      run.client
        ..getBool('checkout', false)
        ..exposure('checkout');
      final flushed = run.client.flush();
      final exchange = await run.server.next();
      expect(eventsOf(exchange).map((e) => e['type']), ['exposure']);
      await acceptBatch(exchange);
      await flushed;
    });

    test('a reason the server never sends, local_override among them, is '
        'counted as it was served', () async {
      // The SDK passes an evaluation reason it does not know through with
      // the value, and the summary counts what
      // was served, so a forgotten local override in a build is visible.
      final document = experimentValues();
      final flags = document['flags']! as Map<String, Object?>;
      final banner = flags['banner']! as Map<String, Object?>;
      banner['reason'] = Reasons.localOverride;
      final run = await serving(document);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      run.client
        ..getString('banner', 'none')
        ..getString('banner', 'none');
      final flushed = run.client.flush();
      final exchange = await run.server.next();
      expect(eventsOf(exchange).single['counts'], [
        {
          'flag': 'banner',
          'variation': 'short',
          'reason': Reasons.localOverride,
          'count': 2,
        },
      ]);
      await acceptBatch(exchange);
      await flushed;
    });

    test('a window over an hour old is dropped rather than delivered '
        'late', () async {
      // An earlier run left a report the platform never answered; by the
      // time the app runs again its interval is too old to place
      //, so it goes and only the new window is reported.
      final stale = DateTime.now().toUtc().subtract(const Duration(hours: 2));
      final store = InMemoryStore({
        clientKeys.events: jsonEncode([
          {
            'type': 'evaluation_summary',
            'event_id': '0192a7c4-5b1e-7d3f-8e2a-1c4b6d9f0a37',
            'interval_start': stale.toIso8601String(),
            'interval_end': stale
                .add(const Duration(minutes: 1))
                .toIso8601String(),
            'ruleset_version': 7,
            'counts': [
              {
                'flag': 'gone',
                'variation': null,
                'reason': 'fallback',
                'count': 99,
              },
            ],
          },
        ]),
      });
      final run = await serving(experimentValues(), store: store);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      run.client.getBool('checkout', false);
      final flushed = run.client.flush();
      final exchange = await run.server.next();
      final event = eventsOf(exchange).single;
      expect(event['counts'], [
        {
          'flag': 'checkout',
          'variation': 'treatment',
          'reason': 'experiment',
          'count': 1,
        },
      ]);
      expect(run.logs.fieldsOf(LogEvents.queueDropped), [
        {'cause': 'window_too_old', 'events': 1},
      ]);
      await acceptBatch(exchange);
      await flushed;
    });
  });

  group('offline mode', () {
    test('events are recorded and counted, and nothing is sent', () async {
      final client = Flagnetic.client(
        clientKey,
        context: testContext,
        options: ClientOptions(
          offline: OfflineSource.document(experimentValues()),
        ),
      );
      addTearDown(client.close);

      expect(client.getBool('checkout', false), isTrue);
      client
        ..exposure('checkout')
        ..alias(
          fromUnitType: 'anonymous',
          fromUnitId: 'd-8f1c',
          toUnitType: 'user',
          toUnitId: 'u-1001',
        );
      await client.flush();

      expect(client.offline!.recorded.map((e) => e['type']), [
        'exposure',
        'alias',
      ]);
      expect(client.offline!.summary, [
        (
          flag: 'checkout',
          variation: 'treatment',
          reason: Reasons.experiment,
          count: 1,
        ),
      ]);
    });
  });
}

/// A store whose every call throws, as a device with no writable storage
/// does.
final class _ThrowingStore implements FlagneticStore {
  @override
  Never get(String key) => throw StateError('no store');

  @override
  Never set(String key, String value) => throw StateError('no store');

  @override
  Never remove(String key) => throw StateError('no store');
}
