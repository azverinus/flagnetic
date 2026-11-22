@TestOn('vm')
// The VM only: this suite reads the documented table and the sources from
// disk.
library;

// Operators alert on the names the logger hook receives, so every SDK logs
// the same ones: the table under "Log names" in the SDK conformance guide.
// These tests hold `LogEvents` to that table and keep the names it replaced
// out of the package.
import 'dart:io';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

/// The guide's table, read relative to the package root, where `dart test`
/// runs.
const _guide = '../../docs/sdks/conformance.md';

/// The names the table replaced; none may be logged any more.
const _replaced = [
  'sse_connected',
  'sse_dropped',
  'internal_error',
  'deprecation_received',
  'deprecated_operation',
];

/// The first-column names of the table under the "Log names" heading.
List<String> _documentedNames() {
  final lines = File(_guide).readAsLinesSync();
  final start = lines.indexWhere((l) => l.trim() == '## Log names');
  expect(start, isNonNegative, reason: 'the guide has a "Log names" section');
  final names = <String>[];
  for (final line in lines.skip(start + 1)) {
    if (line.startsWith('## ')) break;
    final match = RegExp(r'^\|\s*`([a-z_]+)`\s*\|').firstMatch(line);
    if (match != null) names.add(match.group(1)!);
  }
  return names;
}

/// Every string value declared in the `LogEvents` class, read from its
/// source: Dart has no reflection over static constants.
Set<String> _declaredNames() {
  final source = File('lib/src/client/types.dart').readAsStringSync();
  final start = source.indexOf('abstract final class LogEvents');
  expect(start, isNonNegative);
  final end = source.indexOf('\n}', start);
  return RegExp(
    r"static const \w+ = '([a-z_]+)';",
  ).allMatches(source.substring(start, end)).map((m) => m.group(1)!).toSet();
}

void main() {
  test('every documented name is a LogEvents constant', () {
    final documented = _documentedNames();
    expect(documented, isNotEmpty);
    expect(_declaredNames(), containsAll(documented));
    expect(documented.toSet(), {
      LogEvents.eventsFailed,
      LogEvents.exposuresOff,
      LogEvents.evaluationFailed,
      LogEvents.queueDropped,
      LogEvents.streamConnected,
      LogEvents.streamDropped,
      LogEvents.operationDeprecated,
    });
  });

  test('the replaced names are neither declared nor logged', () {
    expect(_declaredNames(), isNot(anyElement(isIn(_replaced))));
    final sources = Directory('lib')
        .listSync(recursive: true)
        .whereType<File>()
        .where((f) => f.path.endsWith('.dart'));
    for (final file in sources) {
      final text = file.readAsStringSync();
      for (final name in _replaced) {
        expect(text, isNot(contains("'$name'")), reason: file.path);
      }
    }
  });

  test(
    'the cause of a summary window older than an hour is window_too_old',
    () {
      final ingest = File('lib/src/client/ingest.dart').readAsStringSync();
      expect(ingest, contains("'cause': 'window_too_old'"));
    },
  );
}
