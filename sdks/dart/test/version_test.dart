@TestOn('vm')
// The VM only: this suite reads pubspec.yaml through `dart:io`.
library;

// The version in `User-Agent`, the `sdk_version` attribute and the summary
// envelope is `sdkVersion`, a constant, because a published package cannot
// read its own pubspec at run time. This keeps it equal to the version pub.dev
// publishes .
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
  // The schema range this SDK reads
  // and the SDK API path it speaks, which docs/sdks/compatibility.md lists per
  // SDK line and .github/scripts/compatibility.py reads from options.dart.
  test('the schema range and the SDK API path are declared', () {
    expect(schemaVersionMin, 1);
    expect(schemaVersionMax, 1);
    expect(sdkApiPath, '/sdk/v1');
    expect(supportedSchemaVersions, [
      for (var v = schemaVersionMin; v <= schemaVersionMax; v++) v,
    ]);
  });
}
