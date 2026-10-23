import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'backoff.dart';
import 'context.dart';
import 'events.dart';
import 'ingest.dart';
import 'log.dart';
import 'options.dart';
import 'overrides.dart';
import 'store.dart';
import 'stream.dart';
import 'transport/transport.dart';
import 'types.dart';
import 'values.dart';

/// The entry point of the Flagnetic SDK: [client] builds a client-mode
/// client.
abstract final class Flagnetic {
  /// A client-mode client for a Flutter app or any other client: it posts
  /// [context] to remote evaluation at [baseUrl] with the client key
  /// [sdkKey] and serves every flag's value from memory.
  ///
  /// Values serve synchronously from the moment this returns: bootstrap
  /// values as `fresh`, else the last good values the store holds for the
  /// context's unit set as `last_good`, else built-in defaults. A store that
  /// answers with a future loads before the first request is sent.
  ///
  /// Throws [ArgumentError] for a server key (`fn_srv_…`), the one mistake
  /// the SDK refuses loudly: a server key must never ship in an app. Nothing
  /// else this client does throws.
  static FlagneticClient client(
    String sdkKey, {
    required FlagneticContext context,
    Uri? baseUrl,
    ClientOptions options = const ClientOptions(),
  }) {
    if (sdkKey.startsWith('fn_srv_')) {
      throw ArgumentError(
        'a server key cannot build a client-mode client; use a client key '
            '(fn_cli_…)',
        'sdkKey',
      );
    }
    return FlagneticClient._(sdkKey, baseUrl, context, options);
  }
}

/// A listener of changed flag keys.
typedef ChangeListener = void Function(List<String> changedKeys);

/// A listener of stage transitions.
typedef StageListener = void Function(Stage stage);

enum _Outcome { success, failure, refused, noRetry }

/// What asked for an evaluate. All of them coalesce into one in-flight
/// request; only [foreground] is throttled by the minimum refetch gap.
enum _Trigger {
  /// Construction, or the ruleset stream having (re)connected.
  start,

  /// `setContext`: the held values answer the previous context.
  context,

  /// `refresh`.
  refresh,

  /// The app came back to the foreground, or the network came back.
  foreground,

  /// A `ruleset` event above the held version.
  ruleset;

  /// Whether the application asked for this itself, so that it runs even
  /// while the application is in the background.
  bool get isCall => this == context || this == refresh;
}

/// A client-mode client: values from remote evaluation, cached on device as
/// last good, served through the fallback chain. Every public member is
/// total: an internal failure is logged and never escapes.
final class FlagneticClient {
  FlagneticClient._(this._sdkKey, this._baseUrl, this._context, this._options)
    : _store = _options.store ?? InMemoryStore(),
      _keys = StoreKeys(_sdkKey) {
    try {
      _start();
    } on Object catch (e) {
      _log(LogLevel.error, LogEvents.internalError, {
        'operation': 'construct',
        'cause': '$e',
      });
    }
  }

  final String _sdkKey;
  final Uri? _baseUrl;
  FlagneticContext _context;
  final ClientOptions _options;
  final FlagneticStore _store;

  /// The keys this connection writes: its own values and its own event queue.
  final StoreKeys _keys;
  final Random _jitter = Random();
  Transport? _transport;

  ValuesDocument? _values;
  DateTime? _receivedAt;
  Stage _stage = Stage.builtIn;
  DateTime? _staleSince;

  Future<void> _storeLoaded = Future.value();
  Future<void> _writes = Future.value();
  Timer? _timer;
  bool _inFlight = false;
  bool _closed = false;
  bool _foreground = true;
  bool _again = false;
  bool _fetchedOnce = false;
  int _generation = 0;
  DateTime? _lastAttempt;
  List<Completer<void>> _waiters = [];
  RulesetStream? _rulesetStream;
  int _failures = 0;
  bool _revokedLogged = false;
  bool _schemaLogged = false;
  final Set<String> _mismatched = {};
  final Set<String> _ignoredLogged = {};
  final Completer<void> _firstValues = Completer<void>();
  final List<ChangeListener> _changeListeners = [];
  final List<StageListener> _stageListeners = [];
  final SummaryCounter _summary = SummaryCounter();
  // The most recent detail of every flag whose evaluation carried an
  // experiment block, which is what an exposure call reports. A flag that
  // stops carrying one is forgotten, so the set is bounded by the running
  // experiments of the values held, and the call sends nothing rather than
  // the assignment of an experiment the platform no longer serves.
  final Map<String, Detail> _details = {};
  late final ExposureSuppressor _exposed = ExposureSuppressor(
    _options.exposureMemory,
  );
  Ingest? _ingest;
  OfflineMode? _offline;
  Map<String, LocalOverride> _overrides = {};

