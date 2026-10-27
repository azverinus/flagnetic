// A client that serves a flag from the values remote evaluation returned
// for its context, records an exposure and closes. In a Flutter app,
// flagnetic_flutter adds widgets, the app lifecycle and a store.
import 'package:flagnetic/flagnetic.dart';

Future<void> main() async {
  final client = Flagnetic.client(
    'fn_cli_…',
    baseUrl: Uri.parse('https://flags.example.com'),
    context: const FlagneticContext(units: {'user': 'u-1001'}),
    options: const ClientOptions(platform: 'android', appVersion: '1.42.0'),
  );

  // Getters are synchronous and never throw: until the values arrive, the
  // built-in default given here is served.
  await client.ready(const Duration(seconds: 2));
  final checkout = client.getDetail('new-checkout', false);
  print('new-checkout: ${checkout.value} (${checkout.reason})');

  client.onChange((keys) => print('changed: $keys'));
  client.exposure('new-checkout');
  await client.close();
}
