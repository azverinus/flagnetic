import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flagnetic/flagnetic.dart';

import 'scripted.dart';

export 'scripted.dart';

/// The protocol fixtures of the monorepo checkout, relative to this
/// package's root, where `dart test` runs.
const protocolDir = '../../conformance/protocol';

/// The decoded JSON of a protocol fixture file.
Object? protocolJson(String path) =>
    jsonDecode(File('$protocolDir/$path').readAsStringSync());

/// The bytes of a protocol fixture file.
List<int> protocolBytes(String path) =>
    File('$protocolDir/$path').readAsBytesSync();

/// The protocol fixtures read from the checkout.
final class CheckoutFixtures implements Fixtures {
  /// The fixtures under [protocolDir].
  const CheckoutFixtures();

  @override
  Object? json(String path) => protocolJson(path);

  @override
  List<int> bytes(String path) => protocolBytes(path);
}

/// What every answer of a server reached cross-origin carries, as the
/// platform's CORS answer does: any origin, and the headers an SDK acts on
/// readable by a script.
const corsHeaders = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Expose-Headers':
      'ETag, Retry-After, RateLimit-Limit, RateLimit-Remaining, '
      'RateLimit-Reset',
};

/// One request the SDK sent, held open until the test answers it.
final class Exchange implements ScriptedExchange {
  Exchange._(
    this._request,
    this.headers,
    this.rawBody,
    this.arrivedAt, {
    required bool cors,
  }) : _cors = cors;

  final HttpRequest _request;
  final bool _cors;

  /// Request headers, lower-case names.
  @override
  final Map<String, String> headers;

  /// The body as sent, before decompression.
  @override
  final List<int> rawBody;

  /// When the request arrived.
  @override
  final DateTime arrivedAt;

  /// The request method.
  @override
  String get method => _request.method;

  /// The request path.
  @override
  String get path => _request.uri.path;

  @override
  Map<String, String> get query => _request.uri.queryParameters;

  /// The body, gunzipped when the request says so, as decoded JSON.
  @override
  Object? get json => jsonDecode(
    utf8.decode(
      headers['content-encoding'] == 'gzip' ? gzip.decode(rawBody) : rawBody,
    ),
  );

  bool _answered = false;

  /// Answers with [status], [headers] and a [body] of bytes or a string.
  @override
  Future<void> respond(
    int status, {
    Object? body,
    Map<String, String> headers = const {},
  }) async {
    _answered = true;
    final response = _request.response..statusCode = status;
    if (_cors) {
      corsHeaders.forEach(response.headers.set);
      // A browser sends a request again, unseen by the page, when a
      // connection it reused drops without an answer; a fresh connection
      // per request lets a dropped one fail as scripted.
      response.persistentConnection = false;
    }
    headers.forEach(response.headers.set);
    switch (body) {
      case final String s:
        response.add(utf8.encode(s));
      case final List<int> bytes:
        response.add(bytes);
    }
    await response.close();
  }

  /// Answers 200 with a JSON document.
  @override
  Future<void> respondJson(Object? document) => respond(
    200,
    body: jsonEncode(document),
    headers: {'Content-Type': 'application/json', 'Cache-Control': 'no-store'},
  );

  /// Answers 200 with an open `text/event-stream` the test writes to: the
  /// change stream.
  ///
  /// The stream opens with one comment line, because `dart:io` holds the
  /// response head back until the body has bytes and the SDK must see the
  /// head before anything is sent. An event-stream parser ignores comments,
  /// exactly as it ignores the platform's `: hb` heartbeats, so a scripted
  /// transcript still arrives as it was written.
  @override
  Future<OpenStream> respondStream({
    Map<String, String> headers = eventStreamHeaders,
  }) async {
    _answered = true;
    // The socket is written by hand: a stream the SDK ends can only be told
    // from a quiet one by reading the socket, which `HttpResponse` hides.
    final socket = await _request.response.detachSocket(writeHeaders: false);
    final head = StringBuffer('HTTP/1.1 200 OK\r\nConnection: close\r\n');
    if (_cors) {
      corsHeaders.forEach((name, value) => head.write('$name: $value\r\n'));
    }
    headers.forEach((name, value) => head.write('$name: $value\r\n'));
    head.write('\r\n');
    final open = OpenStream._(socket);
    socket.add(utf8.encode(head.toString()));
    await open.send(': open\n\n');
    return open;
  }

  /// Drops the connection without an answer.
  @override
  Future<void> dropConnection() async {
    _answered = true;
    final socket = await _request.response.detachSocket(writeHeaders: false);
    socket.destroy();
  }
}

/// A change stream the test holds open and writes to.
final class OpenStream implements ScriptedStream {
  OpenStream._(this._socket) {
    _socket.listen(
      (_) {},
      onDone: () => _gone = true,
      onError: (Object _) => _gone = true,
      cancelOnError: true,
    );
  }

  final Socket _socket;
  bool _gone = false;

  /// Writes [bytes] and flushes, so the SDK reads them at once.
  @override
  Future<void> send(String bytes) async {
    _socket.add(utf8.encode(bytes));
    await _socket.flush();
  }

  /// Whether the SDK ends the connection within [timeout].
  @override
  Future<bool> closedBySdk(Duration timeout) async {
    final deadline = DateTime.now().add(timeout);
    while (!_gone && DateTime.now().isBefore(deadline)) {
      await Future<void>.delayed(const Duration(milliseconds: 1));
    }
    return _gone;
  }

