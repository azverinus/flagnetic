import 'options.dart';
import 'types.dart';

/// The `schema_version`s this SDK understands: every one from
/// [schemaVersionMin] to [schemaVersionMax]. A const list for callers in const
/// contexts, so spelled out; `test/version_test.dart` keeps it equal to the
/// range.
const supportedSchemaVersions = [schemaVersionMin];

/// The flag types this SDK serves. A flag of any other type is absent.
const flagTypes = {'boolean', 'string', 'number', 'json'};

/// The flag type a getter asks for with [value] as its default: `bool` is
/// boolean, `String` is string, `num` is number, anything else is JSON.
String flagTypeOf(Object? value) => switch (value) {
  bool() => 'boolean',
  String() => 'string',
  num() => 'number',
  _ => 'json',
};

/// True when [value] can be served by a flag of [type].
bool fitsFlagType(String type, Object? value) => switch (type) {
  'boolean' => value is bool,
  'string' => value is String,
  'number' => value is num,
  _ => true,
};

/// True when [value] is a number with no fractional part that an integer
/// holds exactly on every platform, that is within ±2^53.
///
/// The bound is checked for an `int` too: compiled for the web every whole
/// number is an `int`, `1e300` included, and on the VM an integer beyond it
/// would serve a value the web could not.
bool isWholeNumber(Object? value) =>
    value is num &&
    value.isFinite &&
    value.abs() <= 9007199254740991 &&
    value == value.truncateToDouble();

/// Why a document was refused.
enum RefusalCause {
  /// Not a values document: bad JSON, a missing field, a wrong type, or the
  /// ruleset endpoint's document.
  parse('parse'),

  /// A `schema_version` outside [supportedSchemaVersions].
  schema('schema'),

  /// A `ruleset_version` below the one held.
  version('version');

  const RefusalCause(this.wireName);

  /// The name in the `fetch_failed` log event.
  final String wireName;
}

/// A document the fallback chain refuses.
final class Refused implements Exception {
  /// A refusal for [cause] with a human-readable [detail].
  const Refused(this.cause, this.detail, {this.schemaVersion});

  /// Why it was refused.
  final RefusalCause cause;

  /// What was wrong.
  final String detail;

  /// The refused `schema_version`, for [RefusalCause.schema].
  final Object? schemaVersion;

  @override
  String toString() => 'Refused(${cause.wireName}): $detail';
}

/// One flag of a values document as the server evaluated it.
final class ServedFlag {
  /// A served flag.
  const ServedFlag({
    required this.type,
    required this.reason,
    required this.hasValue,
    this.value,
    this.variation,
    this.errorCode,
    this.overrideInfluenced = false,
    this.automaticExposure = false,
    this.experiment,
  });

  /// One of [flagTypes].
  final String type;

  /// The evaluation reason, passed through even when unknown.
  final String reason;

  /// Whether the document carried a `value` at all.
  final bool hasValue;

  /// The served value as decoded JSON.
  final Object? value;

  /// The variation served.
  final String? variation;

  /// The evaluator's error code with reason `error`.
  final String? errorCode;

  /// True when a platform override decided the value.
  final bool overrideInfluenced;

  /// True when the flag opted into automatic exposure: every evaluation
  /// whose result carries an experiment block records an exposure, instead
  /// of waiting for the explicit call.
  final bool automaticExposure;

  /// The experiment block, when present and well formed.
  final Experiment? experiment;
}

/// An attribute the server ignored.
typedef IgnoredAttribute = ({String name, String code});

/// A remote evaluation response: every flag's value for one context.
final class ValuesDocument {
  ValuesDocument._({
    required this.raw,
    required this.rulesetVersion,
    required this.flags,
    required this.ignoredAttributes,
    required this.protectedEnvironment,
  });

