@TestOn('vm')
// The VM only: some scenarios need a `dart:io` mock server.
library;

// Local overrides: substitutions of flag
// values that never reach the platform. Exist only with
// ClientOptions.allowOverrides, refused for a protected environment, reason
// `local_override`, no experiment block, exposure a no-op, counted in
// summaries with no value, and reflected in FlagneticClient.allFlags for the
// Dev Tools screen.
import 'dart:convert';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

import 'support/mock_server.dart';

/// The context every client here is built with.
const testContext = FlagneticContext(units: {'anonymous': 'd-8f1c'});

/// A values document carrying [flags],
/// [protected] and [rulesetVersion].
Map<String, Object?> valuesDoc({
  bool protected = false,
  int rulesetVersion = 1,
  Map<String, Object?> flags = const {},
}) => {
  'schema_version': 1,
  'ruleset_version': rulesetVersion,
  'environment': {'key': 'production', 'protected': protected},
  'flags': flags,
};

Map<String, Object?> boolFlag(
  bool value, {
  String reason = 'targeting_match',
}) => {'type': 'boolean', 'value': value, 'variation': 'on', 'reason': reason};

Map<String, Object?> stringFlag(
  String value, {
  String reason = 'targeting_match',
}) => {'type': 'string', 'value': value, 'variation': 'a', 'reason': reason};

/// An offline client with local overrides enabled, serving [flags].
FlagneticClient overrideClient({
  Map<String, Object?> flags = const {},
  bool protected = false,
  bool allowOverrides = true,
  LogRecorder? logs,
}) => Flagnetic.client(
  clientKey,
  context: testContext,
  options: ClientOptions(
    allowOverrides: allowOverrides,
    offline: OfflineSource.document(
      valuesDoc(protected: protected, flags: flags),
    ),
    logger: logs?.call,
  ),
);

/// A client against a mock server, local overrides enabled, with the first
/// evaluate already answered. It never polls again and never flushes on its
/// own, so a test drives it.
Future<({FlagneticClient client, MockServer server, LogRecorder logs})>
servingOverrides(Map<String, Object?> flags, {bool protected = false}) async {
  final server = await MockServer.start();
  final logs = LogRecorder();
  final client = Flagnetic.client(
    clientKey,
    baseUrl: server.url,
    context: testContext,
    options: scaledOptions(
      logger: logs.call,
      pollInterval: const Duration(hours: 1),
      flushInterval: const Duration(hours: 1),
      platform: 'android',
      allowOverrides: true,
    ),
  );
  await (await server.next()).respondJson(
    valuesDoc(protected: protected, flags: flags),
  );
  await eventually(() => client.stage == Stage.fresh, reason: 'fresh values');
  return (client: client, server: server, logs: logs);
}

