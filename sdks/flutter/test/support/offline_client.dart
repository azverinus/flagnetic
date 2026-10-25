import 'dart:convert';

import 'package:flagnetic_flutter/flagnetic_flutter.dart';

/// A client key as the platform issues them.
const clientKey = 'fn_cli_TESTKEY0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/// The store keys [clientKey] writes; another key writes its own.
final clientKeys = StoreKeys(clientKey);

/// The context of every widget test.
const testContext = FlagneticContext(units: {'anonymous': 'd-8f1c'});

/// An evaluate response serving [flags], each entry a flag key and the
/// value the platform evaluated for the context.
Map<String, Object?> valuesDocument(Map<String, Object?> flags) => {
  'schema_version': 1,
  'ruleset_version': 1,
  'flags': {
    for (final MapEntry(:key, :value) in flags.entries)
      key: {
        'type': switch (value) {
          bool() => 'boolean',
          String() => 'string',
          num() => 'number',
          _ => 'json',
        },
        'value': value,
        'variation': 'on',
        'reason': 'fallthrough',
      },
  },
};

/// A client that never touches the network and serves [flags]; its
/// `offline` helpers change what it serves.
FlagneticClient offlineClient(
  Map<String, Object?> flags, {
  bool allowOverrides = false,
  Logger? logger,
}) => Flagnetic.client(
  clientKey,
  context: testContext,
  options: ClientOptions(
    offline: OfflineSource.document(valuesDocument(flags)),
    allowOverrides: allowOverrides,
    logger: logger,
  ),
);

/// An [InMemoryStore] holding [flags] as the last good values of
/// [testContext]'s unit set, as a previous run left them.
InMemoryStore storeHolding(Map<String, Object?> flags) => InMemoryStore({
  clientKeys.lastGood(testContext.units): jsonEncode({
    'units': testContext.units,
    'ruleset_version': 1,
    'received_at': DateTime.utc(2026, 9, 20).toIso8601String(),
    'values': valuesDocument(flags),
  }),
});

/// A store answering with futures, as a real device store does: the client
/// starts at `built_in` and reaches `last_good` once it answers.
final class AsyncStore implements FlagneticStore {
  /// Wraps [entries].
  AsyncStore(this.entries);

  /// What it holds.
  final InMemoryStore entries;

  @override
  Future<String?> get(String key) async => entries.get(key);

  @override
  Future<void> set(String key, String value) async => entries.set(key, value);

  @override
  Future<void> remove(String key) async => entries.remove(key);
}

/// A store whose every call throws, as a device with no writable storage
/// does.
final class ThrowingStore implements FlagneticStore {
  @override
  Never get(String key) => throw StateError('no store');

  @override
  Never set(String key, String value) => throw StateError('no store');

  @override
  Never remove(String key) => throw StateError('no store');
}

/// A logger hook that keeps every event.
final class LogRecorder {
  /// Every event received, in order.
  final List<({LogLevel level, String event, Map<String, Object?> fields})>
  events = [];

  /// The hook to pass as [ClientOptions.logger] or [FlagneticDevTools].
  void call(LogLevel level, String event, Map<String, Object?> fields) =>
      events.add((level: level, event: event, fields: fields));

  /// How many events named [event] were received.
  int count(String event) => events.where((e) => e.event == event).length;

  /// The fields of every event named [event].
  List<Map<String, Object?>> fieldsOf(String event) => [
    for (final e in events)
      if (e.event == event) e.fields,
  ];
}
