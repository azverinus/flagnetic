import 'files.dart';

/// No file system here: the store keeps everything in the preferences.
FileStore? createFileStore(Future<String> Function()? directory) => null;
