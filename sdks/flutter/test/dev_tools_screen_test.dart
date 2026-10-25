// The Dev Tools screen: lists every flag with value, reason and stage,
// edits an override of the flag's type and clears all.
import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/offline_client.dart';

void main() {
  testWidgets('lists every flag with its value, reason and stage', (
    tester,
  ) async {
    final client = offlineClient({
      'checkout': true,
      'banner': 'Try free',
    }, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);

    await tester.pumpWidget(
      MaterialApp(home: FlagneticDevToolsScreen(devTools: devTools)),
    );

    expect(find.text('banner'), findsOne);
    expect(find.text('Try free · fallthrough · fresh'), findsOne);
    expect(find.text('checkout'), findsOne);
    expect(find.text('true · fallthrough · fresh'), findsOne);
  });

  testWidgets('toggling a boolean flag\'s switch sets an override', (
    tester,
  ) async {
    final client = offlineClient({'checkout': false}, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);

    await tester.pumpWidget(
      MaterialApp(home: FlagneticDevToolsScreen(devTools: devTools)),
    );
    expect(find.text('false · fallthrough · fresh'), findsOne);

    await tester.tap(find.byType(Switch));
    await tester.pump();

    expect(find.text('true · local_override · fresh'), findsOne);
    expect(client.getBool('checkout', false), isTrue);
    expect(devTools.list(), {'checkout': true});
  });

  testWidgets('editing a string flag opens a dialog and sets an override', (
    tester,
  ) async {
    final client = offlineClient({'banner': 'Try free'}, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);

    await tester.pumpWidget(
      MaterialApp(home: FlagneticDevToolsScreen(devTools: devTools)),
    );
    await tester.tap(find.byIcon(Icons.edit));
    await tester.pumpAndSettle();

    expect(find.text('Override banner'), findsOne);
    expect(find.text('Try free'), findsOne, reason: 'pre-filled');
    await tester.enterText(find.byType(TextField), 'Half price');
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();

    expect(find.text('Half price · local_override · fresh'), findsOne);
    expect(client.getString('banner', ''), 'Half price');
  });

  testWidgets('editing a number flag parses the value; an invalid one is '
      'refused with an error, the override left unset', (tester) async {
    final client = offlineClient({'max_items': 10}, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);

    await tester.pumpWidget(
      MaterialApp(home: FlagneticDevToolsScreen(devTools: devTools)),
    );
    await tester.tap(find.byIcon(Icons.edit));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField), 'not a number');
    await tester.tap(find.text('Save'));
    await tester.pump();
    expect(find.text('Not a valid number value'), findsOne);
    expect(devTools.list(), isEmpty);

    await tester.enterText(find.byType(TextField), '25');
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    expect(client.getNumber('max_items', 0), 25);
  });

  testWidgets('editing a JSON flag parses the text as JSON', (tester) async {
    final client = offlineClient({
      'config': {'a': 1},
    }, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);

    await tester.pumpWidget(
      MaterialApp(home: FlagneticDevToolsScreen(devTools: devTools)),
    );
    await tester.tap(find.byIcon(Icons.edit));
    await tester.pumpAndSettle();
    expect(find.text('{"a":1}'), findsOne, reason: 'pre-filled as JSON');

    await tester.enterText(find.byType(TextField), '{"a":2,"b":"x"}');
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();

    expect(client.getJson('config', null), {'a': 2, 'b': 'x'});
  });

  testWidgets('cancel leaves the flag unchanged', (tester) async {
    final client = offlineClient({'banner': 'Try free'}, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);

    await tester.pumpWidget(
      MaterialApp(home: FlagneticDevToolsScreen(devTools: devTools)),
    );
    await tester.tap(find.byIcon(Icons.edit));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'Half price');
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();

    expect(find.text('Try free · fallthrough · fresh'), findsOne);
    expect(devTools.list(), isEmpty);
  });

  testWidgets('an overridden flag shows an undo button removing it', (
    tester,
  ) async {
    final client = offlineClient({'banner': 'Try free'}, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);
    devTools.set('banner', 'Half price');

    await tester.pumpWidget(
      MaterialApp(home: FlagneticDevToolsScreen(devTools: devTools)),
    );
    expect(find.text('Half price · local_override · fresh'), findsOne);
    expect(find.byTooltip('Remove override'), findsOne);

    await tester.tap(find.byTooltip('Remove override'));
    await tester.pump();

    expect(find.text('Try free · fallthrough · fresh'), findsOne);
    expect(find.byTooltip('Remove override'), findsNothing);
  });

  testWidgets('clear all clears every override', (tester) async {
    final client = offlineClient({
      'checkout': false,
      'banner': 'Try free',
    }, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);
    devTools
      ..set('checkout', true)
      ..set('banner', 'Half price');

    await tester.pumpWidget(
      MaterialApp(home: FlagneticDevToolsScreen(devTools: devTools)),
    );
    expect(find.text('true · local_override · fresh'), findsOne);

    await tester.tap(find.byTooltip('Clear all overrides'));
    await tester.pump();

    expect(find.text('false · fallthrough · fresh'), findsOne);
    expect(find.text('Try free · fallthrough · fresh'), findsOne);
    expect(devTools.list(), isEmpty);
  });
}
