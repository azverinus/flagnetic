@TestOn('vm')
// The VM only: this suite needs a `dart:io` mock server.
library;

// The foreground stream against the mock server: held by default on a
// device, closed in the background, a change evaluated after a random wait,
// a resume refetching at most once per minimum gap. Durations are the
// protocol's divided by 1000, as in the other behaviour tests.
import 'dart:async';
import 'dart:math';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

import 'support/mock_server.dart';

const anonymous = FlagneticContext(units: {'anonymous': 'd-8f1c'});

const streamPath = '/sdk/v1/stream';
const evaluatePath = '/sdk/v1/evaluate';

Map<String, Object?> get evaluation =>
    protocolJson('server/evaluation.json')! as Map<String, Object?>;
Map<String, Object?> get afterKillSwitch =>
    protocolJson('server/evaluation-after-kill-switch.json')!
        as Map<String, Object?>;

/// A random source that always draws [value].
final class FixedRandom implements Random {
  FixedRandom(this.value);

  final double value;

  @override
  double nextDouble() => value;

  @override
  int nextInt(int max) => (value * max).floor();

  @override
  bool nextBool() => value >= 0.5;
}

/// Builds a client against [server] and returns it with its stream, once the
/// first values are held.
Future<(FlagneticClient, OpenStream)> streaming(
  MockServer server, {
  bool? sse,
  Duration jitter = Duration.zero,
  Random? random,
  Duration minimumRefetchGap = Duration.zero,
  DateTime Function()? clock,
}) async {
  final client = Flagnetic.client(
    clientKey,
    baseUrl: server.url,
    context: anonymous,
    options: scaledOptions(
      sse: sse,
      pollInterval: const Duration(hours: 1),
      streamSilenceTimeout: const Duration(seconds: 30),
      rulesetEventJitter: jitter,
      random: random,
      minimumRefetchGap: minimumRefetchGap,
      clock: clock,
    ),
  );
  addTearDown(() async {
    await client.close();
    await server.close();
  });
  final stream = await (await server.next(path: streamPath)).respondStream();
  await (await server.next(path: evaluatePath)).respondJson(evaluation);
  await eventually(() => client.stage == Stage.fresh);
  return (client, stream);
}

int count(MockServer server, String path) =>
    server.received.where((e) => e.path == path).length;

const killSwitch = 'event: ruleset\nid: 2\ndata: {"version":2}\n\n';

