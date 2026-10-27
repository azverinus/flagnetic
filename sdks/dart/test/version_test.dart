@TestOn('vm')
// The VM only: this suite reads pubspec.yaml and the docs through `dart:io`.
library;

// The version in `User-Agent`, the `sdk_version` attribute and the summary
// envelope is `sdkVersion`, a constant, because a published package cannot
// read its own pubspec at run time. This keeps it equal to the version pub.dev
// publishes.
import 'dart:io';

import 'package:flagnetic/src/client/options.dart';
import 'package:flagnetic/src/client/values.dart';
import 'package:test/test.dart';

void main() {
  test('sdkVersion is the version in pubspec.yaml', () {
    final pubspec = File('pubspec.yaml').readAsLinesSync();
    final version = pubspec
        .firstWhere((line) => line.startsWith('version:'))
        .substring('version:'.length)
        .trim();
    expect(sdkVersion, version);
  });

  // The publishing workflow sets FLAGNETIC_RELEASE_VERSION to the version of
  // the tag it publishes, so a package whose version is not the tag's is
  // never published.
  final release = Platform.environment['FLAGNETIC_RELEASE_VERSION'];
  test(
    "sdkVersion is the release tag's version",
    () => expect(sdkVersion, release),
    skip: release == null ? 'not a release run' : false,
  );

  test('the schema range and the SDK API path are declared', () {
    expect(schemaVersionMin, 1);
    expect(schemaVersionMax, 1);
    expect(sdkApiPath, '/sdk/v1');
    expect(supportedSchemaVersions, [
      for (var v = schemaVersionMin; v <= schemaVersionMax; v++) v,
    ]);
  });

  // docs/sdks/compatibility.md promises, per SDK line, the schema range and
  // the SDK API path; its last Dart row is this build's line, so a change to
  // either needs a row there.
  test("the compatibility table's last Dart row is this build's schema range "
      'and SDK API path', () {
    final rows = File(
      '../../docs/sdks/compatibility.md',
    ).readAsLinesSync().where((line) => line.startsWith('| Dart |')).toList();
    expect(
      rows,
      isNotEmpty,
      reason: 'docs/sdks/compatibility.md has no Dart row',
    );
    final cells = rows.last.split('|').map((cell) => cell.trim()).toList();
    final schema = schemaVersionMin == schemaVersionMax
        ? '$schemaVersionMax'
        : '$schemaVersionMin–$schemaVersionMax';
    expect(cells[3], schema);
    expect(cells[4], '`$sdkApiPath`');
  });
}
