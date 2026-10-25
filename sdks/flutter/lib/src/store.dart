import 'package:flagnetic/flagnetic.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'files/files.dart';

/// The default [FlagneticStore] of a Flutter app: shared preferences for the
/// small entries (the index of unit sets, anonymous unit IDs) and one file
/// under the application support directory for each unit set's last good
/// values, which are a whole evaluate response and do not belong in a
/// preference.
///
/// On the web there is no application support directory, so every entry
/// lives in the preferences.
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
  /// The default store. [directory] answers where the files go; the
  /// application support directory when it is null.
  FlutterStore({Future<String> Function()? directory})
    : _files = createFileStore(directory);

  final FileStore? _files;
  Future<SharedPreferences>? _preferences;

  Future<SharedPreferences> get _prefs =>
      _preferences ??= SharedPreferences.getInstance();

  /// The file holding [key], or null when [key] belongs in the preferences.
  /// Only a unit set's last good values are big enough to be worth a file.
  String? _fileName(String key) => _files == null || !isLastGoodValuesKey(key)
      ? null
      : '${key.replaceAll(RegExp('[^A-Za-z0-9._-]'), '_')}.json';

  @override
  Future<String?> get(String key) async {
    final name = _fileName(key);
    if (name != null) return _files!.read(name);
    return (await _prefs).getString(key);
  }

  @override
  Future<void> set(String key, String value) async {
    final name = _fileName(key);
    if (name != null) return _files!.write(name, value);
    await (await _prefs).setString(key, value);
  }

  @override
  Future<void> remove(String key) async {
    final name = _fileName(key);
    if (name != null) return _files!.delete(name);
    await (await _prefs).remove(key);
  }
}