  void _start() {
    if (!_options.sendExposures) {
      _log(LogLevel.warn, LogEvents.exposuresOff, const {});
    }
    if (_options.allowOverrides) {
      _log(LogLevel.warn, LogEvents.overridesAllowed, const {});
    }
    final offline = _options.offline;
    if (offline != null) {
      _offline = OfflineMode._(this, offline);
      _offline!._load();
      _firstValues.complete();
      return;
    }
    final bootstrap = _options.bootstrap;
    if (bootstrap != null && _accept(bootstrap, 'bootstrap')) {
      _firstValues.complete();
    } else {
      _loadStored();
    }
    if (_baseUrl == null) {
      _failed({'source': 'network', 'cause': 'network', 'detail': 'no URL'});
      return;
    }
    _transport = defaultTransport();
    _ingest = Ingest(
      url: _sdkUrl(_baseUrl, 'events'),
      sdkKey: _sdkKey,
      settings: (
        platform: _options.platform,
        appVersion: _options.appVersion,
        requestTimeout: _options.requestTimeout,
        flushInterval: _options.flushInterval,
        eventBatchSize: _options.eventBatchSize,
        eventQueueCapacity: _options.eventQueueCapacity,
        retryBackoff: _options.retryBackoff,
        retryBackoffCap: _options.retryBackoffCap,
        unauthorizedBackoffCap: _options.unauthorizedBackoffCap,
        sendSummaries: _options.sendSummaries,
      ),
      store: _store,
      keys: _keys,
      transport: _transport!,
      summary: _summary,
      rulesetVersion: () => _values?.rulesetVersion,
      keyRefused: _keyRefused,
      log: _log,
    );
    // What an earlier run recorded while offline is read before anything is
    // sent, so it goes out once, with the event IDs it was recorded under.
    unawaited(_ingest!.load());
    _ingest!.start();
    if (!_options.sse) {
      _trigger(_Trigger.start);
      return;
    }
    // The stream opens first and the first evaluate follows it, so that no
    // version is missed between the two.
    // A stream that cannot connect must not hold the values back: the first
    // failed attempt starts the fetch loop, which then polls.
    _rulesetStream = RulesetStream(
      url: _sdkUrl(_baseUrl, 'stream'),
      sdkKey: _sdkKey,
      settings: (
        platform: _options.platform,
        requestTimeout: _options.requestTimeout,
        backoff: _options.streamBackoff,
        backoffCap: _options.streamBackoffCap,
        closeJitter: _options.streamCloseJitter,
        silenceTimeout: _options.streamSilenceTimeout,
        stableAfter: _options.streamStableAfter,
      ),
      transport: _transport!,
      lastEventId: () => _values?.rulesetVersion.toString(),
      onRuleset: (version) {
        final held = _values?.rulesetVersion;
        if (held == null || version > held) _trigger(_Trigger.ruleset);
      },
      onConnected: () => _trigger(_Trigger.start),
      onFailed: () {
        if (!_fetchedOnce) _trigger(_Trigger.start);
      },
      onRefused: (status) {
        _keyRefused(status);
        // The stream is done for good; the fetch loop must still start, so
        // that a key restored by an operator recovers without a restart.
        if (!_fetchedOnce) _trigger(_Trigger.start);
      },
      log: _log,
    )..start();
  }

  /// The stage of the fallback chain.
  Stage get stage => _stage;

  /// The stage and when the held values became stale: the first failure
  /// since the last success, or null while no attempt has failed since.
  Status status() => Status(stage: _stage, staleSince: _staleSince);

  /// The offline test helpers, when constructed with
  /// [ClientOptions.offline]; null otherwise.
  OfflineMode? get offline => _offline;

  /// The SDK key this client was constructed with. Not a secret: a client
  /// key ships in application bundles.
  String get sdkKey => _sdkKey;

  /// The local overrides of this client: substitutions of flag values that
  /// never reach the platform.
  ///
  /// Throws [StateError], and logs `override_refused`, unless the client was
  /// built with [ClientOptions.allowOverrides]: an override that silently did
  /// nothing would hide a misconfigured test.
  LocalOverrides get overrides {
    if (!_options.allowOverrides) {
      _log(LogLevel.warn, LogEvents.overrideRefused, const {
        'cause': 'not_allowed',
      });
      throw StateError(
        'local overrides are not enabled; build the client with '
        'ClientOptions(allowOverrides: true)',
      );
    }
    return LocalOverrides.of(_ClientOverrides(this));
  }

  /// Every flag the held values carry, local overrides applied, for a Dev
  /// Tools screen: reading this is not an evaluation, so it records nothing
  /// in the evaluation summary. Ordered by key; empty at stage `built_in`.
  List<FlagSummary> get allFlags {
    final values = _values;
    final keys = {...?values?.flags.keys, ..._overrides.keys}.toList()..sort();
    final result = <FlagSummary>[];
    for (final key in keys) {
      final override = _currentOverride(key);
      if (override != null) {
        result.add(
          FlagSummary(
            key: key,
            type: override.type,
            value: override.value,
            reason: Reasons.localOverride,
            overridden: true,
          ),
        );
        continue;
      }
      final flag = values?.flags[key];
      if (flag == null) continue;
      result.add(
        FlagSummary(
          key: key,
          type: flag.type,
          value: flag.hasValue ? flag.value : null,
          reason: flag.reason,
        ),
      );
    }
    return result;
  }

  /// The context every evaluate posts: the one given at construction until
  /// [setContext] replaces it.
  FlagneticContext get context => _context;

