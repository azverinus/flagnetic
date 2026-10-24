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

    test('getNumber still serves the fraction', () {
      expect(offlineClient(flags).getNumber('fraction', 0), 2.5);
    });

    test('a missing flag serves the default', () {
      expect(offlineClient(flags).getInt('absent', 7), 7);
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
