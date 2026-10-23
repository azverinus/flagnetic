/// The step of the fallback chain a client serves from.
enum Stage {
  /// No values were ever received: getters serve built-in defaults.
  builtIn('built_in'),

  /// The held values came from an earlier success, in this process or from
  /// the store, and the most recent attempt failed or none was made yet.
  lastGood('last_good'),

  /// The most recent attempt succeeded. Bootstrap and offline values are
  /// fresh.
  fresh('fresh');

  const Stage(this.wireName);

  /// The name in logs and on the wire.
  final String wireName;
}

/// The stage of a client and how long its values have been stale.
final class Status {
  /// A status.
  const Status({required this.stage, this.staleSince});

  /// The step of the fallback chain the client serves from.
  final Stage stage;

  /// When the first fetch failed since the last success, or null after a
  /// success and before any attempt failed.
  final DateTime? staleSince;
}

/// Evaluation reasons by wire name. A client key learns only that targeting
/// decided a value, never which rule, rollout or prerequisite did, so the
/// server answers it [targetingMatch] where it would answer a server key
/// with a finer reason. The SDK produces [localOverride] and [fallback]. A
/// reason the SDK does not know is passed through as it arrived.
abstract final class Reasons {
  /// Targeting decided the value: a rule, a rollout, the fallthrough or a
  /// failed prerequisite.
  static const targetingMatch = 'targeting_match';

  /// An experiment assigned the variation.
  static const experiment = 'experiment';

  /// A platform override decided the value.
  static const override = 'override';

  /// The server's evaluator failed; a getter serves [fallback] instead.
  static const error = 'error';

  /// The flag is off.
  static const off = 'off';

  /// A local override substituted the value.
  static const localOverride = 'local_override';

  /// The built-in default was served.
  static const fallback = 'fallback';
}

/// Why a [Detail] has reason `fallback`, by wire name. The error codes of the
/// server's evaluator (`inconsistent_ruleset`, `prerequisite_cycle`, …) are
/// passed through as they arrived.
abstract final class FallbackCodes {
  /// The values hold no flag with the key.
  static const flagNotFound = 'flag_not_found';

  /// The flag's type differs from the getter's.
  static const typeMismatch = 'type_mismatch';

  /// Stage `built_in`: no values to serve from.
  static const noValues = 'no_values';

  /// The served value does not fit the flag's type.
  static const inconsistentRuleset = 'inconsistent_ruleset';

  /// An internal failure was caught at the API boundary.
  static const general = 'general';
}

/// The experiment block of a served value: everything an exposure reports,
/// so an analyst can recompute both buckets.
final class Experiment {
  /// An experiment block.
  const Experiment({
    required this.key,
    required this.variation,
    required this.unitType,
    required this.unitId,
    required this.inclusionSalt,
    required this.variationSalt,
    required this.inclusionBucket,
    required this.variationBucket,
    required this.override,
  });

  /// The experiment key.
  final String key;

  /// The variation assigned.
  final String variation;

  /// The unit type the experiment hashes on.
  final String unitType;

  /// The unit ID hashed.
  final String unitId;

  /// The public salt of the inclusion bucket.
  final String inclusionSalt;

  /// The public salt of the variation bucket.
  final String variationSalt;

  /// The unit's inclusion bucket.
  final int inclusionBucket;

  /// The unit's variation bucket.
  final int variationBucket;

  /// True when a platform override decided the value, so an exposure is
  /// marked and excluded from analysis.
  final bool override;
}

/// A served value and why it was served.
final class Detail {
  /// A detail.
  const Detail({
    required this.value,
    required this.reason,
    required this.stage,
    this.variation,
    this.errorCode,
    this.rulesetVersion,
    this.receivedAt,
    this.overridden = false,
    this.experiment,
  });

  /// The served value: `bool`, `String`, `num` or a decoded JSON value by
  /// flag type, or the built-in default as passed with reason `fallback`.
  final Object? value;

  /// The evaluation reason's wire name (see [Reasons]).
  final String reason;

  /// The variation served; null with reason `fallback`.
  final String? variation;

  /// Why the built-in default was served; set with reason `fallback` (see
  /// [FallbackCodes]).
  final String? errorCode;

  /// The ruleset version the held values were evaluated against; null at
  /// stage `built_in`.
  final int? rulesetVersion;

  /// When the held values were received; null at stage `built_in`. The age
  /// of last good values is observable here: they never expire.
  final DateTime? receivedAt;

  /// The stage of the fallback chain when the value was served.
  final Stage stage;

  /// True when a platform override decided the value, directly or through a
  /// prerequisite. A local override is reason `local_override` instead.
  final bool overridden;

  /// Present when the value came from a running experiment, including an
  /// override landing in one.
  final Experiment? experiment;
}