  /// Whether the app is foregrounded. Fetching runs only while it is
  ///: see [foreground] and [background].
  bool get isForeground => _foreground;

  /// Evaluates for [context] from now on and completes when the answer has
  /// been served.
  ///
  /// The values held keep serving until then, at stage `last_good`: they
  /// answer the previous context, so signing in causes no flash of built-in
  /// defaults. When the answer arrives the stage is `fresh` again and the
  /// change listeners receive the keys whose value differs. Keep the
  /// anonymous unit in [FlagneticContext.units] alongside the user unit so that a
  /// cross-sign-up experiment keeps its assignment.
  ///
  /// The future completes when the attempt for this context finishes,
  /// whether it succeeded or failed, so a caller awaiting it never hangs.
  Future<void> setContext(FlagneticContext context) {
    try {
      if (_closed) return Future.value();
      _context = context;
      _generation++;
      // The details held answer the previous context; an exposure reports
      // the detail of the context the flag was evaluated for.
      _details.clear();
      if (_values != null) {
        // The held values answer the previous context, so they are stale
        // from here: last good, not fresh. No attempt failed, so this is
        // not a `fetch_failed`.
        _staleSince ??= DateTime.now().toUtc();
        _setStage(Stage.lastGood);
      }
      return _refetch(_Trigger.context);
    } on Object catch (e) {
      _internal('set_context', e);
      return Future.value();
    }
  }

  /// Evaluates again now, completing when the attempt finishes. Never
  /// throttled: it is the app asking.
  Future<void> refresh() {
    try {
      return _refetch(_Trigger.refresh);
    } on Object catch (e) {
      _internal('refresh', e);
      return Future.value();
    }
  }

  /// The app came back to the foreground, or the network came back: fetch,
  /// unless the last attempt is less than [ClientOptions.minimumRefetchGap]
  /// old, and run the interval again.
  ///
  /// `flagnetic_flutter` calls this from a `WidgetsBindingObserver`; a plain
  /// Dart application calls it from whatever tells it the same.
  void foreground() {
    try {
      if (_closed) return;
      _foreground = true;
      _rulesetStream?.start();
      _ingest?.setForeground(foreground: true);
      if (_withinRefetchGap) {
        _scheduleInterval();
        return;
      }
      _trigger(_Trigger.foreground);
    } on Object catch (e) {
      _internal('foreground', e);
    }
  }

  /// The app went to the background: nothing is fetched until [foreground].
  /// Values keep serving and queued events flush best effort.
  void background() {
    try {
      if (_closed) return;
      _foreground = false;
      _timer?.cancel();
      _timer = null;
      // The stream is radio too: it closes with everything else.
      unawaited(_rulesetStream?.suspend() ?? Future<void>.value());
      // Best effort: what does not get out stays in the store for the next
      // foreground or the next run.
      _ingest?.setForeground(foreground: false);
    } on Object catch (e) {
      _internal('background', e);
    }
  }

  /// Completes when the first values arrive or after [timeout], whichever is
  /// first, with the stage reached. Never fails. Evaluation before ready is
  /// legal and follows the fallback chain.
  Future<Stage> ready([Duration timeout = const Duration(seconds: 5)]) {
    if (_firstValues.isCompleted || _closed) return Future.value(_stage);
    final done = Completer<Stage>();
    final timer = Timer(timeout, () {
      if (!done.isCompleted) done.complete(_stage);
    });
    unawaited(
      _firstValues.future.then((_) {
        timer.cancel();
        if (!done.isCompleted) done.complete(_stage);
      }),
    );
    return done.future;
  }

  /// The boolean flag [key], or [defaultValue].
  bool getBool(String key, bool defaultValue) =>
      _serve(key, 'boolean', defaultValue).value as bool? ?? defaultValue;

  /// The string flag [key], or [defaultValue].
  String getString(String key, String defaultValue) =>
      _serve(key, 'string', defaultValue).value as String? ?? defaultValue;

  /// The number flag [key], or [defaultValue].
  num getNumber(String key, num defaultValue) =>
      _serve(key, 'number', defaultValue).value as num? ?? defaultValue;

  /// The number flag [key] as an integer, or [defaultValue]. A served number
  /// that is not whole, or does not fit an integer, serves [defaultValue]
  /// with error code `type_mismatch` rather than a truncated value.
  int getInt(String key, int defaultValue) =>
      _serve(key, 'number', defaultValue, whole: true).value as int? ??
      defaultValue;

  /// The JSON flag [key] as a decoded value the caller may keep and mutate,
  /// or [defaultValue].
  Object? getJson(String key, Object? defaultValue) =>
      _serve(key, 'json', defaultValue).value;

  /// The flag [key] with why it was served. The type of [defaultValue]
  /// names the flag type asked for: `bool` is boolean, `String` is string,
  /// `num` is number, anything else is JSON. An `int` default reads the flag
  /// as [getInt] does.
  Detail getDetail(String key, Object? defaultValue) => _serve(
    key,
    flagTypeOf(defaultValue),
    defaultValue,
    whole: defaultValue is int,
  );

