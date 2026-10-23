/// Who a flag is evaluated for: [units] identify the subjects experiments
/// bucket by, [attributes] feed targeting.
///
/// A unit ID names a unit type (`user`, `anonymous`, `device`); keep the
/// anonymous unit beside the user unit after sign-in so that an experiment
/// started before sign-up keeps its assignment. An attribute value is a
/// string, number, boolean, a list of those, or a date as an RFC 3339 string.
final class FlagneticContext {
  /// A context of [units] and [attributes].
  const FlagneticContext({this.units = const {}, this.attributes = const {}});

  /// Unit IDs by unit type.
  final Map<String, String> units;

  /// Attribute values by attribute key.
  final Map<String, Object?> attributes;
}
