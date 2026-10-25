import 'dart:io';

import 'package:path_provider/path_provider.dart';

import 'files.dart';

/// The `dart:io` file store, under [directory] or the application support
/// directory.
FileStore? createFileStore(Future<String> Function()? directory) =>
    _IoFileStore(directory ?? _applicationSupport);

/// Numbers the temporary file of each write in this isolate, shared by every
/// store, so two stores over one directory never collide either.
var _writes = 0;

Future<String> _applicationSupport() async =>
    (await getApplicationSupportDirectory()).path;

final class _IoFileStore implements FileStore {
  _IoFileStore(this._root);

  final Future<String> Function() _root;
  Future<Directory>? _directory;

  Future<Directory> _open() =>
      _directory ??= _root().then((path) => Directory('$path/flagnetic'));

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
