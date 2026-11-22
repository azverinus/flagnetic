// The Flutter program of the quickstart in docs/sdks/dart.md is the code
// between the markers below, line for line, with the placeholder key and
// address named by the constants of this file: a snippet that no longer
// compiles or works with the API fails here.
@TestOn('vm')
library;

import 'dart:io';

import 'package:flagnetic_flutter/flagnetic_flutter.dart';
// package:material_ui does not exist before Flutter 3.47, which this package still supports.
// ignore: deprecated_member_use
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/offline_client.dart';

/// The address of no server: the client falls back to its defaults.
final baseUrl = Uri.parse('http://127.0.0.1:1').toString();

const _guide = '../../docs/sdks/dart.md';

// quickstart begin
Future<void> quickstart() async {
  WidgetsFlutterBinding.ensureInitialized();
  final store = FlutterStore();
  final client = Flagnetic.client(
    clientKey,
    baseUrl: Uri.parse(baseUrl),
    context: FlagneticContext(
      units: {'anonymous': await anonymousUnitId(store, 'anonymous')},
    ),
    options: ClientOptions(store: store),
  );
  runApp(FlagneticScope(client: client, child: const App()));
}

class App extends StatelessWidget {
  const App({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
    home: FlagBuilder<bool>(
      flagKey: 'checkout',
      defaultValue: false,
      builder: (context, showCheckout, detail) =>
          Text(showCheckout ? 'New checkout' : 'Basket'),
    ),
  );
}
// quickstart end

/// The lines of [text] between the markers, or of the guide's second Dart
/// block of the Quickstart, which is the Flutter program.
List<String> _lines(String code) => [
  for (final line in code.split('\n'))
    if (line.trim().isNotEmpty) line.trimRight(),
];

String _guideProgram() {
  final text = File(_guide).readAsStringSync();
  final start = text.indexOf('\n## Quickstart\n');
  expect(start, isNonNegative, reason: 'the guide has a Quickstart section');
  final end = text.indexOf('\n## ', start + 1);
  final section = text.substring(start, end < 0 ? text.length : end);
  final blocks = RegExp(
    r'```dart\n(.*?)```',
    dotAll: true,
  ).allMatches(section).map((m) => m.group(1)!).toList();
  expect(blocks, hasLength(2), reason: 'a Dart and a Flutter program');
  final program = blocks[1];
  return program
      .split('\n')
      .where((line) => !line.startsWith('import '))
      .join('\n')
      .replaceAll("'fn_cli_…'", 'clientKey')
      .replaceAll(
        "Uri.parse('https://flags.example.com')",
        'Uri.parse(baseUrl)',
      )
      .replaceAll('Future<void> main()', 'Future<void> quickstart()');
}

void main() {
  test('the guide shows the program this test runs', () {
    final source = File('test/quickstart_test.dart').readAsStringSync();
    final region = source
        .split('// quickstart begin\n')[1]
        .split('// quickstart end')[0];
    expect(_lines(_guideProgram()), _lines(region));
  });

  testWidgets('the quickstart program builds and serves the default', (
    tester,
  ) async {
    SharedPreferences.setMockInitialValues({});
    await tester.runAsync(quickstart);
    await tester.pump();
    final scope = FlagneticScope.of(tester.element(find.byType(MaterialApp)));
    addTearDown(() async {
      await tester.runAsync(scope.close);
    });
    expect(find.text('Basket'), findsOneWidget);
  });
}
