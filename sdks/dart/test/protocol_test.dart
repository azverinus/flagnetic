@TestOn('vm')
// The VM only: this suite needs a `dart:io` mock server.
library;

// The protocol fixtures of conformance/protocol run against a mock server:
// the degradation cases of the evaluate and events endpoints and every
// client-mode scenario of the lists, with no skip list. Every duration of a
// scenario is divided by 1000 (the README allows one factor for real-time
// runs).
import 'dart:async';
import 'dart:convert';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

import 'support/mock_server.dart';

/// Scenario durations in milliseconds become microseconds.
Duration scaled(Object? ms) => Duration(microseconds: (ms! as num).toInt());

/// Scheduling latency allowed above a window's upper bound.
const latency = Duration(milliseconds: 150);

/// The path of the ingest endpoint.
const eventsPath = '/sdk/v1/events';

/// The path of the remote evaluation endpoint.
const evaluatePath = '/sdk/v1/evaluate';

void main() {
  final cases = protocolJson('degradation/cases.json')! as Map<String, Object?>;
  final caseContext = contextOf(cases['context']);
  group('degradation cases of POST /sdk/v1/evaluate', () {
    for (final c
        in (cases['cases']! as List<Object?>)
            .cast<Map<String, Object?>>()
            .where((c) => c['endpoint'] == 'POST /sdk/v1/evaluate')) {
      test(c['name'], () => runCase(c, caseContext));
    }
  });
  group('degradation cases of POST /sdk/v1/events', () {
    for (final c
        in (cases['cases']! as List<Object?>)
            .cast<Map<String, Object?>>()
            .where((c) => c['endpoint'] == 'POST /sdk/v1/events')) {
      test(c['name'], () => runEventsCase(c, caseContext));
    }
  });
  // The ruleset endpoint serves server keys only; every other case is a
  // client-mode case this runner plays.
  test('every degradation case is played or belongs to server mode', () {
    for (final c
        in (cases['cases']! as List<Object?>).cast<Map<String, Object?>>()) {
      expect(
        ['POST /sdk/v1/evaluate', 'POST /sdk/v1/events', 'GET /sdk/v1/ruleset'],
        contains(c['endpoint']),
        reason: '${c['name']}',
      );
    }
  });

  // Every runner appends the scenarios of the other lists to those of
  // scenarios.json and merges their answers into its own.
  final doc = protocolJson('scenarios.json')! as Map<String, Object?>;
  final all = (doc['scenarios']! as List<Object?>).cast<Map<String, Object?>>();
  final answers = doc['answers']! as Map<String, Object?>;
  for (final name in const [
    'versioning-scenarios.json',
    'experiment-scenarios.json',
  ]) {
    final extra = protocolJson(name)! as Map<String, Object?>;
    all.addAll((extra['scenarios']! as List<Object?>).cast());
    answers.addAll((extra['answers'] as Map<String, Object?>?) ?? const {});
  }
  final scenarios = all.where((s) => s['mode'] == 'client').toList();
  group('client scenarios', () {
    for (final s in scenarios) {
      test(
        s['name'],
        () => runScenario(s, doc),
        timeout: const Timeout.factor(4),
      );
    }
  });
  test('every scenario is a client or a server scenario, once', () {
    expect(scenarios, isNotEmpty);
    for (final s in all) {
      expect(['client', 'server'], contains(s['mode']), reason: '${s['name']}');
    }
    expect(
      {for (final s in scenarios) s['name']},
      hasLength(scenarios.length),
      reason: 'scenario names are unique',
    );
  });
}

/// The SDK under test, seen through what a scenario asks of it.
abstract interface class Sdk {
  /// The stage of the fallback chain.
  Stage get stage;

  /// When the held document became stale.
  DateTime? get staleSince;

  /// The detail getter.
  Detail detail(String flag, Object? defaultValue);

  /// The typed getter of [type].
  Object? typed(String type, String flag, Object? defaultValue);

  /// Sets a local override, answering whether it took effect.
  bool setOverride(String flag, Object? value);

  /// The exposure call.
  void exposure(String flag);

  /// The alias call of the scenarios: `d-8f1c` to `u-1001`.
  void alias();

  /// Flushes.
  Future<void> flush();

  /// What an offline client recorded.
  List<Map<String, Object?>> get recorded;

  /// Closes.
  Future<void> close();
}

final class ClientSdk implements Sdk {
  ClientSdk(this.client);

  final FlagneticClient client;