  /// Calls [listener] with the keys whose value changed,
  /// added and removed flags included. Returns a function removing it.
  void Function() onChange(ChangeListener listener) {
    _changeListeners.add(listener);
    return () => _changeListeners.remove(listener);
  }

  /// Calls [listener] on every stage transition. Returns a function removing
  /// it.
  void Function() onStage(StageListener listener) {
    _stageListeners.add(listener);
    return () => _stageListeners.remove(listener);
  }

  /// Records that the unit of the current context encountered the behaviour
  /// the flag [key] decides.
  ///
  /// The most recent detail this client served for [key] and the current
  /// context is what is reported, and only when it carries an experiment
  /// block: a flag never evaluated, one served by the built-in
  /// default with reason `fallback` and stage `built_in`, where nothing is
  /// held at all, each record nothing and log at debug. At stage
  /// `last_good` the cached experiment block and its ruleset version are
  /// reported: a cached assignment is a valid assignment.
  ///
  /// Repeats of one (experiment, unit type, unit ID, variation) are
  /// suppressed in a bounded in-process set; the platform's deduplication by
  /// event ID and its first exposure are authoritative.
  void exposure(String key) {
    try {
      if (_currentOverride(key) != null) {
        _skipped(key, 'local_override');
        return;
      }
      final detail = _details[key];
      if (detail == null) {
        _skipped(key, _values == null ? 'no_values' : 'no_experiment');
        return;
      }
      _emitExposure(key, detail);
    } on Object catch (e) {
      _internal('exposure', e);
    }
  }

  /// Records an alias linking the unit [fromUnitId] of [fromUnitType] to
  /// [toUnitId] of [toUnitType], typically an anonymous ID to a user ID at
  /// sign-up. The context is unchanged: keep the anonymous ID in its units.
  ///
  /// The event joins the persisted queue and goes out with the next batch;
  /// an offline client records it in [OfflineMode.recorded] instead.
  void alias({
    required String fromUnitType,
    required String fromUnitId,
    required String toUnitType,
    required String toUnitId,
  }) {
    try {
      _record(
        aliasEvent(DateTime.now().toUtc(), (
          fromUnitType: fromUnitType,
          fromUnitId: fromUnitId,
          toUnitType: toUnitType,
          toUnitId: toUnitId,
        )),
      );
    } on Object catch (e) {
      _internal('alias', e);
    }
  }

  /// Sends the queued exposures and aliases with the evaluation summary,
  /// completing once the batches in flight have settled. A batch waiting on
  /// a backoff is left to it. Never fails.
  Future<void> flush() async {
    try {
      await (_ingest?.flush() ?? Future<void>.value());
    } on Object catch (e) {
      _internal('flush', e);
    }
  }

  /// Builds one exposure from a detail carrying an experiment block and
  /// records it, unless this unit has already been recorded seeing this
  /// variation of this experiment.
  void _emitExposure(String key, Detail detail) {
    final experiment = detail.experiment;
    if (experiment == null) return;
    if (!_options.sendExposures) {
      _skipped(key, 'exposures_off');
      return;
    }
    final admitted = _exposed.admit((
      key: experiment.key,
      variation: experiment.variation,
      unitType: experiment.unitType,
      unitId: experiment.unitId,
    ));
    if (!admitted) return;
    _record(
      exposureEvent(DateTime.now().toUtc(), (
        flagKey: key,
        experimentKey: experiment.key,
        variationKey: experiment.variation,
        unitType: experiment.unitType,
        unitId: experiment.unitId,
        // The experiment block is present with reason `experiment`, and with
        // reason `override` when an override's shadow evaluation lands in
        // the experiment.
        reason: detail.reason == Reasons.override
            ? Reasons.override
            : Reasons.experiment,
        rulesetVersion: detail.rulesetVersion ?? 0,
        override: experiment.override,
        platform: _predeclared('platform', _options.platform),
        appVersion: _predeclared('app_version', _options.appVersion),
      )),
    );
  }

  /// One predeclared attribute of the current context, falling back to what
  /// the options name. Null when neither says.
  String? _predeclared(String name, String? fallback) =>
      switch (_context.attributes[name]) {
        final String value => value,
        _ => fallback,
      };

  void _skipped(String key, String cause) => _log(
    LogLevel.debug,
    LogEvents.exposureSkipped,
    {'flag': key, 'cause': cause},
  );

  /// Queues one event, or hands it to the offline recorder.
  void _record(QueuedEvent event) {
    final offline = _offline;
    if (offline != null) {
      offline._recorded.add(event);
      return;
    }
    final ingest = _ingest;
    if (ingest == null) {
      // No base URL: there is nowhere for the event to go, ever.
      _log(LogLevel.warn, LogEvents.queueDropped, {
        'cause': 'not_configured',
        'events': 1,
      });
      return;
    }
    ingest.record(event);
  }

