// The report the protocol runner writes for docs/sdks/behaviour-matrix.md:
// the names of the scenarios that passed, as a JSON array, in the file
// FLAGNETIC_SCENARIO_REPORT names. The VM run and the browser run (through
// its hybrid isolate) each add the scenarios they played, so one file read
// after both is the Dart column; name a file that does not exist yet.
import 'dart:convert';
import 'dart:io';

/// Adds [passed] to the report, keeping what an earlier run of the same
/// report wrote. Does nothing without FLAGNETIC_SCENARIO_REPORT.
void reportPassed(Iterable<String> passed) {
  final path = Platform.environment['FLAGNETIC_SCENARIO_REPORT'];
  if (path == null || path.isEmpty) return;
  final file = File(path);
  final names = <String>{
    if (file.existsSync())
      ...(jsonDecode(file.readAsStringSync()) as List<Object?>).cast<String>(),
    ...passed,
  };
  file.writeAsStringSync(jsonEncode(names.toList()));
}
