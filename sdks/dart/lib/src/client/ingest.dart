import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'backoff.dart';
import 'events.dart';
import 'options.dart';
import 'store.dart';
import 'transport/transport.dart';
import 'types.dart';

/// The largest age an unreported window may reach before it is dropped
/// rather than delivered late. The interval's start is what
/// ages, because a window that merged forward is measured from where its
/// counting began.
const maxWindowAge = Duration(hours: 1);

/// What the platform did with one batch, and whether the batch may be sent
/// again.
enum _Outcome {
  /// 202. The batch is done, the events the platform rejected included: a
  /// rejection is final.
  accepted,

  /// 429, 5xx or a network error. The same batch, with the same event IDs,
  /// goes again after a backoff.
  retry,

  /// 401 or 403. Retriable too, but at the unauthorized cap, since an
  /// operator restoring the key is what ends it.
  unauthorized,

  /// Any other 4xx. Sending it again would fail the same way, so the batch
  /// goes and the cause is logged.
  dropped,
}

typedef _Attempt = ({_Outcome outcome, int? status, String? cause});

/// How an ingest queue behaves.
typedef IngestSettings = ({
  /// The platform named in `User-Agent` and the batch envelope.
  String? platform,

  /// The `app_version` of the evaluation summary.
  String? appVersion,
  Duration requestTimeout,

  /// How often the queue is sent.
  Duration flushInterval,
  int eventBatchSize,
  int eventQueueCapacity,
  Duration retryBackoff,
  Duration retryBackoffCap,
  Duration unauthorizedBackoffCap,
  bool sendSummaries,
});

/// The event queue and the ingest batches it sends.
///
/// Exposures and aliases are appended with the event ID they keep for every
/// retry, and the evaluation summary rides in the same batch. The queue
/// lives in the client's store, so an app killed while offline sends what it
/// recorded on its next start, exactly once.
///
/// Every store call may throw: the queue logs `store_failed` and carries on
/// in memory, so a device with no writable storage still serves flags and
/// still reports what it can.
final class Ingest {
  /// A queue posting to [url] with [sdkKey], persisted in [store] under the
  /// connection's own [keys], so no other connection sends what it holds.
  /// Without a store the queue lives in memory only.
  Ingest({
    required Uri url,
    required String sdkKey,
    required IngestSettings settings,
    required Transport transport,
    required SummaryCounter summary,
    required int? Function() rulesetVersion,
    required void Function(int status, {String? code}) keyRefused,
    required void Function(LogLevel, String, Map<String, Object?>) log,
    FlagneticStore? store,
    StoreKeys? keys,
  }) : _url = url,
       _sdkKey = sdkKey,
       _settings = settings,
       _store = store,
       _keys = keys,
       _transport = transport,
       _summary = summary,
       _rulesetVersion = rulesetVersion,
       _keyRefused = keyRefused,
       _log = log;

  final Uri _url;
  final String _sdkKey;
  final IngestSettings _settings;
  final FlagneticStore? _store;
  final StoreKeys? _keys;
  final Transport _transport;
  final SummaryCounter _summary;
  final int? Function() _rulesetVersion;
  final void Function(int status, {String? code}) _keyRefused;
  final void Function(LogLevel, String, Map<String, Object?>) _log;

  final List<QueuedEvent> _queue = [];
  final Random _jitter = Random();

  Future<void> _loaded = Future.value();
  Future<void> _writes = Future.value();
  Future<void> _settled = Future.value();
  Timer? _timer;
  Timer? _retry;
  // The event IDs of the batch being posted. A summary among them is not
  // merged forward while the platform may still be accepting it, which
  // would report its rows twice.
  Set<Object?> _inFlight = const {};
  bool _sending = false;
  bool _closed = false;
  bool _foreground = true;
  int _failures = 0;

  /// How many events wait to be sent.
  int get queued => _queue.length;

  /// Reads what an earlier run left in the store, so that events recorded
  /// while offline are sent after a restart. Failing to read one is not a
  /// failure of the client: the entry is dropped and the queue starts
  /// empty.
  Future<void> load() {
    final store = _store;
    final keys = _keys;
    if (store == null || keys == null) return _loaded;
    final entry = _storeCall('read', () => store.get(keys.events));
    return _loaded = entry.then(_restore);
  }

  /// Starts the flush interval; called once the client is running.
  void start() => _arm();

