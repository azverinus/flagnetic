import 'dart:async';
import 'dart:convert';

import 'package:flagnetic/flagnetic.dart';
import 'package:flutter/foundation.dart';

import 'log.dart';

/// The Dev Tools helper for local overrides: gates every mutation
/// behind the build mode — a no-op, `override_refused`, in a release build,
/// however [FlagneticClient.overrides] was constructed, because the pure
/// Dart client has no notion of a Flutter build mode — persists overrides in
/// [FlagneticStore] under their own key so QA's flips survive a restart, and
/// exposes [hasOverrides] for a badge.
///
/// ```dart
/// final devTools = FlagneticDevTools(client: client, store: store);
/// // …
/// devTools.dispose();
/// ```
///
/// Construct one alongside a client built with
/// `ClientOptions(allowOverrides: true, …)`; without that option every
/// mutation is refused just the same, for the ordinary reason
/// (`override_refused`, cause `not_allowed`).
final class FlagneticDevTools {
  /// A Dev Tools helper for [client], persisting overrides in [store] under
  /// their own key and restoring them at once.
  ///
  /// [releaseMode] is for a test asserting the release-mode refusal; every
  /// other caller leaves it at its default, the application's real build
  /// mode.
  FlagneticDevTools({
    required this.client,
    required FlagneticStore store,
    Logger? logger,
    @visibleForTesting bool releaseMode = kReleaseMode,
  }) : _store = store,
       _keys = StoreKeys(client.sdkKey),
       _logger = logger,
       _releaseMode = releaseMode,
       _hasOverrides = ValueNotifier(client.overrides.list().isNotEmpty) {
    _restore();
    _removeChange = client.onChange((_) => _sync());
  }

  /// The client this helper edits the overrides of.
  final FlagneticClient client;

  final FlagneticStore _store;
  final StoreKeys _keys;
  final Logger? _logger;
  final bool _releaseMode;
  final ValueNotifier<bool> _hasOverrides;
  late final void Function() _removeChange;
  String? _persisted;

  /// Whether any local override is set, for a badge. Starts false until the
  /// store has been read back.
  ValueListenable<bool> get hasOverrides => _hasOverrides;

  /// True in a release build: [set], [remove] and [clear] are refused.
  bool get isReleaseBuild => _releaseMode;

  /// The local overrides by flag key.
  Map<String, Object?> list() => client.overrides.list();

  /// Substitutes [value] for the flag [key]. Refused in a release build,
  /// logging `override_refused` with cause `release_build`: see
  /// [FlagneticClient.overrides] for every other refusal.
  bool set(String key, Object? value) {
    if (_releaseMode) {
      _refused(key);
      return false;
    }
    return client.overrides.set(key, value);
  }

  /// Drops the override of [key]. A no-op in a release build.
  void remove(String key) {
    if (_releaseMode) {
      _refused(key);
      return;
    }
    client.overrides.remove(key);
  }

  /// Drops every override. A no-op in a release build.
  void clear() {
    if (_releaseMode) return;
    client.overrides.clear();
  }

  /// Stops following [client] and releases [hasOverrides].
  void dispose() {
    _removeChange();
    _hasOverrides.dispose();
  }

  void _refused(String key) => emitLog(
    _logger,
    LogLevel.warn,
    LogEvents.overrideRefused,
    {'flag': key, 'cause': 'release_build'},
  );

  /// Reads what an earlier run persisted and applies it, so overrides
  /// survive a restart. A store that fails or holds nothing leaves the
  /// client with none, same as any other store failure in this SDK.
  void _restore() {
    if (_releaseMode) return;
    try {
      final entry = _store.get(_keys.overrides);
      if (entry is Future<String?>) {
        unawaited(
          entry.then(_apply, onError: (Object e) => _storeFailed('get', e)),
        );
      } else {
        _apply(entry);
      }
    } on Object catch (e) {
      // Never breaks construction: the client still serves without them.
      _storeFailed('get', e);
    }
  }

  void _apply(String? raw) {
    if (raw == null) return;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! Map<String, Object?>) return;
      for (final MapEntry(:key, :value) in decoded.entries) {
        client.overrides.set(key, value);
      }
    } on Object {
      // A torn entry is not worth keeping; the client still serves.
    }
    _persisted = raw;
    _hasOverrides.value = client.overrides.list().isNotEmpty;
  }

  /// Re-persists the overrides whenever they change; every other change
  /// (a new ruleset, a stage transition) costs one cheap comparison and no
  /// write.
  void _sync() {
    final overrides = client.overrides.list();
    _hasOverrides.value = overrides.isNotEmpty;
    final encoded = overrides.isEmpty ? null : jsonEncode(overrides);
    if (encoded == _persisted) return;
    _persisted = encoded;
    try {
      final write = encoded == null
          ? _store.remove(_keys.overrides)
          : _store.set(_keys.overrides, encoded);
      if (write is Future<void>) {
        unawaited(write.catchError((Object e) => _storeFailed('set', e)));
      }
    } on Object catch (e) {
      // A store that throws still leaves the client serving the overrides
      // held in memory; only surviving a restart is lost.
      _storeFailed('set', e);
    }
  }

  void _storeFailed(String operation, Object cause) => emitLog(
    _logger,
    LogLevel.warn,
    LogEvents.storeFailed,
    {'operation': operation, 'key': _keys.overrides, 'cause': '$cause'},
  );
}