  /// Stops fetching and flushes best effort within
  /// [ClientOptions.closeTimeout]. Values keep serving afterwards; [ready]
  /// completes at once.
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    try {
      _timer?.cancel();
      _timer = null;
      await _rulesetStream?.close();
      if (!_firstValues.isCompleted) _firstValues.complete();
      for (final waiter in _waiters) {
        if (!waiter.isCompleted) waiter.complete();
      }
      _waiters = [];
      // The queue flushes before the transport goes: what does not get out
      // within the close timeout stays in the store for the next run.
      await Future(() async {
        await Future.wait([flush(), _writes]);
        await (_ingest?.writes ?? Future<void>.value());
      }).timeout(_options.closeTimeout, onTimeout: () {});
    } on Object catch (e) {
      _internal('close', e);
    } finally {
      _ingest?.close();
      _transport?.close();
    }
  }

  // Serving.

  Detail _serve(
    String key,
    String type,
    Object? defaultValue, {
    bool whole = false,
  }) {
    Detail detail;
    try {
      detail = _detail(key, type, defaultValue, whole);
    } on Object catch (e) {
      _internal('evaluate', e);
      detail = _fallback(defaultValue, FallbackCodes.general);
    }
    try {
      // Every evaluation is counted, `fallback` and `local_override`
      // included, so a flag no build evaluates any more is visible. Evaluation records no exposure of its own unless the
      // flag opted into automatic exposure.
      _summary.count(key, detail.variation, detail.reason);
      if (detail.experiment == null) {
        _details.remove(key);
      } else {
        _details[key] = detail;
        if (_values?.flags[key]?.automaticExposure == true) {
          _emitExposure(key, detail);
        }
      }
    } on Object catch (e) {
      _internal('evaluate', e);
    }
    return detail;
  }

  Detail _detail(String key, String type, Object? defaultValue, bool whole) {
    final override = _currentOverride(key);
    if (override != null) {
      if (override.type != type) {
        if (_mismatched.add('override:$key')) {
          _log(LogLevel.warn, LogEvents.typeMismatch, {
            'flag': key,
            'flag_type': override.type,
            'requested_type': type,
          });
        }
        return _fallback(defaultValue, FallbackCodes.typeMismatch);
      }
      if (whole && !isWholeNumber(override.value)) {
        return _fallback(defaultValue, FallbackCodes.typeMismatch);
      }
      return Detail(
        value: whole
            ? (override.value! as num).toInt()
            : type == 'json'
            ? copyJson(override.value)
            : override.value,
        reason: Reasons.localOverride,
        rulesetVersion: _values?.rulesetVersion,
        receivedAt: _receivedAt,
        stage: _stage,
      );
    }
    final values = _values;
    if (values == null) return _fallback(defaultValue, FallbackCodes.noValues);
    final flag = values.flags[key];
    if (flag == null) {
      return _fallback(defaultValue, FallbackCodes.flagNotFound);
    }
    if (flag.type != type) {
      if (_mismatched.add(key)) {
        _log(LogLevel.warn, LogEvents.typeMismatch, {
          'flag': key,
          'flag_type': flag.type,
          'requested_type': type,
        });
      }
      return _fallback(defaultValue, FallbackCodes.typeMismatch);
    }
    if (flag.reason == Reasons.error) {
      return _fallback(
        defaultValue,
        flag.errorCode ?? FallbackCodes.inconsistentRuleset,
      );
    }
    if (!flag.hasValue || !fitsFlagType(type, flag.value)) {
      return _fallback(defaultValue, FallbackCodes.inconsistentRuleset);
    }
    if (whole && !isWholeNumber(flag.value)) {
      return _fallback(defaultValue, FallbackCodes.typeMismatch);
    }
    return Detail(
      value: whole
          ? (flag.value! as num).toInt()
          : type == 'json'
          ? copyJson(flag.value)
          : flag.value,
      reason: flag.reason,
      variation: flag.variation,
      rulesetVersion: values.rulesetVersion,
      receivedAt: _receivedAt,
      stage: _stage,
      overridden: flag.overrideInfluenced,
      experiment: flag.experiment,
    );
  }

  Detail _fallback(Object? defaultValue, String code) => Detail(
    value: defaultValue,
    reason: Reasons.fallback,
    errorCode: code,
    rulesetVersion: _values?.rulesetVersion,
    receivedAt: _receivedAt,
    stage: _stage,
  );

  // The fallback chain.

  /// Accepts a decoded values document from [source]; false, with a log,
  /// when the fallback chain refuses it.
  bool _accept(Object? json, String source, {bool anyVersion = false}) {
    final ValuesDocument doc;
    try {
      doc = ValuesDocument.parse(json);
      final held = _values;
      if (!anyVersion &&
          held != null &&
          doc.rulesetVersion < held.rulesetVersion) {
        throw Refused(
          RefusalCause.version,
          'ruleset_version ${doc.rulesetVersion} is below the held '
          '${held.rulesetVersion}',
        );
      }
    } on Refused catch (r) {
      _refused(r, source);
      return false;
    }
    final changed = ValuesDocument.changedKeys(_values, doc);
    _values = doc;
    _receivedAt = DateTime.now().toUtc();
    _staleSince = null;
    _log(LogLevel.info, LogEvents.valuesLoaded, {
      'source': source,
      'ruleset_version': doc.rulesetVersion,
      'flags': doc.flags.length,
    });
    for (final ignored in doc.ignoredAttributes) {
      if (_ignoredLogged.add(ignored.name)) {
        _log(LogLevel.warn, LogEvents.attributeIgnored, {
          'name': ignored.name,
          'code': ignored.code,
        });
      }
    }
    final dropped = refuseLocalOverrides(
      _ClientOverrides(this),
      protectedEnvironment: doc.protectedEnvironment,
    );
    _setStage(Stage.fresh);
    final union = {...changed, ...dropped}.toList()..sort();
    if (union.isNotEmpty) _emitChange(union);
    return true;
  }

  /// The local override of [key], or null when there is none or the held
  /// values name a protected environment.
  LocalOverride? _currentOverride(String key) {
    if (_overrides.isEmpty) return null;
    if (_values?.protectedEnvironment == true) return null;
    return _overrides[key];
  }

  void _refused(Refused r, String source) {
    if (r.cause == RefusalCause.schema && !_schemaLogged) {
      _schemaLogged = true;
      _log(LogLevel.error, LogEvents.schemaUnsupported, {
        'schema_version': r.schemaVersion,
        'supported': supportedSchemaVersions,
      });
    }
    _failed({'source': source, 'cause': r.cause.wireName, 'detail': r.detail});
  }

  /// Records a failure: values kept, `fresh` becomes `last_good`.
  void _failed(Map<String, Object?> fields) {
    _staleSince ??= DateTime.now().toUtc();
    _log(LogLevel.warn, LogEvents.fetchFailed, fields);
    if (_stage == Stage.fresh) _setStage(Stage.lastGood);
  }

  void _setStage(Stage stage) {
    if (stage == _stage) return;
    final from = _stage;
    _stage = stage;
    _log(LogLevel.info, LogEvents.stageChanged, {
      'from': from.wireName,
      'to': stage.wireName,
    });
    for (final listener in [..._stageListeners]) {
      try {
        listener(stage);
      } on Object catch (e) {
        _log(LogLevel.error, LogEvents.listenerFailed, {'cause': '$e'});
      }
    }
  }

  void _emitChange(List<String> changed) {
    for (final listener in [..._changeListeners]) {
      try {
        listener(List.unmodifiable(changed));
      } on Object catch (e) {
        _log(LogLevel.error, LogEvents.listenerFailed, {'cause': '$e'});
      }
    }
  }

  // Refetch triggers.

  /// Asks for an evaluate. Triggers arriving while one is in flight coalesce
  /// into a single follow-up, so a context change during a poll costs one
  /// extra request, not one per trigger.
  void _trigger(_Trigger trigger) {
    if (_closed || _transport == null || _offline != null) return;
    // Nothing runs in the background except what the app asks
    // for itself: `setContext` and `refresh` are calls, not timers.
    if (!_foreground && !trigger.isCall) return;
    if (_inFlight) {
      _again = true;
      return;
    }
    _timer?.cancel();
    _timer = null;
    scheduleMicrotask(_fetch);
  }

  /// [_trigger], with a future completing when the attempt it asks for has
  /// finished.
  Future<void> _refetch(_Trigger trigger) {
    if (_closed || _transport == null || _offline != null) {
      return Future.value();
    }
    final waiter = Completer<void>();
    _waiters.add(waiter);
    _trigger(trigger);
    return waiter.future;
  }

  bool get _withinRefetchGap {
    final last = _lastAttempt;
    return last != null &&
        DateTime.now().difference(last) < _options.minimumRefetchGap;
  }

  /// The interval between fetches, never below the minimum poll interval.
  Duration get _pollInterval =>
      _options.pollInterval < _options.minimumPollInterval
      ? _options.minimumPollInterval
      : _options.pollInterval;

  void _scheduleInterval() {
    if (_closed || _inFlight || _timer != null || !_foreground) return;
    _timer = Timer(_pollInterval, _fetch);
  }

  // Fetching.

  Future<void> _fetch() async {
    if (_closed || _inFlight) return;
    _inFlight = true;
    _again = false;
    _timer?.cancel();
    _timer = null;
    _lastAttempt = DateTime.now();
    // Whoever asked before this attempt started is answered by it; a
    // trigger arriving while it runs waits for the follow-up.
    final waiting = _waiters;
    _waiters = [];
    var outcome = _Outcome.failure;
    try {
      await _storeLoaded;
      if (!_closed) outcome = await _attempt();
    } on Object catch (e) {
      _internal('fetch', e);
      _failed({'source': 'network', 'cause': 'internal', 'detail': '$e'});
    } finally {
      _inFlight = false;
      _fetchedOnce = true;
      for (final waiter in waiting) {
        if (!waiter.isCompleted) waiter.complete();
      }
    }
    if (_closed) return;
    if (_again) {
      _again = false;
      scheduleMicrotask(_fetch);
      return;
    }
    final delay = _nextDelay(outcome);
    if (delay != null) _timer = Timer(delay, _fetch);
  }

  /// When to fetch next, or null while the app is backgrounded: nothing
  /// runs there.
  Duration? _nextDelay(_Outcome outcome) {
    if (outcome == _Outcome.success) _failures = 0;
    if (!_foreground) return null;
    if (outcome == _Outcome.success || outcome == _Outcome.noRetry) {
      return _pollInterval;
    }
    _failures++;
    return fullJitter(
      _failures,
      _options.retryBackoff,
      outcome == _Outcome.refused
          ? _options.unauthorizedBackoffCap
          : _options.retryBackoffCap,
      _jitter,
    );
  }

  Future<_Outcome> _attempt() async {
    final baseUrl = _baseUrl!;
    final context = _context;
    final generation = _generation;
    final TransportResponse response;
    try {
      response = await _transport!.postJson(
        _sdkUrl(baseUrl, 'evaluate'),
        {
          'Authorization': 'Bearer $_sdkKey',
          'Content-Type': 'application/json',
          'User-Agent': '$sdkName/$sdkVersion (${_options.platform ?? 'dart'})',
          'Flagnetic-Schema-Version': '$schemaVersionMax',
        },
        utf8.encode(jsonEncode(_requestBody(context), toEncodable: _encode)),
        _options.requestTimeout,
      );
    } on TransportException catch (e) {
      if (_closed) return _Outcome.failure;
      _failed({'source': 'network', 'cause': e.kind.name, 'detail': e.detail});
      return _Outcome.failure;
    }
    if (_closed) return _Outcome.failure;
    final status = response.status;
    if (status != 200) {
      final code = _problemCode(response.body);
      if (status == 401 || status == 403) _keyRefused(status, code: code);
      _failed({
        'source': 'network',
        'cause': 'status',
        'status': status,
        'code': code,
      });
      return switch (status) {
        401 || 403 => _Outcome.refused,
        429 || >= 500 => _Outcome.failure,
        _ => _Outcome.noRetry,
      };
    }
    Object? json;
    try {
      json = jsonDecode(utf8.decode(response.body));
    } on FormatException catch (e) {
      _refused(Refused(RefusalCause.parse, e.message), 'network');
      return _Outcome.failure;
    }
    if (generation != _generation) {
      // `setContext` ran while this was in flight: the answer describes the
      // previous context. It is that unit set's last good, not what this
      // client serves now; the refetch `setContext` queued serves.
      _revokedLogged = false;
      try {
        _persist(
          context.units,
          ValuesDocument.parse(json),
          DateTime.now().toUtc(),
        );
      } on Refused {
        // A document the fallback chain refuses is no unit set's last good.
      }
      return _Outcome.success;
    }
    if (!_accept(json, 'network')) return _Outcome.failure;
    _revokedLogged = false;
    _persist(context.units, _values!, _receivedAt!);
    if (!_firstValues.isCompleted) _firstValues.complete();
    return _Outcome.success;
  }

  /// The SDK key was refused with 401 or 403; logged once until a request
  /// succeeds again.
  /// Values keep serving.
  void _keyRefused(int status, {String? code}) {
    if (_revokedLogged) return;
    _revokedLogged = true;
    _log(LogLevel.error, LogEvents.keyRevoked, {
      'status': status,
      'code': code,
    });
  }

  /// [path] under `/sdk/v1` of [base].
  Uri _sdkUrl(Uri base, String path) {
    final prefix = base.path.endsWith('/')
        ? base.path.substring(0, base.path.length - 1)
        : base.path;
    return base.replace(path: '$prefix$sdkApiPath/$path');
  }

  /// The context posted, with the predeclared attributes the options and
  /// the SDK know added unless the context sets them.
  Map<String, Object?> _requestBody(FlagneticContext context) => {
    'context': {
      'units': context.units,
      'attributes': {
        'platform': ?_options.platform,
        'app_version': ?_options.appVersion,
        'sdk': sdkName,
        'sdk_version': sdkVersion,
        ...context.attributes,
      },
    },
  };

  static Object? _encode(Object? value) => switch (value) {
    final DateTime t => t.toUtc().toIso8601String(),
    final Iterable<Object?> list => list.toList(),
    _ => throw JsonUnsupportedObjectError(value),
  };

  static String? _problemCode(List<int> body) {
    try {
      final json = jsonDecode(utf8.decode(body));
      if (json case {'code': final String code}) return code;
    } on Object {
      // Not a problem document; the status says enough.
    }
    return null;
  }

  // The store.

  void _loadStored() {
    final key = _keys.lastGood(_context.units);
    final FutureOr<String?> entry;
    try {
      entry = _store.get(key);
    } on Object catch (e) {
      _storeFailed('get', key, e);
      return;
    }
    if (entry is Future<String?>) {
      _storeLoaded = entry.then(
        (raw) => _applyStored(key, raw),
        onError: (Object e) => _storeFailed('get', key, e),
      );
    } else {
      _applyStored(key, entry);
    }
  }

  void _applyStored(String key, String? raw) {
    if (raw == null || _values != null) return;
    try {
      final entry = jsonDecode(raw);
      if (entry case {
        'units': final Map<String, Object?> units,
        'received_at': final String receivedAt,
        'values': final Object? values,
      } when sameUnits(units.cast<String, String>(), _context.units)) {
        final doc = ValuesDocument.parse(values);
        _values = doc;
        _receivedAt = DateTime.parse(receivedAt).toUtc();
        _log(LogLevel.info, LogEvents.valuesLoaded, {
          'source': 'store',
          'ruleset_version': doc.rulesetVersion,
          'flags': doc.flags.length,
        });
        _setStage(Stage.lastGood);
        return;
      }
      throw const FormatException('not a last good entry of this unit set');
    } on Object catch (e) {
      _storeFailed('read', key, e);
    }
  }

  /// Persists [values] as the last good of [units], keeping the most recent
  /// [ClientOptions.storedUnitSets] unit sets. Writes run one after another.
  void _persist(
    Map<String, String> units,
    ValuesDocument values,
    DateTime receivedAt,
  ) {
    _writes = _writes.then((_) async {
      final key = _keys.lastGood(units);
      final entry = jsonEncode({
        'units': units,
        'ruleset_version': values.rulesetVersion,
        'received_at': receivedAt.toIso8601String(),
        'values': values.raw,
      });
      if (!await _storeCall('set', key, () => _store.set(key, entry))) return;
      var keys = <String>[];
      await _storeCall('get', _keys.index, () async {
        final index = await _store.get(_keys.index);
        if (index != null) {
          keys = (jsonDecode(index) as List<Object?>)
              .whereType<String>()
              .toList();
        }
      });
      keys
        ..remove(key)
        ..insert(0, key);
      final keep = max(1, _options.storedUnitSets);
      final evicted = keys.skip(keep).toList();
      await _storeCall(
        'set',
        _keys.index,
        () => _store.set(_keys.index, jsonEncode(keys.take(keep).toList())),
      );
      for (final old in evicted) {
        await _storeCall('remove', old, () => _store.remove(old));
      }
    });
  }

  Future<bool> _storeCall(
    String operation,
    String key,
    FutureOr<void> Function() call,
  ) async {
    try {
      await call();
      return true;
    } on Object catch (e) {
      _storeFailed(operation, key, e);
      return false;
    }
  }

  void _storeFailed(String operation, String key, Object cause) => _log(
    LogLevel.warn,
    LogEvents.storeFailed,
    {'operation': operation, 'key': key, 'cause': '$cause'},
  );

  // Logging.

  void _internal(String operation, Object cause) => _log(
    LogLevel.error,
    LogEvents.internalError,
    {'operation': operation, 'cause': '$cause'},
  );

  void _log(LogLevel level, String event, Map<String, Object?> fields) =>
      emitLog(_options.logger, level, event, fields);
}

