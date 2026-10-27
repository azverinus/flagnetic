@TestOn('vm')
// The VM only: the smoke runs the harness's control commands as processes.
library;

// The client mode's end-to-end smoke against two real replicas, brought up
// by `flagnetic-e2e up` (e2e/README.md): FLAGNETIC_E2E_SEED names the seed
// it wrote, and FLAGNETIC_E2E_CMD the command when it is not on the PATH.
//
//   FLAGNETIC_E2E_SEED=… dart test test/e2e_test.dart
//
// Without a seed it is skipped, so `dart test` stays a unit run. The tests
// run in the order they are written and share the instance: the kill
// switches change the seed until it is restored, and the revocation ends
// the client key, so it comes last.
import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flagnetic/flagnetic.dart';
import 'package:flagnetic/src/client/options.dart' show sdkName, sdkVersion;
import 'package:test/test.dart';

/// How many apps stream from replica B while the kill switch is measured,
/// and how many times it is flipped: every app's every observation is one
/// sample, so the 99th percentile is read over [measuredApps] times
/// [killSwitchRuns] of them. A kill and its restore make every app evaluate
/// twice, which keeps the runs within the client key's evaluate limit of
/// one address on one replica.
const measuredApps = 4;
const killSwitchRuns = 12;

/// The declared propagation of a kill switch to an app in the foreground,
/// at the 99th percentile.
const declaredPropagation = Duration(seconds: 5);

/// How many apps behind one address stream from replica A when the burst of
/// evaluates one change causes is recorded: more than the client key's
/// default of 10 concurrent streams per address and replica, so the record
/// shows what that limit leaves streaming.
const burstApps = 12;