  @override
  Stage get stage => client.stage;
  @override
  DateTime? get staleSince => client.status().staleSince;
  @override
  Detail detail(String flag, Object? defaultValue) =>
      client.getDetail(flag, defaultValue);
  @override
  Object? typed(String type, String flag, Object? def) => switch (type) {
    'boolean' => client.getBool(flag, def! as bool),
    'string' => client.getString(flag, def! as String),
    'number' => client.getNumber(flag, def! as num),
    _ => client.getJson(flag, def),
  };
  @override
  bool setOverride(String flag, Object? value) {
    // Without the option the overrides refuse by throwing.
    try {
      return client.overrides.set(flag, value);
    } on StateError {
      return false;
    }
  }

  @override
  void exposure(String flag) => client.exposure(flag);
  @override
  void alias() => client.alias(
    fromUnitType: 'anonymous',
    fromUnitId: 'd-8f1c',
    toUnitType: 'user',
    toUnitId: 'u-1001',
  );
  @override
  Future<void> flush() => client.flush();
  @override
  List<Map<String, Object?>> get recorded => client.offline?.recorded ?? [];
  @override
  Future<void> close() => client.close();
}

/// Checks that [sdk] serves each answer of a protocol fixture: `flag`,
/// `type`, `default`, and the `value`, `variation` and `reason` expected,
/// through the detail getter and the typed one.
void expectSdkAnswers(Sdk sdk, List<Object?> answers) {
  for (final answer in answers.cast<Map<String, Object?>>()) {
    final flag = answer['flag']! as String;
    final def = answer['default'];
    final detail = sdk.detail(flag, def);
    expect(
      [detail.value, detail.variation, detail.reason],
      [answer['value'], answer['variation'], answer['reason']],
      reason: 'answer for $flag',
    );
    expect(
      sdk.typed(answer['type']! as String, flag, def),
      answer['value'],
      reason: 'typed getter for $flag',
    );
  }
}

/// True when [sdk] serves every answer (see [expectSdkAnswers]).
bool servesSdkAnswers(Sdk sdk, List<Object?> answers) {
  try {
    expectSdkAnswers(sdk, answers);
    return true;
  } on TestFailure {
    return false;
  }
}

Future<void> runCase(Map<String, Object?> c, FlagneticContext context) async {
  final server = await MockServer.start();
  final logs = LogRecorder();
  final Sdk sdk = ClientSdk(
    Flagnetic.client(
      clientKey,
      baseUrl: server.url,
      context: context,
      options: scaledOptions(
        logger: logs.call,
        pollInterval: const Duration(milliseconds: 10),
      ),
    ),
  );
  addTearDown(() async {
    await sdk.close();
    await server.close();
  });
  final held = protocolJson(c['held']! as String)! as Map<String, Object?>;
  final first = await server.next(path: evaluatePath);
  await first.respondJson(held);
  await eventually(() => sdk.stage == Stage.fresh, reason: 'held');

  final response = c['response']! as Map<String, Object?>;
  final body = response['body'] as String?;
  await (await server.next(path: evaluatePath)).respond(
    response['status']! as int,
    headers: (response['headers']! as Map<String, Object?>)
        .cast<String, String>(),
    body: body == null ? null : protocolBytes(body),
  );
  final keep = c['outcome'] == 'keep_held';
  final answers = c['answers']! as List<Object?>;
  await eventually(
    () =>
        (keep ? logs.count(LogEvents.fetchFailed) == 1 : true) &&
        sdk.stage == (keep ? Stage.lastGood : Stage.fresh) &&
        servesSdkAnswers(sdk, answers),
    reason: c['outcome']! as String,
  );
  expectSdkAnswers(sdk, answers);
  expect(sdk.staleSince, keep ? isNotNull : isNull);
}

