@TestOn('vm')
// The VM run of the client-mode protocol runner, against an in-process
// server; protocol_web_test.dart is the browser run.
library;

import 'package:test/test.dart';

import 'support/mock_server.dart';
import 'support/protocol_runner.dart';

void main() {
  final harness = Harness(
    startServer: MockServer.start,
    fixtures: const CheckoutFixtures(),
  );
  final suite = ProtocolSuite.read(harness.fixtures);
  group('degradation cases of POST /sdk/v1/evaluate', () {
    for (final c in suite.casesOf('POST /sdk/v1/evaluate')) {
      test(c['name'], () => runCase(harness, c, suite.caseContext));
    }
  });
  group('degradation cases of POST /sdk/v1/events', () {
    for (final c in suite.casesOf('POST /sdk/v1/events')) {
      test(c['name'], () => runEventsCase(harness, c, suite.caseContext));
    }
  });
  // The ruleset endpoint serves server keys only; every other case is a
  // client-mode case this runner plays.
  test('every degradation case is played or belongs to server mode', () {
    for (final c in suite.cases) {
      expect(
        ['POST /sdk/v1/evaluate', 'POST /sdk/v1/events', 'GET /sdk/v1/ruleset'],
        contains(c['endpoint']),
        reason: '${c['name']}',
      );
    }
  });

  final scenarios = suite.clientScenarios;
  group('client scenarios', () {
    for (final s in scenarios) {
      test(
        s['name'],
        () => runScenario(harness, s, suite.doc),
        timeout: const Timeout.factor(4),
      );
    }
  });
  test('every scenario is a client or a server scenario, once', () {
    expect(scenarios, isNotEmpty);
    for (final s in suite.scenarios) {
      expect(['client', 'server'], contains(s['mode']), reason: '${s['name']}');
    }
    expect(
      {for (final s in scenarios) s['name']},
      hasLength(scenarios.length),
      reason: 'scenario names are unique',
    );
  });
}
