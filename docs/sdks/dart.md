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

The [packages' READMEs](../../sdks/dart/README.md) cover every option, the log events, the offline test mode, local overrides and the anonymous ID; [`flagnetic_flutter`'s](../../sdks/flutter/README.md) the widgets, the store and the Dev Tools screen.

## Propagation

| The app | A change reaches it |
|---|---|
| in the foreground on iOS, Android or desktop, holding the [ruleset stream](stream.md) (the default) | within 5 seconds at the 99th percentile |
| in the background | at its next resume, which refetches unless the last attempt was less than 10 seconds ago |
| with `ClientOptions(sse: false)`, or behind a proxy that blocks `text/event-stream` | within 5 minutes, its poll interval, and at every resume |
| on the web, where the stream is opt-in (`sse: true`) | within 5 minutes, and at once when the page is shown again or its network comes back |

The stream carries only the news that the ruleset changed: each app then waits a random 0–3 seconds before it evaluates, so the apps open at a kill switch do not all ask at once. The 99th percentile is what the end-to-end smoke measures on every pull request that changes the package.

A client key's limits count per key and client address on each replica (see [client keys behind a shared address](../operations.md#client-keys-behind-a-shared-address)): apps behind one NAT share 10 streams per replica by default, and an app refused a stream keeps retrying it and stays fresh within its poll interval meanwhile. One change makes each streaming app evaluate once, well within the 120 evaluates a minute an address may make.

## Compatibility

`flagnetic` 0.x speaks `/sdk/v1`, reads `schema_version` 1 and works with every Flagnetic server from 0.1.0 on; see the [compatibility table](compatibility.md). `flagnetic_flutter` makes no request of its own and follows the `flagnetic` it requires.

## How it is tested

- **Delivery and events**: every client-mode protocol scenario and degradation case against a mock server, on the VM for the device scenarios (foreground, background, the stream's jitter) and in Chrome for the page scenarios (visibility, the beacon on hide), the requests crossing origins; the [behaviour matrix](behaviour-matrix.md) shows which passed.
- **Flutter**: widget tests of the scope, the builder, the lifecycle observer, the store and the Dev Tools screen in debug and release builds, on the VM and in Chrome; and the package against the `flagnetic` released on pub.dev as well as the working tree.
- **Packages**: `dart pub publish --dry-run` and [pana](https://pub.dev/packages/pana) at full score on every pull request.
- **End to end**: on every pull request that changes `flagnetic`, against two replicas on one PostgreSQL brought up by [`flagnetic-e2e up`](../../e2e/README.md): the client reaches `fresh` and serves every seeded case with its client reason; a kill switch saved through one replica reaches four apps streaming from the other, twelve times over, within 5 seconds at the 99th percentile; the evaluates one change causes across twelve apps behind one address are recorded; the SDK appears in the inventory as `flagnetic-dart`; and it is refused once its key is revoked.

To run the smoke yourself, with Docker running:

```bash
go build -o /tmp/flagnetic-e2e ./e2e/cmd/flagnetic-e2e
rm -f /tmp/seed.json
FLAGNETIC_E2E_SEED=/tmp/seed.json /tmp/flagnetic-e2e up &
until [ -s /tmp/seed.json ]; do sleep 1; done # up writes the seed once both replicas serve
cd sdks/dart && FLAGNETIC_E2E_SEED=/tmp/seed.json FLAGNETIC_E2E_CMD=/tmp/flagnetic-e2e dart test test/e2e_test.dart
```
