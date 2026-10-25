import 'package:flagnetic/flagnetic.dart';
import 'package:flutter/foundation.dart';

/// The value of one flag as a [ValueListenable], so a widget follows it
/// without plumbing: it notifies whenever the client's values change or the
/// fallback chain moves to another stage.
///
/// The type of [defaultValue] names the flag type asked for, exactly as
/// `getDetail` does: `bool` is boolean, `String` is string, `num` is number,
/// anything else is JSON. A missing flag, a type mismatch or stage
/// `built_in` yields [defaultValue] with reason `fallback`; reading
/// [detail] says why.
///
/// Call [dispose] when the listenable is no longer used; [FlagBuilder] does
/// it for you.
final class FlagListenable<T> extends ChangeNotifier
    implements ValueListenable<T> {
  /// Follows [flagKey] on [client], falling back to [defaultValue].
  FlagListenable(this.client, this.flagKey, this.defaultValue)
    : _detail = client.getDetail(flagKey, defaultValue) {
    _removeChange = client.onChange((keys) {
      if (keys.contains(flagKey)) _read();
    });
    _removeStage = client.onStage((_) => _read());
  }

  /// The client the value comes from.
  final FlagneticClient client;

  /// The flag followed.
  final String flagKey;

  /// The built-in default: what the app ships with.
  final T defaultValue;

  Detail _detail;
  late final void Function() _removeChange;
  late final void Function() _removeStage;

  @override
  T get value => _detail.value as T;

  /// Why the current value was served: reason, variation, ruleset version
  /// and the stage of the fallback chain.
  Detail get detail => _detail;

  void _read() {
    final next = client.getDetail(flagKey, defaultValue);
    if (next.reason == _detail.reason &&
        next.stage == _detail.stage &&
        next.variation == _detail.variation &&
        _jsonEquals(next.value, _detail.value)) {
      return;
    }
    _detail = next;
    notifyListeners();
  }

  @override
  void dispose() {
    _removeChange();
    _removeStage();
    super.dispose();
  }
}

/// The stage of a client's fallback chain as a [ValueListenable]: `built_in`
/// until values arrive, `fresh` while the last attempt succeeded and
/// `last_good` while it did not. A badge showing that a build is serving
/// cached values reads this.
///
/// Call [dispose] when it is no longer used.
final class StageListenable extends ChangeNotifier
    implements ValueListenable<Stage> {
  /// Follows the stage of [client].
  StageListenable(this.client) : _stage = client.stage {
    _remove = client.onStage((stage) {
      if (stage == _stage) return;
      _stage = stage;
      notifyListeners();
    });
  }

  /// The client the stage comes from.
  final FlagneticClient client;

  Stage _stage;
  late final void Function() _remove;

  @override
  Stage get value => _stage;

  @override
  void dispose() {
    _remove();
    super.dispose();
  }
}

/// Deep equality of decoded JSON values, so a JSON flag whose new document
/// equals the old one notifies nobody. Numbers compare by value: `1` equals
/// `1.0`.
bool _jsonEquals(Object? a, Object? b) {
  if (a is num && b is num) return a == b;
  if (a is List<Object?> && b is List<Object?>) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!_jsonEquals(a[i], b[i])) return false;
    }
    return true;
  }
  if (a is Map<String, Object?> && b is Map<String, Object?>) {
    if (a.length != b.length) return false;
    for (final MapEntry(:key, :value) in a.entries) {
      if (!b.containsKey(key) || !_jsonEquals(value, b[key])) return false;
    }
    return true;
  }
  return a == b;
}