void main() {
  group('construction', () {
    test('off by default: the overrides throw a StateError', () {
      final logs = LogRecorder();
      final client = overrideClient(
        flags: {'checkout': boolFlag(false)},
        allowOverrides: false,
        logs: logs,
      );
      addTearDown(client.close);
      expect(() => client.overrides, throwsStateError);
      expect(client.getBool('checkout', false), isFalse);
      expect(logs.fieldsOf(LogEvents.overrideRefused), [
        {'cause': 'not_allowed'},
      ]);
    });

    test('on logs a warning at construction', () {
      final logs = LogRecorder();
      final client = overrideClient(logs: logs);
      addTearDown(client.close);
      expect(logs.count(LogEvents.overridesAllowed), 1);
      expect(logs.fieldsOf(LogEvents.overridesAllowed), [
        const <String, Object?>{},
      ]);
    });

    test('off logs nothing at construction', () {
      final logs = LogRecorder();
      final client = overrideClient(allowOverrides: false, logs: logs);
      addTearDown(client.close);
      expect(logs.count(LogEvents.overridesAllowed), 0);
    });

    test('the SDK key it was constructed with', () {
      final client = overrideClient();
      addTearDown(client.close);
      expect(client.sdkKey, clientKey);
    });
  });

  group('set, remove, clear, list', () {
    test('substitutes the value: reason local_override, no experiment '
        'block, stage unchanged, not platform-overridden', () {
      final client = overrideClient(flags: {'checkout': boolFlag(false)});
      addTearDown(client.close);
      expect(client.overrides.set('checkout', true), isTrue);
      final detail = client.getDetail('checkout', false);
      expect(detail.value, isTrue);
      expect(detail.reason, Reasons.localOverride);
      expect(detail.experiment, isNull);
      expect(detail.stage, Stage.fresh);
      expect(detail.overridden, isFalse);
      expect(detail.variation, isNull);
    });

    test('a key the held values do not carry takes the type of the value', () {
      final client = overrideClient();
      addTearDown(client.close);
      expect(client.overrides.set('new_flag', 'hello'), isTrue);
      expect(client.getString('new_flag', 'built-in'), 'hello');
    });

    test('set and remove fire the change callback', () {
      final client = overrideClient(flags: {'checkout': boolFlag(false)});
      addTearDown(client.close);
      final changes = <List<String>>[];
      client.onChange(changes.add);

      client.overrides.set('checkout', true);
      expect(changes, [
        ['checkout'],
      ]);
      expect(client.getBool('checkout', false), isTrue);

      client.overrides.remove('checkout');
      expect(changes, [
        ['checkout'],
        ['checkout'],
      ]);
      expect(client.getBool('checkout', false), isFalse);

      // Removing what is not there fires nothing.
      client.overrides.remove('checkout');
      expect(changes, hasLength(2));
    });

    test('clear drops every override and fires the changed keys sorted', () {
      final client = overrideClient(
        flags: {'checkout': boolFlag(false), 'banner': stringFlag('hi')},
      );
      addTearDown(client.close);
      client.overrides
        ..set('checkout', true)
        ..set('banner', 'bye');
      final changes = <List<String>>[];
      client.onChange(changes.add);

      client.overrides.clear();
      expect(changes, [
        ['banner', 'checkout'],
      ]);
      expect(client.overrides.list(), isEmpty);
      expect(client.getBool('checkout', false), isFalse);
      expect(client.getString('banner', ''), 'hi');

      // Clearing an empty set of overrides fires nothing.
      client.overrides.clear();
      expect(changes, hasLength(1));
    });

    test('list returns every override by key', () {
      final client = overrideClient();
      addTearDown(client.close);
      client.overrides
        ..set('a', true)
        ..set('b', 'x');
      expect(client.overrides.list(), {'a': true, 'b': 'x'});
    });

    test('a value of another type than the flag throws', () {
      final client = overrideClient(flags: {'checkout': boolFlag(false)});
      addTearDown(client.close);
      expect(
        () => client.overrides.set('checkout', 'not a bool'),
        throwsArgumentError,
      );
      expect(client.overrides.list(), isEmpty);
    });

    test('a getter of another type than the override serves the built-in '
        'default with fallback', () {
      final client = overrideClient();
      addTearDown(client.close);
      client.overrides.set('checkout', true);
      final detail = client.getDetail('checkout', 'default-string');
      expect(detail.value, 'default-string');
      expect(detail.reason, Reasons.fallback);
      expect(detail.errorCode, FallbackCodes.typeMismatch);
    });

    test('a JSON override is the caller\'s copy, both ways', () {
      final client = overrideClient();
      addTearDown(client.close);
      final value = {'a': 1};
      client.overrides.set('config', value);
      value['a'] = 2;
      expect(client.getJson('config', null), {'a': 1});
      (client.getJson('config', null)! as Map<String, Object?>)['a'] = 99;
      expect(client.getJson('config', null), {'a': 1});
    });
  });

  group('refused for a protected environment', () {
    test('set is refused when the held values already say protected', () {
      final logs = LogRecorder();
      final client = overrideClient(
        flags: {'checkout': boolFlag(false)},
        protected: true,
        logs: logs,
      );
      addTearDown(client.close);
      expect(client.overrides.set('checkout', true), isFalse);
      expect(client.getBool('checkout', false), isFalse);
      expect(logs.fieldsOf(LogEvents.overrideRefused), [
        {'flag': 'checkout', 'cause': 'protected_environment'},
      ]);
    });

    test('an existing override is dropped when the environment becomes '
        'protected', () {
      final logs = LogRecorder();
      var protected = false;
      final client = Flagnetic.client(
        clientKey,
        context: testContext,
        options: ClientOptions(
          allowOverrides: true,
          offline: OfflineSource.read(
            () => valuesDoc(
              protected: protected,
              flags: {'checkout': boolFlag(false)},
            ),
          ),
          logger: logs.call,
        ),
      );
      addTearDown(client.close);
      expect(client.overrides.set('checkout', true), isTrue);
      expect(client.getBool('checkout', false), isTrue);

      final changes = <List<String>>[];
      client.onChange(changes.add);
      protected = true;
      client.offline!.reload();

      expect(client.getBool('checkout', false), isFalse);
      expect(changes, [
        ['checkout'],
      ]);
      expect(client.overrides.list(), isEmpty);
      expect(logs.fieldsOf(LogEvents.overrideRefused), [
        {'flag': 'checkout', 'cause': 'protected_environment'},
      ]);
    });
  });

  group('exposure and summaries', () {
    test('exposure is a no-op for an overridden flag, logged at debug', () {
      final logs = LogRecorder();
      final client = overrideClient(
        flags: {'checkout': boolFlag(false)},
        logs: logs,
      );
      addTearDown(client.close);
      client.overrides.set('checkout', true);
      client.getBool('checkout', false);
      client.exposure('checkout');
      expect(client.offline!.recorded, isEmpty);
      expect(logs.fieldsOf(LogEvents.exposureSkipped), [
        {'flag': 'checkout', 'cause': 'local_override'},
      ]);
    });

    test('exposure is a no-op even without a prior getter call', () {
      final client = overrideClient(flags: {'checkout': boolFlag(false)});
      addTearDown(client.close);
      client.overrides.set('checkout', true);
      client.exposure('checkout');
      expect(client.offline!.recorded, isEmpty);
    });

    test('the evaluation is counted under local_override with no value', () {
      final client = overrideClient(flags: {'checkout': boolFlag(false)});
      addTearDown(client.close);
      client.overrides.set('checkout', true);
      client.getBool('checkout', false);
      expect(client.offline!.summary, [
        (
          flag: 'checkout',
          variation: null,
          reason: Reasons.localOverride,
          count: 1,
        ),
      ]);
    });
  });

  group('never reaches the platform (mock server)', () {
    test('the substituted value appears in no request body', () async {
      final run = await servingOverrides({'banner': stringFlag('Try free')});
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      const sentinel = 'SUBSTITUTED-SENTINEL-VALUE';
      expect(run.client.overrides.set('banner', sentinel), isTrue);
      expect(run.client.getString('banner', 'built-in'), sentinel);
      // A no-op: nothing about the override is queued.
      run.client.exposure('banner');

      final flushed = run.client.flush();
      final exchange = await run.server.next();
      expect(exchange.path, '/sdk/v1/events');
      // Only the evaluation summary rides along; no exposure event, and the
      // substituted value is nowhere in the request the SDK sent.
      final events = eventsOf(exchange);
      expect(events.map((e) => e['type']), ['evaluation_summary']);
      expect(utf8.decode(exchange.rawBody), isNot(contains(sentinel)));
      final counts = (events.single['counts']! as List<Object?>)
          .cast<Map<String, Object?>>();
      expect(counts, [
        {
          'flag': 'banner',
          'variation': null,
          'reason': 'local_override',
          'count': 1,
        },
      ]);
      await acceptBatch(exchange);
      await flushed;
    });

    test('an override refused for a protected environment is logged and '
        'never applied', () async {
      final run = await servingOverrides({
        'checkout': boolFlag(false),
      }, protected: true);
      addTearDown(() async {
        await run.client.close();
        await run.server.close();
      });

      expect(run.client.overrides.set('checkout', true), isFalse);
      expect(run.client.getBool('checkout', false), isFalse);
      expect(run.logs.fieldsOf(LogEvents.overrideRefused), [
        {'flag': 'checkout', 'cause': 'protected_environment'},
      ]);
    });
  });

  group('allFlags, for the Dev Tools screen', () {
    test('lists held flags in order, overrides applied, an override-only '
        'key included', () {
      final client = overrideClient(
        flags: {'checkout': boolFlag(false), 'banner': stringFlag('Try free')},
      );
      addTearDown(client.close);
      expect(client.allFlags.map((f) => f.key), ['banner', 'checkout']);

      client.overrides
        ..set('checkout', true)
        ..set('new_flag', 'hi');
      final byKey = {for (final f in client.allFlags) f.key: f};
      expect(byKey.keys, unorderedEquals(['banner', 'checkout', 'new_flag']));
      expect(byKey['checkout']!.value, isTrue);
      expect(byKey['checkout']!.reason, Reasons.localOverride);
      expect(byKey['checkout']!.overridden, isTrue);
      expect(byKey['banner']!.value, 'Try free');
      expect(byKey['banner']!.reason, 'targeting_match');
      expect(byKey['banner']!.overridden, isFalse);
      expect(byKey['new_flag']!.value, 'hi');
      expect(byKey['new_flag']!.overridden, isTrue);
    });

    test('empty at stage built_in', () {
      final client = Flagnetic.client('not-a-key', context: testContext);
      addTearDown(client.close);
      expect(client.allFlags, isEmpty);
    });
  });
}
