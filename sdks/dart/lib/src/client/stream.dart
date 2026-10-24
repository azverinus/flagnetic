import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'backoff.dart';
import 'options.dart';
import 'transport/transport.dart';
import 'types.dart';

/// One event the ruleset stream dispatched.
typedef SseEvent = ({String type, String data, String? id});

/// Reads the `text/event-stream` bytes of the ruleset stream by the WHATWG
/// rules: CRLF, LF or CR ends a line even when split between reads, a blank
/// line dispatches what was gathered, a line opening with a colon is a
/// comment, `data` lines join with newlines and the last event ID carries
/// over. The platform only ever writes `retry`, `ruleset` events and `: hb`
/// heartbeats, but a proxy may reframe them.
final class SseParser {
  final List<int> _buffer = [];
  bool _skipLineFeed = false;
  bool _started = false;
  String _type = '';
  final StringBuffer _data = StringBuffer();
  String? _id;

  /// The last `id` the stream carried, sent as `Last-Event-ID` on a
  /// reconnect; null until an event carried one.
  String? get lastEventId => _id;

  /// Reads [bytes], calling [onEvent] for every event they complete.
  void add(List<int> bytes, void Function(SseEvent event) onEvent) {
    var from = 0;
    if (_skipLineFeed && bytes.isNotEmpty) {
      if (bytes.first == 0x0a) from = 1;
      _skipLineFeed = false;
    }
    _buffer.addAll(bytes.sublist(from));
    _stripByteOrderMark();
    var start = 0;
    for (var i = 0; i < _buffer.length; i++) {
      final byte = _buffer[i];
      if (byte != 0x0a && byte != 0x0d) continue;
      _line(
        utf8.decode(_buffer.sublist(start, i), allowMalformed: true),
        onEvent,
      );
      if (byte == 0x0d && i + 1 == _buffer.length) {
        _skipLineFeed = true;
      } else if (byte == 0x0d && _buffer[i + 1] == 0x0a) {
        i++;
      }
      start = i + 1;
    }
    _buffer.removeRange(0, start);
  }

  static const List<int> _bom = [0xef, 0xbb, 0xbf];

  void _stripByteOrderMark() {
    if (_started || _buffer.length < _bom.length) return;
    _started = true;
    for (var i = 0; i < _bom.length; i++) {
      if (_buffer[i] != _bom[i]) return;
    }
    _buffer.removeRange(0, _bom.length);
  }

  void _line(String line, void Function(SseEvent event) onEvent) {
    if (line.isEmpty) return _dispatch(onEvent);
    if (line.startsWith(':')) return;
    final colon = line.indexOf(':');
    final field = colon < 0 ? line : line.substring(0, colon);
    var value = colon < 0 ? '' : line.substring(colon + 1);
    if (value.startsWith(' ')) value = value.substring(1);
    switch (field) {
      case 'event':
        _type = value;
      case 'data':
        _data.writeln(value);
      case 'id':
        if (!value.contains('\u0000')) _id = value;
    }
  }

  void _dispatch(void Function(SseEvent event) onEvent) {
    final data = _data.toString();
    final type = _type;
    _data.clear();
    _type = '';
    if (data.isEmpty) return;
    onEvent((
      type: type.isEmpty ? 'message' : type,
      // One trailing newline was added by the last data line.
      data: data.substring(0, data.length - 1),
      id: _id,
    ));
  }
}

/// How a ruleset stream behaves: the platform named in `User-Agent`, how
/// long the response head may take, and the reconnect durations.
typedef StreamSettings = ({
  String? platform,
  Duration requestTimeout,

  /// The first reconnect bound after an error, doubling up to [backoffCap].
  Duration backoff,
  Duration backoffCap,

  /// The longest wait before reconnecting a stream the server closed or
  /// that fell silent.
  Duration closeJitter,

  /// How long a stream may stay silent before it is reconnected.
  Duration silenceTimeout,

  /// How long a connection must stay up before the backoff starts over.
  Duration stableAfter,
});

