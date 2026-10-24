// The browser side of hybrid_server.dart: mock servers run in a VM isolate
// on another origin than the test page, so every request the SDK sends is
// cross-origin, as it is from a page to the platform.
import 'dart:async';
import 'dart:convert';

import 'package:stream_channel/stream_channel.dart';
import 'package:test/test.dart';

import 'scripted.dart';

/// The hybrid isolate: its fixtures, and the servers it starts.
final class Remote {
  Remote._(this._channel) {
    _channel.stream.listen((message) {
      final answer = (message! as Map).cast<String, Object?>();
      if (answer['fixtures'] case final Map<Object?, Object?> files) {
        _fixtures.complete(HeldFixtures(files.cast<String, String>()));
        return;
      }
      final waiter = _waiting.remove(answer['id'])!;
      if (answer.containsKey('timeout')) {
        waiter.completeError(TimeoutException('${answer['timeout']}'));
      } else if (answer.containsKey('error')) {
        waiter.completeError(StateError('${answer['error']}'));
      } else {
        waiter.complete(answer['result']);
      }
    });
  }

  /// Spawns the isolate for the whole suite and waits for its fixtures.
  /// Call it from `setUpAll`.
  static Future<Remote> spawn() async {
    final remote = Remote._(
      spawnHybridUri(
        '/test/support/hybrid_server.dart',
        stayAlive: true,
      ).cast<Object?>(),
    );
    remote.fixtures = await remote._fixtures.future;
    return remote;
  }

  final StreamChannel<Object?> _channel;
  final Completer<Fixtures> _fixtures = Completer();
  final Map<int, Completer<Object?>> _waiting = {};
  var _ids = 0;

  /// The protocol fixtures, as the isolate read them.
  late final Fixtures fixtures;

  Future<Object?> _call(String op, [Map<String, Object?> args = const {}]) {
    final id = ++_ids;
    final waiter = Completer<Object?>();
    _waiting[id] = waiter;
    _channel.sink.add({'id': id, 'op': op, ...args});
    return waiter.future;
  }

  /// Starts a mock server in the isolate.
  Future<ScriptedServer> startServer() async {
    final started = (await _call('start'))! as Map;
    return _RemoteServer(
      this,
      _int(started['server']),
      Uri.parse(started['url']! as String),
    );
  }
}

/// A number from the channel, which a page compiled to WebAssembly reads
/// as a double.
int _int(Object? number) => (number! as num).toInt();

final class _RemoteServer implements ScriptedServer {
  _RemoteServer(this._remote, this._id, this.url);

  final Remote _remote;
  final int _id;

  @override
  final Uri url;

  @override
  Future<ScriptedExchange> next({
    String? path,
    Duration timeout = const Duration(seconds: 5),
  }) async {
    final got =
        (await _remote._call('next', {
              'server': _id,
              'path': path,
              'timeout_us': timeout.inMicroseconds,
            }))!
            as Map;
    return _RemoteExchange(
      _remote,
      _int(got['exchange']),
      method: got['method']! as String,
      path: got['path']! as String,
      query: (got['query']! as Map).cast<String, String>(),
      headers: (got['headers']! as Map).cast<String, String>(),
      rawBody: base64.decode(got['body']! as String),
      arrivedAt: DateTime.fromMicrosecondsSinceEpoch(
        _int(got['arrived_at_us']),
      ),
    );
  }

  @override
  Future<void> answerAll(
    String path,
    int status, {
    Map<String, String> headers = const {},
    List<int>? body,
  }) => _remote._call('answer_all', {
    'server': _id,
    'path': path,
    'status': status,
    'headers': headers,
    'body': body == null ? null : base64.encode(body),
  });

  @override
  Future<int> countReceived({String? path}) async => _int(
    await _remote._call('count_received', {'server': _id, 'path': path}),
  );

  @override
  Future<int> unansweredAt(DateTime time) async => _int(
    await _remote._call('unanswered_at', {
      'server': _id,
      'at_us': time.microsecondsSinceEpoch,
    }),
  );

  @override
  Future<void> close() => _remote._call('close', {'server': _id});
}

final class _RemoteExchange implements ScriptedExchange {
  _RemoteExchange(
    this._remote,
    this._id, {
    required this.method,
    required this.path,
    required this.query,
    required this.headers,
    required this.rawBody,
    required this.arrivedAt,
  });

  final Remote _remote;
  final int _id;

  @override
  final String method;

  @override
  final String path;

  @override
  final Map<String, String> query;

  @override
  final Map<String, String> headers;

  @override
  final List<int> rawBody;

  @override
  final DateTime arrivedAt;

  @override
  Object? get json => jsonDecode(utf8.decode(rawBody));

  @override
  Future<void> respond(
    int status, {
    Object? body,
    Map<String, String> headers = const {},
  }) => _remote._call('respond', {
    'exchange': _id,
    'status': status,
    'headers': headers,
    'body': switch (body) {
      final String s => base64.encode(utf8.encode(s)),
      final List<int> bytes => base64.encode(bytes),
      _ => null,
    },
  });

  @override
  Future<void> respondJson(Object? document) => respond(
    200,
    body: jsonEncode(document),
    headers: {'Content-Type': 'application/json', 'Cache-Control': 'no-store'},
  );

  @override
  Future<ScriptedStream> respondStream({
    Map<String, String> headers = eventStreamHeaders,
  }) async {
    final opened =
        (await _remote._call('respond_stream', {
              'exchange': _id,
              'headers': headers,
            }))!
            as Map;
    return _RemoteStream(_remote, _int(opened['stream']));
  }

  @override
  Future<void> dropConnection() => _remote._call('drop', {'exchange': _id});
}

final class _RemoteStream implements ScriptedStream {
  _RemoteStream(this._remote, this._id);

  final Remote _remote;
  final int _id;

  @override
  Future<void> send(String bytes) =>
      _remote._call('send', {'stream': _id, 'bytes': bytes});

  @override
  Future<bool> closedBySdk(Duration timeout) async =>
      (await _remote._call('closed_by_sdk', {
            'stream': _id,
            'timeout_us': timeout.inMicroseconds,
          }))!
          as bool;

  @override
  Future<void> closeStream() => _remote._call('close_stream', {'stream': _id});
}
