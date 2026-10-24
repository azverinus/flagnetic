// The anonymous unit ID: one per installation (per origin in a browser)
// and unit type, kept outside every SDK key's entries, so a rotated client
// key never moves an anonymous user to another experiment arm. Runs on the
// VM and in a browser.
import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

import 'support/scripted.dart';

const _rotated = 'fn_cli_TESTKEY1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';

void main() {
  test('one ID per unit type, kept across calls', () async {
    final store = InMemoryStore();
    final first = await anonymousUnitId(store, 'anonymous');
    expect(first, isNotEmpty);
    expect(await anonymousUnitId(store, 'anonymous'), first);
    expect(await anonymousUnitId(store, 'device'), isNot(first));
  });

  test('a key rotation keeps the ID', () async {
    final store = InMemoryStore();
    final id = await anonymousUnitId(store, 'anonymous');
    // What each key writes lives under its own scope; the ID under none.
    for (final keys in [clientKeys, StoreKeys(_rotated)]) {
      final scope = keys.index.substring(0, keys.index.lastIndexOf('.'));
      expect(anonymousUnitIdKey('anonymous'), isNot(startsWith(scope)));
      store.set(keys.events, '[]');
    }
    expect(await anonymousUnitId(store, 'anonymous'), id);
  });

  test('a failing store still yields an ID, and logs it', () async {
    final logs = LogRecorder();
    final id = await anonymousUnitId(
      _BrokenStore(),
      'anonymous',
      logger: logs.call,
    );
    expect(id, isNotEmpty);
    expect(logs.count(LogEvents.storeFailed), 2);
  });
}

final class _BrokenStore implements FlagneticStore {
  @override
  String? get(String key) => throw StateError('no storage');

  @override
  void set(String key, String value) => throw StateError('no storage');

  @override
  void remove(String key) => throw StateError('no storage');
}
