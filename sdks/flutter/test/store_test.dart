// The default store on a device keeps a unit set's last good values in a
// file under the application support directory and everything else in
// shared preferences.
@TestOn('vm')
library;

import 'dart:convert';
import 'dart:io';

import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/offline_client.dart';

void main() {
  late Directory directory;
  late FlutterStore store;

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    directory = Directory.systemTemp.createTempSync('flagnetic-store');
    store = FlutterStore(directory: () async => directory.path);
  });

  tearDown(() => directory.deleteSync(recursive: true));

  test('a unit set\'s last good values go to a file', () async {
    final key = clientKeys.lastGood(testContext.units);
    final document = jsonEncode(valuesDocument({'checkout': true}));
    expect(await store.get(key), isNull);

    await store.set(key, document);
    expect(await store.get(key), document);
    final files = Directory('${directory.path}/flagnetic').listSync();
    expect(files, hasLength(1), reason: 'one file, no temporary left behind');
    expect(files.single.path, endsWith('.json'));
    expect(
      (await SharedPreferences.getInstance()).getKeys(),
      isEmpty,
      reason: 'a whole evaluate response does not belong in a preference',
    );

    await store.remove(key);
    expect(await store.get(key), isNull);
    // Removing what is not there is not an error.
    await store.remove(key);
  });

  test('the index and an anonymous unit ID go to the preferences', () async {
    await store.set(clientKeys.index, '["a"]');
    expect(await store.get(clientKeys.index), '["a"]');
    final id = await anonymousUnitId(store, 'anonymous');
    expect(await anonymousUnitId(store, 'anonymous'), id);
    expect(
      (await SharedPreferences.getInstance()).getKeys(),
      unorderedEquals([clientKeys.index, anonymousUnitIdKey('anonymous')]),
    );
    expect(Directory('${directory.path}/flagnetic').existsSync(), isFalse);
  });

  test('the event queue goes to the preferences', () async {
    // A batch of exposures and aliases is small and is rewritten on every
    // record; it is a preference, not a file of its own.
    const queue =
        '[{"type":"alias","event_id":"0192a7c5-0e42-7a9b-b1d3-5f6e7a8b9c0d"}]';
    await store.set(clientKeys.events, queue);
    expect(await store.get(clientKeys.events), queue);
    expect(
      (await SharedPreferences.getInstance()).getKeys(),
      unorderedEquals([clientKeys.events]),
    );
    expect(Directory('${directory.path}/flagnetic').existsSync(), isFalse);

    await store.remove(clientKeys.events);
    expect(await store.get(clientKeys.events), isNull);
  });

  test('a client picks up the queue an earlier run left behind', () async {
    final client = Flagnetic.client(
      clientKey,
      context: testContext,
      options: ClientOptions(store: ThrowingStore()),
    );
    addTearDown(client.close);
    // A store that throws on every call: the client still serves, records
    // and flushes without an exception reaching the caller.
    expect(
      await client.ready(const Duration(milliseconds: 300)),
      Stage.builtIn,
    );
    expect(client.getBool('checkout', true), isTrue);
    client
      ..exposure('checkout')
      ..alias(
        fromUnitType: 'anonymous',
        fromUnitId: 'd-8f1c',
        toUnitType: 'user',
        toUnitId: 'u-1001',
      );
    await client.flush();
  });

  test('a rewritten document replaces the previous one whole', () async {
    final key = clientKeys.lastGood(testContext.units);
    await store.set(key, jsonEncode(valuesDocument({'checkout': true})));
    final second = jsonEncode(valuesDocument({'banner': 'Try free'}));
    await store.set(key, second);
    expect(await store.get(key), second);
    expect(Directory('${directory.path}/flagnetic').listSync(), hasLength(1));
  });

  test('a client reads its last good values back from the store', () async {
    final key = clientKeys.lastGood(testContext.units);
    await store.set(
      key,
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
