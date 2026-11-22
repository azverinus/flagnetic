---
title: "Dart and Flutter SDK"
description: "The pub.dev packages flagnetic and flagnetic_flutter, the client mode on devices and the web, the Flutter widgets, and how soon each app serves a change."
---

# Dart and Flutter SDK

Two packages on pub.dev, each released on its own tag:

| Package | Directory | What it is | Needs |
|---|---|---|---|
| `flagnetic` | [`sdks/dart`](../../sdks/dart/README.md) | The client: remote evaluation with a client key, the fallback chain, events, the ruleset stream in the foreground, local overrides and an offline test mode, on the Dart VM and on the web | Dart ≥ 3.9, no dependency |
| `flagnetic_flutter` | [`sdks/flutter`](../../sdks/flutter/README.md) | Flutter widgets over it: `FlagneticScope`, `FlagBuilder`, flag and stage listenables, the lifecycle observer, `FlutterStore` and the Dev Tools screen. It re-exports `flagnetic` | Flutter ≥ 3.35, `path_provider` and `shared_preferences` |

```bash
flutter pub add flagnetic_flutter # a Flutter app
dart pub add flagnetic            # Dart without Flutter
```

Supported runtimes follow upstream support: Flutter's stable releases of about the last twelve months and the Dart each of them ships, on iOS, Android, desktop and the web. CI runs `flagnetic` on Dart 3.9.0 and the current stable, on the VM, in Chrome and in Chrome as WebAssembly, and `flagnetic_flutter` on Flutter 3.35.0 and the current stable, on the VM and in Chrome. A version that leaves upstream support is dropped in a minor release.

There is no OpenFeature provider for Dart yet.

## Quickstart

**Install.** Add the package to the app:

```bash
dart pub add flagnetic            # Dart without Flutter
flutter pub add flagnetic_flutter # a Flutter app
```

**Key.** Create a client key (`fn_cli_…`) for the environment in the console and pass it to the client; see [SDK keys](keys.md). It ships inside the app, so it can only learn the values served to the context you give it. A server key is refused with an `ArgumentError`.

**Evaluate, context, exposure and shutdown** in one program:

```dart
import 'package:flagnetic/flagnetic.dart';

Future<void> main() async {
  final client = Flagnetic.client(
    'fn_cli_…',
    baseUrl: Uri.parse('https://flags.example.com'),
    context: const FlagneticContext(units: {'anonymous': 'd-8f1c'}),
  );

  // Evaluate: getters are synchronous and never throw. Until the first answer
  // arrives they serve the default you pass.
  await client.ready(const Duration(seconds: 2));
  final showCheckout = client.getBool('checkout', false);
  print('checkout: $showCheckout');

  // Context: after sign-in, evaluate for the user too. The values held keep
  // serving until the new answer arrives.
  await client.setContext(
    const FlagneticContext(units: {'anonymous': 'd-8f1c', 'user': 'u-1001'}),
  );

  // Exposure: evaluating never records one. Read the flag where the user sees
  // what the experiment decided, then record that they saw it: the exposure
  // reports the most recent evaluation of the flag.
  if (client.getBool('checkout', false)) {
    client.exposure('checkout');
  }

  // Shutdown: sends what is queued, then releases the connections.
  await client.close();
}
```

In a Flutter app, hold the client in a `FlagneticScope` and read a flag with a `FlagBuilder`, which rebuilds when it changes:

```dart
import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter/material.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final store = FlutterStore();
  final client = Flagnetic.client(
    'fn_cli_…',
    baseUrl: Uri.parse('https://flags.example.com'),
    context: FlagneticContext(
      units: {'anonymous': await anonymousUnitId(store, 'anonymous')},
    ),
    options: ClientOptions(store: store),
  );
  runApp(FlagneticScope(client: client, child: const App()));
}

class App extends StatelessWidget {
  const App({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
    home: FlagBuilder<bool>(
      flagKey: 'checkout',
      defaultValue: false,
      builder: (context, showCheckout, detail) =>
          Text(showCheckout ? 'New checkout' : 'Basket'),
    ),
  );
}
```

The package's tests run the first program against a mock server and the second one in a widget test, so neither can drift from the API.

## Mode

The SDK runs in client mode only: it takes a client key (`fn_cli_…`), posts its context to [remote evaluation](evaluate.md) and serves every flag from the values it was answered, so it learns served values only, with reasons no finer than `targeting_match`. A server key is an `ArgumentError` at construction: it must never ship in an app. Getters are synchronous and never throw; a flag that cannot be served gives the default you pass, with reason `fallback`. Values, the event queue and anonymous unit IDs persist in the store, so a cold start offline serves the last good values and sends the events recorded offline later.