/// The ruleset stream of one environment: a long-lived `GET /sdk/v1/stream`
/// that carries nothing but the new ruleset version, reconnecting on a
/// close, an error and 45 s of silence. It never throws: every
/// failure is a log and a reconnect.
final class RulesetStream {
  /// A stream of [url], authenticated with [sdkKey].
  ///
  /// [lastEventId] answers the version to revalidate from on a reconnect:
  /// the held ruleset version, or null when nothing is held. [onRuleset] is
  /// called with the version of every `ruleset` event, [onConnected] once
  /// per connection that answered 200, [onFailed] after every attempt that
  /// did not, [onRefused] when the key was refused, which stops the stream
  /// for good, and [onDropped] when a connection that answered 200 ended.
  RulesetStream({
    required Uri url,
    required String sdkKey,
    required StreamSettings settings,
    required Transport transport,
    required String? Function() lastEventId,
    required void Function(int version) onRuleset,
    required void Function() onConnected,
    required void Function() onFailed,
    required void Function(int status) onRefused,
    required void Function(
      LogLevel level,
      String event,
      Map<String, Object?> fields,
    )
    log,
    void Function()? onDropped,
  }) : _url = url,
       _sdkKey = sdkKey,
       _settings = settings,
       _transport = transport,
       _lastEventId = lastEventId,
       _onRuleset = onRuleset,
       _onConnected = onConnected,
       _onFailed = onFailed,
       _onRefused = onRefused,
       _onDropped = onDropped,
       _log = log;

  final Uri _url;
  final String _sdkKey;
  final StreamSettings _settings;
  final Transport _transport;
  final String? Function() _lastEventId;
  final void Function(int version) _onRuleset;
  final void Function() _onConnected;
  final void Function() _onFailed;
  final void Function(int status) _onRefused;
  final void Function()? _onDropped;
  final void Function(LogLevel, String, Map<String, Object?>) _log;
  final Random _jitter = Random();

  Timer? _reconnect;
  Timer? _silence;
  StreamSubscription<List<int>>? _body;
  DateTime? _connectedAt;
  int _failures = 0;
  bool _closed = false;
  bool _suspended = false;
  bool _running = false;

  // Counts the connections given up by [suspend] and [close]. A connect still
  // waiting for its response when the app goes to the background and back
  // compares it after the wait: without it, both that connect and the one the
  // foreground starts would listen, and every change would be fetched twice
  // over a stream nothing could cancel.
  int _generation = 0;

  /// Connects, and keeps the stream connected until [suspend] or [close].
  /// True when this call began a connect, which then ends in exactly one of
  /// `onConnected`, `onFailed` or `onRefused` unless [suspend] or [close]
  /// comes first; false when the stream is
  /// closed or already connecting or connected.
  bool start() {
    _suspended = false;
    if (_closed || _running) return false;
    unawaited(_connect());
    return true;
  }

  /// Drops the connection until [start]: the app went to the background,
  /// where nothing runs.
  Future<void> suspend() async {
    _suspended = true;
    _generation++;
    _reconnect?.cancel();
    _reconnect = null;
    _failures = 0;
    _running = false;
    await _stop();
  }

  /// Stops the stream; it never reconnects afterwards.
  Future<void> close() async {
    _closed = true;
    _generation++;
    _reconnect?.cancel();
    await _stop();
  }

  Future<void> _stop() async {
    _drop();
    await _body?.cancel();
    _body = null;
  }

