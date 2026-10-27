# flagnetic

Feature flags and experiments for Dart and Flutter apps, served by a self-hosted [Flagnetic](https://github.com/azverinus/flagnetic) instance. This package is the client mode: it runs on the Dart VM and has no dependency, Flutter included. A Flutter app adds [`flagnetic_flutter`](https://pub.dev/packages/flagnetic_flutter) for widgets that rebuild when a flag changes, the app lifecycle, a store on every platform and a Dev Tools screen.

```bash
dart pub add flagnetic            # Dart without Flutter
flutter pub add flagnetic_flutter # a Flutter app: it brings this package
```

Requires Dart 3.9 or newer, on iOS, Android, desktop and the web, with no dependency. Supported versions follow Flutter's stable releases of about the last twelve months and the Dart each ships; a version that leaves upstream support is dropped in a minor release. There is no OpenFeature provider for Dart yet.

A client posts its context to remote evaluation with a **client key** and serves every flag from memory, so getters are synchronous and never throw. The values it last received are kept in a store on the device, so a cold start without a network serves them. A server key cannot build a client: it must never ship in an app.

```dart
import 'package:flagnetic/flagnetic.dart';

final client = Flagnetic.client(
  'fn_cli_…',
  baseUrl: Uri.parse('https://flags.example.com'),
  context: const FlagneticContext(units: {'anonymous': 'd-8f1c'}),
  options: ClientOptions(
    store: myStore, // a FlagneticStore: get, set and remove by key
    platform: 'android',
    appVersion: '1.42.0',
  ),
);

await client.ready(const Duration(seconds: 2));
final showCheckout = client.getBool('checkout', false);
final limit = client.getInt('upload_limit', 10);
client.onChange((keys) => rebuild(keys));

// After sign-in, keep the anonymous unit beside the user unit.
await client.setContext(
  const FlagneticContext(units: {'anonymous': 'd-8f1c', 'user': 'u-1001'}),
);

// Record that the user saw what an experiment decided.
client.exposure('checkout');
client.alias(
  fromUnitType: 'anonymous',
  fromUnitId: 'd-8f1c',
  toUnitType: 'user',
  toUnitId: 'u-1001',
);
await client.close();
```

## The fallback chain

Values come from, in order: the fresh response, the last good values in the store, the built-in defaults you pass to the getter. `client.stage` and `client.status()` tell which one is serving, and `getDetail` returns the value with why it was served.

## Events

Exposures and aliases are queued in the store, sent in batches and flushed every 5 seconds while the app is in the foreground, so events recorded offline are sent on a later start. Call `foreground()` and `background()` as the app's lifecycle changes.

## Staying fresh

On iOS, Android and desktop the client holds the ruleset stream while the app is in the foreground and closes it in the background. When a change is announced it waits a random 0–3 seconds before evaluating, so the apps open at a kill switch do not all ask at once. A kill switch therefore reaches an open app within 5 seconds at the 99th percentile, and a backgrounded app at its next `foreground()`, which refetches unless the last attempt was less than 10 seconds ago. Pass `ClientOptions(sse: false)` to hold no connection: values then stay fresh within the 5 minute poll and at every `foreground()`. On the web the stream is opt-in (`sse: true`).

| The app | A change reaches it |
|---|---|
| in the foreground, holding the stream (the default on iOS, Android and desktop) | within 5 seconds at the 99th percentile |
| in the background | at its next `foreground()` |
| with `sse: false` | within 5 minutes, and at every `foreground()` |
| on the web without the stream | within 5 minutes, and when the page is shown again or its network comes back |

## On the web

The same package runs in an app compiled for the web, with the browser's `fetch` and no extra dependency. The client follows the page by itself: a hidden or closing page stops polling, closes the stream and hands its queued events to `navigator.sendBeacon`, so the last exposures of a visit still arrive; a page shown again, or one whose network comes back, refetches unless the last attempt was less than 10 seconds ago. A browser cannot set `User-Agent`, so the client names itself in what it posts: `sdk`, `sdk_version` and `platform` (`web` unless `ClientOptions.platform` says otherwise) in the context, and the events envelope's `sdk`. The stream, when enabled, carries the client key as the query parameter `sdk_key`; list your app's origins on the key to keep other sites from using it.

## Anonymous users

`anonymousUnitId(store, unitType)` returns one random ID per device (per origin on the web) and unit type, kept in the store outside the SDK key's entries: a rotated client key keeps every anonymous user in the experiment arm they were in.

## Tests

Build a client with `ClientOptions(offline: OfflineSource.document(...))` to serve a values document without a network. `client.offline` sets values, reloads the document and lists the events the client recorded.

## Local overrides

`ClientOptions(allowOverrides: true)` enables `client.overrides` to substitute flag values on the device. Without it, `client.overrides` throws a `StateError`. An environment marked protected refuses overrides.
