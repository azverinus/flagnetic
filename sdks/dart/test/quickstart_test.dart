@TestOn('vm')
// The VM only: the quickstart runs in an isolate against a `dart:io` mock
// server.
library;

// The Dart program of the quickstart in docs/sdks/dart.md is the program
// this test runs, with the placeholder key and address replaced by the mock
// server's: a snippet that no longer compiles or no longer works with the
// API fails here.
import 'dart:io';
import 'dart:isolate';

import 'package:test/test.dart';

import 'support/mock_server.dart';

const _guide = '../../docs/sdks/dart.md';

/// The first Dart block under the "Quickstart" heading of the guide.
String _quickstartProgram() {
  final text = File(_guide).readAsStringSync();
  final start = text.indexOf('\n## Quickstart\n');
  expect(start, isNonNegative, reason: 'the guide has a Quickstart section');
  final end = text.indexOf('\n## ', start + 1);
  final section = text.substring(start, end < 0 ? text.length : end);
  final block = RegExp(r'```dart\n(.*?)```', dotAll: true).firstMatch(section);
  expect(block, isNotNull, reason: 'the Quickstart has a Dart program');
  return block!.group(1)!;
}

void main() {
  test('the quickstart program runs against a server', () async {
    final server = await MockServer.start();
    addTearDown(server.close);
    var evaluates = 0;
    final events = <Exchange>[];
    server
      // A device holds the ruleset stream, which the first evaluate waits for.
      ..autoRespond('/sdk/v1/stream', (e) async {
        await e.respondStream();
      })
      ..autoRespond('/sdk/v1/evaluate', (e) async {
        evaluates++;
        await e.respondJson(experimentValues(automaticExposure: false));
      })
      ..autoRespond('/sdk/v1/events', (e) async {
        events.add(e);
        await e.respond(
          202,
          headers: {'Content-Type': 'application/json'},
          body: '{"accepted":1,"dropped_rows":0,"duplicates":0,"rejected":[]}',
        );
      });

    final program = _quickstartProgram();
    expect(program, contains("'fn_cli_…'"));
    expect(program, contains("'https://flags.example.com'"));
    final dir = Directory.systemTemp.createTempSync('flagnetic-quickstart');
    addTearDown(() => dir.deleteSync(recursive: true));
    final file = File('${dir.path}/quickstart.dart')
      ..writeAsStringSync(
        program
            .replaceAll("'fn_cli_…'", "'$clientKey'")
            .replaceAll('https://flags.example.com', '${server.url}'),
      );

    final done = ReceivePort();
    final errors = ReceivePort();
    await Isolate.spawnUri(
      file.uri,
      const [],
      null,
      packageConfig: Uri.file(
        '${Directory.current.path}/.dart_tool/package_config.json',
      ),
      onExit: done.sendPort,
      onError: errors.sendPort,
    );
    final failure = await Future.any<Object?>([
      errors.first,
      done.first.then((_) => null),
    ]).timeout(const Duration(seconds: 30));
    expect(failure, isNull, reason: 'the program ends without an error');

    expect(evaluates, greaterThanOrEqualTo(2), reason: 'one per context');
    final sent = [for (final e in events) e.json! as Map<String, Object?>];
    expect(sent, isNotEmpty, reason: 'close sends what is queued');
    final exposures = [
      for (final batch in sent)
        for (final event in batch['events']! as List<Object?>)
          if ((event! as Map<String, Object?>)['type'] == 'exposure') event,
    ];
    expect(exposures, hasLength(1));
  });
}
