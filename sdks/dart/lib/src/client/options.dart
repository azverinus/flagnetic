import 'dart:math';

import 'store.dart';
import 'types.dart';

/// The version this package reports in `User-Agent`, the `sdk_version`
/// attribute and events.
const sdkVersion = '0.1.0'; // x-release-please-version

/// The SDK name in `User-Agent`, the `sdk` attribute and events.
const sdkName = 'flagnetic-dart';

/// The lowest `schema_version` of the documents this SDK reads. A document
/// outside [schemaVersionMin] to [schemaVersionMax] is a fallback chain
/// failure, logged once as
/// `schema_unsupported`. A change to the range or to [sdkApiPath] needs a
/// row in docs/sdks/compatibility.md.
const schemaVersionMin = 1;

/// The highest `schema_version` this SDK reads, asked for in
/// `Flagnetic-Schema-Version` by every evaluate and events request.
const schemaVersionMax = 1;

/// The SDK API path this SDK speaks. A new path is a new major version.
const sdkApiPath = '/sdk/v1';

/// A document to serve instead of the network: a values document, exactly
/// what remote evaluation returns. A document saved from the platform works
/// as is.
final class OfflineSource {
  /// A decoded document.
  OfflineSource.document(Map<String, Object?> document)
    : _read = (() => document);

  /// A source read by [read] at construction and on every reload: a JSON
  /// string or a decoded document, for example
  /// `OfflineSource.read(() => File(path).readAsStringSync())`.
  OfflineSource.read(Object? Function() read) : _read = read;

  final Object? Function() _read;

  /// Reads the source.
  Object? read() => _read();
}

/// Whether the code was compiled for the web, where the stream is opt-in.
const bool _isWeb = bool.fromEnvironment('dart.library.js_interop');

/// How a client behaves. Every duration is configurable so that a test can
/// run a whole scenario in milliseconds.
final class ClientOptions {
  /// Client options.
  const ClientOptions({
    this.store,
    this.storedUnitSets = 4,
    this.bootstrap,
    this.offline,
    this.logger,
    this.platform,
    this.appVersion,
    this.pollInterval = const Duration(minutes: 5),
    this.minimumPollInterval = const Duration(seconds: 30),
    this.minimumRefetchGap = const Duration(seconds: 10),
    this.requestTimeout = const Duration(seconds: 5),
    this.retryBackoff = const Duration(seconds: 2),
    this.retryBackoffCap = const Duration(minutes: 5),
    this.unauthorizedBackoffCap = const Duration(minutes: 15),
    this.closeTimeout = const Duration(seconds: 2),
    this.flushInterval = const Duration(seconds: 5),
    this.eventBatchSize = 100,
    this.eventQueueCapacity = 1000,
    this.exposureMemory = 1000,
    this.sendExposures = true,
    this.sendSummaries = true,
    this.allowOverrides = false,
    this.sse,
    this.rulesetEventJitter = const Duration(seconds: 3),
    this.random,
    this.clock,
    this.streamBackoff = const Duration(seconds: 1),
    this.streamBackoffCap = const Duration(seconds: 30),
    this.streamCloseJitter = const Duration(seconds: 2),
    this.streamSilenceTimeout = const Duration(seconds: 45),
    this.streamStableAfter = const Duration(seconds: 60),
  });

  /// Where last good values persist; an [InMemoryStore] when null.
  final FlagneticStore? store;

  /// How many unit sets keep their last good values, so the anonymous and
  /// the signed-in state each keep theirs. The least recently fetched set is
  /// removed beyond this.
  final int storedUnitSets;

  /// A values document to start from, served as `fresh`, for example one
  /// produced during server rendering. The client still fetches.
  final Map<String, Object?>? bootstrap;

  /// Serve this document and never touch the network: stage `fresh`, events
  /// recorded in memory (see `FlagneticClient.offline`).
  final OfflineSource? offline;

  /// Receives every log event; warn and above go to `dart:developer` when
  /// null.
  final Logger? logger;

  /// The `platform` attribute and the platform in `User-Agent`, such as
  /// `android`, `ios` or `web`. When null, `User-Agent` names the operating
  /// system and the context goes without the attribute; in a browser, which
  /// cannot send `User-Agent`, the attribute is `web`.
  final String? platform;

  /// The `app_version` attribute: the application's semantic version.
  final String? appVersion;

  /// The gap between fetches while they succeed. It runs only while the app
  /// is foregrounded (`FlagneticClient.background` stops it), and never
  /// falls below [minimumPollInterval].
  final Duration pollInterval;