/// One flag's current state, for a Dev Tools screen listing every flag: not
/// an evaluation of it, so reading `FlagneticClient.allFlags`
/// records nothing in the evaluation summary. Empty at stage `built_in`.
final class FlagSummary {
  /// A flag summary.
  const FlagSummary({
    required this.key,
    required this.type,
    required this.value,
    required this.reason,
    this.overridden = false,
  });

  /// The flag key.
  final String key;

  /// One of `boolean`, `string`, `number` or `json`.
  final String type;

  /// The value the held values document carries for this flag, or the
  /// current local override's value when [overridden] is true.
  final Object? value;

  /// The evaluation reason the held values document carries, or
  /// `local_override` when [overridden] is true.
  final String reason;

  /// True when a local override is currently serving this flag. Unlike
  /// [Detail.overridden], which marks a platform override, this one is
  /// always the SDK-side substitution: the two never overlap, since a local
  /// override pre-empts the flag's real value.
  final bool overridden;
}

/// The severity of a log event.
enum LogLevel {
  /// Diagnostics.
  debug,

  /// Normal operation worth recording.
  info,

  /// Degraded but serving.
  warn,

  /// Needs attention: a revoked key, a caught internal failure.
  error,
}

/// Receives every log event: its level, a stable name from [LogEvents] and
/// structured fields. Called synchronously; an exception it throws is
/// swallowed.
typedef Logger =
    void Function(LogLevel level, String event, Map<String, Object?> fields);

/// Stable log event names: alert on them.
abstract final class LogEvents {
  /// (info) Values were accepted. Fields: source (`network`, `store`,
  /// `bootstrap`, `offline`), ruleset_version, flags.
  static const valuesLoaded = 'values_loaded';

  /// (warn) A fetch failed; the held values keep serving.
  /// Fields: cause (`timeout`, `network`, `status`, `parse`, `schema`,
  /// `version`), and status, code or detail where known.
  static const fetchFailed = 'fetch_failed';

  /// (info) The stage changed. Fields: from, to.
  static const stageChanged = 'stage_changed';

  /// (error) The SDK key was refused with 401 or 403; logged once until a
  /// fetch succeeds again. Fields: status, code.
  static const keyRevoked = 'key_revoked';

  /// (error) A document's `schema_version` is outside the supported range;
  /// logged once. Fields: schema_version, supported.
  static const schemaUnsupported = 'schema_unsupported';

  /// (warn) The server ignored a context attribute; logged once per name.
  /// Fields: name, code (`undeclared`, `type_mismatch`, …).
  static const attributeIgnored = 'attribute_ignored';

  /// (warn) A getter asked for a type other than the flag's; logged once per
  /// flag key. Fields: flag, flag_type, requested_type.
  static const typeMismatch = 'type_mismatch';

  /// (warn) The store failed or held an unreadable entry; the client carries
  /// on without it. Fields: operation, key, cause.
  static const storeFailed = 'store_failed';

  /// (error) An internal failure was caught at the API boundary. Fields:
  /// operation, cause.
  static const internalError = 'internal_error';

  /// (warn) Events left the queue without reaching the platform. Fields:
  /// cause (`queue_full`, `client_error`, `refused_by_the_platform`,
  /// `window_too_old`, `not_configured`), events, and capacity, status or
  /// codes where known.
  static const queueDropped = 'queue_dropped';

  /// (warn) An ingest batch failed and goes again, with the same event IDs,
  /// after the delay in the fields. Fields: cause, status where known,
  /// events, retry_in_ms.
  static const eventsFailed = 'events_failed';

  /// (debug) An exposure call recorded nothing. Fields: flag, cause:
  /// `no_values` (stage `built_in`: nothing is held), `no_experiment` (no
  /// evaluation of this flag for this context carried an experiment block),
  /// `local_override` (the flag is currently overridden) or `exposures_off`.
  static const exposureSkipped = 'exposure_skipped';

  /// (warn) Exposures are switched off; logged once at construction.
  static const exposuresOff = 'exposures_off';

  /// (warn) `ClientOptions.allowOverrides` is on; logged once at
  /// construction.
  static const overridesAllowed = 'local_overrides_allowed';

  /// (warn) A local override took effect. Fields: flag.
  static const overrideSet = 'override_set';

  /// (warn) A local override was refused. Fields: flag (absent for
  /// `not_allowed`), cause (`not_allowed` without
  /// `ClientOptions.allowOverrides`, `protected_environment`, or, from
  /// `flagnetic_flutter`'s Dev Tools helper, `release_build`).
  static const overrideRefused = 'override_refused';

  /// (info) The ruleset stream connected. Fields: last_event_id.
  static const sseConnected = 'sse_connected';

  /// (warn) The ruleset stream ended or failed; it reconnects after the
  /// delay in the fields. Fields: cause (`closed`, `silent`, `status`,
  /// `network`), status where known, retry_in_ms.
  static const sseDropped = 'sse_dropped';

  /// (error) A change or stage listener threw. Fields: cause.
  static const listenerFailed = 'listener_failed';
}