/// Plays a degradation case of the events endpoint: a batch answered with
/// the case's response is never sent again, whatever the answer rejects.
Future<void> runEventsCase(
  Map<String, Object?> c,
  FlagneticContext context,
) async {
  expect(c['outcome'], 'never_retry_rejected');
  final server = await MockServer.start();
  final logs = LogRecorder();
  final client = Flagnetic.client(
    clientKey,
    baseUrl: server.url,
    context: context,
    options: scaledOptions(
      logger: logs.call,
      pollInterval: const Duration(hours: 1),
      sendSummaries: false,
    ),
  );
  addTearDown(() async {
    await client.close();
    await server.close();
  });
  await (await server.next(
    path: evaluatePath,
  )).respondJson(protocolJson('server/evaluation.json'));
  await eventually(() => client.stage == Stage.fresh, reason: 'fresh');

  void link(String userId) => client.alias(
    fromUnitType: 'anonymous',
    fromUnitId: 'd-8f1c',
    toUnitType: 'user',
    toUnitId: userId,
  );

  link('u-1001');
  final flushed = client.flush();
  final first = await server.next(path: eventsPath);
  final ids = eventIdsOf(first);
  final response = c['response']! as Map<String, Object?>;
  await first.respond(
    response['status']! as int,
    headers: (response['headers']! as Map<String, Object?>)
        .cast<String, String>(),
    body: protocolBytes(response['body']! as String),
  );
  await flushed;
  expect(logs.count(LogEvents.eventsFailed), 0, reason: 'the batch landed');

  link('u-1002');
  final again = client.flush();
  final second = await server.next(path: eventsPath);
  expect(
    eventIdsOf(second).toSet().intersection(ids.toSet()),
    isEmpty,
    reason: c['outcome']! as String,
  );
  await acceptBatch(second);
  await again;
}

