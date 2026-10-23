import 'dart:async';

import 'log.dart';
import 'store.dart';
import 'types.dart';
import 'uuid.dart';

/// The store key holding the anonymous unit ID of [unitType].
String anonymousUnitIdKey(String unitType) =>
    '${storeKeyPrefix}anonymous.$unitType';

/// The unit ID this installation uses for the anonymous unit type
/// [unitType], generating and persisting a random one the first time.
///
/// The unit type is the application's: a project declares its own unit
/// types and the SDK never invents one. Pass whichever the project declared
/// for anonymous traffic, and keep the ID in the context's units after
/// sign-in so a cross-sign-up experiment does not change variation mid
/// funnel.
///
/// ```dart
/// final store = InMemoryStore();
/// final anonymous = await anonymousUnitId(store, 'anonymous');
/// final client = Flagnetic.client(
///   key,
///   baseUrl: url,
///   context: FlagneticContext(units: {'anonymous': anonymous}),
/// );
/// ```
///
/// Total, like every other call: a store that throws yields a fresh ID that
/// does not survive the restart, and a `store_failed` log.
Future<String> anonymousUnitId(
  FlagneticStore store,
  String unitType, {
  Logger? logger,
}) async {
  final key = anonymousUnitIdKey(unitType);
  void log(String operation, Object cause) => emitLog(
    logger,
    LogLevel.warn,
    LogEvents.storeFailed,
    {'operation': operation, 'key': key, 'cause': '$cause'},
  );

  try {
    final held = await store.get(key);
    if (held != null && held.isNotEmpty) return held;
  } on Object catch (e) {
    log('get', e);
  }
  final id = uuidV7(DateTime.now().toUtc());
  try {
    await store.set(key, id);
  } on Object catch (e) {
    log('set', e);
  }
  return id;
}
