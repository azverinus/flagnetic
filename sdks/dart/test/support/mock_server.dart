import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

/// The protocol fixtures of the monorepo checkout, relative to this
/// package's root, where `dart test` runs.
const protocolDir = '../../conformance/protocol';

/// The decoded JSON of a protocol fixture file.
Object? protocolJson(String path) =>
    jsonDecode(File('$protocolDir/$path').readAsStringSync());

/// The bytes of a protocol fixture file.
List<int> protocolBytes(String path) =>
    File('$protocolDir/$path').readAsBytesSync();

/// A client key as the platform issues them.
const clientKey = 'fn_cli_TESTKEY0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/// The store keys [clientKey] writes; another key writes its own.
final clientKeys = StoreKeys(clientKey);

/// The path of the evaluate endpoint.
const evaluatePath = '/sdk/v1/evaluate';

/// One request the SDK sent, held open until the test answers it.
final class Exchange {
  Exchange._(this._request, this.headers, this.rawBody, this.arrivedAt);

  final HttpRequest _request;

  /// Request headers, lower-case names.
  final Map<String, String> headers;

  /// The body as sent, before decompression.
  final List<int> rawBody;

  /// When the request arrived.
  final DateTime arrivedAt;

  /// The request method.
  String get method => _request.method;

  /// The request path.
  String get path => _request.uri.path;

  /// The body, gunzipped when the request says so, as decoded JSON.
  Object? get json => jsonDecode(
    utf8.decode(
      headers['content-encoding'] == 'gzip' ? gzip.decode(rawBody) : rawBody,
    ),
  );

  bool _answered = false;

  /// Answers with [status], [headers] and a [body] of bytes or a string.
  Future<void> respond(
    int status, {
    Object? body,
    Map<String, String> headers = const {},
  }) async {
    _answered = true;
    final response = _request.response..statusCode = status;
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
  Future<OpenStream> respondStream({
    Map<String, String> headers = const {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      'X-Accel-Buffering': 'no',
    },
  }) async {
    _answered = true;
    final response = _request.response
      ..statusCode = 200
      ..bufferOutput = false;
    response.headers.contentLength = -1;
    headers.forEach(response.headers.set);
    final open = OpenStream._(response);
    await open.send(': open\n\n');
    return open;
  }

  /// Drops the connection without an answer.
  Future<void> dropConnection() async {
    _answered = true;
    final socket = await _request.response.detachSocket(writeHeaders: false);
    socket.destroy();
  }
}

/// A change stream the test holds open and writes to.
final class OpenStream {
  OpenStream._(this._response);

  final HttpResponse _response;

  /// Writes [bytes] and flushes, so the SDK reads them at once.
  Future<void> send(String bytes) async {
    _response.add(utf8.encode(bytes));
    await _response.flush();
  }

