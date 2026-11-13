// The getters that need no network: an offline client serves a document.
import 'dart:math';

import 'package:flagnetic/flagnetic.dart';
import 'package:test/test.dart';

Map<String, Object?> numberFlag(Object value) => {
  'type': 'number',
  'value': value,
  'variation': 'v',
  'reason': 'targeting_match',
};

FlagneticClient offlineClient(Map<String, Object?> flags) => Flagnetic.client(
  'fn_cli_TESTKEY0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  context: const FlagneticContext(units: {'anonymous': 'd-8f1c'}),
  options: ClientOptions(
    offline: OfflineSource.document({
      'schema_version': 1,
      'ruleset_version': 1,
      'flags': flags,
    }),
  ),
);

void main() {
  group('getInt', () {
    final flags = {
      'limit': numberFlag(25),
      'whole_double': numberFlag(3.0),
      'fraction': numberFlag(2.5),
      'huge': numberFlag(1e300),
      // Whole, but beyond what every platform holds exactly.
      'beyond_2_53': numberFlag(pow(2, 60)),
    };

    test('serves a whole number as an int', () {
      final client = offlineClient(flags);
      expect(client.getInt('limit', 7), 25);
      expect(client.getInt('whole_double', 7), 3);
      expect(client.getInt('whole_double', 7), isA<int>());
    });

    test('a number that is not whole serves the default as a mismatch', () {
      final client = offlineClient(flags);
      for (final key in ['fraction', 'huge', 'beyond_2_53']) {
        expect(client.getInt(key, 7), 7, reason: key);
        final detail = client.getDetail(key, 7);
        expect(detail.reason, Reasons.fallback, reason: key);
        expect(detail.errorCode, FallbackCodes.typeMismatch, reason: key);
      }
    });

    test('a double default reads a served fraction on every platform', () {
      // Under dart2js `1.0 is int`, so wholeness must come from the static
      // type: only an `int` default asks for an integer.
      final client = offlineClient(flags);
      expect(client.getDetail('fraction', 1.0).value, 2.5);
      expect(client.getDetail('fraction', 1.0).reason, Reasons.targetingMatch);
      expect(client.getDetail('fraction', 1.5 + 1.5).value, 2.5);
      expect(client.getDetail('fraction', 1.0 as num).value, 2.5);
      expect(client.getDetail('whole_double', 1.0).value, 3);
    });

    test('an int default still asks for a whole number', () {
      final client = offlineClient(flags);
      expect(client.getDetail('limit', 7).value, 25);
      expect(client.getDetail<int>('fraction', 7).value, 7);
    });

    test('getNumber still serves the fraction', () {
      expect(offlineClient(flags).getNumber('fraction', 0), 2.5);
    });

    test('a missing flag serves the default', () {
      expect(offlineClient(flags).getInt('absent', 7), 7);
    });
  });

  group('onChange', () {
    Map<String, Object?> flag({Map<String, Object?>? experiment}) => {
      'type': 'boolean',
      'value': true,
      'variation': experiment == null ? 'on' : 'control',
      'reason': experiment == null ? 'targeting_match' : 'experiment',
      'experiment': ?experiment,
    };
    Map<String, Object?> document(Map<String, Object?> checkout) => {
      'schema_version': 1,
      'ruleset_version': 1,
      'flags': {'checkout': checkout},
    };
    Map<String, Object?> experiment(String variation) => {
      'key': 'checkout_v2',
      'variation': variation,
      'unit_type': 'anonymous',
      'unit_id': 'd-8f1c',
      'inclusion_salt': 'a3f9c2e17b4d0e58',
      'variation_salt': '7e1d4c90b2a6f3d1',
      'inclusion_bucket': 3537,
      'variation_bucket': 2898,
    };

    test('an experiment starting on an unchanged value fires', () {
      var source = document(flag());
      final client = Flagnetic.client(
        'fn_cli_TESTKEY0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        context: const FlagneticContext(units: {'anonymous': 'd-8f1c'}),
        options: ClientOptions(offline: OfflineSource.read(() => source)),
      );
      final changes = <List<String>>[];
      client.onChange(changes.add);

      // The same value, now with a reason, a variation and an experiment
      // block: a reader of details has something new to read and a control
      // user's exposure becomes possible.
      source = document(flag(experiment: experiment('control')));
      client.offline!.reload();
      expect(changes, [
        ['checkout'],
      ]);
      expect(client.getDetail('checkout', false).variation, 'control');

      // Nothing served differs: no change.
      source = document(flag(experiment: experiment('control')));
      client.offline!.reload();
      expect(changes, hasLength(1));
    });
  });

  group('status', () {
    test('an offline client is fresh and not stale', () {
      final status = offlineClient({}).status();
      expect(status.stage, Stage.fresh);
      expect(status.staleSince, isNull);
    });
  });

  group('construction', () {
    test('a server key is refused', () {
      expect(
        () => Flagnetic.client(
          'fn_srv_TESTKEY0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
          context: const FlagneticContext(),
        ),
        throwsArgumentError,
      );
    });
  });
}