  Future<void> _connect() async {
    if (_closed || _suspended || _running) return;
    _running = true;
    final generation = _generation;
    final id = _lastEventId();
    final TransportStream stream;
    try {
      stream = await _transport.openStream(_url, {
        'Authorization': 'Bearer $_sdkKey',
        'User-Agent':
            '$sdkName/$sdkVersion (${_settings.platform ?? hostPlatform()})',
        'Last-Event-ID': ?id,
      }, _settings.requestTimeout);
    } on Object catch (e) {
      // A given-up connect leaves `_running` to the connect that replaced it.
      if (generation != _generation) return;
      _running = false;
      if (_gone) return;
      _onFailed();
      _retryAfterError({'cause': 'network', 'detail': '$e'});
      return;
    }
    if (generation != _generation || _gone) {
      if (generation == _generation) _running = false;
      unawaited(stream.body.drain<void>().catchError((Object _) {}));
      return;
    }
    if (stream.status != 200) {
      _running = false;
      unawaited(stream.body.drain<void>().catchError((Object _) {}));
      if (stream.status == 401 || stream.status == 403) {
        _closed = true;
        _onRefused(stream.status);
        return;
      }
      _onFailed();
      _retryAfterError({'cause': 'status', 'status': stream.status});
      return;
    }
    _failures = 0;
    _connectedAt = DateTime.now();
    _log(LogLevel.info, LogEvents.sseConnected, {'last_event_id': id});
    final parser = SseParser();
    _body = stream.body.listen(
      (chunk) {
        _beat();
        try {
          parser.add(chunk, _event);
        } on Object catch (e) {
          _log(LogLevel.warn, LogEvents.sseDropped, {
            'cause': 'parse',
            'detail': '$e',
          });
        }
      },
      onError: (Object e) {
        _running = false;
        _drop();
        if (_gone) return;
        _onDropped?.call();
        _retryAfterError({'cause': 'network', 'detail': '$e'});
      },
      onDone: () {
        _running = false;
        _drop();
        if (_gone) return;
        _onDropped?.call();
        _retryAfterClose('closed');
      },
      cancelOnError: true,
    );
    _beat();
    _onConnected();
  }

  void _event(SseEvent event) {
    if (event.type != 'ruleset') return;
    try {
      final data = jsonDecode(event.data);
      if (data case {'version': final int version}) _onRuleset(version);
    } on FormatException {
      // A `ruleset` event the SDK cannot read says only that something
      // changed; the safety poll picks it up.
    }
  }

  /// Restarts the silence timer: 45 s without a byte means the connection is
  /// gone without the operating system having noticed.
  void _beat() {
    _silence?.cancel();
    _silence = Timer(_settings.silenceTimeout, () {
      if (_gone) return;
      _running = false;
      _drop();
      _onDropped?.call();
      _retryAfterClose('silent');
    });
  }

  void _drop() {
    _silence?.cancel();
    _silence = null;
    final body = _body;
    _body = null;
    if (body != null) unawaited(body.cancel());
  }

  /// A stream the server closed, or one that fell silent, reconnects within
  /// the close jitter: it is the platform draining, not an outage.
  void _retryAfterClose(String cause) {
    final delay = _settings.closeJitter * _jitter.nextDouble();
    _log(LogLevel.warn, LogEvents.sseDropped, {
      'cause': cause,
      'retry_in_ms': delay.inMilliseconds,
    });
    _schedule(delay);
  }

  void _retryAfterError(Map<String, Object?> fields) {
    if (_stable) _failures = 0;
    _failures++;
    final delay = fullJitter(
      _failures,
      _settings.backoff,
      _settings.backoffCap,
      _jitter,
    );
    _log(LogLevel.warn, LogEvents.sseDropped, {
      ...fields,
      'retry_in_ms': delay.inMilliseconds,
    });
    _schedule(delay);
  }

  /// True when the last connection stayed up long enough to call the stream
  /// healthy again, so the backoff starts from the bottom.
  bool get _stable {
    final at = _connectedAt;
    return at != null && DateTime.now().difference(at) >= _settings.stableAfter;
  }

  /// True while the stream must not reconnect: closed for good, or
  /// suspended because the app is in the background.
  bool get _gone => _closed || _suspended;

  void _schedule(Duration delay) {
    _connectedAt = null;
    _reconnect?.cancel();
    _reconnect = Timer(delay, () {
      if (!_gone) unawaited(_connect());
    });
  }
}