/// The test helpers of an offline client: no network at all, stage
/// `fresh`, events recorded in memory.
final class OfflineMode {
  OfflineMode._(this._client, this._source);

  final FlagneticClient _client;
  final OfflineSource _source;
  final List<Map<String, Object?>> _recorded = [];

  /// The events the client emitted, oldest first.
  List<Map<String, Object?>> get recorded => List.unmodifiable(_recorded);

  /// The evaluation summary the client would have reported: every
  /// evaluation since construction, per flag, variation and evaluation
  /// reason.
  List<SummaryRow> get summary => _client._summary.rows;

  /// Serves [value] for the flag [key], as if the server had evaluated it,
  /// and fires the change listeners. The flag takes the type of [value] and
  /// no variation; it keeps its reason when its type is unchanged, else (and
  /// for reason `error`) reads `targeting_match`.
  void set(String key, Object? value) {
    final client = _client;
    try {
      final held = client._values;
      final raw = held == null
          ? <String, Object?>{
              'schema_version': schemaVersionMax,
              'ruleset_version': 0,
              'flags': <String, Object?>{},
            }
          : copyJson(held.raw)! as Map<String, Object?>;
      final flags = raw['flags']! as Map<String, Object?>;
      final type = flagTypeOf(value);
      final flag = flags[key];
      final reason = flag is Map<String, Object?> && flag['type'] == type
          ? flag['reason']
          : null;
      flags[key] = {
        'type': type,
        'value': value,
        'reason': reason is String && reason != Reasons.error
            ? reason
            : Reasons.targetingMatch,
      };
      client._accept(raw, 'offline', anyVersion: true);
    } on Object catch (e) {
      client._internal('offline_set', e);
    }
  }

  /// Reads the source again and serves it; changed keys fire the change
  /// listeners. A source that no longer reads keeps the values held.
  void reload() => _load();

  void _load() {
    final client = _client;
    try {
      var json = _source.read();
      if (json is String) json = jsonDecode(json);
      client._accept(json, 'offline', anyVersion: true);
    } on Object catch (e) {
      client._failed({'source': 'offline', 'cause': 'parse', 'detail': '$e'});
    }
  }
}

/// The client-mode side of [LocalOverrides].
final class _ClientOverrides implements OverrideHost {
  _ClientOverrides(this._client);

  final FlagneticClient _client;

  @override
  bool get protectedEnvironment =>
      _client._values?.protectedEnvironment ?? false;

  @override
  String? flagType(String key) => _client._values?.flags[key]?.type;

  @override
  Map<String, LocalOverride> get overrides => _client._overrides;

  @override
  set overrides(Map<String, LocalOverride> value) => _client._overrides = value;

  @override
  Logger? get logger => _client._options.logger;

  @override
  void emitChange(List<String> keys) => _client._emitChange(keys);
}
