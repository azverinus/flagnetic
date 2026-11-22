import 'dart:math';

/// The knobs a test turns to run a whole scenario in milliseconds: retry and
/// stream timings, the jitter that spreads apps, the source of randomness
/// and the clock. An app never sets them, and they are not part of the
/// public API the package promises: they may change in any release.
///
/// Pass an instance as `ClientOptions.testing`.
final class TestingOptions {
  /// Testing options; every one defaults to the value an app runs with.
  const TestingOptions({
    this.minimumPollInterval = const Duration(seconds: 30),
    this.minimumRefetchGap = const Duration(seconds: 10),
    this.retryBackoff = const Duration(seconds: 2),
    this.retryBackoffCap = const Duration(minutes: 5),
    this.unauthorizedBackoffCap = const Duration(minutes: 15),
    this.closeTimeout = const Duration(seconds: 2),
    this.rulesetEventJitter = const Duration(seconds: 3),
    this.random,
    this.clock,
    this.streamBackoff = const Duration(seconds: 1),
    this.streamBackoffCap = const Duration(seconds: 30),
    this.streamCloseJitter = const Duration(seconds: 2),
    this.streamSilenceTimeout = const Duration(seconds: 45),
  });

  /// The floor `ClientOptions.pollInterval` is clamped to, so a misconfigured interval cannot
  /// hammer the platform.
  final Duration minimumPollInterval;

  /// The shortest gap between a fetch and a foreground-triggered refetch.
  /// A foreground within this gap of the last attempt is dropped, so that
  /// switching between apps does not fetch on every return. `setContext`,
  /// `refresh` and a ruleset event are not throttled.
  final Duration minimumRefetchGap;

  /// The first retry bound after a failure; each further failure doubles it,
  /// and the retry waits a random time up to the bound (full jitter).
  final Duration retryBackoff;

  /// The largest retry bound.
  final Duration retryBackoffCap;

  /// The largest retry bound while the SDK key is refused (401 or 403).
  final Duration unauthorizedBackoffCap;

  /// How long `close` may take.
  final Duration closeTimeout;

  /// The longest random wait between a `ruleset` event and the evaluate it
  /// asks for, so that the apps open when a kill switch is flipped do not
  /// all evaluate at once. The wait is uniform from zero to this duration;
  /// zero evaluates at once.
  final Duration rulesetEventJitter;

  /// The source of the wait after a ruleset event, of the evaluate retry
  /// backoff and of the stream's reconnect waits; a [Random] when null. A
  /// test injects a fixed one to pin a wait. The event flush draws its own.
  final Random? random;

  /// The time the minimum refetch gap is measured against; `DateTime.now`
  /// when null.
  final DateTime Function()? clock;

  /// The first reconnect bound after a stream error; each further failure
  /// doubles it up to [streamBackoffCap], and the attempt waits a random
  /// time up to the bound (full jitter).
  final Duration streamBackoff;

  /// The largest reconnect bound after a stream error.
  final Duration streamBackoffCap;

  /// The longest wait before reconnecting a stable stream the server closed
  /// or one that fell silent; the wait is a random time up to it. A stream
  /// the server closes before its first heartbeat backs off instead.
  final Duration streamCloseJitter;

  /// How long a stream may stay silent before it is reconnected: the server
  /// sends a heartbeat every 15 s.
  final Duration streamSilenceTimeout;
}
