// What a test scripts against a mock server, and the helpers that need no
// `dart:io`, so the same scripts run on the VM and in a browser.
import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

/// A client key as the platform issues them.
const clientKey = 'fn_cli_TESTKEY0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/// The store keys [clientKey] writes; another key writes its own.
final clientKeys = StoreKeys(clientKey);

/// The path of the evaluate endpoint.
const evaluatePath = '/sdk/v1/evaluate';

/// The documents of conformance/protocol, by path.
abstract interface class Fixtures {
  /// The decoded JSON of the file at [path].
  Object? json(String path);

  /// The bytes of the file at [path].
  List<int> bytes(String path);
}

/// Fixtures read once and held in memory, for a browser, which cannot read
/// the checkout.
final class HeldFixtures implements Fixtures {
  /// The fixtures in [files], text by path.
  HeldFixtures(this.files);

  /// Every file, by path.
  final Map<String, String> files;

  String _file(String path) =>
      files[path] ?? (throw ArgumentError.value(path, 'path', 'no fixture'));

  @override
  Object? json(String path) => jsonDecode(_file(path));

  @override
  List<int> bytes(String path) => utf8.encode(_file(path));
}

/// The headers a mock change stream answers with by default.
const eventStreamHeaders = {
  'Content-Type': 'text/event-stream',
  'Cache-Control': 'no-cache',
  'X-Accel-Buffering': 'no',
};

/// A server that holds every request until the test answers it.
abstract interface class ScriptedServer {
  /// The base URL SDKs are built with.
  Uri get url;

  /// The next request not yet taken, to [path] when one is named, waiting
  /// up to [timeout].
  Future<ScriptedExchange> next({String? path, Duration timeout});

  /// Answers every request to [path] that no [next] is waiting for with
  /// [status], [headers] and [body].
  Future<void> answerAll(
    String path,
    int status, {
    Map<String, String> headers,
    List<int>? body,
  });

  /// How many requests arrived, to [path] when one is named.
  Future<int> countReceived({String? path});

  /// How many requests that arrived no later than [time] are not taken yet.
  Future<int> unansweredAt(DateTime time);

  /// Answers whatever is still open and stops.
  Future<void> close();
}

/// One request the SDK sent, held open until the test answers it.
abstract interface class ScriptedExchange {
  /// The request method.
  String get method;

  /// The request path.
  String get path;

  /// The query parameters.
  Map<String, String> get query;

  /// Request headers, lower-case names.
  Map<String, String> get headers;

  /// The body as sent.
  List<int> get rawBody;

  /// When the request arrived.
  DateTime get arrivedAt;

  /// The body as decoded JSON.
  Object? get json;

  /// Answers with [status], [headers] and a [body] of bytes or a string.
  Future<void> respond(int status, {Object? body, Map<String, String> headers});

  /// Answers 200 with a JSON document.
  Future<void> respondJson(Object? document);

  /// Answers 200 with an open `text/event-stream` the test writes to.
  Future<ScriptedStream> respondStream({Map<String, String> headers});

  /// Drops the connection without an answer.
  Future<void> dropConnection();
}

/// A change stream the test holds open and writes to.
abstract interface class ScriptedStream {
  /// Writes [bytes] and flushes, so the SDK reads them at once.
  Future<void> send(String bytes);

  /// Whether the SDK ends the connection within [timeout].
  Future<bool> closedBySdk(Duration timeout);

  /// Ends the stream from the server's side.
  Future<void> closeStream();
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
  bool? sse = false,
  Duration rulesetEventJitter = Duration.zero,
  Random? random,
  DateTime Function()? clock,
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
  rulesetEventJitter: rulesetEventJitter,
  random: random,
  clock: clock,
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
      'reason': 'targeting_match',
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
  ScriptedExchange exchange, {
  List<Object?> rejected = const [],
}) => exchange.respond(
  202,
  body: jsonEncode(ingestAnswer(rejected: rejected)),
  headers: {'Content-Type': 'application/json'},
);

/// The events of an ingest envelope.
List<Map<String, Object?>> eventsOf(ScriptedExchange exchange) =>
    ((exchange.json! as Map<String, Object?>)['events']! as List<Object?>)
        .cast<Map<String, Object?>>();

/// The `event_id`s of an ingest envelope, in order.
List<Object?> eventIdsOf(ScriptedExchange exchange) => [
  for (final event in eventsOf(exchange)) event['event_id'],
];
