import 'files_stub.dart' if (dart.library.io) 'files_io.dart' as platform;

/// Where the default store keeps the documents that are too big for a
/// preference: one file per unit set under the application support
/// directory.
abstract interface class FileStore {
  /// The contents of [name], or null when there is no such file.
  Future<String?> read(String name);

  /// Writes [value] to [name], creating the directory when needed.
  Future<void> write(String name, String value);

  /// Removes [name]; a missing file is not an error.
  Future<void> delete(String name);
}

/// The platform's file store, or null where there is no file system: on the
/// web every entry lives in the preferences instead.
///
/// [directory] answers the directory to write in; the application support
/// directory when it is null.
FileStore? createFileStore(Future<String> Function()? directory) =>
    platform.createFileStore(directory);
