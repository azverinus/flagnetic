import 'dart:async';
import 'dart:convert';

import 'hash.dart';

/// Where a client keeps what must survive a restart: the last good values
/// of each recent unit set, its event queue and its local overrides. Keys and
/// values are strings; an implementation may answer synchronously or with a future. Shared preferences, secure
/// storage or an app database all fit.
///
/// Every call may throw: the client logs `store_failed` and carries on
/// without the store, so a broken store never breaks flag checks.
abstract interface class FlagneticStore {
  /// The value under [key], or null.
  FutureOr<String?> get(String key);

  /// Stores [value] under [key].
  FutureOr<void> set(String key, String value);

  /// Removes [key]; a missing key is not an error.
  FutureOr<void> remove(String key);
}

/// A [FlagneticStore] in memory: nothing survives the process. The default
/// store, and the one tests use.
final class InMemoryStore implements FlagneticStore {
  /// An empty store, or one holding [entries].
  InMemoryStore([Map<String, String>? entries]) : entries = {...?entries};

  /// Everything stored, by key.
  final Map<String, String> entries;

  @override
  String? get(String key) => entries[key];

  @override
  void set(String key, String value) => entries[key] = value;

  @override
  void remove(String key) => entries.remove(key);
}

/// The prefix of every key the client writes.
const storeKeyPrefix = 'flagnetic.';

/// The keys one SDK key writes. An app that talks to two environments, or to
/// two installations, shares one store between them, and neither may serve
/// the other's values or send the other's events under its own key.
/// The SDK key, which is what names an environment of an
/// installation, names every key; the unit set names only the values. The
/// base URL takes no part: the same key behind a proxy, a renamed host or
/// another port is the same environment.
final class StoreKeys {
  /// The keys of the connection [sdkKey] opens.
  StoreKeys(String sdkKey) : this._(_digest(sdkKey));

  StoreKeys._(String scope)
    : index = '$storeKeyPrefix$scope.last_good',
      events = '$storeKeyPrefix$scope.events',
      overrides = '$storeKeyPrefix$scope.overrides';

  /// The key listing this connection's stored unit sets, most recent first.
  final String index;

  /// The key holding this connection's events recorded but not yet accepted
  /// by the platform: exposures, aliases and evaluation summaries, in order,
  /// as JSON. They survive a restart, so offline usage is counted once.
  final String events;

  /// The key `flagnetic_flutter`'s Dev Tools helper persists local overrides
  /// under, so they survive a restart. The pure Dart client never
  /// writes it: overrides live in memory there.
  final String overrides;

  /// The key of the last good values of a unit set: a hash of its unit types
  /// and IDs, sorted, so the order a context lists them in does not matter.
  /// The entry repeats the units and a hit is checked against them.
  String lastGood(Map<String, String> units) {
    final sorted = units.keys.toList()..sort();
    final canonical = jsonEncode([
      for (final type in sorted) [type, units[type]],
    ]);
    return '$index.${_digest(canonical)}';
  }
}

/// True when [key] holds one unit set's last good values: a whole evaluate
/// response, which a store may want to keep somewhere other than its small
/// entries. The index of unit sets and an anonymous unit ID are not.
bool isLastGoodValuesKey(String key) =>
    key.startsWith(storeKeyPrefix) && key.contains('.last_good.');

String _digest(String text) =>
    murmur3(utf8.encode(text)).toRadixString(16).padLeft(8, '0');

/// True when [a] and [b] hold the same unit IDs by unit type.
bool sameUnits(Map<String, String> a, Map<String, String> b) =>
    a.length == b.length && a.entries.every((e) => b[e.key] == e.value);
