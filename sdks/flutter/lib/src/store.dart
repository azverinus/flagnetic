import 'package:flagnetic/flagnetic.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'files/files.dart';

/// The default [FlagneticStore] of a Flutter app.
///
/// On Android, iOS and macOS every entry (the anonymous unit IDs, the event
/// queue, the index of unit sets and each unit set's last good values) is a
/// file in a directory the platform leaves out of device backups and
/// device-to-device transfers: Android's `noBackupFilesDir`, and a directory
/// marked excluded from backup on iOS and macOS. An anonymous ID belongs to
/// one device; kept in shared preferences or the application support
/// directory it would travel to a new phone with a restored backup, and two
/// devices would count as one unit in an experiment. An entry an earlier
/// version of this store left in the preferences or in the application
/// support directory moves to the new directory the first time it is read,
/// value unchanged, and is removed from where it was.
///
/// Where the platform offers no such directory (Windows and Linux, which have
/// no device backup of this kind), the small entries live in shared
/// preferences and each unit set's last good values, a whole evaluate
/// response, in a file under the application support directory. On the web
/// there is no file system and every entry lives in the preferences.
///
/// Construct it and pass it straight to the client: every call answers with
/// a future, and the client loads the last good values before it sends its
/// first request.
///
/// ```dart
/// final client = Flagnetic.client(
///   'fn_cli_…',
///   baseUrl: Uri.parse('https://flags.example.com'),
///   context: FlagneticContext(units: {'anonymous': anonymous}),
///   options: ClientOptions(store: FlutterStore()),
/// );
/// ```
final class FlutterStore implements FlagneticStore {
  /// The default store. [directory] answers where the files of a platform
  /// without a backup-free directory go; the application support directory
  /// when it is null.
  FlutterStore({Future<String> Function()? directory})
    : _files = createFileStore(directory);

  final FileStore? _files;
  Future<SharedPreferences>? _preferences;
  Future<FileStore?>? _backupFree;

  Future<SharedPreferences> get _prefs =>
      _preferences ??= SharedPreferences.getInstance();

  /// The backup-free directory, or null where there is none to offer.
  Future<FileStore?> get _excluded => _backupFree ??= _files == null
      ? Future.value()
      : createBackupFreeFileStore();

  /// The file of [key] in the backup-free directory.
  String _entryName(String key) =>
      '${key.replaceAll(RegExp('[^A-Za-z0-9._-]'), '_')}.entry';

  /// The file holding [key] in the application support directory, or null
  /// when [key] belongs in the preferences. Only a unit set's last good
  /// values are big enough to be worth a file there.
  String? _legacyFileName(String key) =>
      _files == null || !isLastGoodValuesKey(key)
      ? null
      : '${key.replaceAll(RegExp('[^A-Za-z0-9._-]'), '_')}.json';

  Future<String?> _legacyGet(String key) async {
    final name = _legacyFileName(key);
    if (name != null) return _files!.read(name);
    return (await _prefs).getString(key);
  }

  Future<void> _legacyRemove(String key) async {
    final name = _legacyFileName(key);
    if (name != null) return _files!.delete(name);
    await (await _prefs).remove(key);
  }

  @override
  Future<String?> get(String key) async {
    final excluded = await _excluded;
    if (excluded == null) return _legacyGet(key);
    final held = await excluded.read(_entryName(key));
    if (held != null) return held;
    final old = await _legacyGet(key);
    if (old == null) return null;
    // Moved, not copied: what stays behind rides along in a backup. Should
    // the move fail the entry is still served, and moves on the next read.
    try {
      await excluded.write(_entryName(key), old);
      await _legacyRemove(key);
    } on Object {
      return old;
    }
    return old;
  }

  @override
  Future<void> set(String key, String value) async {
    final excluded = await _excluded;
    if (excluded == null) {
      final name = _legacyFileName(key);
      if (name != null) return _files!.write(name, value);
      await (await _prefs).setString(key, value);
      return;
    }
    await excluded.write(_entryName(key), value);
    // The old copy goes too, so a restored backup carries no ID. The value
    // is safe now, so a failure to remove it is no failure to store.
    try {
      await _legacyRemove(key);
    } on Object {
      // Removed on the next write or read of this entry.
    }
  }

  @override
  Future<void> remove(String key) async {
    final excluded = await _excluded;
    if (excluded != null) await excluded.delete(_entryName(key));
    await _legacyRemove(key);
  }
}