  /// Reads a decoded evaluate response, refusing anything the fallback chain
  /// must not accept. Unknown fields are
  /// ignored and a flag of an unknown type is absent.
  factory ValuesDocument.parse(Object? json) {
    if (json is! Map<String, Object?>) {
      throw const Refused(RefusalCause.parse, 'not a JSON object');
    }
    final schema = json['schema_version'];
    if (schema == null) {
      throw const Refused(RefusalCause.parse, 'schema_version missing');
    }
    if (schema is! int || !supportedSchemaVersions.contains(schema)) {
      throw Refused(
        RefusalCause.schema,
        'schema_version $schema unsupported',
        schemaVersion: schema,
      );
    }
    final version = json['ruleset_version'];
    if (version is! int) {
      throw Refused(
        RefusalCause.parse,
        json.containsKey('version')
            ? 'a ruleset document, not a values document'
            : 'ruleset_version missing',
      );
    }
    final flagsJson = json['flags'];
    if (flagsJson is! Map<String, Object?>) {
      throw const Refused(RefusalCause.parse, 'flags is not an object');
    }
    final flags = <String, ServedFlag>{};
    for (final MapEntry(:key, :value) in flagsJson.entries) {
      if (value is! Map<String, Object?>) {
        throw Refused(RefusalCause.parse, 'flag $key is not an object');
      }
      final flag = _flag(value);
      if (flag != null) flags[key] = flag;
    }
    return ValuesDocument._(
      raw: json,
      rulesetVersion: version,
      flags: flags,
      ignoredAttributes: [
        if (json['ignored_attributes'] case final List<Object?> list)
          for (final item in list)
            if (item case {
              'name': final String name,
              'code': final String code,
            })
              (name: name, code: code),
      ],
      protectedEnvironment: switch (json['environment']) {
        {'protected': true} => true,
        _ => false,
      },
    );
  }

  /// The document as decoded, persisted as it arrived.
  final Map<String, Object?> raw;

  /// The ruleset version the server evaluated against.
  final int rulesetVersion;

  /// The flags of a known type, by key.
  final Map<String, ServedFlag> flags;

  /// The attributes the server ignored.
  final List<IgnoredAttribute> ignoredAttributes;

  /// Whether the server named this environment protected: local overrides
  /// are refused there. A document with no `environment` field
  /// (an offline document written by hand) is treated as unprotected.
  final bool protectedEnvironment;

  /// The keys whose served value differs between [before] and [after],
  /// added and removed flags included, sorted.
  static List<String> changedKeys(
    ValuesDocument? before,
    ValuesDocument after,
  ) {
    final old = before?.flags ?? const <String, ServedFlag>{};
    final now = after.flags;
    bool same(ServedFlag? a, ServedFlag? b) =>
        a != null &&
        b != null &&
        a.type == b.type &&
        a.hasValue == b.hasValue &&
        jsonEquals(a.value, b.value);
    final keys = {
      for (final key in {...old.keys, ...now.keys})
        if (!same(old[key], now[key])) key,
    };
    return keys.toList()..sort();
  }
}

ServedFlag? _flag(Map<String, Object?> json) {
  final type = json['type'];
  if (type is! String || !flagTypes.contains(type)) return null;
  final reason = json['reason'];
  return ServedFlag(
    type: type,
    reason: reason is String ? reason : Reasons.error,
    hasValue: json.containsKey('value'),
    value: json['value'],
    variation: _string(json['variation']),
    errorCode: _string(json['error_code']),
    overrideInfluenced: json['override_influenced'] == true,
    automaticExposure: json['automatic_exposure'] == true,
    experiment: _experiment(json['experiment']),
  );
}

String? _string(Object? v) => v is String ? v : null;

Experiment? _experiment(Object? json) => switch (json) {
  {
    'key': final String key,
    'variation': final String variation,
    'unit_type': final String unitType,
    'unit_id': final String unitId,
    'inclusion_salt': final String inclusionSalt,
    'variation_salt': final String variationSalt,
    'inclusion_bucket': final int inclusionBucket,
    'variation_bucket': final int variationBucket,
  } =>
    Experiment(
      key: key,
      variation: variation,
      unitType: unitType,
      unitId: unitId,
      inclusionSalt: inclusionSalt,
      variationSalt: variationSalt,
      inclusionBucket: inclusionBucket,
      variationBucket: variationBucket,
      override: (json as Map<String, Object?>)['override'] == true,
    ),
  _ => null,
};

/// A deep copy of a decoded JSON value, so a caller may keep and mutate
/// what a getter served.
Object? copyJson(Object? json) => switch (json) {
  final List<Object?> list => [for (final v in list) copyJson(v)],
  final Map<String, Object?> map => {
    for (final MapEntry(:key, :value) in map.entries) key: copyJson(value),
  },
  _ => json,
};

/// Deep equality of decoded JSON values. Numbers compare by value, so `1`
/// equals `1.0`.
bool jsonEquals(Object? a, Object? b) {
  if (a is num && b is num) return a == b;
  if (a is List<Object?> && b is List<Object?>) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!jsonEquals(a[i], b[i])) return false;
    }
    return true;
  }
  if (a is Map<String, Object?> && b is Map<String, Object?>) {
    if (a.length != b.length) return false;
    for (final MapEntry(:key, :value) in a.entries) {
      if (!b.containsKey(key) || !jsonEquals(value, b[key])) return false;
    }
    return true;
  }
  return a == b;
}