  /// The floor [pollInterval] is clamped to, so a misconfigured interval cannot
  /// hammer the platform.
  final Duration minimumPollInterval;

  /// The shortest gap between a fetch and a foreground-triggered refetch.
  /// A foreground within this gap of the last attempt is dropped, so that
  /// switching between apps does not fetch on every return. `setContext`,
  /// `refresh` and a ruleset event are not throttled.
  final Duration minimumRefetchGap;

  /// How long a fetch may take before it is a failure.
  final Duration requestTimeout;

  /// The first retry bound after a failure; each further failure doubles it,
  /// and the retry waits a random time up to the bound (full jitter).
  final Duration retryBackoff;

  /// The largest retry bound.
  final Duration retryBackoffCap;

  /// The largest retry bound while the SDK key is refused (401 or 403).
  final Duration unauthorizedBackoffCap;

  /// How long `close` may take.
  final Duration closeTimeout;

  /// How often the persisted event queue is sent while the app is
  /// foregrounded. It also flushes when a batch has filled, when the app
  /// goes to the background, and on `flush` and `close`.
  final Duration flushInterval;

  /// The most events one ingest batch carries. The endpoint's own limit is
  /// 500 and bounds this.
  final int eventBatchSize;

  /// The most events the persisted queue holds. Beyond it the oldest are
  /// dropped and `queue_dropped` is logged, so a device offline for weeks
  /// keeps the most recent exposures rather than nothing at all.
  final int eventQueueCapacity;

  /// The most (experiment, unit type, unit ID, variation) assignments the
  /// in-process set suppressing repeated exposures holds. Beyond it the
  /// oldest assignment is forgotten; the platform's deduplication by event
  /// ID and its first exposure stay authoritative.
  final int exposureMemory;

  /// Whether exposures leave the SDK. Off, evaluation still counts in the
  /// evaluation summary and `exposure` records nothing; a warning is logged
  /// at construction.
  final bool sendExposures;

  /// Whether evaluation summaries leave the SDK. They carry no unit IDs,
  /// attribute values or flag values.
  final bool sendSummaries;

  /// Enables `FlagneticClient.overrides`: substitutions of flag values that
  /// never reach the platform. Off by default; the client logs
  /// `local_overrides_allowed` at construction when on, and still refuses an
  /// override while the held values name a protected environment.
  /// `flagnetic_flutter`'s Dev Tools helper layers a release-build
  /// refusal on top of this option.
  final bool allowOverrides;

  /// Whether to hold the ruleset stream while the app is in the foreground,
  /// so a kill switch reaches the app within about 5 s: the stream closes in
  /// the background, opens again on `foreground`, and a change waits up to
  /// [rulesetEventJitter] before the app evaluates.
  ///
  /// When null the stream is held on iOS, Android and desktop and is off on
  /// the web, where a connection per tab is the app's choice (the browser
  /// client behaves the same). Off, values stay fresh within [pollInterval]
  /// (five minutes by default) and at every `foreground`.
  final bool? sse;

  /// The longest random wait between a `ruleset` event and the evaluate it
  /// asks for, so that the apps open when a kill switch is flipped do not
  /// all evaluate at once. The wait is uniform from zero to this duration;
  /// zero evaluates at once.
  final Duration rulesetEventJitter;

  /// The source of the wait after a ruleset event and of the evaluate retry
  /// backoff; a [Random] when null. A test injects a fixed one to pin a wait.
  /// The stream's reconnect and the event flush draw their own.
  final Random? random;

  /// The time the minimum refetch gap is measured against; `DateTime.now`
  /// when null.
  final DateTime Function()? clock;

  /// [sse] resolved: the explicit choice, else the platform's default.
  bool get streamEnabled => sse ?? !_isWeb;

  /// The first reconnect bound after a stream error; each further failure
  /// doubles it up to [streamBackoffCap], and the attempt waits a random
  /// time up to the bound (full jitter).
  final Duration streamBackoff;

  /// The largest reconnect bound after a stream error.
  final Duration streamBackoffCap;

  /// The longest wait before reconnecting a stream the server closed or that
  /// fell silent; the wait is a random time up to it.
  final Duration streamCloseJitter;

  /// How long a stream may stay silent before it is reconnected: the server
  /// sends a heartbeat every 15 s.
  final Duration streamSilenceTimeout;

  /// How long a connection must stay up before the reconnect backoff starts
  /// from [streamBackoff] again.
  final Duration streamStableAfter;
}
