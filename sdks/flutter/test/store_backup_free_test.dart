// On Android, iOS and macOS the default store keeps every entry in the
// directory the platform leaves out of device backups, and moves what an
// earlier version left in the preferences or in the application support
// directory when it is first read.
@TestOn('vm')
library;

import 'dart:convert';
import 'dart:io';

import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/offline_client.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('flagnetic_flutter/storage');
  late Directory support;
  late Directory excluded;
  late FlutterStore store;

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    support = Directory.systemTemp.createTempSync('flagnetic-support');
    excluded = Directory.systemTemp.createTempSync('flagnetic-excluded');
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          expect(call.method, 'backupFreeDirectory');
          return excluded.path;
        });
    store = FlutterStore(directory: () async => support.path);
  });

  tearDown(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
    support.deleteSync(recursive: true);
    excluded.deleteSync(recursive: true);
  });

  Future<Set<String>> preferenceKeys() async =>
      (await SharedPreferences.getInstance()).getKeys();

  test('an anonymous unit ID lands in the backup-free directory and not '
      'in the preferences', () async {
    final id = await anonymousUnitId(store, 'anonymous');
    expect(await anonymousUnitId(store, 'anonymous'), id);
    expect(await preferenceKeys(), isEmpty);
    expect(Directory('${support.path}/flagnetic').existsSync(), isFalse);
    final files = excluded.listSync().whereType<File>().toList();
    expect(files, hasLength(1));
    expect(files.single.readAsStringSync(), id);
  });

  test('the index, the event queue and the last good values do too', () async {
    final key = clientKeys.lastGood(testContext.units);
    final document = jsonEncode(valuesDocument({'checkout': true}));
    await store.set(clientKeys.index, '["a"]');
    await store.set(clientKeys.events, '[]');
    await store.set(key, document);
    expect(await store.get(clientKeys.index), '["a"]');
    expect(await store.get(clientKeys.events), '[]');
    expect(await store.get(key), document);
    expect(await preferenceKeys(), isEmpty);
    expect(excluded.listSync(), hasLength(3));

    await store.remove(key);
    expect(await store.get(key), isNull);
    // Removing what is not there is not an error.
    await store.remove(key);
  });

  test('an anonymous ID already in the preferences is read back unchanged, '
      'moved and removed from them', () async {
    SharedPreferences.setMockInitialValues({
      anonymousUnitIdKey('anonymous'): 'd-8f1c',
    });
    expect(await anonymousUnitId(store, 'anonymous'), 'd-8f1c');
    expect(await preferenceKeys(), isEmpty);
    final file = excluded.listSync().whereType<File>().single;
    expect(file.readAsStringSync(), 'd-8f1c');
    expect(await anonymousUnitId(store, 'anonymous'), 'd-8f1c');
  });

  test('a write while an old entry is being moved is not overwritten by '
      'the old value', () async {
    SharedPreferences.setMockInitialValues({clientKeys.events: '["old"]'});
    final reading = store.get(clientKeys.events);
    final writing = store.set(clientKeys.events, '["new"]');
    expect(await reading, '["old"]');
    await writing;
    expect(await store.get(clientKeys.events), '["new"]');
    expect(await preferenceKeys(), isEmpty);
  });

  test(
    'last good values in the application support directory move too',
    () async {
      final key = clientKeys.lastGood(testContext.units);
      final document = jsonEncode(valuesDocument({'checkout': true}));
      // What the store of an earlier version wrote: the same store, without a
      // backup-free directory to offer.
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, null);
      await FlutterStore(
        directory: () async => support.path,
      ).set(key, document);
      expect(Directory('${support.path}/flagnetic').listSync(), hasLength(1));
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (_) async => excluded.path);

      expect(await store.get(key), document);
      expect(Directory('${support.path}/flagnetic').listSync(), isEmpty);
      expect(excluded.listSync(), hasLength(1));
    },
  );

  test('a native side that fails leaves the old locations in use', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (_) async {
          throw PlatformException(code: 'unavailable');
        });
    await store.set(clientKeys.index, '["a"]');
    expect(await store.get(clientKeys.index), '["a"]');
    expect(await preferenceKeys(), [clientKeys.index]);
  });
}
