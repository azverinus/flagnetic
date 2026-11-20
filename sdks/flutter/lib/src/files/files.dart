import 'files_stub.dart' if (dart.library.io) 'files_io.dart' as platform;

/// Where the default store keeps documents as files: one file per entry,
/// written beside itself and renamed so a crash never leaves half a document.
abstract interface class FileStore {
  /// The contents of [name], or null when there is no such file.
  Future<String?> read(String name);

  /// Writes [value] to [name], creating the directory when needed.
  Future<void> write(String name, String value);

  /// Removes [name]; a missing file is not an error.
  Future<void> delete(String name);
}

/// The platform's file store under the application support directory, or
/// null where there is no file system: on the web every entry lives in the
/// preferences instead.
///
/// [directory] answers the directory to write in; the application support
/// directory when it is null.
FileStore? createFileStore(Future<String> Function()? directory) =>
    platform.createFileStore(directory);

/// The file store in a directory the platform leaves out of device backups
/// and device-to-device transfers (Android's `noBackupFilesDir`, a directory
/// marked excluded from backup on iOS and macOS), or null where the platform
/// has none to offer: the web, Windows and Linux.
Future<FileStore?> createBackupFreeFileStore() =>
    platform.createBackupFreeFileStore();
