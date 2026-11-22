import 'types.dart';

/// Logs the `Deprecation` header of an answer: once per operation for the
/// life of one client, at warn level, naming the operation and the `Sunset`
/// when the answer carries one. It only reads headers: the call it belongs
/// to proceeds exactly as it would without them.
final class DeprecationLog {
  /// A log that writes through [log].
  DeprecationLog(this._log);

  final void Function(LogLevel, String, Map<String, Object?>) _log;
  final Set<String> _seen = {};

  /// Notes the headers (lower-case names) of an answer to [operation], the
  /// operation ID of `api/sdk.yaml` (`evaluate`, `postEvents`,
  /// `streamRuleset`).
  void note(String operation, Map<String, String> headers) {
    if (!headers.containsKey('deprecation') || !_seen.add(operation)) return;
    final sunset = headers['sunset'];
    _log(LogLevel.warn, LogEvents.deprecatedOperation, {
      'operation': operation,
      if (sunset != null && sunset.isNotEmpty) 'sunset': sunset,
    });
  }
}
