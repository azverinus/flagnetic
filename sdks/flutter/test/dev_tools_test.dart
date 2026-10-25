// The Dev Tools helper for local overrides: persists overrides in the store
// under their own key across restarts, exposes hasOverrides for a badge, and
// refuses every mutation in a release build — impossible to ship, however
// the client was constructed.
import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/offline_client.dart';

void main() {
  testWidgets('set, remove and clear edit the client\'s overrides', (
    tester,
  ) async {
    final client = offlineClient({'checkout': false}, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);

    expect(devTools.set('checkout', true), isTrue);
    expect(client.getBool('checkout', false), isTrue);
    expect(devTools.list(), {'checkout': true});

    devTools.remove('checkout');
    expect(client.getBool('checkout', false), isFalse);

    devTools.set('checkout', true);
    devTools.clear();
    expect(devTools.list(), isEmpty);
  });

  testWidgets('hasOverrides tracks whether any override is set', (
    tester,
  ) async {
    final client = offlineClient({'checkout': false}, allowOverrides: true);
    addTearDown(client.close);
    final devTools = FlagneticDevTools(client: client, store: InMemoryStore());
    addTearDown(devTools.dispose);
    final seen = <bool>[];
    devTools.hasOverrides.addListener(
      () => seen.add(devTools.hasOverrides.value),
    );

    expect(devTools.hasOverrides.value, isFalse);
    devTools.set('checkout', true);
    expect(devTools.hasOverrides.value, isTrue);
    devTools.clear();
    expect(devTools.hasOverrides.value, isFalse);
    expect(seen, [true, false]);
  });

  testWidgets('persists overrides under their own key, restored across a '
      'restart', (tester) async {
    final store = InMemoryStore();
    final first = offlineClient({'checkout': false}, allowOverrides: true);
    final firstDevTools = FlagneticDevTools(client: first, store: store);
    firstDevTools.set('checkout', true);
    addTearDown(() {
      firstDevTools.dispose();
      first.close();
    });

    expect(store.entries.keys, contains(clientKeys.overrides));

    final second = offlineClient({'checkout': false}, allowOverrides: true);
    final secondDevTools = FlagneticDevTools(client: second, store: store);
    addTearDown(() {
      secondDevTools.dispose();
      second.close();
    });

    expect(second.getBool('checkout', false), isTrue);
    expect(secondDevTools.hasOverrides.value, isTrue);
  });

  testWidgets('clearing removes the persisted entry', (tester) async {
    final store = InMemoryStore();
    final client = offlineClient({'checkout': false}, allowOverrides: true);
    final devTools = FlagneticDevTools(client: client, store: store);
    addTearDown(() {
      devTools.dispose();
      client.close();
    });

    devTools.set('checkout', true);
    expect(store.entries.keys, contains(clientKeys.overrides));
    devTools.clear();
    expect(store.entries.keys, isNot(contains(clientKeys.overrides)));
  });

  group('a release build', () {
    testWidgets('never restores a persisted override', (tester) async {
      final store = InMemoryStore();
      final debugClient = offlineClient({
        'checkout': false,
      }, allowOverrides: true);
      final debugDevTools = FlagneticDevTools(
        client: debugClient,
        store: store,
      );
      addTearDown(() {
        debugDevTools.dispose();
        debugClient.close();
      });
      debugDevTools.set('checkout', true);

      final releaseClient = offlineClient({
        'checkout': false,
      }, allowOverrides: true);
      final releaseDevTools = FlagneticDevTools(
        client: releaseClient,
        store: store,
        releaseMode: true,
      );
      addTearDown(() {
        releaseDevTools.dispose();
        releaseClient.close();
      });

      expect(releaseClient.getBool('checkout', false), isFalse);
      expect(releaseDevTools.hasOverrides.value, isFalse);
    });

    testWidgets('refuses every mutation, logging override_refused', (
      tester,
    ) async {
      final logs = LogRecorder();
      final client = offlineClient(
        {'checkout': false},
        allowOverrides: true,
        logger: logs.call,
      );
      addTearDown(client.close);
      final devTools = FlagneticDevTools(
        client: client,
        store: InMemoryStore(),
        logger: logs.call,
        releaseMode: true,
      );
      addTearDown(devTools.dispose);

      expect(devTools.isReleaseBuild, isTrue);
      expect(devTools.set('checkout', true), isFalse);
      expect(client.getBool('checkout', false), isFalse);
      devTools.remove('checkout');
      devTools.clear();
      expect(client.getBool('checkout', false), isFalse);
      expect(
        logs.fieldsOf(LogEvents.overrideRefused),
        everyElement(containsPair('cause', 'release_build')),
      );
      expect(
        logs.count(LogEvents.overrideRefused),
        2,
        reason: 'set and remove',
      );
    });
  });

  group('a client built without allowOverrides', () {
    testWidgets('constructs, refuses every mutation and persists nothing', (
      tester,
    ) async {
      final logs = LogRecorder();
      final store = InMemoryStore();
      final client = offlineClient({'checkout': false});
      addTearDown(client.close);
      final devTools = FlagneticDevTools(
        client: client,
        store: store,
        logger: logs.call,
      );
      addTearDown(devTools.dispose);

      expect(devTools.list(), isEmpty);
      expect(devTools.hasOverrides.value, isFalse);
      expect(devTools.set('checkout', true), isFalse);
      devTools
        ..remove('checkout')
        ..clear();
      // A change of the client's values reaches the helper's listener,
      // which must not throw from inside the client.
      client.offline!.set('checkout', true);
      expect(client.getBool('checkout', false), isTrue);
      expect(store.entries, isEmpty);
      expect(
        logs.fieldsOf(LogEvents.overrideRefused),
        everyElement(containsPair('cause', 'not_allowed')),
      );
      expect(logs.count(LogEvents.overrideRefused), 2, reason: 'set, remove');
    });

    testWidgets('in a release build, never asks the client for overrides', (
      tester,
    ) async {
      final logs = LogRecorder();
      final client = offlineClient({'checkout': false}, logger: logs.call);
      addTearDown(client.close);
      final devTools = FlagneticDevTools(
        client: client,
        store: InMemoryStore(),
        releaseMode: true,
      );
      addTearDown(devTools.dispose);

      expect(devTools.list(), isEmpty);
      expect(
        logs.count(LogEvents.overrideRefused),
        0,
        reason: 'a release build with overrides off logs nothing on start',
      );
    });
  });
}