void main() {
  final seedPath = Platform.environment['FLAGNETIC_E2E_SEED'] ?? '';
  if (seedPath.isEmpty) {
    test(
      'the client mode against two replicas',
      () {},
      skip: 'set FLAGNETIC_E2E_SEED to the seed of `flagnetic-e2e up`',
    );
    return;
  }
  final seed = Seed(
    jsonDecode(File(seedPath).readAsStringSync()) as Map<String, Object?>,
  );
  final built = <FlagneticClient>[];

  /// A client of the seed's client key against [baseUrl], closed after the
  /// test; [logs] receives the names of its log events with their time.
  FlagneticClient app(
    String baseUrl, {
    FlagneticContext context = const FlagneticContext(units: {}),
    Logs? logs,
    bool sse = false,
  }) {
    final client = Flagnetic.client(
      seed.clientKey,
      baseUrl: Uri.parse(baseUrl),
      context: context,
      options: ClientOptions(
        logger: logs?.call,
        sse: sse,
        pollInterval: const Duration(hours: 1),
        platform: 'dart-e2e',
      ),
    );
    built.add(client);
    return client;
  }

  tearDown(() async {
    await Future.wait([for (final client in built) client.close()]);
    built.clear();
  });

  test('ready reaches fresh and every case serves its value with its client '
      'reason', () async {
    final client = app(seed.replicaA);
    expect(await client.ready(const Duration(seconds: 10)), Stage.fresh);
    expect(seed.cases, isNotEmpty);
    for (final c in seed.cases) {
      await client.setContext(c.context);
      expect(client.stage, Stage.fresh, reason: c.name);
      final detail = client.getDetail(c.flag, unlike(c));
      expect(
        [detail.value, detail.variation, detail.reason],
        [c.value, c.variation, c.clientReason],
        reason: c.name,
      );
    }
  });

  test(
    'a kill switch through replica A reaches apps streaming from replica B '
    'within 5 s at the 99th percentile',
    timeout: const Timeout(Duration(minutes: 5)),
    () async {
      final flag = seed.flagOn;
      final logs = [for (var i = 0; i < measuredApps; i++) Logs()];
      final apps = [
        for (final log in logs)
          app(
            seed.replicaB,
            context: seed.outsideRollout,
            logs: log,
            sse: true,
          ),
      ];
      for (final client in apps) {
        expect(await client.ready(const Duration(seconds: 10)), Stage.fresh);
      }
      await eventually(
        () => logs.every((l) => l.has(LogEvents.sseConnected)),
        const Duration(seconds: 10),
        'a stream to replica B never connected',
      );
      // Each replica announces the version an app holds as its stream
      // opens; let that settle so that the kill switch is news to every app.
      await Future<void>.delayed(const Duration(seconds: 1));

      final samples = <int>[];
      for (var run = 0; run < killSwitchRuns; run++) {
        expect(apps.every((c) => c.getBool(flag, false)), isTrue);
        final off = [for (final _ in apps) Completer<DateTime>()];
        final unwatch = [
          for (final (i, client) in apps.indexed)
            client.onChange((keys) {
              if (keys.contains(flag) &&
                  client.getDetail(flag, true).reason == Reasons.off &&
                  !off[i].isCompleted) {
                off[i].complete(DateTime.now());
              }
            }),
        ];
        try {
          final killed = await control(['kill', flag]) as Map<String, Object?>;
          expect(killed['changed'], isTrue, reason: 'the kill changed nothing');
          final committed = DateTime.parse(killed['committed_at']! as String);
          final seen = await Future.wait([
            for (final c in off) c.future.timeout(const Duration(seconds: 10)),
          ]);
          samples.addAll([
            for (final at in seen) at.difference(committed).inMilliseconds,
          ]);
        } finally {
          for (final stop in unwatch) {
            stop();
          }
          await control(['restore']);
        }
        await eventually(
          () => apps.every((c) => c.getBool(flag, false)),
          const Duration(seconds: 10),
          'the restore never reached every app',
        );
      }

      final p99 = percentile(samples, 99);
      report({
        'sdk': sdkName,
        'kill_switch_ms': p99,
        'percentile': 99,
        'samples': samples.length,
        'median_ms': percentile(samples, 50),
        'max_ms': samples.reduce(max),
      });
      expect(p99, lessThanOrEqualTo(declaredPropagation.inMilliseconds));
    },
  );

  // Recorded for the client key's limits, which count per key and address
  // on each replica: apps behind one NAT share them.
  test(
    'the evaluates one change causes across apps behind one address are '
    'recorded',
    timeout: const Timeout(Duration(minutes: 2)),
    () async {
      final flag = seed.flagOn;
      final logs = [for (var i = 0; i < burstApps; i++) Logs()];
      final apps = [
        for (final log in logs)
          app(
            seed.replicaA,
            context: seed.outsideRollout,
            logs: log,
            sse: true,
          ),
      ];
      for (final client in apps) {
        expect(await client.ready(const Duration(seconds: 10)), Stage.fresh);
      }
      // A refused stream retries with its backoff, so give every app the
      // time to connect or to be refused more than once.
      await Future<void>.delayed(const Duration(seconds: 5));
      final streaming = [
        for (final (i, log) in logs.indexed)
          if (log.has(LogEvents.sseConnected)) apps[i],
      ];
      expect(streaming, isNotEmpty, reason: 'no stream to replica A connected');

      final changed = DateTime.now();
      try {
        final killed = await control(['kill', flag]) as Map<String, Object?>;
        expect(killed['changed'], isTrue, reason: 'the kill changed nothing');
        await eventually(
          () => streaming.every((c) => c.getBool(flag, true) == false),
          const Duration(seconds: 10),
          'the kill switch never reached every streaming app',
        );
        // Every evaluate the change asked for has had its 0–3 s wait.
        await Future<void>.delayed(const Duration(seconds: 1));
      } finally {
        await control(['restore']);
      }

      final evaluates = [
        for (final log in logs)
          ...log
              .since(changed, LogEvents.valuesLoaded)
              .where((e) => e.fields['source'] == 'network')
              .map((e) => e.at),
      ]..sort();
      final refused = [
        for (final log in logs)
          ...log
              .since(changed, LogEvents.fetchFailed)
              .where((e) => e.fields['status'] == 429),
      ];
      final perSecond = <int, int>{};
      for (final at in evaluates) {
        final second = at.difference(evaluates.first).inSeconds;
        perSecond[second] = (perSecond[second] ?? 0) + 1;
      }
      report({
        'sdk': sdkName,
        'evaluate_burst': {
          'apps': burstApps,
          'streaming': streaming.length,
          'evaluates': evaluates.length,
          'spread_ms': evaluates.isEmpty
              ? 0
              : evaluates.last.difference(evaluates.first).inMilliseconds,
          'busiest_second': perSecond.values.fold(0, max),
          'refused': refused.length,
        },
      });
      expect(refused, isEmpty, reason: 'an evaluate was refused 429');
    },
  );

  // Each replica writes the SDKs it saw once a minute.
  test(
    'the SDK is in the inventory under its own name',
    timeout: const Timeout(Duration(minutes: 2)),
    () async {
      await app(seed.replicaB).ready(const Duration(seconds: 10));
      await eventuallyAsync(
        () async => ((await control(['inventory'])) as List<Object?>)
            .cast<Map<String, Object?>>()
            .any(
              (e) =>
                  e['sdk_name'] == sdkName &&
                  e['sdk_version'] == sdkVersion &&
                  (e['sdk_key']! as Map<String, Object?>)['kind'] == 'client',
            ),
        const Duration(seconds: 90),
        '$sdkName $sdkVersion never appeared in the inventory',
      );
    },
  );

  // A replica checks the key on every request; the revocation goes through
  // replica A and is refused by replica B.
  test('a revocation through replica A refuses the key on replica B', () async {
    await control(['revoke', 'client']);
    await eventuallyAsync(
      () async {
        final logs = Logs();
        final client = app(seed.replicaB, logs: logs);
        return await client.ready(const Duration(seconds: 2)) ==
                Stage.builtIn &&
            logs.has(LogEvents.keyRevoked);
      },
      const Duration(seconds: 20),
      'replica B kept answering the revoked client key',
    );
  });
}

