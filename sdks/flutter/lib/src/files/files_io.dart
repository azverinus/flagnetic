import 'dart:io';

import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

import 'files.dart';

/// The `dart:io` file store, under `flagnetic` in [directory] or the
/// application support directory.
FileStore? createFileStore(Future<String> Function()? directory) =>
    _IoFileStore(
      () async => '${await (directory ?? _applicationSupport)()}/flagnetic',
    );

/// The channel of the plugin's native side, which knows the directory the
/// platform keeps out of backups: `path_provider` offers neither Android's
/// `noBackupFilesDir` nor the exclusion attribute of iOS and macOS.
const _channel = MethodChannel('flagnetic_flutter/storage');

/// The backup-free directory, or null where no native side answers: Windows
/// and Linux have none, and a plugin that is not registered (a test, a
/// headless engine) is the same as none.
Future<FileStore?> createBackupFreeFileStore() async {
  try {
    final path = await _channel.invokeMethod<String>('backupFreeDirectory');
    if (path == null || path.isEmpty) return null;
    return _IoFileStore(() async => path);
  } on MissingPluginException {
    return null;
  } on PlatformException {
    return null;
  }
}

/// Numbers the temporary file of each write in this isolate, shared by every
/// store, so two stores over one directory never collide either.
var _writes = 0;

/// How old a temporary file must be before it is taken for the leftover of a
/// write that never finished: a write in flight, in this process or another,
/// finishes within moments.
const _staleTemporary = Duration(minutes: 1);

Future<String> _applicationSupport() async =>
    (await getApplicationSupportDirectory()).path;

final class _IoFileStore implements FileStore {
  _IoFileStore(this._root);

  final Future<String> Function() _root;
  Future<Directory>? _directory;

  Future<Directory> _open() => _directory ??= _root().then((path) {
    final directory = Directory(path);
    _sweep(directory);
    return directory;
  });

  /// An app killed between writing a temporary file and renaming it leaves
  /// the temporary for good, and the counter that names it restarts at 0 in
  /// every process, so nothing else would ever remove it.
  static void _sweep(Directory directory) {
    try {
      if (!directory.existsSync()) return;
      final limit = DateTime.now().subtract(_staleTemporary);
      for (final entity in directory.listSync()) {
        if (entity is File &&
            entity.path.endsWith('.tmp') &&
            entity.lastModifiedSync().isBefore(limit)) {
          entity.deleteSync();
        }
      }
    } on FileSystemException {
      // A leftover that cannot be removed now is tried again next start.
    }
  }

  Future<File> _file(String name) async =>
      File('${(await _open()).path}/$name');

  @override
  Future<String?> read(String name) async {
    final file = await _file(name);
    if (!file.existsSync()) return null;
    return file.readAsString();
  }

  @override
  Future<void> write(String name, String value) async {
    final file = await _file(name);
    await file.parent.create(recursive: true);
    // Write beside the entry and rename, so a crash mid-write leaves the
    // previous last good values readable rather than half a document.
    // Each write has a temporary of its own: two writes of one entry in
    // flight together must not truncate or rename each other's file.
    final temporary = File('${file.path}.${_writes++}.tmp');
    await temporary.writeAsString(value, flush: true);
    await temporary.rename(file.path);
  }

  @override
  Future<void> delete(String name) async {
    final file = await _file(name);
    if (file.existsSync()) await file.delete();
  }
}