  /// Ends the stream from the server's side.
  @override
  Future<void> closeStream() async {
    try {
      await _socket.close();
    } on Object {
      // The SDK already went away.
    }
  }
}

/// An in-process HTTP server that queues every request until the test
/// answers it, so a test observes the SDK between request and response.
final class MockServer implements ScriptedServer {
  MockServer._(this._server, {required bool cors}) {
    _server.listen((request) async {
      if (cors && request.method == 'OPTIONS') {
        // A preflight, answered as the platform answers it and not one of
        // the SDK's requests.
        final response = request.response
          ..statusCode = 204
          ..persistentConnection = false;
        corsHeaders.forEach(response.headers.set);
        response.headers
          ..set('Access-Control-Allow-Methods', 'GET, POST, OPTIONS')
          ..set(
            'Access-Control-Allow-Headers',
            'Authorization, Content-Type, Content-Encoding, '
                'Flagnetic-Schema-Version, Last-Event-ID',
          )
          ..set('Access-Control-Max-Age', '86400');
        await response.close();
        return;
      }
      final body = await request.fold<List<int>>(
        [],
        (all, chunk) => all..addAll(chunk),
      );
      final headers = <String, String>{};
      request.headers.forEach(
        (name, values) => headers[name.toLowerCase()] = values.join(', '),
      );
      final exchange = Exchange._(
        request,
        headers,
        body,
        DateTime.now(),
        cors: cors,
      );
      received.add(exchange);
      final waiter = _takeWaiter(exchange.path);
      if (waiter != null) {
        waiter.complete(exchange);
        return;
      }
      final auto = _auto[exchange.path];
      if (auto != null) {
        await auto(exchange);
        return;
      }
      _pending.add(exchange);
    });
  }

  /// Starts a server on the loopback interface; with [cors], one a page
  /// of another origin may call, as it may call the platform.
  static Future<MockServer> start({bool cors = false}) async => MockServer._(
    await HttpServer.bind(InternetAddress.loopbackIPv4, 0),
    cors: cors,
  );

  final HttpServer _server;
  final List<Exchange> _pending = [];
  final List<({String? path, Completer<Exchange> completer})> _waiters = [];
  final Map<String, Future<void> Function(Exchange)> _auto = {};

  /// Answers every request to [path] that no [next] is waiting for with
  /// [handler].
  ///
  /// The scenarios run on real time, so the SDK's own safety poll
  /// interleaves with the requests a scenario scripts. A poll answered with
  /// the document already held changes nothing a scenario asserts, and
  /// leaving it unanswered would stall the client instead.
  void autoRespond(String path, Future<void> Function(Exchange) handler) =>
      _auto[path] = handler;

  @override
  Future<void> answerAll(
    String path,
    int status, {
    Map<String, String> headers = const {},
    List<int>? body,
  }) async => autoRespond(
    path,
    (poll) => poll.respond(status, headers: headers, body: body),
  );

  Completer<Exchange>? _takeWaiter(String path) {
    for (final waiter in _waiters) {
      if (waiter.path == null || waiter.path == path) {
        _waiters.remove(waiter);
        return waiter.completer;
      }
    }
    return null;
  }

  /// Every request received, in order.
  final List<Exchange> received = [];

  /// The base URL SDKs are built with.
  @override
  Uri get url => Uri.parse('http://127.0.0.1:${_server.port}');

  /// The next request not yet taken, to [path] when one is named, waiting
  /// up to [timeout].
  @override
  Future<Exchange> next({
    String? path,
    Duration timeout = const Duration(seconds: 5),
  }) {
    for (final exchange in _pending) {
      if (path == null || exchange.path == path) {
        _pending.remove(exchange);
        return Future.value(exchange);
      }
    }
    final waiter = (path: path, completer: Completer<Exchange>());
    _waiters.add(waiter);
    return waiter.completer.future.timeout(
      timeout,
      onTimeout: () {
        _waiters.remove(waiter);
        throw TimeoutException(
          'no request to ${path ?? 'any path'} '
          'within $timeout',
        );
      },
    );
  }

  /// Requests received but not yet taken by [next].
  int get pendingCount => _pending.length;

  /// Requests received no later than [time] but not yet taken by [next]:
  /// what a script left unanswered, as opposed to a request the SDK sends
  /// on its own schedule after the script ended.
  int pendingAt(DateTime time) =>
      _pending.where((e) => !e.arrivedAt.isAfter(time)).length;

  @override
  Future<int> countReceived({String? path}) async => path == null
      ? received.length
      : received.where((e) => e.path == path).length;

  @override
  Future<int> unansweredAt(DateTime time) async => pendingAt(time);

  /// Answers whatever is still open and stops.
  @override
  Future<void> close() async {
    for (final exchange in received) {
      if (!exchange._answered) {
        try {
          await exchange.respond(503);
        } on Object {
          // The SDK already went away.
        }
      }
    }
    await _server.close(force: true);
  }
}

/// A store holding [document] as the last good values of [context]'s unit
/// set, put there the way a real run does: a client fetched it and closed.
Future<InMemoryStore> storeHolding(
  Map<String, Object?> document,
  FlagneticContext context, {
  InMemoryStore? store,
}) async {
  final target = store ?? InMemoryStore();
  final server = await MockServer.start();
  final client = Flagnetic.client(
    clientKey,
    baseUrl: server.url,
    context: context,
    options: scaledOptions(
      store: target,
      pollInterval: const Duration(hours: 1),
    ),
  );
  await (await server.next()).respondJson(document);
  await eventually(() => client.stage == Stage.fresh);
  await client.close();
  await server.close();
  return target;
}