/// The seed `flagnetic-e2e up` wrote, read by field name.
final class Seed {
  Seed(this._json);

  final Map<String, Object?> _json;

  Map<String, Object?> _map(String name) =>
      _json[name]! as Map<String, Object?>;

  String get replicaA => _map('replicas')['a']! as String;
  String get replicaB => _map('replicas')['b']! as String;
  String get clientKey => _json['client_key']! as String;
  String get flagOn => _map('flags')['on']! as String;

  /// A unit that the seed's flags serve their fallthrough.
  FlagneticContext get outsideRollout => FlagneticContext(
    units: {
      _json['unit_type']! as String:
          _map('units')['outside_rollout']! as String,
    },
  );

  List<SeedCase> get cases => [
    for (final c in _json['cases']! as List<Object?>)
      SeedCase(c! as Map<String, Object?>),
  ];
}

/// One evaluation the smoke asserts.
final class SeedCase {
  SeedCase(this._json);

  final Map<String, Object?> _json;

  String get name => _json['name']! as String;
  String get flag => _json['flag']! as String;
  String get type => _json['type']! as String;
  Object? get value => _json['value'];
  String get variation => _json['variation']! as String;
  String get clientReason => _json['client_reason']! as String;

  FlagneticContext get context {
    final json = _json['context']! as Map<String, Object?>;
    return FlagneticContext(
      units: (json['units'] as Map<String, Object?>? ?? const {})
          .cast<String, String>(),
      attributes: json['attributes'] as Map<String, Object?>? ?? const {},
    );
  }
}

/// A built-in default of the case's type that the flag never serves, so a
/// fallback cannot pass for the case's value.
Object? unlike(SeedCase c) => switch (c.type) {
  'boolean' => !(c.value! as bool),
  'number' => (c.value! as num) - 1,
  'string' => 'not ${c.value}',
  _ => {'not': c.value},
};

/// Runs a control command of the harness and returns its JSON output, or
/// null when it printed nothing.
Future<Object?> control(List<String> arguments) async {
  final command = Platform.environment['FLAGNETIC_E2E_CMD'] ?? 'flagnetic-e2e';
  final done = await Process.run(command, arguments);
  expect(
    done.exitCode,
    0,
    reason: 'flagnetic-e2e ${arguments.join(' ')}: ${done.stderr}',
  );
  final out = '${done.stdout}'.trim();
  return out.isEmpty ? null : jsonDecode(out);
}

/// The nearest-rank [p]th percentile of [samples].
int percentile(List<int> samples, int p) {
  final sorted = [...samples]..sort();
  return sorted[max(0, (sorted.length * p / 100).ceil() - 1)];
}

/// Prints [line] as one JSON line and, in CI, adds it to the job summary.
void report(Map<String, Object?> line) {
  final json = jsonEncode(line);
  // The line is the smoke's output: CI copies it beside the other SDKs'.
  // ignore: avoid_print
  print(json);
  final summary = Platform.environment['GITHUB_STEP_SUMMARY'] ?? '';
  if (summary.isNotEmpty) {
    File(summary).writeAsStringSync('$json\n', mode: FileMode.append);
  }
}

/// Waits until [check] holds, failing with [message] after [timeout].
Future<void> eventually(
  bool Function() check,
  Duration timeout,
  String message,
) => eventuallyAsync(() async => check(), timeout, message);

/// Waits until [check] answers true, failing with [message] after
/// [timeout].
Future<void> eventuallyAsync(
  Future<bool> Function() check,
  Duration timeout,
  String message,
) async {
  final deadline = DateTime.now().add(timeout);
  while (!await check()) {
    if (DateTime.now().isAfter(deadline)) fail(message);
    await Future<void>.delayed(const Duration(milliseconds: 100));
  }
}

/// A client's log events, each with the time it was received.
final class Logs {
  final List<({DateTime at, String event, Map<String, Object?> fields})>
  _events = [];

  /// The hook to pass as [ClientOptions.logger].
  void call(LogLevel level, String event, Map<String, Object?> fields) =>
      _events.add((at: DateTime.now(), event: event, fields: fields));

  /// Whether an event named [event] was received.
  bool has(String event) => _events.any((e) => e.event == event);

  /// The events named [event] received at or after [time].
  Iterable<({DateTime at, String event, Map<String, Object?> fields})> since(
    DateTime time,
    String event,
  ) => _events.where((e) => e.event == event && !e.at.isBefore(time));
}