  /// Follows the application: the interval runs while it is foregrounded,
  /// and going to the background flushes best effort: by beacon in a
  /// browser, where the page may be gone before a request is answered, and
  /// by a request elsewhere.
  void setForeground({required bool foreground}) {
    if (foreground == _foreground) return;
    _foreground = foreground;
    if (foreground) {
      _arm();
      return;
    }
    _timer?.cancel();
    _timer = null;
    if (!_beacon()) unawaited(flush());
  }

  /// Hands the queue to the browser's beacon, batch by batch within the
  /// browser's quota for a page, and drops what the browser took: it posts
  /// after the page has gone and nobody reads the answer, so the events are
  /// not kept for a retry that would only count them as duplicates. What
  /// the quota leaves stays queued for the next foreground or the next
  /// load. A batch already in flight is left to its own request. False
  /// where there is no beacon, so the caller flushes by a request instead.
  ///
  /// The client key travels as `sdk_key` and the envelope as `text/plain`,
  /// since a beacon can set no header.
  bool _beacon() {
    if (_closed) return true;
    _materialize();
    final url = _url.replace(
      queryParameters: {..._url.queryParameters, 'sdk_key': _sdkKey},
    );
    var budget = maxBeaconBytes;
    var first = true;
    while (true) {
      final pending = [
        for (final event in _queue)
          if (!_inFlight.contains(event['event_id'])) event,
      ];
      if (pending.isEmpty) return true;
      final overhead = _envelopeOverhead();
      final batch = pending
          .take(
            batchLength(
              pending,
              _settings.eventBatchSize,
              overhead,
              maxBytes: budget,
            ),
          )
          .toList();
      final body = jsonEncode(_envelope(batch));
      final size = utf8.encode(body).length;
      if (size > budget || !_transport.beacon(url, body)) {
        // Nothing taken (no beacon on this platform, or a first batch the
        // browser refused): the caller sends the queue by a request, which
        // may still land before the page goes.
        if (first) return false;
        _log(LogLevel.warn, LogEvents.eventsFailed, {
          'cause': 'beacon_refused',
          'events': pending.length,
        });
        return true;
      }
      first = false;
      budget -= size;
      final sent = {for (final event in batch) event['event_id']};
      _queue.removeWhere((event) => sent.contains(event['event_id']));
      _persist();
    }
  }

  /// Appends one event and sends the queue once a batch has filled.
  void record(QueuedEvent event) {
    if (_closed) return;
    _append(event);
    if (_queue.length >= _settings.eventBatchSize) {
      unawaited(flush());
    }
  }

  /// Sends what is queued, the evaluation summary included, and completes once the batches in flight have settled. A
  /// batch waiting on a backoff is left to it rather than posted into the
  /// same failure again.
  Future<void> flush() async {
    if (_closed) return;
    await _loaded;
    if (_closed) return;
    // The window closes even when the queue cannot be posted yet, so what
    // has been counted reaches the store rather than dying with the
    // process.
    _materialize();
    if (_sending || _retry != null) return _settled;
    if (_queue.isEmpty) return;
    return _settled = _send();
  }

  /// Stops the timers. What is queued stays in the store for the next run.
  void close() {
    _closed = true;
    _timer?.cancel();
    _timer = null;
    _retry?.cancel();
    _retry = null;
  }

  /// Completes once every write to the store has been made.
  Future<void> get writes => _writes;

  // The queue.

  void _append(QueuedEvent event) {
    _queue.add(event);
    _trim();
    _persist();
  }

  /// Drops the oldest events over the capacity, logging what went.
  void _trim() {
    final over = _queue.length - _settings.eventQueueCapacity;
    if (over <= 0) return;
    _queue.removeRange(0, over);
    _log(LogLevel.warn, LogEvents.queueDropped, {
      'cause': 'queue_full',
      'events': over,
      'capacity': _settings.eventQueueCapacity,
    });
  }