Future<void> runScenario(
  Map<String, Object?> scenario,
  Map<String, Object?> doc,
) async {
  const sdkKey = clientKey;
  const fetchPath = evaluatePath;
  final given = scenario['given']! as Map<String, Object?>;
  final options = given['options']! as Map<String, Object?>;
  final context = contextOf(doc['context']);
  final answers = doc['answers']! as Map<String, Object?>;
  final stored = given['stored'] as String?;
  final offline = given['offline'] as String?;
  final steps = (scenario['steps']! as List<Object?>)
      .cast<Map<String, Object?>>();
  // A scenario asserting the exact rows of an evaluation summary cannot also
  // assert `answers_of`: calling every getter of the answers is itself
  // evaluation, and the fixture's counts hold only the scenario's own
  // evaluate steps, so honouring both at once cannot pass.
  final countsAsserted = steps.any(
    (step) => _events(step)?.containsKey('summary_counts') ?? false,
  );
  final assertsEvents = steps.any((step) => _path(step) == eventsPath);
  final lastFetch = steps.lastIndexWhere((step) => _path(step) == fetchPath);

  final storedDoc = stored == null
      ? null
      : protocolJson(stored)! as Map<String, Object?>;
  final store = storedDoc == null
      ? InMemoryStore()
      : await storeHolding(storedDoc, context);
  final server = await MockServer.start();
  final logs = LogRecorder();
  final offlineSource = offline == null
      ? null
      : OfflineSource.document(protocolJson(offline)! as Map<String, Object?>);
  final flushInterval = options['flush_interval_ms'] == null
      ? const Duration(hours: 1)
      : scaled(options['flush_interval_ms']);
  final Sdk sdk = ClientSdk(
    Flagnetic.client(
      clientKey,
      baseUrl: server.url,
      context: context,
      options: scaledOptions(
        store: store,
        logger: logs.call,
        pollInterval: scaled(options['poll_interval_ms']),
        sse: options['sse'] == true,
        offline: offlineSource,
        flushInterval: flushInterval,
        sendExposures: options['send_exposures'] != false,
        sendSummaries: options['send_summaries'] == true,
        allowOverrides: options['allow_overrides'] == true,
      ),
    ),
  );
  addTearDown(() async {
    await sdk.close();
    await server.close();
  });

  // Delays count from the previous answer: the SDK's timers start when it
  // reads one.
  var stepEnd = DateTime.now();
  OpenStream? stream;
  // The event IDs of the batch each step received, which the retry policy
  // compares across steps.
  final eventIds = <int, List<Object?>>{};
  Detail? lastResult;
  bool? lastRefused;
  for (final (i, step) in steps.indexed) {
    final label = 'step $i${step['note'] == null ? '' : ' (${step['note']})'}';
    if (step['request'] case final Map<String, Object?> request) {
      final window = request['delay_ms'] as Map<String, Object?>?;
      final max = window == null ? Duration.zero : scaled(window['max']);
      final exchange = await server.next(
        path: request['path'] as String?,
        timeout: max + latency * 10,
      );
      final waited = exchange.arrivedAt.difference(stepEnd);
      expect(exchange.method, request['method'], reason: label);
      expect(exchange.path, request['path'], reason: label);
      (request['headers'] as Map<String, Object?>? ?? const {}).forEach((
        name,
        value,
      ) {
        expect(
          exchange.headers[name.toLowerCase()],
          (value! as String).replaceAll('<sdk key>', sdkKey),
          reason: '$label: $name',
        );
      });
      for (final name
          in (request['absent_headers'] as List<Object?>? ?? const [])
              .cast<String>()) {
        expect(
          exchange.headers.containsKey(name.toLowerCase()),
          isFalse,
          reason: '$label: $name is absent',
        );
      }
      if (window != null) {
        expect(
          waited.inMicroseconds,
          inInclusiveRange(
            (scaled(window['min']) - const Duration(milliseconds: 1))
                .inMicroseconds,
            (max + latency).inMicroseconds,
          ),
          reason: '$label: delay',
        );
      }
      if (request['events'] case final Map<String, Object?> events) {
        eventIds[i] = checkBatch(exchange, events, eventIds, label);
      }
      final respond = step['respond']! as Map<String, Object?>;
      stepEnd = DateTime.now();
      if (respond['network_error'] == true) {
        await exchange.dropConnection();
      } else if (respond['stream'] == true) {
        stream = await exchange.respondStream(
          headers: (respond['headers']! as Map<String, Object?>)
              .cast<String, String>(),
        );
      } else {
        final body = respond['body'] as String?;
        final headers =
            (respond['headers'] as Map<String, Object?>? ?? const {})
                .cast<String, String>();
        final wasFresh = sdk.stage == Stage.fresh;
        await exchange.respond(
          respond['status']! as int,
          headers: headers,
          body: body == null ? null : protocolBytes(body),
        );
        // The first values are read asynchronously. A step that calls the SDK
        // next, without an expectation of its own, must find them held.
        final callNext =
            i + 1 < steps.length && steps[i + 1].containsKey('call');
        if (!wasFresh &&
            callNext &&
            step['expect'] == null &&
            respond['status'] == 200 &&
            request['path'] == fetchPath) {
          await eventually(
            () => sdk.stage == Stage.fresh,
            reason: '$label: the first values are accepted',
          );
        }
        // A scenario that scripts ingest requests runs long enough for the
        // SDK's own safety poll to interleave. It is answered with the
        // document the client already holds, which changes nothing the
        // scenario asserts; the alternative is an unanswered request that
        // stalls the client.
        if (assertsEvents && i == lastFetch && body != null) {
          final bytes = protocolBytes(body);
          final status = respond['status']! as int;
          server.autoRespond(
            fetchPath,
            (poll) => poll.respond(status, headers: headers, body: bytes),
          );
        }
      }
    }
    if (step['call'] case final Map<String, Object?> call) {
      if (call['op'] == 'set_override') {
        lastRefused = !sdk.setOverride(call['flag']! as String, call['value']);
      } else {
        for (var n = 0; n < ((call['times'] as int?) ?? 1); n++) {
          lastResult = makeCall(sdk, call, label);
        }
      }
    }
    if (step['stream_send'] case final String bytes) {
      await (stream ?? fail('$label: no stream is open')).send(bytes);
      stepEnd = DateTime.now();
    }
    if (step['stream_close'] == true) {
      await (stream ?? fail('$label: no stream is open')).closeStream();
      stream = null;
      stepEnd = DateTime.now();
    }
    if (step['advance_ms'] case final Object ms) {
      await Future<void>.delayed(scaled(ms));
      stepEnd = DateTime.now();
    }
    if (step['no_request'] case final Map<String, Object?> none) {
      final path = none['path'] as String?;
      int sent() => path == null
          ? server.received.length
          : server.received.where((e) => e.path == path).length;
      final before = sent();
      await Future<void>.delayed(scaled(none['for_ms']));
      expect(sent(), before, reason: '$label: no request to ${path ?? 'any'}');
    }
    if (step['expect'] case final Map<String, Object?> expected) {
      await eventually(
        () =>
            _holds(expected, sdk, logs, answers, checkAnswers: !countsAsserted),
        reason: '$label: $expected',
      );
      if (countsAsserted) {
        // Calling every getter would count evaluations the fixture's rows
        // do not hold.
      } else if (expected['answers_of'] case final String of) {
        expectSdkAnswers(sdk, answers[of]! as List<Object?>);
      }
      if (expected['result'] case final Map<String, Object?> want) {
        final result = lastResult ?? fail('$label: no call to check');
        expect(
          [result.value, result.variation, result.reason],
          [want['value'], want['variation'], want['reason']],
          reason: '$label: result',
        );
      }
      if (expected['refused'] case final bool want) {
        expect(
          lastRefused ?? fail('$label: no set_override call to check'),
          want,
          reason: '$label: refused',
        );
      }
    }
  }
  if (offline != null) expect(server.received, isEmpty);
  // Nothing the script sent for may be left unanswered. A request the SDK
  // sends on its own schedule once the script is over (a retry after a
  // last step that failed, 0 to 1 scaled seconds later) is not the
  // script's.
  expect(server.pendingAt(stepEnd), 0);
}

