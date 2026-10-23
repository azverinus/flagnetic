import 'dart:developer' as developer;

import 'types.dart';

/// Hands [event] to [logger], or to the platform's logger at warn and above
/// when there is none. Never throws: a logging hook that does must not break
/// the call that logged.
void emitLog(
  Logger? logger,
  LogLevel level,
  String event,
  Map<String, Object?> fields,
) {
  try {
    if (logger != null) {
      logger(level, event, fields);
    } else if (level.index >= LogLevel.warn.index) {
      developer.log(
        '$event $fields',
        name: 'flagnetic',
        level: level == LogLevel.error ? 1000 : 900,
      );
    }
  } on Object {
    // A throwing logger must not break the caller.
  }
}