  /// Ends the stream from the server's side.
  Future<void> closeStream() async {
    try {
      await _response.close();
    } on Object {
      // The SDK already went away.
    }
  }
}

/// An in-process HTTP server that queues every request until the test
/// answers it, so a test observes the SDK between request and response.
final class MockServer {
  MockServer._(this._server) {
    _server.listen((request) async {
      final body = await request.fold<List<int>>(
        [],
        (all, chunk) => all..addAll(chunk),
      );
      final headers = <String, String>{};
      request.headers.forEach(
        (name, values) => headers[name.toLowerCase()] = values.join(', '),
      );
      final exchange = Exchange._(request, headers, body, DateTime.now());
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

  /// Starts a server on the loopback interface.
  static Future<MockServer> start() async =>
      MockServer._(await HttpServer.bind(InternetAddress.loopbackIPv4, 0));

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
  Uri get url => Uri.parse('http://127.0.0.1:${_server.port}');

  /// The next request not yet taken, to [path] when one is named, waiting
  /// up to [timeout].
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

  /// Answers whatever is still open and stops.
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

/// A logger hook that keeps every event.
final class LogRecorder {
  /// Every event received, in order.
  final List<({LogLevel level, String event, Map<String, Object?> fields})>
  events = [];

  /// The hook to pass as [ClientOptions.logger].
  void call(LogLevel level, String event, Map<String, Object?> fields) =>
      events.add((level: level, event: event, fields: fields));

  /// How many events named [event] were received.
  int count(String event) => events.where((e) => e.event == event).length;

  /// The fields of every event named [event].
  List<Map<String, Object?>> fieldsOf(String event) => [
    for (final e in events)
      if (e.event == event) e.fields,
  ];
}

/// Waits until [condition] holds, failing with [reason] after [timeout].
Future<void> eventually(
  bool Function() condition, {
  String reason = 'condition',
  Duration timeout = const Duration(seconds: 3),
}) async {
  final deadline = DateTime.now().add(timeout);
  while (!condition()) {
    if (DateTime.now().isAfter(deadline)) fail('timed out waiting: $reason');
    await Future<void>.delayed(const Duration(milliseconds: 1));
  }
}

/// The client's durations divided by 1000, so scenarios that span
/// minutes run in milliseconds. The request timeout stays real.
ClientOptions scaledOptions({
  FlagneticStore? store,
  Logger? logger,
  Duration pollInterval = const Duration(milliseconds: 300),
  Map<String, Object?>? bootstrap,
  OfflineSource? offline,
  String? platform,
  String? appVersion,
  int storedUnitSets = 4,
  Duration requestTimeout = const Duration(seconds: 5),
  Duration closeTimeout = const Duration(seconds: 2),
  bool sse = false,
  Duration minimumRefetchGap = const Duration(milliseconds: 10),
  Duration streamSilenceTimeout = const Duration(milliseconds: 45),
  Duration flushInterval = const Duration(hours: 1),
  Duration retryBackoff = const Duration(milliseconds: 2),
  Duration retryBackoffCap = const Duration(milliseconds: 300),
  Duration unauthorizedBackoffCap = const Duration(milliseconds: 900),
  int eventBatchSize = 100,
  int eventQueueCapacity = 1000,
  int exposureMemory = 1000,
  bool sendExposures = true,
  bool sendSummaries = true,
  bool allowOverrides = false,
}) => ClientOptions(
  store: store,
  logger: logger,
  pollInterval: pollInterval,
  bootstrap: bootstrap,
  offline: offline,
  platform: platform,
  appVersion: appVersion,
  storedUnitSets: storedUnitSets,
  requestTimeout: requestTimeout,
  closeTimeout: closeTimeout,
  retryBackoff: retryBackoff,
  retryBackoffCap: retryBackoffCap,
  unauthorizedBackoffCap: unauthorizedBackoffCap,
  flushInterval: flushInterval,
  eventBatchSize: eventBatchSize,
  eventQueueCapacity: eventQueueCapacity,
  exposureMemory: exposureMemory,
  sendExposures: sendExposures,
  sendSummaries: sendSummaries,
  allowOverrides: allowOverrides,
  minimumPollInterval: const Duration(milliseconds: 30),
  minimumRefetchGap: minimumRefetchGap,
  sse: sse,
  streamBackoff: const Duration(milliseconds: 1),
  streamBackoffCap: const Duration(milliseconds: 30),
  streamCloseJitter: const Duration(milliseconds: 2),
  streamSilenceTimeout: streamSilenceTimeout,
  streamStableAfter: const Duration(milliseconds: 60),
);

/// Reads a scenario context.
FlagneticContext contextOf(Object? json) {
  final map = json! as Map<String, Object?>;
  return FlagneticContext(
    units: (map['units']! as Map<String, Object?>).cast<String, String>(),
    attributes: map['attributes']! as Map<String, Object?>,
  );
}

/// Checks that [client] serves each answer of a protocol fixture: `flag`,
/// `type`, `default`, and the `value`, `variation` and `reason` expected.
void expectAnswers(FlagneticClient client, List<Object?> answers) {
  for (final answer in answers.cast<Map<String, Object?>>()) {
    final flag = answer['flag']! as String;
    final def = answer['default'];
    final detail = client.getDetail(flag, def);
    expect(
      [detail.value, detail.variation, detail.reason],
      [answer['value'], answer['variation'], answer['reason']],
      reason: 'answer for $flag',
    );
    final typed = switch (answer['type']) {
      'boolean' => client.getBool(flag, def! as bool),
      'string' => client.getString(flag, def! as String),
      'number' => client.getNumber(flag, def! as num),
      _ => client.getJson(flag, def),
    };
    expect(typed, answer['value'], reason: 'typed getter for $flag');
  }
}

/// True when [client] serves every answer (see [expectAnswers]).
bool servesAnswers(FlagneticClient client, List<Object?> answers) {
  try {
    expectAnswers(client, answers);
    return true;
  } on TestFailure {
    return false;
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

/// A values document serving `checkout` from a running experiment, in the
/// shape `POST /sdk/v1/evaluate` answers, plus a `banner` flag
/// with no experiment at all.
Map<String, Object?> experimentValues({
  bool automaticExposure = false,
  String variation = 'treatment',
  String reason = 'experiment',
  bool override = false,
  int rulesetVersion = 42,
}) => {
  'schema_version': 1,
  'ruleset_version': rulesetVersion,
  'environment': {'key': 'production', 'protected': false},
  'flags': {
    'checkout': {
      'type': 'boolean',
      'value': true,
      'variation': variation,
      'reason': reason,
      'rule_id': '01JA',
      'automatic_exposure': automaticExposure,
      'override_influenced': override,
      'experiment': {
        'key': 'checkout_v2',
        'variation': variation,
        'unit_type': 'anonymous',
        'unit_id': 'd-8f1c',
        'inclusion_salt': 'a3f9c2e17b4d0e58',
        'variation_salt': '7e1d4c90b2a6f3d1',
        'inclusion_bucket': 3537,
        'variation_bucket': 2898,
        'override': override,
      },
    },
    'banner': {
      'type': 'string',
      'value': 'Try free',
      'variation': 'short',
      'reason': 'fallthrough',
      'automatic_exposure': false,
    },
  },
};

/// The 202 answer of the events endpoint.
Map<String, Object?> ingestAnswer({
  int accepted = 1,
  int duplicates = 0,
  List<Object?> rejected = const [],
}) => {
  'accepted': accepted,
  'duplicates': duplicates,
  'dropped_rows': 0,
  'rejected': rejected,
};

/// Answers [exchange] with a 202 the endpoint would send.
Future<void> acceptBatch(
  Exchange exchange, {
  List<Object?> rejected = const [],
}) => exchange.respond(
  202,
  body: jsonEncode(ingestAnswer(rejected: rejected)),
  headers: {'Content-Type': 'application/json'},
);

/// The events of an ingest envelope.
List<Map<String, Object?>> eventsOf(Exchange exchange) =>
    ((exchange.json! as Map<String, Object?>)['events']! as List<Object?>)
        .cast<Map<String, Object?>>();

/// The `event_id`s of an ingest envelope, in order.
List<Object?> eventIdsOf(Exchange exchange) => [
  for (final event in eventsOf(exchange)) event['event_id'],
];
