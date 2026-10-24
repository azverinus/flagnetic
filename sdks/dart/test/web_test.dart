@TestOn('browser')
// What the client does in a browser that the protocol cannot show: how it
// names itself without `User-Agent`, how the stream carries the key, and how
// it follows the page: a hidden or closing page hands its last events to the
// beacon, a page shown again or back online refetches.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

import 'support/remote_server.dart';
import 'support/scripted.dart';

@JS('Object.defineProperty')
external void _defineProperty(JSObject target, String name, JSAny descriptor);

@JS('document')
external _Target get _document;

@JS('window')
external _Target get _window;

extension type _Target._(JSObject _) implements JSObject {
  external bool dispatchEvent(_Event event);
}

@JS('Event')
extension type _Event._(JSObject _) implements JSObject {
  external factory _Event(String type);
}

/// Makes the page [state] (`visible` or `hidden`) and tells it so.
void showPage(String state) {
  _defineProperty(
    _document,
    'visibilityState',
    {'value': state, 'configurable': true}.jsify()!,
  );
  _document.dispatchEvent(_Event('visibilitychange'));
}

const _context = FlagneticContext(units: {'anonymous': 'd-8f1c'});

void main() {
  late Remote remote;
  setUpAll(() async => remote = await Remote.spawn());
  tearDown(() => showPage('visible'));

  Future<(FlagneticClient, ScriptedServer)> started({bool sse = false}) async {
    final server = await remote.startServer();
    final client = Flagnetic.client(
      clientKey,
      baseUrl: server.url,
      context: _context,
      options: scaledOptions(
        sse: sse,
        pollInterval: const Duration(hours: 1),
        minimumRefetchGap: Duration.zero,
      ),
    );
    addTearDown(() async {
      await client.close();
      await server.close();
    });
    return (client, server);
  }

  Future<void> fresh(FlagneticClient client, ScriptedServer server) async {
    await (await server.next(
      path: evaluatePath,
    )).respondJson(experimentValues());
    await eventually(() => client.stage == Stage.fresh);
  }

  test('names the SDK and the platform web in what it posts', () async {
    final (client, server) = await started();
    final evaluate = await server.next(path: evaluatePath);
    expect(
      ((evaluate.json! as Map)['context']! as Map)['attributes'],
      allOf(
        containsPair('sdk', 'flagnetic-dart'),
        containsPair('sdk_version', isNotEmpty),
        containsPair('platform', 'web'),
      ),
    );
    // The browser names itself; the SDK cannot, and a header it tried to
    // set would fail the CORS check.
    expect(evaluate.headers['user-agent'], isNot(contains('flagnetic')));
    await evaluate.respondJson(experimentValues());
    await eventually(() => client.stage == Stage.fresh);

    client
      ..getBool('checkout', false)
      ..exposure('checkout');
    final flushed = client.flush();
    final batch = await server.next(path: '/sdk/v1/events');
    expect((batch.json! as Map)['sdk'], containsPair('platform', 'web'));
    expect(
      eventsOf(batch).singleWhere((e) => e['type'] == 'exposure')['platform'],
      'web',
    );
    await acceptBatch(batch);
    await flushed;
  });

  test('opens the stream with the key as sdk_key', () async {
    final (_, server) = await started(sse: true);
    final stream = await server.next(path: '/sdk/v1/stream');
    expect(stream.query['sdk_key'], clientKey);
    expect(stream.headers.containsKey('authorization'), isFalse);
    await stream.respondStream();
    await (await server.next(
      path: evaluatePath,
    )).respondJson(experimentValues());
  });

  test('a hidden page closes the stream and beacons its events', () async {
    final (client, server) = await started(sse: true);
    final stream = await (await server.next(
      path: '/sdk/v1/stream',
    )).respondStream();
    await fresh(client, server);
    client
      ..getBool('checkout', false)
      ..exposure('checkout');

    showPage('hidden');
    final beacon = await server.next(path: '/sdk/v1/events');
    expect(beacon.query['sdk_key'], clientKey);
    expect(beacon.headers['content-type'], startsWith('text/plain'));
    expect(beacon.headers.containsKey('authorization'), isFalse);
    // The window counted so far closes and goes with it.
    expect(
      [for (final e in eventsOf(beacon)) e['type']],
      ['exposure', 'evaluation_summary'],
    );
    await acceptBatch(beacon);
    expect(
      await stream.closedBySdk(const Duration(seconds: 1)),
      isTrue,
      reason: 'a hidden tab holds no connection',
    );

    // What the beacon took is not sent again.
    showPage('visible');
    final stream2 = await server.next(path: '/sdk/v1/stream');
    await stream2.respondStream();
    await (await server.next(
      path: evaluatePath,
    )).respondJson(experimentValues());
    await client.flush();
    expect(await server.countReceived(path: '/sdk/v1/events'), 1);
  });

  test('a closing page beacons its events', () async {
    final (client, server) = await started();
    await fresh(client, server);
    client.alias(
      fromUnitType: 'anonymous',
      fromUnitId: 'd-8f1c',
      toUnitType: 'user',
      toUnitId: 'u-1001',
    );
    _window.dispatchEvent(_Event('pagehide'));
    final beacon = await server.next(path: '/sdk/v1/events');
    expect(beacon.query['sdk_key'], clientKey);
    final envelope = jsonDecode(utf8.decode(beacon.rawBody)) as Map;
    expect(envelope['schema_version'], 1);
    expect([for (final e in eventsOf(beacon)) e['type']], ['alias']);
    await acceptBatch(beacon);
  });

  test('a page closing after it was hidden beacons what came since', () async {
    final (client, server) = await started();
    await fresh(client, server);
    showPage('hidden');
    // Nothing queued yet: the hide has nothing to beacon.
    client.alias(
      fromUnitType: 'anonymous',
      fromUnitId: 'd-8f1c',
      toUnitType: 'user',
      toUnitId: 'u-1001',
    );
    _window.dispatchEvent(_Event('pagehide'));
    final beacon = await server.next(path: '/sdk/v1/events');
    expect(beacon.query['sdk_key'], clientKey);
    expect([for (final e in eventsOf(beacon)) e['type']], ['alias']);
    await acceptBatch(beacon);
  });

  test('the beacons stay within the quota; the rest goes later', () async {
    final server = await remote.startServer();
    final client = Flagnetic.client(
      clientKey,
      baseUrl: server.url,
      context: _context,
      options: scaledOptions(
        pollInterval: const Duration(hours: 1),
        minimumRefetchGap: Duration.zero,
        sendSummaries: false,
        eventBatchSize: 500,
      ),
    );
    addTearDown(() async {
      await client.close();
      await server.close();
    });
    await fresh(client, server);
    const recorded = 400;
    for (var i = 0; i < recorded; i++) {
      client.alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-$i',
        toUnitType: 'user',
        toUnitId: 'u-$i',
      );
    }

    showPage('hidden');
    final ids = <Object?>[];
    var beaconed = 0;
    while (beaconed < 64 * 1024) {
      final ScriptedExchange beacon;
      try {
        beacon = await server.next(
          path: '/sdk/v1/events',
          timeout: const Duration(milliseconds: 500),
        );
      } on TimeoutException {
        break;
      }
      beaconed += beacon.rawBody.length;
      ids.addAll(eventIdsOf(beacon));
      await acceptBatch(beacon);
    }
    expect(beaconed, lessThanOrEqualTo(64 * 1024));
    expect(ids.length, inExclusiveRange(0, recorded));

    showPage('visible');
    await (await server.next(
      path: evaluatePath,
    )).respondJson(experimentValues());
    final flushed = client.flush();
    final rest = await server.next(path: '/sdk/v1/events');
    expect(rest.headers['authorization'], 'Bearer $clientKey');
    ids.addAll(eventIdsOf(rest));
    await acceptBatch(rest);
    await flushed;
    expect(ids.toSet(), hasLength(recorded), reason: 'each event once');
  });

  test('a page shown again refetches', () async {
    final (client, server) = await started();
    await fresh(client, server);
    showPage('hidden');
    showPage('visible');
    await (await server.next(
      path: evaluatePath,
    )).respondJson(experimentValues(rulesetVersion: 43));
    await eventually(
      () => client.getDetail('checkout', false).rulesetVersion == 43,
    );
  });

  test('the network coming back refetches a shown page only', () async {
    final (client, server) = await started();
    await fresh(client, server);
    showPage('hidden');
    _window.dispatchEvent(_Event('online'));
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(await server.countReceived(path: evaluatePath), 1);

    showPage('visible');
    await (await server.next(
      path: evaluatePath,
    )).respondJson(experimentValues());
    _window.dispatchEvent(_Event('online'));
    await (await server.next(
      path: evaluatePath,
    )).respondJson(experimentValues(rulesetVersion: 44));
    await eventually(
      () => client.getDetail('checkout', false).rulesetVersion == 44,
    );
  });
}
