import 'files.dart';

/// No file system here: the store keeps everything in the preferences.
FileStore? createFileStore(Future<String> Function()? directory) => null;

/// No file system here, so no directory to keep out of backups.
Future<FileStore?> createBackupFreeFileStore() async => null;
