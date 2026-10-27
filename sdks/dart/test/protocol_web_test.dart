@TestOn('browser')
// The browser run of the client-mode protocol runner: the SDK compiled for
// the web, its requests sent by the browser to a mock server on another
// origin. A browser test declares its tests before it can read the
// fixtures, so each list is one test that plays every entry and reports
// each one that failed.
library;

import 'package:test/test.dart';

import 'support/page.dart';
import 'support/protocol_runner.dart';
import 'support/remote_server.dart';

void main() {
  late Remote remote;
  late Harness harness;
  late ProtocolSuite suite;
  setUpAll(() async {
    remote = await Remote.spawn();
    harness = Harness(
      startServer: remote.startServer,
      fixtures: remote.fixtures,
      // Each scripted step crosses to the isolate and back.
      latency: const Duration(milliseconds: 250),
      page: const DomPage(),
    );
    suite = ProtocolSuite.read(harness.fixtures, inPage: true);
  });

  test('every degradation case of POST /sdk/v1/evaluate', () async {
    await playAll(
      suite.casesOf('POST /sdk/v1/evaluate'),
      (c) => runCase(harness, c, suite.caseContext),
    );
  });
  test('every degradation case of POST /sdk/v1/events', () async {
    await playAll(
      suite.casesOf('POST /sdk/v1/events'),
      (c) => runEventsCase(harness, c, suite.caseContext),
    );
  });
  test(
    'every client scenario',
    timeout: const Timeout(Duration(minutes: 5)),
    () async {
      final passed = await playAll(
        suite.clientScenarios,
        (s) => runScenario(harness, s, suite.doc),
      );
      await remote.reportPassed(passed);
    },
  );
}

/// Plays every entry of [entries], then fails naming each one that failed;
/// returns the names of those that passed.
Future<List<String>> playAll(
  List<Map<String, Object?>> entries,
  Future<void> Function(Map<String, Object?> entry) play,
) async {
  expect(entries, isNotEmpty);
  final failed = <String>[];
  final passed = <String>[];
  for (final entry in entries) {
    try {
      await play(entry);
      passed.add(entry['name']! as String);
    } on Object catch (e) {
      failed.add('${entry['name']}: $e');
    }
  }
  if (failed.isNotEmpty) fail(failed.join('\n\n'));
  return passed;
}