void main() {
  group('the stream default', () {
    test('is held on a device without being asked for', () async {
      final server = await MockServer.start();
      await streaming(server);
      expect(count(server, streamPath), 1);
    });

    test(
      'is off when the app turns it off, and the poll is the bound',
      () async {
        final server = await MockServer.start();
        final client = Flagnetic.client(
          clientKey,
          baseUrl: server.url,
          context: anonymous,
          options: scaledOptions(
            sse: false,
            pollInterval: const Duration(milliseconds: 60),
          ),
        );
        addTearDown(() async {
          await client.close();
          await server.close();
        });
        await (await server.next()).respondJson(evaluation);
        await (await server.next(
          timeout: const Duration(seconds: 1),
        )).respondJson(afterKillSwitch);
        expect(count(server, streamPath), 0);
      },
    );
  });

  group('the wait after a ruleset event', () {
    test('is a random share of the jitter, never longer', () async {
      final server = await MockServer.start();
      final (client, stream) = await streaming(
        server,
        jitter: const Duration(milliseconds: 600),
        random: FixedRandom(0.5),
      );
      final sentAt = DateTime.now();
      await stream.send(killSwitch);
      final fetch = await server.next(path: evaluatePath);
      final waited = fetch.arrivedAt.difference(sentAt);
      expect(waited, greaterThanOrEqualTo(const Duration(milliseconds: 290)));
      expect(waited, lessThan(const Duration(milliseconds: 600)));
      await fetch.respondJson(afterKillSwitch);
      await eventually(() => client.stage == Stage.fresh);
    });

    test('is none when the draw is zero', () async {
      final server = await MockServer.start();
      final (_, stream) = await streaming(
        server,
        jitter: const Duration(seconds: 30),
        random: FixedRandom(0),
      );
      await stream.send(killSwitch);
      await server.next(
        path: evaluatePath,
        timeout: const Duration(milliseconds: 500),
      );
    });

    test(
      'is shared by events that arrive while it runs: one evaluate',
      () async {
        final server = await MockServer.start();
        final (_, stream) = await streaming(
          server,
          jitter: const Duration(milliseconds: 200),
          random: FixedRandom(0.99),
        );
        await stream.send(killSwitch);
        await stream.send('event: ruleset\nid: 3\ndata: {"version":3}\n\n');
        final fetch = await server.next(path: evaluatePath);
        await fetch.respondJson(afterKillSwitch);
        await Future<void>.delayed(const Duration(milliseconds: 400));
        expect(count(server, evaluatePath), 2);
      },
    );

    test('is abandoned when the app goes to the background', () async {
      final server = await MockServer.start();
      final (client, stream) = await streaming(
        server,
        jitter: const Duration(milliseconds: 300),
        random: FixedRandom(0.9),
      );
      await stream.send(killSwitch);
      await Future<void>.delayed(const Duration(milliseconds: 50));
      client.background();
      await Future<void>.delayed(const Duration(milliseconds: 500));
      expect(count(server, evaluatePath), 1);
    });
  });

  group('the foreground', () {
    test(
      'closes the stream in the background and opens it again on resume',
      () async {
        final server = await MockServer.start();
        final (client, stream) = await streaming(server);
        client.background();
        await Future<void>.delayed(const Duration(milliseconds: 100));
        await stream.send(killSwitch);
        await Future<void>.delayed(const Duration(milliseconds: 100));
        expect(count(server, evaluatePath), 1);

        client.foreground();
        final reopened = await server.next(path: streamPath);
        expect(reopened.headers['last-event-id'], '1');
        await reopened.respondStream();
        await (await server.next(
          path: evaluatePath,
        )).respondJson(afterKillSwitch);
        // The resume and the reopened stream ask for one evaluate between
        // them, not one each.
        await Future<void>.delayed(const Duration(milliseconds: 200));
        expect(count(server, evaluatePath), 2);
      },
    );

    test('refetches on resume when the stream cannot reopen', () async {
      final server = await MockServer.start();
      final (client, _) = await streaming(server);
      client
        ..background()
        ..foreground();
      await (await server.next(path: streamPath)).respond(503);
      await (await server.next(
        path: evaluatePath,
      )).respondJson(afterKillSwitch);
      await Future<void>.delayed(const Duration(milliseconds: 200));
      expect(count(server, evaluatePath), 2);
    });

    test(
      'evaluates on a later reconnect after a resume within the gap',
      () async {
        var now = DateTime.utc(2026, 1, 1);
        final server = await MockServer.start();
        final (client, _) = await streaming(
          server,
          minimumRefetchGap: const Duration(seconds: 10),
          clock: () => now,
        );
        client
          ..background()
          ..foreground();
        // The reopened stream fails, so the resume never connects; the
        // connect after it is a reconnect like any other and catches up.
        await (await server.next(path: streamPath)).respond(503);
        now = now.add(const Duration(seconds: 11));
        await (await server.next(
          path: streamPath,
          timeout: const Duration(seconds: 2),
        )).respondStream();
        await (await server.next(
          path: evaluatePath,
        )).respondJson(afterKillSwitch);
      },
    );

    test('refetches on resume at most once per minimum gap', () async {
      var now = DateTime.utc(2026, 1, 1);
      final server = await MockServer.start();
      final (client, _) = await streaming(
        server,
        minimumRefetchGap: const Duration(seconds: 10),
        clock: () => now,
      );
      // A resume within the gap of the fetch above: the stream reopens, the
      // evaluate it would ask for does not happen.
      client
        ..background()
        ..foreground();
      unawaited((await server.next(path: streamPath)).respondStream());
      await Future<void>.delayed(const Duration(milliseconds: 150));
      expect(count(server, evaluatePath), 1);

      now = now.add(const Duration(seconds: 11));
      client
        ..background()
        ..foreground();
      unawaited((await server.next(path: streamPath)).respondStream());
      await (await server.next(
        path: evaluatePath,
      )).respondJson(afterKillSwitch);
    });
  });
}