  void _restore(String? raw) {
    if (raw == null) return;
    final List<QueuedEvent> restored;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! List<Object?>) {
        throw const FormatException('not an array of events');
      }
      restored = [
        for (final item in decoded)
          if (item is Map<String, Object?> &&
              item['type'] is String &&
              item['event_id'] is String)
            item,
      ];
    } on Object catch (e) {
      // A torn or foreign entry is not worth keeping.
      _storeFailed('read', e);
      // Only a queue with a store has anything to restore.
      unawaited(_storeCall('remove', () => _store!.remove(_keys!.events)));
      return;
    }
    if (restored.isEmpty) return;
    // What an earlier run recorded is older than anything this one has, so
    // it goes in front and is the first to be sent and the first dropped.
    _queue.insertAll(0, restored);
    _trim();
    // An event recorded while the read was in flight has already written a
    // queue holding only itself; this puts the restored ones back in front
    // of it, so a second restart still finds them.
    _persist();
  }

  /// Writes the queue as it stands. Writes run one after another, so the
  /// store never holds an older queue than the one before it.
  void _persist() {
    final store = _store;
    final keys = _keys;
    if (store == null || keys == null) return;
    final encoded = _queue.isEmpty ? null : jsonEncode(_queue);
    _writes = _writes.then(
      (_) => _storeCall(
        encoded == null ? 'remove' : 'set',
        () => encoded == null
            ? store.remove(keys.events)
            : store.set(keys.events, encoded),
      ),
    );
  }

  Future<T?> _storeCall<T>(
    String operation,
    FutureOr<T> Function() call,
  ) async {
    try {
      return await call();
    } on Object catch (e) {
      _storeFailed(operation, e);
      return null;
    }
  }

  void _storeFailed(String operation, Object cause) => _log(
    LogLevel.warn,
    LogEvents.storeFailed,
    {'operation': operation, 'key': _keys?.events, 'cause': '$cause'},
  );

  // The evaluation summary.

  /// Turns what has been counted since the last batch into one evaluation
  /// summary, folding in a window whose report never arrived and dropping
  /// one too old to place.
  void _materialize() {
    _dropStaleWindows();
    if (!_settings.sendSummaries || _summary.isEmpty) return;
    final window = _summary.take();
    if (window.rows.isEmpty) return;
    var rows = window.rows;
    var start = window.start;
    // A window still queued when this one closed merges into it and reports
    // under a new event ID: retrying the old one forever would stall the
    // reporter and post intervals hours stale.
    final held = _takeQueuedSummaries();
    for (final event in held) {
      rows = mergeRows(rows, rowsOf(event));
      final earlier = _intervalStart(event);
      if (earlier != null && earlier.isBefore(start)) start = earlier;
    }
    final at = DateTime.now().toUtc();
    for (final event in summaryEvents(
      at,
      SummaryWindow(start: start, end: window.end, rows: rows),
      _rulesetVersion() ?? 0,
      appVersion: _settings.appVersion,
    )) {
      _append(event);
    }
  }

  /// The queued summaries of earlier windows, removed from the queue. One
  /// the platform may still be accepting is left where it is: merging it
  /// forward would report its rows twice.
  List<QueuedEvent> _takeQueuedSummaries() {
    final held = [
      for (final event in _queue)
        if (event['type'] == EventTypes.evaluationSummary &&
            !_inFlight.contains(event['event_id']))
          event,
    ];
    if (held.isNotEmpty) {
      final taken = {for (final event in held) event['event_id']};
      _queue.removeWhere((e) => taken.contains(e['event_id']));
    }
    return held;
  }

  /// Drops a queued summary whose window began over [maxWindowAge] ago: a
  /// long outage loses counts rather than eventually delivering a report
  /// nobody can place.
  void _dropStaleWindows() {
    final oldest = DateTime.now().toUtc().subtract(maxWindowAge);
    var dropped = 0;
    _queue.removeWhere((event) {
      if (event['type'] != EventTypes.evaluationSummary) return false;
      final start = _intervalStart(event);
      if (start == null || !start.isBefore(oldest)) return false;
      dropped++;
      return true;
    });
    if (dropped == 0) return;
    _persist();
    _log(LogLevel.warn, LogEvents.queueDropped, {
      'cause': 'window_too_old',
      'events': dropped,
    });
  }

  static DateTime? _intervalStart(QueuedEvent event) =>
      switch (event['interval_start']) {
        final String at => DateTime.tryParse(at)?.toUtc(),
        _ => null,
      };

  // Sending.

  void _arm() {
    _timer?.cancel();
    _timer = null;
    if (_closed || !_foreground) return;
    _timer = Timer(_settings.flushInterval, () {
      _timer = null;
      unawaited(flush().whenComplete(_arm));
    });
  }

  Future<void> _send() async {
    final batch = _queue
        .take(
          batchLength(_queue, _settings.eventBatchSize, _envelopeOverhead()),
        )
        .toList();
    if (batch.isEmpty) return;
    _sending = true;
    _inFlight = {for (final event in batch) event['event_id']};
    _Attempt attempt;
    try {
      attempt = await _post(batch);
    } on Object catch (e) {
      // Nothing here is expected to throw, and an unhandled error would
      // escape an unawaited flush. The batch keeps its event IDs and goes
      // again.
      attempt = (outcome: _Outcome.retry, status: null, cause: '$e');
    } finally {
      _sending = false;
      _inFlight = const {};
    }
    switch (attempt.outcome) {
      case _Outcome.accepted:
      case _Outcome.dropped:
        // Rejected events go with the accepted ones: the platform will
        // never take them.
        final sent = {for (final event in batch) event['event_id']};
        _queue.removeWhere((event) => sent.contains(event['event_id']));
        _persist();
        _failures = 0;
        if (attempt.outcome == _Outcome.dropped) {
          _log(LogLevel.warn, LogEvents.queueDropped, {
            'cause': 'client_error',
            'events': batch.length,
            'status': attempt.status,
          });
        }
        if (_queue.isNotEmpty && !_closed) await _send();
      case _Outcome.retry:
      case _Outcome.unauthorized:
        _scheduleRetry(attempt, batch.length);
    }
  }

  /// Counts one failed attempt, logs it and holds the queue back until its
  /// backoff is up, so a platform that keeps failing is asked ever less
  /// often instead of once per flush interval.
  ///
  /// The SDK backs off on its own rather than honouring `Retry-After`, so a
  /// rate limit whose header names minutes does not hold exposures back for
  /// minutes; the scenarios of `conformance/protocol` allow either.
  void _scheduleRetry(_Attempt attempt, int events) {
    _failures++;
    final delay = fullJitter(
      _failures,
      _settings.retryBackoff,
      attempt.outcome == _Outcome.unauthorized
          ? _settings.unauthorizedBackoffCap
          : _settings.retryBackoffCap,
      _jitter,
    );
    _log(LogLevel.warn, LogEvents.eventsFailed, {
      'cause': attempt.cause,
      'status': attempt.status,
      'events': events,
      'retry_in_ms': delay.inMilliseconds,
    });
    if (_closed) return;
    _retry?.cancel();
    _retry = Timer(delay, () {
      _retry = null;
      unawaited(flush());
    });
  }

  Future<_Attempt> _post(List<QueuedEvent> batch) async {
    final TransportResponse response;
    try {
      response = await _transport.postJson(
        _url,
        {
          'Authorization': 'Bearer $_sdkKey',
          'Content-Type': 'application/json',
          'User-Agent':
              '$sdkName/$sdkVersion (${_settings.platform ?? hostPlatform()})',
          'Flagnetic-Schema-Version': '$schemaVersionMax',
        },
        utf8.encode(jsonEncode(_envelope(batch))),
        _settings.requestTimeout,
      );
    } on TransportException catch (e) {
      return (outcome: _Outcome.retry, status: null, cause: e.kind.name);
    }
    final status = response.status;
    if (status == 202) {
      _reportRejected(batch, response.body);
      return (outcome: _Outcome.accepted, status: status, cause: null);
    }
    if (status == 401 || status == 403) {
      _keyRefused(status, code: _problemCode(response.body));
      return (outcome: _Outcome.unauthorized, status: status, cause: 'status');
    }
    if (status == 429 || status >= 500) {
      return (outcome: _Outcome.retry, status: status, cause: 'status');
    }
    return (outcome: _Outcome.dropped, status: status, cause: 'status');
  }

  Map<String, Object?> _envelope(List<QueuedEvent> batch) => {
    'schema_version': schemaVersionMax,
    'sdk': {
      'name': sdkName,
      'version': sdkVersion,
      'platform': ?_settings.platform,
    },
    'sent_at': DateTime.now().toUtc().toIso8601String(),
    'events': batch,
  };

  int _envelopeOverhead() => utf8.encode(jsonEncode(_envelope([]))).length;

  /// Logs the events of an answered batch the platform rejected, which are
  /// never sent again.
  void _reportRejected(List<QueuedEvent> batch, List<int> body) {
    final List<Object?> rejected;
    try {
      final answer = jsonDecode(utf8.decode(body));
      if (answer case {'rejected': final List<Object?> list}) {
        rejected = list;
      } else {
        return;
      }
    } on Object {
      // The batch was accepted either way; an answer that does not decode
      // leaves nothing to retry.
      return;
    }
    if (rejected.isEmpty) return;
    _log(LogLevel.warn, LogEvents.queueDropped, {
      'cause': 'refused_by_the_platform',
      'events': rejected.length,
      'codes': [
        for (final entry in rejected)
          if (entry case {'code': final String code}) code,
      ],
    });
  }

  static String? _problemCode(List<int> body) {
    try {
      final json = jsonDecode(utf8.decode(body));
      if (json case {'code': final String code}) return code;
    } on Object {
      // Not a problem document; the status says enough.
    }
    return null;
  }
}