```dart
final client = Flagnetic.client(
  'fn_cli_…',
  baseUrl: Uri.parse('https://flags.example.com'),
  context: const FlagneticContext(units: {'user': 'u-1001'}),
  options: ClientOptions(store: FlutterStore()),
);
await client.ready(const Duration(seconds: 2)); // Stage.fresh, lastGood or builtIn
client.getBool('new-checkout', false);
```

A `Deprecation` header on any answer is logged once per operation, at warning level, as `operation_deprecated` with the operation (`evaluate`, `events` or `stream`) and the `Sunset` when the answer has one; the call proceeds as before. See [Compatibility and support](../compatibility.md#deprecation-and-removal).

The [packages' READMEs](../../sdks/dart/README.md) cover every option, the log events, the offline test mode, local overrides and the anonymous ID; [`flagnetic_flutter`'s](../../sdks/flutter/README.md) the widgets, the store and the Dev Tools screen.

## Propagation

| The app | A change reaches it |
|---|---|
| in the foreground on iOS, Android or desktop, holding the [ruleset stream](stream.md) (the default) | within 5 seconds at the 99th percentile |
| in the background | at its next resume, which refetches unless the last attempt was less than 10 seconds ago |
| with `ClientOptions(stream: false)`, or behind a proxy that blocks `text/event-stream` | within 5 minutes, its poll interval, and at every resume |
| on the web, where the stream is opt-in (`stream: true`) | within 5 minutes, and at once when the page is shown again or its network comes back |

The stream carries only the news that the ruleset changed: each app then waits a random 0–3 seconds before it evaluates, so the apps open at a kill switch do not all ask at once. The 99th percentile is what the end-to-end smoke measures on every pull request that changes the package.

A client key's limits count per key and client address on each replica (see [client keys behind a shared address](../operations.md#client-keys-behind-a-shared-address)): apps behind one NAT share 10 streams per replica by default, and an app refused a stream keeps retrying it and stays fresh within its poll interval meanwhile. One change makes each streaming app evaluate once, well within the 120 evaluates a minute an address may make.

## Compatibility

`flagnetic` 0.x speaks `/sdk/v1`, reads `schema_version` 1 and works with every Flagnetic server from 0.1.0 on; see the [compatibility table](compatibility.md). `flagnetic_flutter` makes no request of its own and follows the `flagnetic` it requires.

## How it is tested

- **Delivery and events**: every client-mode protocol scenario and degradation case against a mock server, on the VM for the device scenarios (foreground, background, the stream's jitter) and in Chrome for the page scenarios (visibility, the beacon on hide), the requests crossing origins; the [behaviour matrix](behaviour-matrix.md) shows which passed.
- **Flutter**: widget tests of the scope, the builder, the lifecycle observer, the store and the Dev Tools screen in debug and release builds, on the VM and in Chrome; and the package against the `flagnetic` released on pub.dev as well as the working tree.
- **Packages**: `dart pub publish --dry-run` without a warning and [pana](https://pub.dev/packages/pana) at full score on every pull request that changes the package; `flagnetic_flutter` is checked against the `flagnetic` released on pub.dev, so once `flagnetic` is on pub.dev.
- **End to end**: on every pull request that changes `flagnetic`, against two replicas on one PostgreSQL brought up by [`flagnetic-e2e up`](../../e2e/README.md): the client reaches `fresh` and serves every seeded case with its client reason; a kill switch saved through one replica reaches four apps streaming from the other, twelve times over, within 5 seconds at the 99th percentile; the evaluates one change causes across twelve apps behind one address are recorded; the SDK appears in the inventory as `flagnetic-dart`; and it is refused once its key is revoked.

To run the smoke yourself, with Docker running:

```bash
go build -o /tmp/flagnetic-e2e ./e2e/cmd/flagnetic-e2e
rm -f /tmp/seed.json
FLAGNETIC_E2E_SEED=/tmp/seed.json /tmp/flagnetic-e2e up &
until [ -s /tmp/seed.json ]; do sleep 1; done # up writes the seed once both replicas serve
cd sdks/dart && FLAGNETIC_E2E_SEED=/tmp/seed.json FLAGNETIC_E2E_CMD=/tmp/flagnetic-e2e dart test test/e2e_test.dart
```
