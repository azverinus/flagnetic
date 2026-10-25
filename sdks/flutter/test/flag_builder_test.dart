// FlagBuilder and the listenables: the first frame already has a value,
// changes rebuild, and a store that throws on every call still renders
// built-in defaults.
import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/offline_client.dart';

void main() {
  testWidgets('builds from the served value and rebuilds on change', (
    tester,
  ) async {
    final client = offlineClient({'banner': 'Try free'});
    addTearDown(client.close);
    var builds = 0;
    await tester.pumpWidget(
      FlagneticScope(
        client: client,
        child: FlagBuilder<String>(
          flagKey: 'banner',
          defaultValue: 'Built-in banner',
          builder: (context, value, detail) {
            builds++;
            return _text('$value/${detail.reason}/${detail.stage.wireName}');
          },
        ),
      ),
    );
    expect(find.text('Try free/fallthrough/fresh'), findsOne);
    final before = builds;

    client.offline!.set('banner', 'Half price');
    await tester.pump();
    expect(find.text('Half price/fallthrough/fresh'), findsOne);
    expect(builds, greaterThan(before));

    // A flag the builder does not follow rebuilds nothing.
    final after = builds;
    client.offline!.set('other', true);
    await tester.pump();
    expect(builds, after);
  });

  testWidgets('a flag the values do not hold serves the built-in default', (
    tester,
  ) async {
    final client = offlineClient({'banner': 'Try free'});
    addTearDown(client.close);
    await tester.pumpWidget(
      FlagneticScope(
        client: client,
        child: FlagBuilder<bool>(
          flagKey: 'checkout',
          defaultValue: false,
          builder: (context, value, detail) =>
              _text('$value/${detail.reason}/${detail.errorCode}'),
        ),
      ),
    );
    expect(find.text('false/fallback/flag_not_found'), findsOne);
  });

  testWidgets('takes a client without a scope', (tester) async {
    final client = offlineClient({'checkout': true});
    addTearDown(client.close);
    await tester.pumpWidget(
      FlagBuilder<bool>(
        client: client,
        flagKey: 'checkout',
        defaultValue: false,
        builder: (context, value, detail) => _text('$value'),
      ),
    );
    expect(find.text('true'), findsOne);
  });

  testWidgets('says so when there is no scope and no client', (tester) async {
    await tester.pumpWidget(
      FlagBuilder<bool>(
        flagKey: 'checkout',
        defaultValue: false,
        builder: (context, value, detail) => _text('$value'),
      ),
    );
    expect(tester.takeException(), isFlutterError);
  });

  testWidgets('a store throwing on every call still renders built-in '
      'defaults', (tester) async {
    // No base URL either, so the fallback chain has nowhere to go but the
    // built-in defaults the app ships with.
    final client = Flagnetic.client(
      clientKey,
      context: testContext,
      options: ClientOptions(store: ThrowingStore()),
    );
    addTearDown(client.close);
    await tester.pumpWidget(
      FlagneticScope(
        client: client,
        child: FlagBuilder<String>(
          flagKey: 'banner',
          defaultValue: 'Built-in banner',
          builder: (context, value, detail) =>
              _text('$value/${detail.reason}/${detail.stage.wireName}'),
        ),
      ),
    );
    expect(find.text('Built-in banner/fallback/built_in'), findsOne);
    expect(tester.takeException(), isNull);
  });

  group('the listenables', () {
    testWidgets('a flag listenable notifies on change and stops on dispose', (
      tester,
    ) async {
      final client = offlineClient({'max_items': 10});
      addTearDown(client.close);
      final flag = FlagListenable<num>(client, 'max_items', 0);
      var notified = 0;
      flag.addListener(() => notified++);
      expect(flag.value, 10);
      expect(flag.detail.reason, 'fallthrough');

      client.offline!.set('max_items', 25);
      expect(flag.value, 25);
      expect(notified, 1);

      // The same value again notifies nobody.
      client.offline!.set('max_items', 25);
      expect(notified, 1);

      flag.dispose();
      client.offline!.set('max_items', 99);
      expect(notified, 1);
    });

    testWidgets('a stage listenable follows the fallback chain', (
      tester,
    ) async {
      // The client's own timers are real, so this runs outside the test's
      // fake clock.
      await tester.runAsync(() async {
        // A store holding last good values, and no base URL: the stage is
        // built_in while the store is read and last_good once it answers.
        final client = Flagnetic.client(
          clientKey,
          context: testContext,
          options: ClientOptions(
            store: AsyncStore(storeHolding({'checkout': true})),
          ),
        );
        final stage = StageListenable(client);
        final seen = <Stage>[];
        stage.addListener(() => seen.add(stage.value));
        expect(stage.value, Stage.builtIn);

        expect(
          await client.ready(const Duration(milliseconds: 300)),
          Stage.lastGood,
        );
        expect(stage.value, Stage.lastGood);
        expect(seen, [Stage.lastGood]);

        stage.dispose();
        await client.close();
        expect(seen, [Stage.lastGood]);
      });
    });

    testWidgets('a stage listenable starts at the client\'s stage', (
      tester,
    ) async {
      final client = offlineClient({'checkout': true});
      addTearDown(client.close);
      final stage = StageListenable(client);
      expect(stage.value, Stage.fresh);
      stage.dispose();
    });
  });
  testWidgets('a double default reads a whole number as a double', (
    tester,
  ) async {
    final client = offlineClient({'discount': 10});
    addTearDown(client.close);
    await tester.pumpWidget(
      FlagBuilder<double>(
        client: client,
        flagKey: 'discount',
        defaultValue: 0.5,
        builder: (context, value, detail) => _text('$value'),
      ),
    );
    expect(find.text(10.0.toString()), findsOne);
  });
}

Widget _text(String value) =>
    Directionality(textDirection: TextDirection.ltr, child: Text(value));
