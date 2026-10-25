// The default store in a browser, where there is no application support
// directory: every entry, the last good values included, lives in the
// preferences.
@TestOn('browser')
library;

import 'dart:convert';

import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/offline_client.dart';

void main() {
  late FlutterStore store;

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    store = FlutterStore();
  });

  test('a unit set\'s last good values go to the preferences', () async {
    final key = clientKeys.lastGood(testContext.units);
    final document = jsonEncode(valuesDocument({'checkout': true}));
    expect(await store.get(key), isNull);

    await store.set(key, document);
    expect(await store.get(key), document);
    expect((await SharedPreferences.getInstance()).getKeys(), [key]);

    await store.remove(key);
    expect(await store.get(key), isNull);
    // Removing what is not there is not an error.
    await store.remove(key);
  });

  test('the index, the event queue and an anonymous unit ID go to the '
      'preferences', () async {
    await store.set(clientKeys.index, '["a"]');
    await store.set(clientKeys.events, '[]');
    final id = await anonymousUnitId(store, 'anonymous');
    expect(await anonymousUnitId(store, 'anonymous'), id);
    expect(
      (await SharedPreferences.getInstance()).getKeys(),
      unorderedEquals([
        clientKeys.index,
        clientKeys.events,
        anonymousUnitIdKey('anonymous'),
      ]),
    );
  });

  test('a client reads its last good values back from the store', () async {
    await store.set(
      clientKeys.lastGood(testContext.units),
      jsonEncode({
        'units': testContext.units,
        'ruleset_version': 1,
        'received_at': DateTime.utc(2026, 9, 20).toIso8601String(),
        'values': valuesDocument({'checkout': true}),
      }),
    );
    final client = Flagnetic.client(
      clientKey,
      context: testContext,
      options: ClientOptions(store: store),
    );
    addTearDown(client.close);
    // No base URL, so nothing but the store can serve.
    expect(
      await client.ready(const Duration(milliseconds: 300)),
      Stage.lastGood,
    );
    expect(client.getBool('checkout', false), isTrue);
  });
}