/// Performs one step's call on the SDK's API, answering with the detail an
/// `evaluate` call served.
Detail? makeCall(Sdk sdk, Map<String, Object?> call, String label) {
  switch (call['op']) {
    case 'evaluate':
      // One getter per call: calling the typed one too would count the
      // evaluation twice in the evaluation summary.
      return sdk.detail(call['flag']! as String, call['default']);
    case 'exposure':
      sdk.exposure(call['flag']! as String);
    case 'alias':
      sdk.alias();
    case 'flush':
      // Not awaited: the batch it posts is answered by a later step, so
      // waiting here would deadlock the runner against its own script.
      unawaited(sdk.flush());
    default:
      fail('$label: unknown call ${call['op']}');
  }
  return null;
}

/// Asserts what an ingest batch carries: its event types in order, its
/// event IDs against another step's, the rows and ruleset version of its
/// evaluation summary, and the strings that may appear nowhere in the body.
/// Answers the batch's event IDs.
List<Object?> checkBatch(
  Exchange exchange,
  Map<String, Object?> want,
  Map<int, List<Object?>> seen,
  String label,
) {
  final envelope = exchange.json! as Map<String, Object?>;
  final events = (envelope['events']! as List<Object?>)
      .cast<Map<String, Object?>>();
  final ids = [for (final event in events) event['event_id']];
  if (want['types'] case final List<Object?> types) {
    expect(
      [for (final event in events) event['type']],
      types,
      reason: '$label: event types',
    );
  }
  if (want['same_ids_as_step'] case final int other) {
    expect(
      ids,
      seen[other] ?? fail('$label: step $other carried no batch'),
      reason: '$label: the same batch, unchanged',
    );
  }
  if (want['excludes_ids_of_step'] case final int other) {
    final before = seen[other] ?? fail('$label: step $other carried no batch');
    expect(
      ids.toSet().intersection(before.toSet()),
      isEmpty,
      reason: '$label: an answered event is never sent again',
    );
  }
  if (want['summary_counts'] case final List<Object?> counts) {
    final summary = events.singleWhere(
      (event) => event['type'] == 'evaluation_summary',
      orElse: () => fail('$label: no evaluation summary in the batch'),
    );
    expect(
      summary['counts'],
      unorderedEquals(counts),
      reason: '$label: summary counts',
    );
    if (want['summary_ruleset_version'] case final Object version) {
      expect(
        summary['ruleset_version'],
        version,
        reason: '$label: summary ruleset version',
      );
    }
  }
  final body = utf8.decode(exchange.rawBody);
  for (final absent
      in (want['absent_strings'] as List<Object?>? ?? const [])
          .cast<String>()) {
    expect(body, isNot(contains(absent)), reason: '$label: $absent is absent');
  }
  return ids;
}

/// The request of [step], when it has one.
Map<String, Object?>? _request(Map<String, Object?> step) =>
    step['request'] as Map<String, Object?>?;

/// The path [step] expects a request to, when it expects one.
String? _path(Map<String, Object?> step) => _request(step)?['path'] as String?;

/// The `events` expectations of [step], when it has them.
Map<String, Object?>? _events(Map<String, Object?> step) =>
    _request(step)?['events'] as Map<String, Object?>?;

bool _holds(
  Map<String, Object?> expected,
  Sdk client,
  LogRecorder logs,
  Map<String, Object?> answers, {
  required bool checkAnswers,
}) {
  if (expected['stage'] case final String stage
      when client.stage.wireName != stage) {
    return false;
  }
  switch (expected['stale_since']) {
    case 'set' when client.staleSince == null:
    case 'unset' when client.staleSince != null:
      return false;
  }
  if (expected['logs'] case final Map<String, Object?> counts) {
    for (final MapEntry(:key, :value) in counts.entries) {
      if (logs.count(key) != value) return false;
    }
  }
  if (expected['recorded'] case final Map<String, Object?> recorded) {
    final events = client.recorded;
    for (final MapEntry(:key, :value) in recorded.entries) {
      if (events.where((e) => e['type'] == key).length != value) return false;
    }
  }
  if (!checkAnswers) return true;
  if (expected['answers_of'] case final String of) {
    return servesSdkAnswers(client, answers[of]! as List<Object?>);
  }
  return true;
}
