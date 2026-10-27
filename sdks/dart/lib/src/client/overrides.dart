import 'log.dart';
import 'types.dart';
import 'values.dart';

/// One local override: the flag type it serves and its value.
typedef LocalOverride = ({String type, Object? value});

/// What [LocalOverrides] needs from the client it belongs to.
abstract interface class OverrideHost {
  /// Whether the held values name a protected environment.
  bool get protectedEnvironment;

  /// The type the held values give the flag [key], or null when they do not
  /// carry it.
  String? flagType(String key);

  /// The local overrides by flag key; replaced whole, never mutated.
  Map<String, LocalOverride> get overrides;
  set overrides(Map<String, LocalOverride> value);

  /// The logger hook of the client's options.
  Logger? get logger;

  /// Fires the change listeners with [keys].
  void emitChange(List<String> keys);
}

/// Local overrides: substitutions of flag values inside this process that
/// never reach the platform. An overridden flag reads reason
/// `local_override`, with no experiment block and the stage unchanged, so
/// `exposure` is a no-op for it. They exist only with the `allowOverrides`
/// option and are refused in a protected environment. Obtain this through
/// the client's `overrides`.
final class LocalOverrides {
  LocalOverrides._(this._host);

  final OverrideHost _host;

  /// Substitutes [value] for the flag [key] and fires the change listeners.
  ///
  /// Returns false, logging `override_refused`, when the held values name a
  /// protected environment. The value must be of the flag's type; a key
  /// the held values do not carry takes the type of [value]
  /// (`bool` is boolean, `String` is string, `num` is number, anything else
  /// is JSON). Throws [ArgumentError] for a value of another type — the one
  /// local-override mistake this refuses loudly, like a server key refuses
  /// to build a client.
  bool set(String key, Object? value) {
    final h = _host;
    if (h.protectedEnvironment) {
      emitLog(h.logger, LogLevel.warn, LogEvents.overrideRefused, {
        'flag': key,
        'cause': 'protected_environment',
      });
      return false;
    }
    final type = h.flagType(key) ?? flagTypeOf(value);
    if (!fitsFlagType(type, value)) {
      throw ArgumentError.value(
        value,
        'value',
        'a local override of "$key" must be a $type value',
      );
    }
    h.overrides = {
      ...h.overrides,
      key: (type: type, value: type == 'json' ? copyJson(value) : value),
    };
    emitLog(h.logger, LogLevel.warn, LogEvents.overrideSet, {'flag': key});
    h.emitChange([key]);
    return true;
  }

  /// Drops the local override of [key], firing the change listeners when
  /// there was one.
  void remove(String key) {
    final h = _host;
    if (!h.overrides.containsKey(key)) return;
    h
      ..overrides = ({...h.overrides}..remove(key))
      ..emitChange([key]);
  }

  /// Drops every local override, firing the change listeners with the keys
  /// that had one, sorted.
  void clear() {
    final h = _host;
    if (h.overrides.isEmpty) return;
    final keys = h.overrides.keys.toList()..sort();
    h
      ..overrides = {}
      ..emitChange(keys);
  }

  /// The local overrides by flag key.
  Map<String, Object?> list() => {
    for (final MapEntry(:key, :value) in _host.overrides.entries)
      key: value.value,
  };
}

/// The local overrides of [host]; a client hands these out. A function
/// rather than a constructor, so that the package exports the class without
/// a way to build one around a host of the caller's.
LocalOverrides localOverridesOf(OverrideHost host) => LocalOverrides._(host);

/// Drops every local override of [host] when [protectedEnvironment] holds,
/// logging `override_refused` for each, and returns the dropped keys
/// sorted: a protected environment never serves one, even in the moment between
/// its swap and this call.
List<String> refuseLocalOverrides(
  OverrideHost host, {
  required bool protectedEnvironment,
}) {
  if (!protectedEnvironment || host.overrides.isEmpty) return const [];
  final keys = host.overrides.keys.toList()..sort();
  host.overrides = {};
  for (final key in keys) {
    emitLog(host.logger, LogLevel.warn, LogEvents.overrideRefused, {
      'flag': key,
      'cause': 'protected_environment',
    });
  }
  return keys;
}
